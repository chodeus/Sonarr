package nzbvortex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// NzbVortex is the [downloadclients.Provider] implementation for
// the C# `NzbVortex` class at
// `src/NzbDrone.Core/Download/Clients/NzbVortex/NzbVortex.cs:18-262`.
//
// Skeleton-only at 5.J66: Test (connection + API-version-gate) +
// GetStatus + identity methods are real; Download / GetItems /
// RemoveItem stub out to [ErrNotImplemented] until the per-method
// follow-up slices land. The auth-flow + session cache + retry-on-
// NotLoggedIn machinery in [Proxy] is fully live so the follow-up
// slices only add the per-endpoint call shape.
type NzbVortex struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy
}

// New constructs an NzbVortex Provider. proxy=nil binds a default
// [Proxy] with a TLS-permissive HTTP client; log=nil binds
// [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *NzbVortex {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &NzbVortex{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. C# `:49` — note the
// upper-cased "NZBVortex" form.
func (n *NzbVortex) Name() string { return "NZBVortex" }

// Protocol returns Usenet.
func (n *NzbVortex) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClient row.
func (n *NzbVortex) Definition() *downloadclients.DownloadClient { return n.def }

// Settings returns the bound Settings.
func (n *NzbVortex) Settings() Settings { return n.settings }

// Download ports `NzbVortex.AddFromNzbFile` at C# `:35-47`. Fires
// a multipart POST to `nzb/add` with the fetched NzbBody, the
// per-grab priority (Recent vs Older, selected by req.IsRecent),
// and the configured TvCategory as the groupname query param.
// Returns the add_uuid the server assigns. The C# code throws
// `DownloadClientException` on a null response; the Go port
// surfaces [ErrRPC] (already wrapped by [Proxy.DownloadNzb] when
// the server returns an empty add_uuid).
//
// Filename composition matches the C# pattern at AddFromNzbFile —
// `<ReleaseTitle>.nzb` (the orchestrator's pre-fetch step doesn't
// give the Provider a separate `filename` arg the way C#
// UsenetClientBase does, so the Go port derives from ReleaseTitle).
func (n *NzbVortex) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("nzbvortex: nil GrabRequest")
	}
	if n.proxy == nil {
		return "", errors.New("nzbvortex: nil Proxy")
	}
	if strings.TrimSpace(req.ReleaseTitle) == "" {
		return "", errors.New("nzbvortex: GrabRequest.ReleaseTitle is empty")
	}
	if len(req.NzbBody) == 0 {
		return "", errors.New("nzbvortex: GrabRequest.NzbBody is empty")
	}
	priority := n.settings.OlderTvPriority
	if req.IsRecent {
		priority = n.settings.RecentTvPriority
	}
	filename := req.ReleaseTitle + ".nzb"
	id, err := n.proxy.DownloadNzb(ctx, n.settings, req.NzbBody, filename, priority)
	if err != nil {
		return "", fmt.Errorf("nzbvortex: DownloadNzb(%q): %w", req.ReleaseTitle, err)
	}
	return id, nil
}

// GetItems is a STUB at 5.J66. Follow-up: Proxy.GetQueue + state
// table at C# `:51-112` + GetFiles output-path resolution at
// `:232-260`.
func (n *NzbVortex) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return []*downloadclients.Item{}, nil
}

// GetImportItem is the C# base passthrough.
func (n *NzbVortex) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a STUB at 5.J66. Follow-up: Proxy.Remove via
// `nzb/{id}/cancel` or `cancelDelete`.
func (n *NzbVortex) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return fmt.Errorf("%w: RemoveItem", ErrNotImplemented)
}

// GetStatus ports `NzbVortex.GetStatus` at C# `Hadouken.cs:139-147`
// — wait, that's the wrong file, the C# is at
// `NzbVortex.cs:139-147`. IsLocalhost-only; NZBVortex's
// OutputRootFolders are populated at the per-item level inside
// GetItems (DestinationPath) rather than via a global config
// probe, so this method stays minimal.
func (n *NzbVortex) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(n.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default.
func (n *NzbVortex) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (n *NzbVortex) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `NzbVortex.Test(failures)` at C# `:149-155`. Runs the
// full four-arm C# probe chain — TestConnection + TestApiVersion +
// TestAuthentication + TestCategory. Each arm short-circuits the
// next on failure so a dead host doesn't quadruple-fail.
func (n *NzbVortex) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := n.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, n.testConnection(ctx)...)
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, n.testAPIVersion(ctx)...)
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, n.testAuthentication(ctx)...)
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, n.testCategory(ctx)...)
	return failures, nil
}

// testAuthentication ports `TestAuthentication` at C# `:200-212`.
// Fires `GetQueue(limit=1)` — the call requires auth, so a bad
// ApiKey surfaces as [ErrAuth]. The ApiKey-property failure
// matches C# `:208`; any other transport failure surfaces as
// Host (delegated to the [Proxy.get] error chain — those will
// already have been caught by TestConnection so reaching this
// point implies auth-only failure).
func (n *NzbVortex) testAuthentication(ctx context.Context) []downloadclients.TestFailure {
	if _, err := n.proxy.GetQueue(ctx, n.settings, 1); err != nil {
		if errors.Is(err, ErrAuth) {
			return []downloadclients.TestFailure{{
				PropertyName: "ApiKey",
				ErrorMessage: "API Key is incorrect",
			}}
		}
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to authenticate against NZBVortex: %v", err),
		}}
	}
	return nil
}

// testCategory ports `TestCategory` at C# `:214-230`. Calls
// `Proxy.GetGroups`, looks up by name, fails when the configured
// TvCategory is non-empty AND no matching group exists. A blank
// TvCategory disables the check (matches C# `:220`).
func (n *NzbVortex) testCategory(ctx context.Context) []downloadclients.TestFailure {
	want := strings.TrimSpace(n.settings.TvCategory)
	if want == "" {
		return nil
	}
	groups, err := n.proxy.GetGroups(ctx, n.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to read NZBVortex groups: %v", err),
		}}
	}
	for _, g := range groups {
		if g != nil && g.GroupName == want {
			return nil
		}
	}
	return []downloadclients.TestFailure{{
		PropertyName: "TvCategory",
		ErrorMessage: fmt.Sprintf("Group %q does not exist on the NZBVortex server", want),
	}}
}

// testConnection ports `TestConnection` at C# `:157-174`. Calls
// `app/appversion`. Failure surfaces as a Host-property failure.
func (n *NzbVortex) testConnection(ctx context.Context) []downloadclients.TestFailure {
	if _, err := n.proxy.GetVersion(ctx, n.settings); err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to NZBVortex: %v", err),
		}}
	}
	return nil
}

// testAPIVersion ports `TestApiVersion` at C# `:176-198`. Calls
// `app/apilevel`, parses the dotted-numeric version, rejects
// anything below 2.3 (matches the C# `Major < 2 || (Major == 2
// && Minor < 3)` clause). Transport failure surfaces as a Host-
// property failure.
func (n *NzbVortex) testAPIVersion(ctx context.Context) []downloadclients.TestFailure {
	resp, err := n.proxy.GetAPIVersion(ctx, n.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to read NZBVortex API version: %v", err),
		}}
	}
	v := strings.TrimSpace(resp.APILevel)
	if v == "" {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: "NZBVortex did not report an API level",
		}}
	}
	if compareVersion(v, fmt.Sprintf("%d.%d", minAPIMajor, minAPIMinor)) < 0 {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("NZBVortex API level %s is below the required %d.%d", v, minAPIMajor, minAPIMinor),
		}}
	}
	return nil
}

// isLocalHost ports the C# `Settings.Host == "127.0.0.1" || ==
// "localhost"` check at `NzbVortex.cs:143`.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// compareVersion compares two dotted-numeric strings. Returns -1
// / 0 / +1. Non-numeric segments collapse to 0. Capped at 4
// components.
func compareVersion(a, b string) int {
	ai := splitVersion(a)
	bi := splitVersion(b)
	n := len(ai)
	if len(bi) > n {
		n = len(bi)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(ai) {
			av = ai[i]
		}
		if i < len(bi) {
			bv = bi[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(v string) []int {
	parts := strings.SplitN(v, ".", 4)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, r := range strings.TrimSpace(p) {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out = append(out, n)
	}
	return out
}

// ErrNotImplemented signals a 5.J66 skeleton stub.
var ErrNotImplemented = errors.New("nzbvortex: not yet implemented in this build")

// Compile-time guard.
var _ downloadclients.Provider = (*NzbVortex)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("nzbvortex: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("nzbvortex: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	return New(def, settings, nil, log), nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
