// Package nzbget ports the C# NZBGet download client at
// `src/NzbDrone.Core/Download/Clients/Nzbget/`. NZBGet is the
// third-most-common download client in real-world Sonarr
// deployments — and the most-common usenet-side client after
// SABnzbd. Closing this arc alongside the already-shipped
// SABnzbd (sub-arc 14a) + Transmission (14b) + qBittorrent (5.G1)
// + Deluge (5.G2) gets the Go port to ~75% of real-world
// download-client deployments.
//
// Phase 5.G3 slice plan (each slice = one commit + tests):
//
//   - 5.G3a (this slice): Settings + Validate +
//     [downloadclients.Default] registration + a minimal Provider
//     skeleton (Name / Protocol / Definition / RequestAction +
//     GetImportItem live; Download / Test / GetItems / RemoveItem /
//     GetStatus / MarkItemAsImported return
//     [downloadclients.ErrNotImplementedYet]).
//   - 5.G3b: JSON-RPC transport with HTTP Basic auth (much simpler
//     than Deluge's session cookie — NZBGet's /jsonrpc endpoint
//     accepts standard Basic auth headers).
//   - 5.G3c: Test() — version + connectivity probe.
//   - 5.G3d: GetItems() — listgroups + history merge.
//   - 5.G3e: Download() — append + per-grab options.
//   - 5.G3f: RemoveItem() — editqueue with the right action.
//   - 5.G3g: GetStatus() — config + status RPC.
//   - 5.G3h: MarkItemAsImported() — best-effort category re-tag.
//
// NZBGet is a usenet client (Protocol=Usenet) — Settings embeds
// [downloadclients.BaseUsenetSettings] (vs Torrent in 5.G1/5.G2/14b).
//
// C# cites lead each type with file:line references.
package nzbget

import (
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Settings ports `NzbgetSettings` at
// `src/NzbDrone.Core/Download/Clients/Nzbget/NzbgetSettings.cs:22-67`.
//
// Defaults mirror the C# constructor at `:27-33`:
// Host=localhost, Port=6789, TvCategory=tv, RecentTvPriority=Normal,
// OlderTvPriority=Normal.
//
// Auth shape: NZBGet uses HTTP Basic auth — Username + Password
// attached as the standard Authorization header on every request
// (no session token, no login flow). The validator at C# `:14-15`
// enforces that Username and Password must be co-set (both empty
// OR both non-empty).
//
// NOTE: the C# default Username/Password (`nzbget` / `tegbzn6789`)
// are the NZBGet install defaults but the C# Settings does NOT
// initialize them — the form defaults to empty, and the user must
// fill them in. The Go port mirrors.
type Settings struct {
	downloadclients.BaseUsenetSettings

	// Host is the NZBGet host. C# `:36`.
	Host string `json:"host"`
	// Port is the NZBGet port (default 6789). C# `:39`.
	Port int `json:"port"`
	// UseSsl flips http→https. C# `:43`.
	UseSsl bool `json:"useSsl"`
	// UrlBase is the optional URL prefix (C# `:48`). Empty when
	// NZBGet is served at the root; "/nzbget" or similar when the
	// WebUI lives behind a reverse-proxy path.
	UrlBase string `json:"urlBase"`
	// Username is the HTTP-Basic auth username. C# `:51`. Empty
	// when no auth is configured server-side.
	Username string `json:"username"`
	// Password is the HTTP-Basic auth password. C# `:54`.
	Password string `json:"password"`
	// TvCategory is applied as the NZBGet "category" on every
	// added NZB. Default "tv" per C# `:30`.
	TvCategory string `json:"tvCategory"`
	// RecentTvPriority / OlderTvPriority pick between NZBGet's
	// priority tiers based on Subject.IsRecent. C# `:60,63`. See
	// [Priority*] constants.
	RecentTvPriority int `json:"recentTvPriority"`
	OlderTvPriority  int `json:"olderTvPriority"`
	// AddPaused starts NZBs in the paused state. C# `:66`.
	AddPaused bool `json:"addPaused"`
}

// NewSettings returns a Settings with the C# constructor defaults
// at `NzbgetSettings.cs:27-33`. JSON-unmarshalling onto this
// preserves the defaults for any missing fields.
func NewSettings() Settings {
	return Settings{
		Host:             "localhost",
		Port:             6789,
		TvCategory:       "tv",
		RecentTvPriority: PriorityNormal,
		OlderTvPriority:  PriorityNormal,
	}
}

// Validate ports `NzbgetSettingsValidator` at C# `:8-19`. Returns
// the FluentValidation-style failure list.
//
// Rules:
//
//  1. Host non-empty (the C# ValidHost extension also checks
//     DNS-label format; the Go port checks non-empty + no
//     whitespace).
//  2. Port 1-65535 (C# `:13`).
//  3. UrlBase must start with "/" when non-empty (C# `:14`).
//  4. Username + Password must be co-set — empty when one is
//     empty AND non-empty when the other is non-empty (C# `:16,17`).
//  5. TvCategory empty surfaces as a warning, not an error
//     (C# `:19 .AsWarning()`). The Go port surfaces this as a
//     TestFailure with PropertyName="TvCategory" — the v3 React
//     UI doesn't distinguish warning vs error at the field-level
//     surface yet, but the C# semantics are preserved by the
//     non-fatal error message text.
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

	if ub := strings.TrimSpace(s.UrlBase); ub != "" && !strings.HasPrefix(ub, "/") {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "UrlBase",
			ErrorMessage: "URL base must start with '/'",
		})
	}

	user := strings.TrimSpace(s.Username)
	pass := strings.TrimSpace(s.Password)
	if user != "" && pass == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Password",
			ErrorMessage: "Password is required when Username is set",
		})
	}
	if pass != "" && user == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Username",
			ErrorMessage: "Username is required when Password is set",
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

// Priority constants port `NzbgetPriority.cs:3-11`. NZBGet uses
// signed integer priorities ranging from -100 (VeryLow) to 900
// (Force) — the C# enum carries the wire values directly. The Go
// port preserves the numeric scale.
const (
	PriorityVeryLow  = -100
	PriorityLow      = -50
	PriorityNormal   = 0
	PriorityHigh     = 50
	PriorityVeryHigh = 100
	PriorityForce    = 900
)
