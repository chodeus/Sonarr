// Package myanimelist ports the C# `NzbDrone.Core.ImportLists.MyAnimeList`
// concrete import-list provider. Slice 4.CQ (sub-arc C4c of queue
// [C4]).
//
// MAL polls https://api.myanimelist.net/v2/users/@me/animelist with
// the user's Bearer access token (OAuth2 — 3-legged handshake brokered
// by auth.servarr.com). Auto-refresh fires when the access token is
// within 5 minutes of expiry, mirroring the C# `MyAnimeListImport.Fetch`
// check at `:37-40` + `RefreshToken` at `:88-119`.
//
// Slice scope: Settings + Status enum + Validate + Proxy (GetAnimeList
// + RefreshAuthToken) + Provider (Fetch + Test + RequestAction no-op)
// + tests.
//
// Deferred (matches C5 #11):
//   - DB persistence of the refreshed token. The C# code calls
//     `_importListRepository.UpdateSettings(...)` to round-trip the
//     new AccessToken/Expires/RefreshToken to disk. The Go port
//     refreshes in-memory only — next provider construction reads
//     the old token from DB and refreshes again. Acceptable given
//     the orchestrator caches per-process.
//   - The OAuth handshake admin path (startOAuth + getOAuthToken via
//     MyAnimeListImport.RequestAction) — no equivalent Provider slot
//     yet.
package myanimelist

import (
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// Implementation is the value stored in the ImportLists.Implementation
// column for rows wired to this provider. Mirrors C# class name
// `MyAnimeListImport` at `MyAnimeListImport.cs:14`.
const Implementation = "MyAnimeListImport"

// ConfigContract is the value stored in the ImportLists.ConfigContract
// column. Mirrors C# class name `MyAnimeListSettings` at
// `MyAnimeListSettings.cs:31`.
const ConfigContract = "MyAnimeListSettings"

// Status mirrors the C# `MyAnimeListStatus` enum at
// `MyAnimeListStatus.cs:5-25`. Stored as int on Settings.
type Status int

// Status enum values mirror C# enum-name → int.
const (
	StatusAll         Status = 0
	StatusWatching    Status = 1
	StatusCompleted   Status = 2
	StatusOnHold      Status = 3
	StatusDropped     Status = 4
	StatusPlanToWatch Status = 5
)

// queryString returns the MAL API status filter value or "" when
// no filter applies (All). Mirrors C# `MyAnimeListRequestGenerator.StatusMapping`
// at `MyAnimeListRequestGenerator.cs:10-17`.
func (s Status) queryString() string {
	switch s {
	case StatusWatching:
		return "watching"
	case StatusCompleted:
		return "completed"
	case StatusOnHold:
		return "on_hold"
	case StatusDropped:
		return "dropped"
	case StatusPlanToWatch:
		return "plan_to_watch"
	default:
		return ""
	}
}

// Settings ports `MyAnimeListSettings` at
// `MyAnimeListSettings.cs:31-58`.
type Settings struct {
	// BaseURL is the MAL API root. Defaults to
	// https://api.myanimelist.net/v2 per C# `:35`.
	BaseURL string `json:"baseUrl"`
	// ListStatus is the int-encoded [Status] filter. C# `:38`.
	ListStatus int `json:"listStatus"`
	// MinimumScore filters out anime with the user's score below
	// this threshold (0-10). C# `:41`.
	MinimumScore int `json:"minimumScore"`
	// AccessToken is the OAuth2 access token — REQUIRED. C# `:44`.
	AccessToken string `json:"accessToken"`
	// RefreshToken is the OAuth2 refresh token. C# `:47`.
	RefreshToken string `json:"refreshToken"`
	// Expires is the absolute expiry timestamp for AccessToken.
	// C# `:50`.
	Expires time.Time `json:"expires"`
	// SignIn is the placeholder for the OAuth field-type UI element.
	// C# `:53`.
	SignIn string `json:"signIn"`
}

// DefaultSettings returns Settings with the C# constructor defaults.
func DefaultSettings() Settings {
	return Settings{BaseURL: "https://api.myanimelist.net/v2"}
}

// Validate runs the C# `MalSettingsValidator` rules at `:8-28`:
//
//   - BaseUrl is a valid http/https root URL.
//   - AccessToken non-empty (the C# rule overrides the property name
//     to "SignIn" with the message "Must authenticate with MyAnimeList";
//     the Go port emits the override directly).
//   - MinimumScore in [0, 10] inclusive.
//   - ListStatus is a known Status enum value.
func (s *Settings) Validate() []importlists.TestFailure {
	var out []importlists.TestFailure
	if strings.TrimSpace(s.BaseURL) == "" || !isValidRootURL(s.BaseURL) {
		out = append(out, importlists.TestFailure{PropertyName: "BaseUrl", ErrorMessage: "Please enter a valid URL."})
	}
	if strings.TrimSpace(s.AccessToken) == "" {
		out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with MyAnimeList"})
	}
	if s.MinimumScore < 0 || s.MinimumScore > 10 {
		out = append(out, importlists.TestFailure{PropertyName: "MinimumScore", ErrorMessage: "Must be between 0 and 10"})
	}
	switch Status(s.ListStatus) {
	case StatusAll, StatusWatching, StatusCompleted, StatusOnHold, StatusDropped, StatusPlanToWatch:
	default:
		out = append(out, importlists.TestFailure{PropertyName: "ListStatus", ErrorMessage: "Invalid status"})
	}
	return out
}
