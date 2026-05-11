// Package sabnzbd ports the C# `NzbDrone.Core.Download.Clients.Sabnzbd`
// download client. Slice 4.X (sub-arc 14a of queue [14]).
//
// Scope at sub-arc 14a:
//   - [Settings] + Validate ports `SabnzbdSettings.cs` field-by-field.
//   - [Proxy] ports the HTTP wrapper for the Download + Test paths
//     (DownloadNzb, GetVersion, GetConfig). GetQueue/GetHistory/
//     RemoveFromQueue/RemoveFromHistory/RetryDownload/GetFullStatus
//     are deferred — they're consumed only by GetItems / GetStatus /
//     RemoveItem which the Provider stubs in this slice.
//   - [Sabnzbd] Provider implements the [downloadclients.Provider]
//     interface. Name / Protocol / Definition / Download / Test are
//     live; GetItems / GetStatus / RemoveItem / GetImportItem /
//     MarkItemAsImported return [downloadclients.ErrNotImplementedYet]
//     until sub-arc 14b (or queue [15] consumer demand).
//
// Family pattern validated by this sub-arc:
//   - init() time registration into [downloadclients.Default].
//   - Settings deserialisation from the persisted JSON
//     (DownloadClient.Settings column).
//   - HTTP proxy wrapper that consumes net/http with a per-test
//     httptest.Server-mockable transport.
//   - Provider.Test surfaces FluentValidation-style failures.
//
// C# cites lead each type with a file:line reference.
package sabnzbd

import (
	"errors"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Settings ports `SabnzbdSettings` at
// `src/NzbDrone.Core/Download/Clients/Sabnzbd/SabnzbdSettings.cs:34-85`.
//
// Field order + JSON tag names match the persisted Settings JSON the
// DownloadClient row's Settings column carries (the React UI form
// posts JSON keys lowerCamelCase matching the C# property names).
//
// Default values mirror the C# constructor at `:38-45`:
// Host=localhost, Port=8080, TvCategory="tv", RecentTvPriority=
// SabnzbdPriorityDefault, OlderTvPriority=SabnzbdPriorityDefault.
type Settings struct {
	downloadclients.BaseUsenetSettings

	// Host is the SABnzbd hostname or IP. C# `:48`.
	Host string `json:"host"`
	// Port is the SABnzbd HTTP port (1-65535). C# `:51`.
	Port int `json:"port"`
	// UseSsl flips http→https for the API URL. C# `:55`.
	UseSsl bool `json:"useSsl"`
	// UrlBase is the optional URL prefix (when SAB is reverse-
	// proxied behind /sabnzbd/). C# `:60`.
	UrlBase string `json:"urlBase"`
	// APIKey is the preferred auth path. C# `:63`. Either APIKey
	// OR (Username + Password) must be set; validation enforces
	// that at least one path is present.
	APIKey string `json:"apiKey"`
	// Username is the legacy auth username. C# `:66`.
	Username string `json:"username"`
	// Password is the legacy auth password. C# `:69`.
	Password string `json:"password"`
	// TvCategory is the SAB category Sonarr posts every grab under.
	// C# `:72`.
	TvCategory string `json:"tvCategory"`
	// RecentTvPriority is the SAB queue priority for recent
	// episodes (default = Default). C# `:75`.
	RecentTvPriority int `json:"recentTvPriority"`
	// OlderTvPriority is the SAB queue priority for older
	// episodes (default = Default). C# `:78`.
	OlderTvPriority int `json:"olderTvPriority"`
}

// NewSettings returns a Settings with the C# constructor defaults at
// `SabnzbdSettings.cs:38-45`. Callers deserialising from JSON should
// initialise via `s := NewSettings(); json.Unmarshal(...)` so any
// missing JSON fields fall back to the defaults.
func NewSettings() Settings {
	return Settings{
		Host:             "localhost",
		Port:             8080,
		TvCategory:       "tv",
		RecentTvPriority: PriorityDefault,
		OlderTvPriority:  PriorityDefault,
	}
}

// Validate ports `SabnzbdSettingsValidator` at C# `:8-32`. Returns
// the FluentValidation-style failure list (empty = valid). Matches
// the C# rule ordering so error reporting is consistent.
//
// Rules:
//
//  1. Host must be non-empty (the C# ValidHost extension checks the
//     same plus DNS-label format; the Go port checks non-empty +
//     no whitespace as a tighter, deterministic shape).
//  2. Port must be 1-65535. C# `RuleFor(c => c.Port).InclusiveBetween(1, 65535)`.
//  3. UrlBase, when present, must start with "/" — the C#
//     ValidUrlBase extension enforces this.
//  4. Either APIKey OR (Username AND Password) must be present.
//     Mirrors C# `:16-26` cross-field validation.
//  5. TvCategory empty → warning ("A category is recommended").
//     Mirrors C# `:28-30` AsWarning(); the Go port surfaces this
//     in the same returned list.
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

	apiKeySet := strings.TrimSpace(s.APIKey) != ""
	usernameSet := strings.TrimSpace(s.Username) != ""
	passwordSet := strings.TrimSpace(s.Password) != ""

	if !apiKeySet && !usernameSet {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "ApiKey",
			ErrorMessage: "API Key is required when username/password are not configured",
		})
	}
	if !apiKeySet && usernameSet && !passwordSet {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Password",
			ErrorMessage: "Password is required when API key is not configured",
		})
	}
	if !apiKeySet && passwordSet && !usernameSet {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Username",
			ErrorMessage: "Username is required when API key is not configured",
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

// ErrInvalidSettings is returned by [Sabnzbd.Test] / Download
// constructors when the bound Settings fail Validate. Callers wrap
// it with the field-level failures for the v3 API.
var ErrInvalidSettings = errors.New("sabnzbd: invalid settings")

// Priority constants port the C# `SabnzbdPriority` enum at
// `SabnzbdPriority.cs:1-12`. Numeric order matches the SAB API
// values which are wire-stable.
const (
	// PriorityDefault — SAB falls back to the category's configured
	// priority.
	PriorityDefault = -100
	// PriorityPaused enqueues but doesn't start.
	PriorityPaused = -2
	// PriorityLow.
	PriorityLow = -1
	// PriorityNormal.
	PriorityNormal = 0
	// PriorityHigh.
	PriorityHigh = 1
	// PriorityForce bypasses the queue cap.
	PriorityForce = 2
)
