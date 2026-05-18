package nzbget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the NZBGet download client. Mirrors C# class names `Nzbget` +
// `NzbgetSettings`. The (Implementation, ConfigContract) pair is
// the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "Nzbget"
	ConfigContract = "NzbgetSettings"
)

// minMajorVersion is the NZBGet major-version floor Test enforces.
// NZBGet 12 was the first release shipping the JSON-RPC API surface
// Sonarr depends on; older versions lack the methods entirely.
// Matches C# `Nzbget.cs:279` (`Version.Parse(version) < Version.Parse("12.0")`).
const minMajorVersion = 12

// Nzbget is the [downloadclients.Provider] implementation for the
// NZBGet usenet client. Ports C# `Nzbget` at
// `src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs` + the
// `NzbgetProxy.cs` JSON-RPC body.
//
// Slice 5.G3c scope: Name / Protocol / Definition / RequestAction /
// GetImportItem / Test live. Download / GetItems / RemoveItem /
// GetStatus / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method.
type Nzbget struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs an Nzbget Provider against the resolved settings +
// proxy. Used by both the registry [Constructor] and by tests that
// pin a stub HTTPDoer.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Nzbget {
	if log == nil {
		log = slog.Default()
	}
	return &Nzbget{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label.
func (n *Nzbget) Name() string { return "NZBGet" }

// Protocol returns the protocol family. NZBGet is usenet, not
// torrent — embeds [downloadclients.BaseUsenetSettings].
func (n *Nzbget) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClients row.
func (n *Nzbget) Definition() *downloadclients.DownloadClient { return n.def }

// Settings returns the resolved Settings struct.
func (n *Nzbget) Settings() Settings { return n.settings }

// Proxy returns the bound proxy.
func (n *Nzbget) Proxy() *Proxy { return n.proxy }

// Download is filled in by slice 5.G3e. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
}

// GetItems is filled in by slice 5.G3d. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem mirrors the C# base default — pass input through.
func (n *Nzbget) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is filled in by slice 5.G3f. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is filled in by slice 5.G3g. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is filled in by slice 5.G3h. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test ports the connectivity + version-floor probe from C#
// `Nzbget.TestConnection` at
// `src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs:273-300`.
//
// Flow:
//
//  1. Settings.Validate first — structural failures short-circuit
//     before any network round-trip. NOTE: the C# tree treats
//     empty-TvCategory as a warning that doesn't block; the Go
//     port preserves the warning text in the failure but lets the
//     network probe still run.
//  2. Nil proxy → programmer-error path (non-nil error vs
//     TestFailure).
//  3. proxy.Call("version", nil, settings) → connectivity + auth
//     check via the `version` JSON-RPC method. On ErrAuthFailed
//     surface as "Username" failure (matches C# :289-291); on
//     ErrUnavailable surface as "Host" failure (C# :294-297);
//     other errors propagate.
//  4. Parse the leading major-version (C# at :277 splits on '-'
//     and parses as `Version`; the Go port mirrors by taking
//     everything before the first '-' or non-numeric char). Below
//     [minMajorVersion] surfaces a "Version" failure mirroring
//     C# :281-285.
//
// TestCategory + TestSettings at C# :303-352 are deferred — they
// probe `config` and category-list endpoints which land alongside
// the GetItems + GetStatus slices.
func (n *Nzbget) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := n.settings.Validate()
	if n.proxy == nil {
		if hasError(failures) {
			return failures, nil
		}
		return nil, errors.New("nzbget: Test: nil proxy (constructor not wired)")
	}

	raw, err := n.proxy.Call(ctx, "version", nil, n.settings)
	if err != nil {
		if failure := classifyAuthError(err); failure != nil {
			failures = append(failures, *failure)
			return failures, nil
		}
		return nil, err
	}

	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse NZBGet version: %v", err),
		})
		return failures, nil
	}

	major := parseMajor(version)
	if major < 0 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse NZBGet version: %q", version),
		})
		return failures, nil
	}
	if major < minMajorVersion {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("NZBGet %s is too old; %s requires at least %d.0",
				version, n.Name(), minMajorVersion),
		})
		return failures, nil
	}

	return failures, nil
}

// hasError reports whether any failure carries a non-empty
// PropertyName + ErrorMessage. Empty-TvCategory warning at
// Settings.Validate is the one warning-shaped failure; everything
// else is treated as blocking.
//
// The current Settings.Validate output doesn't distinguish warning
// vs error at the TestFailure shape — every failure has a
// PropertyName. So this helper is effectively `len(failures) > 0
// AND any failure isn't the TvCategory warning`. We keep it as a
// hook for the v3 React UI's future warning vs error tagging.
func hasError(failures []downloadclients.TestFailure) bool {
	for _, f := range failures {
		if f.PropertyName != "TvCategory" {
			return true
		}
	}
	return false
}

// classifyAuthError converts a proxy auth/transport error to a
// field-level TestFailure when the error is one of the typed arms.
// Pinning mirrors C# `Nzbget.TestConnection` at `:289-297`.
func classifyAuthError(err error) *downloadclients.TestFailure {
	switch {
	case errors.Is(err, ErrAuthFailed):
		return &downloadclients.TestFailure{
			PropertyName:        "Username",
			ErrorMessage:        "Authentication failed",
			DetailedDescription: err.Error(),
		}
	case errors.Is(err, ErrUnavailable):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "Unable to connect to NZBGet",
			DetailedDescription: err.Error(),
		}
	}
	return nil
}

// versionMajorPattern extracts the leading major-version integer
// from an NZBGet version string. NZBGet versions look like "24.2",
// "21.0-testing", "16.4-betaN". The C# tree at `Nzbget.cs:277`
// splits on '-' first then parses as a `Version`; the Go port
// extracts the first run of digits, which is robust against the
// hyphen-tag suffix.
var versionMajorPattern = regexp.MustCompile(`^\s*(\d+)`)

// parseMajor returns the leading major-version integer from a
// version string, or -1 when no integer can be parsed.
func parseMajor(version string) int {
	m := versionMajorPattern.FindStringSubmatch(version)
	if len(m) < 2 {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}

// RequestAction is the per-impl action map. The NZBGet C# tree has
// no concrete actions — returns (nil, nil) per the C# default at
// `DownloadClientBase.cs:165-167`.
func (n *Nzbget) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: Nzbget satisfies the Provider interface.
var _ downloadclients.Provider = (*Nzbget)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Deluge + Transmission + Sabnzbd factory pattern.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("nzbget: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("nzbget: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}

	// Build the proxy when an HTTPDoer is available. Tests
	// constructing through the registry without a doer (e.g. the
	// 5.G3a round-trip suite) get a nil proxy + a Provider that
	// surfaces programmer-error returns from Test(); production
	// wiring always supplies one.
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("nzbget: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		var err error
		proxy, err = NewProxy(doer, log)
		if err != nil {
			return nil, err
		}
	}

	return New(def, settings, proxy, log), nil
}

// init registers Nzbget with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
