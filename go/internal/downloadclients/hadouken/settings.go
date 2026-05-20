// Package hadouken ports the C# Hadouken download client at
// `src/NzbDrone.Core/Download/Clients/Hadouken/`.
//
// Hadouken is a torrent client built on libtorrent that exposes a
// JSON-RPC 2.0 endpoint at `<base>/api`. Sonarr drives it via four
// calls:
//
//   - `core.getSystemInfo` — version probe used by Test.
//   - `webui.list` — torrent enumeration (GetItems).
//   - `webui.getSettings` — config probe (GetStatus reads
//     `bittorrent.defaultSavePath` for the OutputRootFolders list).
//   - `webui.addTorrent` — file/url add for Download.
//   - `webui.perform` with action="remove" / "removedata" for
//     RemoveItem.
//
// Slice 5.J62 (this slice) ships the SKELETON of the package:
//
//   - [Settings] + [NewSettings] + Validate.
//   - [Proxy] JSON-RPC transport with [Proxy.GetSystemInfo] for the
//     version probe.
//   - [Hadouken] Provider with Name + Protocol + Definition +
//     Settings + Test (TestConnection version-gate at 5.1) +
//     GetStatus (host-based IsLocalhost only — config probe pulled
//     into the GetItems follow-up).
//   - Stubs for Download / GetItems / RemoveItem returning a
//     not-yet-implemented sentinel so the registry binding compiles
//     and the orchestrator can fail cleanly when the operator
//     selects Hadouken at the current commit.
//   - Registry registration via init().
//
// Follow-up slices fill in:
//
//   - [Proxy.GetTorrents] + [HadoukenTorrent] + state-table mapping.
//   - [Hadouken.GetItems] full enumeration + Category-label gate.
//   - [Hadouken.Download] (AddTorrentFile / AddTorrentUri).
//   - [Hadouken.RemoveItem] (webui.perform remove/removedata).
//   - [Hadouken.GetStatus] config probe + OutputRootFolders.
package hadouken

import (
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Implementation + ConfigContract are the persisted-row identifiers.
// Mirrors C# class names `Hadouken` + `HadoukenSettings`.
const (
	Implementation = "Hadouken"
	ConfigContract = "HadoukenSettings"
)

// minVersion is the Hadouken floor the version-probe enforces.
// Mirrors C# `new Version("5.1")` at `Hadouken.cs:166`. Versions
// below 5.1 lack the JSON-RPC surface this package targets.
const minVersion = "5.1"

// Settings ports `HadoukenSettings` at
// `src/NzbDrone.Core/Download/Clients/Hadouken/HadoukenSettings.cs:24-63`.
//
// Defaults mirror the C# constructor at `:28-33`: Host="localhost",
// Port=7070, Category="sonarr-tv".
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the Hadouken host. C# `:35`.
	Host string `json:"host"`

	// Port is the Hadouken JSON-RPC port (default 7070). C# `:38`.
	Port int `json:"port"`

	// UseSsl flips the http→https scheme on the request URL. C#
	// `:42`.
	UseSsl bool `json:"useSsl"`

	// UrlBase is the optional URL prefix between host:port and
	// the `/api` JSON-RPC suffix. C# `:45`. Leading slash is
	// optional — the proxy normalises.
	UrlBase string `json:"urlBase"`

	// Username + Password drive HTTP Basic auth on every request.
	// Both required (no API-key alternative — Hadouken has no
	// out-of-band auth path). C# `:50,53`.
	Username string `json:"username"`
	Password string `json:"password"`

	// Category gates which torrents [Hadouken.GetItems] yields:
	// when non-empty, GetItems filters to torrents whose Label
	// equals Category. Also applied as the label on every newly-
	// added torrent. C# `:56`.
	Category string `json:"category"`
}

// NewSettings returns a Settings with the C# constructor defaults
// applied. JSON-unmarshalling onto this preserves the defaults for
// any missing fields.
func NewSettings() Settings {
	return Settings{
		Host:     "localhost",
		Port:     7070,
		Category: "sonarr-tv",
	}
}

// Validate ports `HadoukenSettingsValidator` at C# `:8-22`. Rules:
//
//  1. Host non-empty (the C# `ValidHost` extension also checks
//     DNS-label format; the Go port keeps it to non-empty for the
//     same reason every other DC settings.Validate does).
//  2. Port 1..65535.
//  3. UrlBase, when set, must start with "/".
//  4. Username required.
//  5. Password required.
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
	if strings.TrimSpace(s.Username) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Username",
			ErrorMessage: "Username must not be empty.",
		})
	}
	if strings.TrimSpace(s.Password) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Password",
			ErrorMessage: "Password must not be empty.",
		})
	}
	return failures
}
