// Package qbittorrent ports the C# qBittorrent download client at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/`. This is the
// most-used Sonarr download client in the wild — closing it
// unblocks every qBittorrent install whose persisted-row
// Implementation = "QBittorrent" previously hit
// [downloadclients.ErrNotRegistered] in the registry lookup.
//
// Phase 5.G1 slice plan (each slice = one commit + tests):
//
//   - 5.G1a (this slice): Settings + Validate +
//     [downloadclients.Default] registration + a minimal Provider
//     skeleton (Name / Protocol / Definition / RequestAction live;
//     Download / Test / GetItems / RemoveItem / GetStatus /
//     MarkItemAsImported return [downloadclients.ErrNotImplementedYet]).
//   - 5.G1b: HTTP transport with cookie-based auth (POST
//     /api/v2/auth/login → SID cookie → session retention).
//   - 5.G1c: Test() — login + version compat via
//     /api/v2/app/version.
//   - 5.G1d: GetItems() — GET /api/v2/torrents/info → []Item.
//   - 5.G1e: Download() — POST /api/v2/torrents/add (URL/magnet).
//   - 5.G1f: RemoveItem() — POST /api/v2/torrents/delete.
//   - 5.G1g: GetStatus() — DownloadClientInfo (output paths +
//     IsLocalhost detection).
//   - 5.G1h: MarkItemAsImported() + RequestAction() — set
//     post-import category.
//
// Reference templates (proven shape):
//
//   - usenet variant: internal/downloadclients/sabnzbd/.
//   - torrent variant (closer analogue): internal/downloadclients/transmission/.
//
// Family pattern (validated by sub-arc 14a/b + the 5.G arc):
//
//   - init() time registration in [downloadclients.Default].
//   - Settings JSON round-trip from DownloadClient.Settings column.
//   - HTTP proxy with cookie-based auth + version probe (qBittorrent
//     uses session-cookie auth via /api/v2/auth/login).
//   - Download routes magnet → torrents/add(urls=magnet) and bytes →
//     torrents/add(multipart 'torrents' field). URL-only also routes
//     through torrents/add(urls=...) — qBittorrent fetches the
//     .torrent itself.
//
// C# cites lead each type with file:line references.
package qbittorrent

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Settings ports `QBittorrentSettings` at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrentSettings.cs:31-99`.
//
// Defaults mirror the C# constructor at `:37-41`:
// Host=localhost, Port=8080, TvCategory="tv-sonarr".
//
// Authentication shape: qBittorrent's WebUI supports either
// username+password (cookie session via /api/v2/auth/login) OR an
// API key (Authorization header). The C# validator at `:18-23`
// enforces "username/password must be empty when ApiKey is set" —
// the Go port mirrors this rule.
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the qBittorrent WebUI host. C# `:45`.
	Host string `json:"host"`
	// Port is the qBittorrent WebUI port (default 8080). C# `:48`.
	Port int `json:"port"`
	// UseSsl flips http→https. C# `:51`.
	UseSsl bool `json:"useSsl"`
	// UrlBase is the optional URL prefix (C# `:55`). Empty when the
	// WebUI is served at the root; "/qbittorrent" or similar when
	// the WebUI lives behind a reverse-proxy path.
	UrlBase string `json:"urlBase"`
	// ApiKey is the optional API-key auth credential. When set, the
	// proxy attaches Authorization headers and skips the cookie
	// login flow. Mutually exclusive with Username/Password (C#
	// `:18-23`). C# `:58`.
	ApiKey string `json:"apiKey"`
	// Username is the WebUI auth username (cookie-login flow). C#
	// `:61`.
	Username string `json:"username"`
	// Password is the WebUI auth password (cookie-login flow). C#
	// `:64`.
	Password string `json:"password"`
	// TvCategory is applied as the qBittorrent "category" on every
	// added torrent. C# `:67`. The C# regex at `:25` permits any
	// non-empty string that doesn't contain backslashes, doesn't
	// start/end with '/', and doesn't contain '//'.
	TvCategory string `json:"tvCategory"`
	// TvImportedCategory is the post-import category Sonarr applies
	// when MarkItemAsImported fires. C# `:70`. Same regex as
	// TvCategory.
	TvImportedCategory string `json:"tvImportedCategory"`
	// RecentTvPriority / OlderTvPriority pick between qBittorrent's
	// Last/First queue positions based on Subject.IsRecent. C#
	// `:73,76`. See `QBittorrentPriority.cs` — 0=Last, 1=First.
	RecentTvPriority int `json:"recentTvPriority"`
	OlderTvPriority  int `json:"olderTvPriority"`
	// InitialState picks the post-add torrent state. C# `:79`. See
	// `QBittorrentState.cs` — 0=Start, 1=ForceStart, 2=Stop.
	InitialState int `json:"initialState"`
	// SequentialOrder enables qBittorrent's "Download in
	// sequential order" flag at add time. C# `:82`.
	SequentialOrder bool `json:"sequentialOrder"`
	// FirstAndLast enables qBittorrent's "Download first and last
	// pieces first" flag at add time. C# `:85`.
	FirstAndLast bool `json:"firstAndLast"`
	// ContentLayout picks how qBittorrent unpacks single-file vs
	// multi-file torrents. C# `:88`. See
	// `QBittorrentContentLayout.cs` — 0=Default (client setting),
	// 1=Original, 2=Subfolder (NoSubfolder is "Original",
	// CreateSubfolder is "Subfolder").
	ContentLayout int `json:"contentLayout"`
	// AddSeriesTags applies the series title as a qBittorrent tag
	// at add time. C# `:91`.
	AddSeriesTags bool `json:"addSeriesTags"`
}

// NewSettings returns a Settings with the C# constructor defaults at
// `QBittorrentSettings.cs:37-41`. JSON-unmarshalling onto this
// preserves the defaults for any missing fields.
func NewSettings() Settings {
	return Settings{
		Host:       "localhost",
		Port:       8080,
		TvCategory: "tv-sonarr",
	}
}

// categoryPattern mirrors the C# regex at
// `QBittorrentSettings.cs:25,26`: `^([^\\\/](\/?[^\\\/])*)?$`. The
// pattern permits an empty string, or any non-empty string that:
//
//   - does not contain backslashes;
//   - does not contain consecutive slashes ("//");
//   - does not start or end with '/'.
//
// The C# regex is anchored start-to-end; Go's regexp engine is
// unanchored by default, so the Go port wraps it explicitly.
var categoryPattern = regexp.MustCompile(`^([^\\/](/?[^\\/])*)?$`)

// Validate ports `QBittorrentSettingsValidator` at C# `:9-29`.
// Returns the FluentValidation-style failure list.
//
// Rules:
//
//  1. Host non-empty (the C# ValidHost extension also checks
//     DNS-label format; the Go port checks non-empty + no
//     whitespace).
//  2. Port 1-65535 (C# `:14`).
//  3. UrlBase must start with "/" when set (C# `:15`).
//  4. Username + Password must be empty when ApiKey is set (C#
//     `:17-23`).
//  5. TvCategory + TvImportedCategory must match
//     [categoryPattern] (C# `:25,26`).
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

	if strings.TrimSpace(s.ApiKey) != "" {
		if strings.TrimSpace(s.Username) != "" {
			failures = append(failures, downloadclients.TestFailure{
				PropertyName: "Username",
				ErrorMessage: "Username must be empty when using API Key.",
			})
		}
		if strings.TrimSpace(s.Password) != "" {
			failures = append(failures, downloadclients.TestFailure{
				PropertyName: "Password",
				ErrorMessage: "Password must be empty when using API Key.",
			})
		}
	}

	if !categoryPattern.MatchString(s.TvCategory) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: `Can not contain '\', '//', or start/end with '/'`,
		})
	}

	if !categoryPattern.MatchString(s.TvImportedCategory) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvImportedCategory",
			ErrorMessage: `Can not contain '\', '//', or start/end with '/'`,
		})
	}

	return failures
}

// Priority constants port `QBittorrentPriority.cs`. The qBittorrent
// "increase/decrease priority" RPCs use numeric positions; Sonarr
// uses Last (0) / First (1) to pick the queue end.
const (
	PriorityLast  = 0
	PriorityFirst = 1
)

// InitialState constants port `QBittorrentState.cs`. Drive the
// post-add torrent state on /api/v2/torrents/add.
const (
	InitialStateStart      = 0
	InitialStateForceStart = 1
	InitialStateStop       = 2
)

// ContentLayout constants port `QBittorrentContentLayout.cs`. Drive
// the contentLayout form field on /api/v2/torrents/add (Original /
// Subfolder / Default).
const (
	ContentLayoutDefault   = 0
	ContentLayoutOriginal  = 1
	ContentLayoutSubfolder = 2
)
