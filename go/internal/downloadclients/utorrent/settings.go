// Package utorrent ports the C# uTorrent download client at
// `src/NzbDrone.Core/Download/Clients/uTorrent/`. uTorrent speaks
// a token-cookie-authenticated HTTP API exposed under `/gui/` (the
// "WebUI" interface) — the token is fetched once via `/gui/token.html`
// and replayed on every subsequent request alongside the
// `GUID`/`__cfduid` session cookies.
//
// The package ships:
//
//   - [Settings] + [NewSettings] + Validate (slice 5.J46 baseline).
//   - [Priority] enum (Last=0, First=1) per UTorrentPriority.cs.
//   - [State] enum (Start, ForceStart, Pause, Stop) per UtorrentState.cs.
//   - Token + cookie auth [Proxy]: token-cache + GUID-cookie replay
//     with auto re-auth on stale-session 400s.
//   - Full Provider surface (Name / Protocol / Definition / Test /
//     Download / GetItems / RemoveItem / GetStatus / MarkItemAsImported
//     / RequestAction) functionally complete vs C# UTorrent.cs at
//     `src/NzbDrone.Core/Download/Clients/UTorrent/`. Test runs the
//     full C# arm (version probe + minBuild gate + TestGetTorrents at
//     J109).
//   - init() registers `(UTorrent, UTorrentSettings)` in
//     [downloadclients.Default].
//
// C# cites lead each type with file:line references.
package utorrent

import (
	"errors"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Settings ports `UTorrentSettings` at
// `src/NzbDrone.Core/Download/Clients/uTorrent/UTorrentSettings.cs:19-72`.
//
// Defaults mirror the C# constructor at `:23-28`: Host=localhost,
// Port=8080, TvCategory="tv-sonarr".
//
// Authentication shape: cookie-based session via the `/gui/token.html`
// → GUID cookie + token-string flow. The C# validator at `:8-17`
// checks Host non-empty + Port in 1..65535 + UrlBase validity (when
// set) + TvCategory non-empty.
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the uTorrent WebUI host. C# `:31`.
	Host string `json:"host"`

	// Port is the uTorrent WebUI port (default 8080). C# `:34`.
	Port int `json:"port"`

	// UseSsl flips http→https. C# `:38`.
	UseSsl bool `json:"useSsl"`

	// UrlBase is the optional URL prefix (C# `:43`). Empty when the
	// WebUI is served at the root; "/utorrent" or similar when the
	// WebUI lives behind a reverse-proxy path. The full endpoint
	// is built as `<scheme>://<host>:<port>/<urlBase>/gui/`.
	UrlBase string `json:"urlBase"`

	// Username is the WebUI auth username. C# `:46`.
	Username string `json:"username"`

	// Password is the WebUI auth password. C# `:49`.
	Password string `json:"password"`

	// TvCategory is the uTorrent label applied to every added
	// torrent via `action=setprops&s=label`. C# `:52`.
	TvCategory string `json:"tvCategory"`

	// TvImportedCategory is the optional label applied via
	// MarkItemAsImported after the import-mechanism confirms the
	// grab is on disk. C# `:55`.
	TvImportedCategory string `json:"tvImportedCategory"`

	// RecentTvPriority is the [Priority] applied when the matched
	// episode aired in the last 14 days. C# `:58`. Default Last (0).
	RecentTvPriority int `json:"recentTvPriority"`

	// OlderTvPriority is the [Priority] applied when no matched
	// episode aired in the last 14 days. C# `:61`. Default Last (0).
	OlderTvPriority int `json:"olderTvPriority"`

	// IntialState is the [State] applied at add time. C# `:65`. The
	// misspelling matches the C# property name (`IntialState`) —
	// preserved here for JSON wire compatibility against any
	// upstream settings already persisted with that key.
	IntialState int `json:"intialState"`
}

// NewSettings returns a Settings with the C# constructor defaults
// applied. JSON-unmarshalling onto this preserves the defaults for
// any missing fields.
func NewSettings() Settings {
	return Settings{
		Host:       "localhost",
		Port:       8080,
		TvCategory: "tv-sonarr",
	}
}

// Validate ports `UTorrentSettingsValidator` at C# `:8-17`. Returns
// the FluentValidation-style failure list.
//
// Rules:
//
//  1. Host non-empty.
//  2. Port in [1, 65535].
//  3. UrlBase must start with `/` when non-empty (matches C#
//     `.ValidUrlBase()` extension; the Go-port pin here is the same
//     check qBittorrent applies at slice 5.G1a).
//  4. TvCategory non-empty.
func (s Settings) Validate() []downloadclients.TestFailure {
	var failures []downloadclients.TestFailure
	if strings.TrimSpace(s.Host) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Host",
			ErrorMessage: "Host is required",
		})
	}
	if s.Port < 1 || s.Port > 65535 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Port",
			ErrorMessage: "Port must be between 1 and 65535",
		})
	}
	if strings.TrimSpace(s.UrlBase) != "" && !strings.HasPrefix(s.UrlBase, "/") {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "UrlBase",
			ErrorMessage: "URL base must start with '/'",
		})
	}
	if strings.TrimSpace(s.TvCategory) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "Category is required",
		})
	}
	return failures
}

// ErrSettingsTypeMismatch is returned when the constructor receives
// a Settings JSON body that doesn't decode cleanly. Exposed so test
// suites can assert against it.
var ErrSettingsTypeMismatch = errors.New("utorrent: settings type mismatch")
