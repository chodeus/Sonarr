// Package deluge ports the C# Deluge download client at
// `src/NzbDrone.Core/Download/Clients/Deluge/`. Deluge is the
// second-most-common torrent client in real-world Sonarr
// deployments (qBittorrent is first; closed by Phase 5.G1).
// Closing this arc lifts every Deluge install from
// [downloadclients.ErrNotRegistered] at grab time.
//
// Phase 5.G2 slice plan (each slice = one commit + tests):
//
//   - 5.G2a (this slice): Settings + Validate +
//     [downloadclients.Default] registration + a minimal Provider
//     skeleton (Name / Protocol / Definition / RequestAction live;
//     Download / Test / GetItems / RemoveItem / GetStatus /
//     MarkItemAsImported return [downloadclients.ErrNotImplementedYet]).
//   - 5.G2b: JSON-RPC transport with auth.login (deluge-web posts
//     {"method":"auth.login","params":[password],"id":N} returning
//     a session cookie; subsequent calls replay it).
//   - 5.G2c: Test() — auth.login + web.update_ui version probe.
//   - 5.G2d: GetItems() — web.update_ui torrent projection.
//   - 5.G2e: Download() — core.add_torrent_magnet /
//     core.add_torrent_url / core.add_torrent_file.
//   - 5.G2f: RemoveItem() — core.remove_torrent.
//   - 5.G2g: GetStatus() — core.get_config response paths +
//     IsLocalhost.
//   - 5.G2h: MarkItemAsImported() + RequestAction() — label.set +
//     label.add via the deluge label plugin.
//
// Reference templates (proven shape):
//
//   - usenet variant: internal/downloadclients/sabnzbd/.
//   - torrent variant: internal/downloadclients/qbittorrent/
//     (closed 5.G1 arc — the closest analogue: WebUI + cookie session +
//     label/category support).
//
// Family pattern (validated by sub-arcs 14a/b + 5.G1):
//
//   - init() time registration in [downloadclients.Default].
//   - Settings JSON round-trip from DownloadClient.Settings column.
//   - HTTP-JSON-RPC proxy with cookie-based session auth.
//   - Download routes magnet/URL/torrent-file to the matching
//     core.add_torrent_* RPC.
//
// C# cites lead each type with file:line references.
package deluge

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Settings ports `DelugeSettings` at
// `src/NzbDrone.Core/Download/Clients/Deluge/DelugeSettings.cs:18-72`.
//
// Defaults mirror the C# constructor at `:23-28`:
// Host=localhost, Port=8112, Password=deluge,
// TvCategory=tv-sonarr.
//
// Authentication shape: deluge-web speaks JSON-RPC over HTTP with
// password-only auth (no username) — the `auth.login` RPC takes a
// single string argument and returns a session cookie subsequent
// requests replay. The Go-port proxy (5.G2b) tracks this cookie.
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the deluge-web host. C# `:32`.
	Host string `json:"host"`
	// Port is the deluge-web port (default 8112). C# `:35`.
	Port int `json:"port"`
	// UseSsl flips http→https. C# `:39`.
	UseSsl bool `json:"useSsl"`
	// UrlBase is the optional URL prefix (C# `:43`). Empty when
	// deluge-web is served at the root; "/deluge" or similar when
	// the WebUI lives behind a reverse-proxy path.
	UrlBase string `json:"urlBase"`
	// Password is the deluge-web auth password (default
	// "deluge"). C# `:46`.
	Password string `json:"password"`
	// TvCategory is applied as a deluge "label" via the label
	// plugin. Constrained to [a-z0-9-] per C# `:14`.
	TvCategory string `json:"tvCategory"`
	// TvImportedCategory is the post-import label Sonarr applies
	// when MarkItemAsImported fires. C# `:52`.
	TvImportedCategory string `json:"tvImportedCategory"`
	// RecentTvPriority / OlderTvPriority pick between deluge's
	// Last (0) / First (1) queue positions based on
	// Subject.IsRecent. C# `:55,58`. See `DelugePriority.cs`.
	RecentTvPriority int `json:"recentTvPriority"`
	OlderTvPriority  int `json:"olderTvPriority"`
	// AddPaused starts torrents in the paused state. C# `:61`.
	AddPaused bool `json:"addPaused"`
	// DownloadDirectory overrides the deluge-side download path
	// (passed as the `download_location` option on add). Empty
	// means "use deluge's configured default". C# `:64`.
	DownloadDirectory string `json:"downloadDirectory"`
	// CompletedDirectory overrides the deluge-side move-on-
	// completion path (passed as `move_completed_path`). Empty
	// means "respect deluge's existing config". C# `:67`.
	CompletedDirectory string `json:"completedDirectory"`
}

// NewSettings returns a Settings with the C# constructor defaults at
// `DelugeSettings.cs:23-28`. JSON-unmarshalling onto this preserves
// the defaults for any missing fields.
func NewSettings() Settings {
	return Settings{
		Host:       "localhost",
		Port:       8112,
		Password:   "deluge",
		TvCategory: "tv-sonarr",
	}
}

// categoryPattern mirrors the C# regex at
// `DelugeSettings.cs:14,15`: `^[-a-z0-9]*$`. The pattern permits
// an empty string or a sequence of lowercase letters, digits, and
// hyphens. The C# regex is anchored start-to-end; Go's regexp
// engine is unanchored by default, so the Go port wraps it
// explicitly.
var categoryPattern = regexp.MustCompile(`^[-a-z0-9]*$`)

// Validate ports `DelugeSettingsValidator` at C# `:7-17`. Returns
// the FluentValidation-style failure list.
//
// Rules:
//
//  1. Host non-empty (the C# ValidHost extension also checks
//     DNS-label format; the Go port checks non-empty + no
//     whitespace).
//  2. Port 1-65535 (C# `:11`).
//  3. TvCategory matches [categoryPattern] (C# `:13`).
//  4. TvImportedCategory matches [categoryPattern] (C# `:14`).
//
// Deluge has no ApiKey arm (unlike qBittorrent at 5.G1a) — only
// password auth.
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

	if !categoryPattern.MatchString(s.TvCategory) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "Allowed characters a-z, 0-9 and -",
		})
	}

	if !categoryPattern.MatchString(s.TvImportedCategory) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvImportedCategory",
			ErrorMessage: "Allowed characters a-z, 0-9 and -",
		})
	}

	return failures
}

// Priority constants port `DelugePriority.cs`. Drive the queue
// position on add — Last appends to the bottom of the queue, First
// moves to the top.
const (
	PriorityLast  = 0
	PriorityFirst = 1
)
