// Package flood ports the C# Flood download client at
// `src/NzbDrone.Core/Download/Clients/Flood/`.
//
// Flood is a modern alternative web UI for rTorrent/qBittorrent/
// Transmission/Deluge that exposes its own JSON-over-HTTP API at
// `<scheme>://<host>:<port>/<urlBase>/api`. Sonarr drives it via:
//
//   - `auth/authenticate` — POST body {username, password} → sets
//     auth cookies for subsequent calls.
//   - `auth/verify` — GET (Test arm).
//   - `torrents` — GET list (GetItems).
//   - `torrents/add-files` — POST (Download torrent-file arm).
//   - `torrents/add-urls` — POST (Download magnet/url arm).
//   - `torrents/delete` — POST (RemoveItem).
//   - `torrents/tags` — PATCH (MarkItemAsImported tag swap).
//   - `torrents/{hash}/contents` — GET (GetImportItem path resolution).
//   - `client/settings` — GET (GetStatus default-directory probe).
//
// The package ships:
//
//   - [Settings] + [NewSettings] + Validate (slice 5.J71 baseline).
//   - [Proxy] with the cookie-cached auth flow (per-(URL,Username)
//     session) + [Proxy.AuthVerify] for the Test arm. Authenticate
//     vs verify split mirrors C# `FloodProxy.AuthAuthenticate` +
//     `AuthVerify`.
//   - Full Provider surface (Name / Protocol / Definition / Test /
//     Download / GetItems / RemoveItem / MarkItemAsImported /
//     GetImportItem / RequestAction) functionally complete vs C#
//     Flood.cs. Seed-config cleanup gate wired at slice J95;
//     AdditionalTags per-Subject expansion (TitleSlug / Quality /
//     Languages / ReleaseGroup / Year / Indexer / Network) wired at
//     slice J99.
//   - Registry registration via init().
//
// GetStatus.OutputRootFolders remains IsLocalhost-only — the C#
// `GetClientSettings` arm that populates from
// `client/settings.directoryDefault` is the only outstanding
// deferral (low priority: post-import nicety, not load-bearing).
package flood

import (
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Implementation + ConfigContract are the persisted-row identifiers.
// Mirrors C# class names `Flood` + `FloodSettings`.
const (
	Implementation = "Flood"
	ConfigContract = "FloodSettings"
)

// AdditionalTag values port C# `Models/AdditionalTags.cs:5-26`. The
// `[FieldOption(Hint=...)]` hints aren't reproduced — they're a UI-
// formatting concern. Numeric values match the C# enum for wire-
// stable Settings round-trips.
const (
	AdditionalTagTitleSlug    = 0
	AdditionalTagQuality      = 1
	AdditionalTagLanguages    = 2
	AdditionalTagReleaseGroup = 3
	AdditionalTagYear         = 4
	AdditionalTagIndexer      = 5
	AdditionalTagNetwork      = 6
)

// Settings ports `FloodSettings` at
// `src/NzbDrone.Core/Download/Clients/Flood/FloodSettings.cs:23-79`.
//
// Defaults mirror the C# constructor at `:27-38`:
// UseSsl=false, Host="localhost", Port=3000, Tags=["sonarr"],
// AdditionalTags=[], StartOnAdd=true.
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the Flood host. C# `:41`.
	Host string `json:"host"`

	// Port is the Flood port (default 3000). C# `:44`.
	Port int `json:"port"`

	// UseSsl flips http→https. C# `:48`.
	UseSsl bool `json:"useSsl"`

	// UrlBase is the optional URL prefix between host:port and
	// the `/api` suffix. C# `:52`.
	UrlBase string `json:"urlBase"`

	// Username + Password drive cookie-based auth. C# `:55,58`.
	Username string `json:"username"`
	Password string `json:"password"`

	// Destination is the optional Flood-side download directory
	// passed on every add-torrent call. Empty omits it.
	// C# `:61`.
	Destination string `json:"destination"`

	// Tags are the labels applied on every new torrent + the gate
	// GetItems filters against. C# `:64`.
	Tags []string `json:"tags"`

	// PostImportTags are added (via SetTorrentsTags PATCH) once an
	// item is imported. Empty means "no post-import tagging".
	// C# `:67`.
	PostImportTags []string `json:"postImportTags"`

	// AdditionalTags is the int-array of AdditionalTag constants
	// to compose per-grab. C# `:70`.
	AdditionalTags []int `json:"additionalTags"`

	// StartOnAdd toggles the `start=true` flag on add-torrent calls.
	// C# `:73`.
	StartOnAdd bool `json:"startOnAdd"`
}

// NewSettings returns a Settings with the C# constructor defaults
// applied.
func NewSettings() Settings {
	return Settings{
		Host:           "localhost",
		Port:           3000,
		Tags:           []string{"sonarr"},
		AdditionalTags: []int{},
		StartOnAdd:     true,
	}
}

// Validate ports `FloodSettingsValidator` at C# `:11-21`.
//
// Rules:
//
//  1. Host non-empty.
//  2. Port 1..65535.
//  3. PostImportTags and Tags must not intersect (matches C#
//     `Must((s, p) => p.Intersect(s.Tags).Empty())` at `:18`).
//
// The C# rule list doesn't enforce Username/Password — Flood
// supports anonymous installs in some configurations.
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
	if len(s.Tags) > 0 && len(s.PostImportTags) > 0 {
		tagSet := map[string]bool{}
		for _, t := range s.Tags {
			tagSet[t] = true
		}
		for _, pit := range s.PostImportTags {
			if tagSet[pit] {
				failures = append(failures, downloadclients.TestFailure{
					PropertyName: "PostImportTags",
					ErrorMessage: "Post-Import Tags must not include any tags in the Tags list.",
				})
				break
			}
		}
	}
	return failures
}
