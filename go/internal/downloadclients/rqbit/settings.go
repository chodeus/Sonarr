// Package rqbit ports the C# RQBit download client at
// `src/NzbDrone.Core/Download/Clients/RQBit/`.
//
// RQBit is a Rust-based BitTorrent client with a minimal JSON-over-
// HTTP API at `<scheme>://<host>:<port><urlBase>` (default
// `http://localhost:3030/`). No authentication required. Sonarr
// drives it via:
//
//   - GET `/` — root payload carrying the version string (Test).
//   - GET `/torrents?with_stats=true` — list (GetItems).
//   - POST `/torrents?overwrite=true` — add torrent. Body is a
//     magnet URL OR the raw .torrent bytes.
//   - POST `/torrents/<hash>/forget` — remove without disk cleanup.
//   - POST `/torrents/<hash>/delete` — remove with disk cleanup.
//
// Slice 5.J80 ships the entire DC end-to-end:
//
//   - [Settings] + [NewSettings] + Validate.
//   - [Proxy] with GetVersion / GetTorrents / AddTorrentFromURL /
//     AddTorrentFromFile / RemoveTorrent.
//   - [RQBit] Provider with full Download (URL + body dispatch),
//     GetItems (3-arm status table), RemoveItem, Test (version
//     probe + 8.0.0 minimum), GetStatus (IsLocalhost), plus
//     RemotePathMapper wiring.
//   - Registry registration via init().
//
// Documented divergences:
//
//   - The C# `IDownloadSeedConfigProvider`-driven cleanup-gate
//     at `RQBit.cs:98-122` (flips CanMoveFiles/CanBeRemoved when
//     ratio/seed-time goals are met) is deferred — the Go port
//     doesn't yet have a seed-config-provider. Cleanup flags
//     default to (RemoveCompletedDownloads AND IsFinished) to
//     keep the manual-import flow predictable.
//   - The C# `SetTorrentLabel` no-op (`:194-197`) is omitted —
//     "currently unsupported by RQBit" per the C# comment.
package rqbit

import (
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Implementation + ConfigContract are the persisted-row identifiers.
// Mirrors C# class names `RQBit` + `RQbitSettings`.
const (
	Implementation = "RQBit"
	ConfigContract = "RQbitSettings"
)

// minVersion is the RQBit floor TestVersion enforces. Mirrors C#
// `new Version(8, 0, 0)` at `RQBit.cs:185`.
const minVersion = "8.0.0"

// Settings ports `RQbitSettings` at
// `src/NzbDrone.Core/Download/Clients/RQBit/RQbitSettings.cs:7-37`.
//
// Defaults mirror the C# constructor at `:11-15`: Host="localhost",
// Port=3030, UrlBase="/".
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the RQBit host. C# `:18`.
	Host string `json:"host"`

	// Port is the RQBit port (default 3030). C# `:21`.
	Port int `json:"port"`

	// UseSsl flips http→https. C# `:25`.
	UseSsl bool `json:"useSsl"`

	// UrlBase is the optional URL prefix between host:port and the
	// `/torrents...` API paths. Default `/`. C# `:30`.
	UrlBase string `json:"urlBase"`
}

// NewSettings returns a Settings with the C# constructor defaults.
func NewSettings() Settings {
	return Settings{
		Host:    "localhost",
		Port:    3030,
		UrlBase: "/",
	}
}

// Validate ports `RQbitSettingsValidator` at C# `:39-47`. Rules:
//
//  1. Host non-empty.
//  2. Port 1..65535.
//  3. UrlBase must start with `/` when non-empty.
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
	return failures
}
