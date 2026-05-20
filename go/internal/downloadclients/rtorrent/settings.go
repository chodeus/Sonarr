// Package rtorrent ports the C# rTorrent download client at
// `src/NzbDrone.Core/Download/Clients/RTorrent/`. rTorrent uses
// XML-RPC over HTTP (typically against the ruTorrent / Flood
// frontends or the bare scgi-via-http endpoint at `/RPC2`); the Go
// port targets the XML-RPC surface directly.
//
// Slice 5.J41 ships the package skeleton:
//
//   - [Settings] + [NewSettings] + Validate.
//   - [Priority] enum (4 levels matching C#).
//   - Provider stub: Name / Protocol / Definition / RequestAction
//     live; Download / Test / GetItems / RemoveItem / GetStatus /
//     MarkItemAsImported / GetImportItem return
//     [downloadclients.ErrNotImplementedYet].
//   - init() registers `(RTorrent, RTorrentSettings)` in
//     [downloadclients.Default].
//
// Follow-up slices:
//
//   - XML-RPC Proxy: NewProxy, base request shape, per-method calls
//     (system.client_version probe + load.start / load.start_magnet
//     for Download / d.multicall for GetItems / d.erase for
//     RemoveItem).
//   - Test() — version probe.
//   - Download — load.start_magnet for magnet, load.raw_start for
//     .torrent bytes.
//   - GetItems — d.multicall.2 to enumerate the per-hash item rows.
//   - RemoveItem — d.erase / d.stop + d.directory_base + DiskProvider
//     for deleteData arm.
//   - GetStatus — fold the global download-rate / output paths into
//     ClientInfo.
//
// Reference templates:
//
//   - qbittorrent (REST + cookie auth): closest peer-shape (proxy
//     pattern + per-method response decoder).
//   - transmission (JSON-RPC): closer wire-shape (single endpoint +
//     per-method method+args envelope) — XML-RPC has the same
//     general shape just with XML instead of JSON.
//
// C# cites lead each type with file:line references.
package rtorrent

import (
	"errors"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Settings ports `RTorrentSettings` at
// `src/NzbDrone.Core/Download/Clients/RTorrent/RTorrentSettings.cs:19-77`.
//
// Defaults mirror the C# constructor at `:23-31`: Host=localhost,
// Port=8080, UrlBase="RPC2", TvCategory="tv-sonarr",
// OlderTvPriority=Normal, RecentTvPriority=Normal.
//
// Authentication shape: HTTP Basic auth (Username + Password). The
// C# validator at `:7-17` checks Host non-empty + Port in 1..65535 +
// TvCategory non-empty (as a warning, not an error).
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the rTorrent XML-RPC host. C# `:34`.
	Host string `json:"host"`

	// Port is the rTorrent XML-RPC port (default 8080). C# `:37`.
	Port int `json:"port"`

	// UseSsl flips http→https. C# `:41`.
	UseSsl bool `json:"useSsl"`

	// UrlBase is the URL path the rTorrent XML-RPC endpoint lives
	// at (default "RPC2"). C# `:46`. Trailing-slash is stripped at
	// the proxy boundary.
	UrlBase string `json:"urlBase"`

	// Username is the HTTP Basic auth username. C# `:49`.
	Username string `json:"username"`

	// Password is the HTTP Basic auth password. C# `:52`.
	Password string `json:"password"`

	// TvCategory is the rTorrent "label" applied to every added
	// torrent. C# `:55`.
	TvCategory string `json:"tvCategory"`

	// TvImportedCategory is the optional label applied via
	// MarkItemAsImported after the import-mechanism confirms the
	// grab is on disk. C# `:58`.
	TvImportedCategory string `json:"tvImportedCategory"`

	// TvDirectory is the optional explicit destination directory
	// the proxy sets via `d.directory.set` on add. Empty leaves the
	// default-download-folder behaviour (the rTorrent global
	// configuration's directory.default value). C# `:61`.
	TvDirectory string `json:"tvDirectory"`

	// RecentTvPriority is the rTorrent priority applied when the
	// matched episode aired in the last 14 days. C# `:64`. Values
	// pinned by [Priority]; default Normal (2).
	RecentTvPriority int `json:"recentTvPriority"`

	// OlderTvPriority is the rTorrent priority applied when no
	// matched episode aired in the last 14 days. C# `:67`. Default
	// Normal (2).
	OlderTvPriority int `json:"olderTvPriority"`

	// AddStopped flips the post-add state. When true the torrent
	// is added in the stopped/paused state via `load.raw` /
	// `load.start_throw_paused` (operator has to start manually).
	// C# `:70`.
	AddStopped bool `json:"addStopped"`
}

// NewSettings returns a Settings with the C# constructor defaults
// applied. JSON-unmarshalling onto this preserves the defaults for
// any missing fields.
func NewSettings() Settings {
	return Settings{
		Host:             "localhost",
		Port:             8080,
		UrlBase:          "RPC2",
		TvCategory:       "tv-sonarr",
		RecentTvPriority: int(PriorityNormal),
		OlderTvPriority:  int(PriorityNormal),
	}
}

// Validate ports `RTorrentSettingsValidator` at C# `:7-17`. Returns
// the FluentValidation-style failure list.
//
// Rules:
//
//  1. Host non-empty.
//  2. Port in [1, 65535].
//  3. TvCategory non-empty — surfaced as a regular failure (C# uses
//     `.AsWarning()` at `:15` to make it non-blocking; the Go-port
//     TestFailure type doesn't yet model warning vs error, so the
//     missing category surfaces as an ordinary failure. Documented
//     divergence — most users set a category anyway, and surfacing
//     the empty-category as a non-blocking warning is a follow-up
//     when [downloadclients.TestFailure] grows a Severity field).
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
	if strings.TrimSpace(s.TvCategory) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "TvCategory",
			ErrorMessage: "A category is recommended",
		})
	}
	return failures
}

// ErrSettingsTypeMismatch is returned when the constructor receives
// a Settings JSON body that doesn't decode cleanly. Exposed so test
// suites can assert against it.
var ErrSettingsTypeMismatch = errors.New("rtorrent: settings type mismatch")
