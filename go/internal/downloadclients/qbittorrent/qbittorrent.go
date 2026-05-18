package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the qBittorrent download client. Mirrors C# class names
// `QBittorrent` + `QBittorrentSettings` (see
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs:23`
// and `QBittorrentSettings.cs:31`). The (Implementation,
// ConfigContract) pair is the registry key — see
// [downloadclients.Registry] doc.
const (
	Implementation = "QBittorrent"
	ConfigContract = "QBittorrentSettings"
)

// minVersion is the qBittorrent product-version floor Test enforces.
// qBittorrent 4.1 was the first release shipping WebUI API v2 — the
// only API surface the Go-port proxy targets. The C# tree applies
// the equivalent floor at the API-version layer
// (`QBittorrent.cs:464` requires WebUI API >= 1.5, available in
// qBittorrent >= 3.2.4); the Go port simplifies by gating on the
// product version exposed by /api/v2/app/version because that
// endpoint is the canonical reachability + version probe.
var minVersion = versionPair{major: 4, minor: 1}

// QBittorrent is the [downloadclients.Provider] implementation for
// the qBittorrent torrent client. Ports C# `QBittorrent` at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs:23-450`
// + the V2 proxy body at `QBittorrentProxyV2.cs:14-449`.
//
// Slice 5.G1c scope: Name / Protocol / Definition / RequestAction /
// Test are live; Download / GetItems / GetStatus / RemoveItem /
// GetImportItem / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method.
type QBittorrent struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs a QBittorrent Provider against the resolved
// settings + proxy. Used by both the registry [Constructor] and by
// tests that pin a stub HTTPDoer.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *QBittorrent {
	if log == nil {
		log = slog.Default()
	}
	return &QBittorrent{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `QBittorrent.Name`
// (inherited default — the class name itself; see
// `DownloadClientBase.cs:62`).
func (q *QBittorrent) Name() string { return "qBittorrent" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol` at `TorrentClientBase.cs:44`.
func (q *QBittorrent) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClients row.
func (q *QBittorrent) Definition() *downloadclients.DownloadClient { return q.def }

// Settings returns the resolved Settings struct. Exposed for the
// 5.G1d+ slices and tests; concrete proxy code in this package
// reads these fields directly via the package-internal accessor at
// slice land time.
func (q *QBittorrent) Settings() Settings { return q.settings }

// Proxy returns the bound proxy. Exposed for tests that want to
// poke the proxy directly without driving the full Provider
// surface.
func (q *QBittorrent) Proxy() *Proxy { return q.proxy }

// Download is filled in by slice 5.G1e. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
}

// GetItems is filled in by slice 5.G1d. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem mirrors the C# base default — pass input through.
// `DownloadClientBase.cs:102-105`.
func (q *QBittorrent) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is filled in by slice 5.G1f. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is filled in by slice 5.G1g. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is filled in by slice 5.G1h. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test ports the connection + version-compat probe from C#
// `QBittorrent.TestConnection` at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs:459-537`.
//
// Flow:
//
//  1. Run Settings.Validate first — structural failures
//     short-circuit so a malformed config never triggers a network
//     round-trip + WebUI lockout.
//  2. Ensure the proxy is bound. A nil proxy is a programmer-error
//     return (non-nil error) rather than a TestFailure — the
//     registry constructor guarantees a non-nil proxy on the
//     production path.
//  3. EnsureAuth runs the login flow (skipped automatically when
//     ApiKey is set). Failures surface as field-level TestFailures
//     pinned to "ApiKey"/"Username"/"Password"/"Host" per the C#
//     branches at `:504-534`.
//  4. GET /api/v2/app/version → version string like "v4.5.0".
//     Non-200 surfaces a "Host" TestFailure (`:518-521`); unparsable
//     bodies surface a "Version" TestFailure; versions below
//     [minVersion] surface a "Version" TestFailure mirroring
//     `:467-472`.
//
// Transport errors (connection refused, TLS handshake, etc.)
// propagate via the returned error — matches the Transmission Test
// pattern at `internal/downloadclients/transmission/transmission.go`
// where transport failures bubble as errors while semantic failures
// stay in the slice.
func (q *QBittorrent) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	if failures := q.settings.Validate(); len(failures) > 0 {
		return failures, nil
	}
	if q.proxy == nil {
		return nil, errors.New("qbittorrent: Test: nil proxy (constructor not wired)")
	}

	// Step 1: login. EnsureAuth is a no-op for ApiKey + no-creds
	// auth modes; for cookie auth it fires POST /api/v2/auth/login.
	if _, err := q.proxy.EnsureAuth(ctx, q.settings, true); err != nil {
		if authFailure := classifyAuthError(err, q.settings); authFailure != nil {
			return []downloadclients.TestFailure{*authFailure}, nil
		}
		return nil, err
	}

	// Step 2: version probe. The C# code uses /api/v2/app/webapiVersion;
	// the Go port uses /api/v2/app/version because:
	//   - the endpoint exists from qBittorrent 4.1.0 onwards (the
	//     WebUI API v2 floor anyway), so the reachability test is
	//     identical;
	//   - the response body ("v4.5.0") is human-readable + maps
	//     directly to the version-floor failure message.
	status, body, err := q.proxy.DoRequest(ctx, http.MethodGet, "/api/v2/app/version", nil, "", q.settings)
	if err != nil {
		if authFailure := classifyAuthError(err, q.settings); authFailure != nil {
			return []downloadclients.TestFailure{*authFailure}, nil
		}
		return nil, err
	}

	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return []downloadclients.TestFailure{{
			PropertyName:        authProperty(q.settings),
			ErrorMessage:        fmt.Sprintf("Authentication failure (HTTP %d)", status),
			DetailedDescription: fmt.Sprintf("qBittorrent rejected the credentials supplied to %s.", q.Name()),
		}}, nil
	}
	if status != http.StatusOK {
		return []downloadclients.TestFailure{{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to reach qBittorrent (HTTP %d)", status),
			DetailedDescription: truncate(body, 256),
		}}, nil
	}

	raw := strings.TrimSpace(string(body))
	version := parseVersion(raw)
	if version == nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse qBittorrent version: %q", raw),
		}}, nil
	}
	if version.less(minVersion) {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("qBittorrent %s is too old; %s requires at least %d.%d",
				raw, q.Name(), minVersion.major, minVersion.minor),
		}}, nil
	}

	// Beyond TestConnection the C# tree also runs TestCategory /
	// TestPrioritySupport / TestGetTorrents (`:454-456`). Those
	// land in later slices once GetItems / category support arrive.
	return nil, nil
}

// classifyAuthError converts a proxy auth/transport error to a
// field-level TestFailure when the error is one of the typed auth
// arms. Returns nil for other errors so the caller surfaces them as
// an error return.
func classifyAuthError(err error, settings Settings) *downloadclients.TestFailure {
	switch {
	case errors.Is(err, ErrIPBanned):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "qBittorrent has banned the Sonarr IP due to repeated failed login attempts.",
			DetailedDescription: "Restart qBittorrent or wait for the WebUI ban timeout, then retry.",
		}
	case errors.Is(err, ErrAuthFailed):
		return &downloadclients.TestFailure{
			PropertyName:        authProperty(settings),
			ErrorMessage:        "Authentication failed",
			DetailedDescription: err.Error(),
		}
	case errors.Is(err, ErrUnavailable):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "Unable to connect to qBittorrent",
			DetailedDescription: err.Error(),
		}
	}
	return nil
}

// authProperty returns the field name a credentials failure should
// be pinned to. Mirrors the C# branch at `QBittorrent.cs:508` —
// ApiKey-bound credentials surface against "ApiKey", everything
// else against "Username".
func authProperty(settings Settings) string {
	if strings.TrimSpace(settings.ApiKey) != "" {
		return "ApiKey"
	}
	return "Username"
}

// versionPair holds a parsed qBittorrent product version (major +
// minor). Mirrors Transmission's helper at
// `internal/downloadclients/transmission/transmission.go::versionPair`.
type versionPair struct {
	major, minor int
}

// less reports whether v < other. Lexicographic on (major, minor).
func (v versionPair) less(other versionPair) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	return v.minor < other.minor
}

// versionPattern extracts the leading "major.minor" pair from a
// qBittorrent /api/v2/app/version reply. qBittorrent prefixes the
// response with a literal 'v' (e.g. "v4.5.0"), but some forks
// (vuetorrent, custom builds) strip the prefix or append a build
// tag. The pattern tolerates both.
var versionPattern = regexp.MustCompile(`(?i)^v?(\d+)\.(\d+)`)

func parseVersion(raw string) *versionPair {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(m) < 3 {
		return nil
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return &versionPair{major: major, minor: minor}
}

// RequestAction is the per-impl action map. Filled in by slice
// 5.G1h. Until then mirrors `DownloadClientBase.cs:165-167` and
// returns (nil, nil).
func (q *QBittorrent) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: QBittorrent satisfies the Provider interface.
var _ downloadclients.Provider = (*QBittorrent)(nil)

// constructor is the registry factory. Mirrors the Transmission +
// SAB factory pattern from sub-arc 14a/b.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("qbittorrent: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("qbittorrent: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}

	// Build the proxy when an HTTPDoer is available. Tests
	// constructing through the registry without a doer (e.g. the
	// 5.G1a round-trip suite) get a nil proxy + a Provider that
	// surfaces failures only via Test(); production wiring always
	// supplies one.
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("qbittorrent: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		var err error
		proxy, err = NewProxy(doer, log)
		if err != nil {
			return nil, err
		}
	}

	return New(def, settings, proxy, log), nil
}

// init registers qBittorrent with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
