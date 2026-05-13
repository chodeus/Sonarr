// Package simkl ports the C# `NzbDrone.Core.ImportLists.Simkl.User.SimklUserImport`
// concrete import-list provider. Slice 4.CR (sub-arc C4d of queue
// [C4]).
//
// Simkl User polls https://api.simkl.com/sync/all-items/{showType}/{listType}
// with the user's Bearer access token (OAuth2 brokered by
// auth.servarr.com). Tokens auto-refresh when Expires < now+5min
// per C# `SimklImportBase.Fetch:45-48`. Each /sync/activities call
// gates the actual /sync/all-items fetch with a "skip when no
// activity since last sync" heuristic per C# `:50-58` — the Go port
// retains the gate via an injected lastFetch hint.
//
// Slice scope: Settings + ListType/ShowType/AnimeType enums + Validate +
// Resource types + Proxy (GetUserList + GetLastActivity +
// RefreshAuthToken) + Provider (Fetch + Test + RequestAction no-op) +
// tests.
//
// Deferred (matches C5 #11):
//   - DB persistence of the refreshed token (in-memory only).
//   - Per-list LastInfoSync gate: the C# code calls
//     `_importListStatusService.GetListStatus(Definition.Id).LastInfoSync`
//     to decide whether to skip /sync/all-items based on the activity
//     timestamp. The Go-port [importliststatus.Service] doesn't yet
//     expose the per-list LastInfoSync field; the gate is DEFERRED.
//     The full /sync/all-items call fires on every Fetch — slightly
//     wasteful but correct.
//   - OAuth handshake admin path (startOAuth + getOAuthToken via
//     RequestAction) — no equivalent Provider slot yet.
package simkl

import (
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// Implementation is the value stored in the ImportLists.Implementation
// column. Mirrors C# class name `SimklUserImport` at
// `Simkl/User/SimklUserImport.cs:9`.
const Implementation = "SimklUserImport"

// ConfigContract is the value stored in the ImportLists.ConfigContract
// column. Mirrors C# class name `SimklUserSettings` at
// `Simkl/User/SimklUserSettings.cs:15`.
const ConfigContract = "SimklUserSettings"

// clientID is the Sonarr application's Simkl OAuth client identifier.
// Mirrors C# `SimklImportBase.ClientId` at
// `SimklImportBase.cs:23`. Sent as the simkl-api-key header on every
// API call.
const clientID = "3281c139f576b2f59c1389b22337140b6b087ee17e000e89dbafdcf20af6dac7"

// ListType mirrors the C# `SimklUserListType` enum at
// `User/SimklUserListType.cs:5-17`.
type ListType int

// ListType values mirror the C# enum names.
const (
	ListTypeWatching    ListType = 0
	ListTypePlanToWatch ListType = 1
	ListTypeHold        ListType = 2
	ListTypeCompleted   ListType = 3
	ListTypeDropped     ListType = 4
)

// queryString returns the Simkl-API path segment for the configured
// list type. Mirrors C# `((SimklUserListType)Settings.ListType).ToString().ToLowerInvariant()`
// at `SimklUserRequestGenerator.cs:24`.
func (lt ListType) queryString() string {
	switch lt {
	case ListTypeWatching:
		return "watching"
	case ListTypePlanToWatch:
		return "plantowatch"
	case ListTypeHold:
		return "hold"
	case ListTypeCompleted:
		return "completed"
	case ListTypeDropped:
		return "dropped"
	default:
		return ""
	}
}

// ShowType mirrors the C# `SimklUserShowType` enum at
// `User/SimklUserShowType.cs:3-7`. Stored as int on Settings.
type ShowType int

// ShowType values mirror C# enum names.
const (
	ShowTypeShows ShowType = 0
	ShowTypeAnime ShowType = 1
)

// queryString returns the Simkl-API path segment for the configured
// show type.
func (st ShowType) queryString() string {
	switch st {
	case ShowTypeShows:
		return "shows"
	case ShowTypeAnime:
		return "anime"
	default:
		return ""
	}
}

// Settings ports `SimklUserSettings` at `User/SimklUserSettings.cs:15-35`
// + the inherited fields from `SimklSettingsBase` at
// `SimklSettingsBase.cs:27-58`.
type Settings struct {
	// BaseURL is the Simkl API root. Defaults to
	// https://api.simkl.com per C# `:37`.
	BaseURL string `json:"baseUrl"`
	// ListType is the int-encoded [ListType] filter. C# `:26`.
	ListType int `json:"listType"`
	// ShowType is the int-encoded [ShowType] filter. C# `:29`.
	ShowType int `json:"showType"`
	// AccessToken is the OAuth2 access token — REQUIRED. C# `:40`.
	AccessToken string `json:"accessToken"`
	// RefreshToken is the OAuth2 refresh token. C# `:43`.
	RefreshToken string `json:"refreshToken"`
	// Expires is the absolute expiry timestamp for AccessToken.
	// C# `:46`. Note: Simkl doesn't currently expire tokens, but
	// the field is preserved per C# `:44`.
	Expires time.Time `json:"expires"`
	// AuthUser is the cached Simkl user-id slug. C# `:49`.
	AuthUser string `json:"authUser"`
	// SignIn is the placeholder for the OAuth field-type UI element.
	// C# `:52`.
	SignIn string `json:"signIn"`
}

// DefaultSettings returns Settings with the C# constructor defaults:
// BaseURL="https://api.simkl.com", ListType=Watching, ShowType=Shows.
func DefaultSettings() Settings {
	return Settings{
		BaseURL:  "https://api.simkl.com",
		ListType: int(ListTypeWatching),
		ShowType: int(ShowTypeShows),
	}
}

// Validate runs the C# `SimklSettingsBaseValidator` + `SimklUserSettingsValidator`
// rules at `SimklSettingsBase.cs:12-24` + `SimklUserSettings.cs:9-12`:
//
//   - BaseUrl is a valid http/https root URL.
//   - AccessToken non-empty (PropertyName override → "SignIn",
//     message "Must authenticate with Simkl" per C# `:17-18`).
//   - Expires non-zero when both AccessToken AND RefreshToken are set
//     (PropertyName override → "SignIn" per C# `:21-23`).
//   - ListType is a known [ListType] enum value.
//   - ShowType is a known [ShowType] enum value.
func (s *Settings) Validate() []importlists.TestFailure {
	var out []importlists.TestFailure
	if strings.TrimSpace(s.BaseURL) == "" || !isValidRootURL(s.BaseURL) {
		out = append(out, importlists.TestFailure{PropertyName: "BaseUrl", ErrorMessage: "Please enter a valid URL."})
	}
	if strings.TrimSpace(s.AccessToken) == "" {
		out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with Simkl"})
	} else if strings.TrimSpace(s.RefreshToken) != "" && s.Expires.IsZero() {
		out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with Simkl"})
	}
	if ListType(s.ListType).queryString() == "" {
		out = append(out, importlists.TestFailure{PropertyName: "ListType", ErrorMessage: "Invalid list type"})
	}
	if ShowType(s.ShowType).queryString() == "" {
		out = append(out, importlists.TestFailure{PropertyName: "ShowType", ErrorMessage: "Invalid show type"})
	}
	return out
}
