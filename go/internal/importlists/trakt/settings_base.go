// Package trakt ports the C# `NzbDrone.Core.ImportLists.Trakt`
// concrete import-list family (User / Popular / List variants).
// Slice 4.CT (sub-arc C4f of queue [C4] — CLOSES [C4]).
//
// All three variants share the [SettingsBase] OAuth2 + Limit fields
// + the auth.servarr.com refresh flow + the trakt-api-version=2 +
// trakt-api-key=ClientId headers. The per-variant Settings types
// extend SettingsBase with the type-specific filter fields.
//
// Deferred (matches C5 #11):
//   - DB persistence of refreshed tokens (in-memory only).
//   - The OAuth handshake admin paths (startOAuth + getOAuthToken via
//     RequestAction) — no equivalent Provider slot yet.
package trakt

import (
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// clientID is the Sonarr application's Trakt OAuth client identifier.
// Mirrors C# `TraktImportBase.ClientId` at `:22`. Sent as the
// trakt-api-key header on every API call.
const clientID = "d44ba57cab40c31eb3f797dcfccd203500796539125b333883ec1d94aa62ed4c"

// SettingsBase ports `TraktSettingsBase<T>` at
// `TraktSettingsBase.cs:37-72`. The three variant Settings types
// embed this struct.
type SettingsBase struct {
	// BaseURL is the Trakt API root. Defaults to https://api.trakt.tv.
	// C# `:48`.
	BaseURL string `json:"baseUrl"`
	// AccessToken is the OAuth2 access token. C# `:51`.
	AccessToken string `json:"accessToken"`
	// RefreshToken is the OAuth2 refresh token. C# `:54`.
	RefreshToken string `json:"refreshToken"`
	// Expires is the absolute expiry timestamp for AccessToken.
	// C# `:57`.
	Expires time.Time `json:"expires"`
	// AuthUser is the cached Trakt user-slug from the last successful
	// OAuth handshake. Used as the per-list username fallback by
	// the User variant. C# `:60`.
	AuthUser string `json:"authUser"`
	// Limit caps the per-poll fetch size. Defaults to 100. C# `:63`.
	Limit int `json:"limit"`
	// SignIn is the placeholder for the OAuth field-type UI element.
	// C# `:66`.
	SignIn string `json:"signIn"`
}

// defaultSettingsBase returns SettingsBase with the C# constructor
// defaults: BaseURL=https://api.trakt.tv, Limit=100.
func defaultSettingsBase() SettingsBase {
	return SettingsBase{BaseURL: "https://api.trakt.tv", Limit: 100}
}

// validateBase runs the C# `TraktSettingsBaseValidator` rules:
//
//   - BaseUrl is a valid http/https root URL.
//   - AccessToken non-empty implies RefreshToken non-empty implies
//     Expires non-zero (chained `.When(...)` per C# `:16-28`). All
//     three failures use PropertyName="SignIn".
//   - Limit > 0.
func (s *SettingsBase) validateBase() []importlists.TestFailure {
	var out []importlists.TestFailure
	if strings.TrimSpace(s.BaseURL) == "" || !isValidRootURL(s.BaseURL) {
		out = append(out, importlists.TestFailure{PropertyName: "BaseUrl", ErrorMessage: "Please enter a valid URL."})
	}
	switch {
	case strings.TrimSpace(s.AccessToken) == "":
		out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with Trakt"})
	case strings.TrimSpace(s.RefreshToken) == "":
		out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with Trakt"})
	case s.Expires.IsZero():
		out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with Trakt"})
	}
	if s.Limit <= 0 {
		out = append(out, importlists.TestFailure{PropertyName: "Limit", ErrorMessage: "Must be integer greater than 0"})
	}
	return out
}
