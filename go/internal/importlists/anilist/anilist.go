// Package anilist ports the C# `NzbDrone.Core.ImportLists.AniList.List.AniListImport`
// concrete import-list provider. Slice 4.CS (sub-arc C4e of queue
// [C4]).
//
// AniList is the only [C4] provider that uses GraphQL — POSTs a
// canned QueryList GraphQL document to https://graphql.anilist.co
// with the user's anime list filters as variables. Pagination is
// handled via the response's PageInfo.HasNextPage flag (max 50
// entries per page).
//
// OAuth2 refresh fires when Settings.Expires < now+5min per C#
// `AniListImportBase.CheckToken:78`. AniList serves PUBLIC user lists
// without an access token, so providers without auth can still
// fetch.
//
// Slice scope: Settings + per-status filter rules + Validate + GraphQL
// query + Resource types + Proxy (fetchPage + refreshAuthToken) +
// Provider (Fetch + Test + RequestAction no-op) + tests.
//
// Deferred (matches C5 #11):
//   - DB persistence of refreshed tokens (in-memory only).
//   - OAuth handshake admin path (startOAuth + getOAuthToken via
//     RequestAction) — no equivalent Provider slot yet.
package anilist

import (
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// Implementation is the value stored in the ImportLists.Implementation
// column. Mirrors C# class name `AniListImport` at
// `AniList/List/AniListImport.cs:20`.
const Implementation = "AniListImport"

// ConfigContract is the value stored in the ImportLists.ConfigContract
// column. Mirrors C# class name `AniListSettings` at
// `AniList/List/AniListSettings.cs:19`.
const ConfigContract = "AniListSettings"

// MediaListStatus values mirror the C# constants at
// `AniListTypes.cs:5-18`. Used in the GraphQL `status_in` variable.
const (
	MediaListStatusCurrent   = "CURRENT"
	MediaListStatusPlanning  = "PLANNING"
	MediaListStatusCompleted = "COMPLETED"
	MediaListStatusDropped   = "DROPPED"
	MediaListStatusPaused    = "PAUSED"
	MediaListStatusRepeating = "REPEATING"
)

// MediaStatus values mirror the C# constants at `AniListTypes.cs:20-31`.
// Used to post-filter the GraphQL response (AniList doesn't support
// filtering on Media.Status at the query level per C# `:42`).
const (
	MediaStatusFinished   = "FINISHED"
	MediaStatusReleasing  = "RELEASING"
	MediaStatusUnreleased = "NOT_YET_RELEASED"
	MediaStatusCancelled  = "CANCELLED"
	MediaStatusHiatus     = "HIATUS"
)

// Settings ports the C# `AniListSettings` at `List/AniListSettings.cs:19-74`
// + the inherited fields from `AniListSettingsBase` at `:32-60`.
type Settings struct {
	// BaseURL is the AniList GraphQL endpoint. Defaults to
	// https://graphql.anilist.co per C# `:42`.
	BaseURL string `json:"baseUrl"`
	// AccessToken is the OAuth2 access token. C# `:45`.
	AccessToken string `json:"accessToken"`
	// RefreshToken is the OAuth2 refresh token. C# `:48`.
	RefreshToken string `json:"refreshToken"`
	// Expires is the absolute expiry timestamp for AccessToken.
	// C# `:51`.
	Expires time.Time `json:"expires"`
	// SignIn is the placeholder for the OAuth field-type UI element.
	// C# `:54`.
	SignIn string `json:"signIn"`
	// Username is the AniList username whose list to fetch — REQUIRED.
	// C# `:34`.
	Username string `json:"username"`

	// Per-status import toggles. The first six map to the GraphQL
	// `status_in` filter (server-side); the last five filter the
	// returned media on the client-side per C# `AniListParser.cs:63-91`.
	ImportCurrent    bool `json:"importCurrent"`
	ImportPlanning   bool `json:"importPlanning"`
	ImportCompleted  bool `json:"importCompleted"`
	ImportDropped    bool `json:"importDropped"`
	ImportPaused     bool `json:"importPaused"`
	ImportRepeating  bool `json:"importRepeating"`
	ImportFinished   bool `json:"importFinished"`
	ImportReleasing  bool `json:"importReleasing"`
	ImportUnreleased bool `json:"importUnreleased"`
	ImportCancelled  bool `json:"importCancelled"`
	ImportHiatus     bool `json:"importHiatus"`
}

// DefaultSettings returns Settings with the C# constructor defaults
// (`AniListSettings():25-31`): BaseUrl=graphql.anilist.co,
// ImportCurrent=ImportPlanning=ImportReleasing=ImportFinished=true.
func DefaultSettings() Settings {
	return Settings{
		BaseURL:         "https://graphql.anilist.co",
		ImportCurrent:   true,
		ImportPlanning:  true,
		ImportReleasing: true,
		ImportFinished:  true,
	}
}

// Validate runs the C# `AniListSettingsBaseValidator` + `AniListSettingsValidator`
// rules:
//
//   - BaseUrl is a valid http/https root URL.
//   - AccessToken non-empty implies RefreshToken non-empty implies
//     Expires non-zero (chained `.When(...)` per C# `:20-28`). The
//     three failures all have PropertyName="SignIn".
//   - Username non-empty per `AniListSettings:11`.
//   - At least one ImportListStatus toggle (Current/Planning/Completed/
//     Dropped/Paused/Repeating) must be true per `:13-15`.
//
// AniList serves PUBLIC user lists without an access token — the
// AccessToken-required rule is the C# default for the OAuth UI flow
// but is non-fatal for read-only fetches. The Go port matches the C#
// validator exactly (AccessToken non-empty IS required) since the
// auth fields are populated by the OAuth handshake admin path the C#
// UI orchestrates.
func (s *Settings) Validate() []importlists.TestFailure {
	var out []importlists.TestFailure
	if strings.TrimSpace(s.BaseURL) == "" || !isValidRootURL(s.BaseURL) {
		out = append(out, importlists.TestFailure{PropertyName: "BaseUrl", ErrorMessage: "Please enter a valid URL."})
	}
	if strings.TrimSpace(s.AccessToken) == "" {
		out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with AniList"})
	} else {
		if strings.TrimSpace(s.RefreshToken) == "" {
			out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with AniList"})
		} else if s.Expires.IsZero() {
			out = append(out, importlists.TestFailure{PropertyName: "SignIn", ErrorMessage: "Must authenticate with AniList"})
		}
	}
	if strings.TrimSpace(s.Username) == "" {
		out = append(out, importlists.TestFailure{PropertyName: "Username", ErrorMessage: "'Username' must not be empty."})
	}
	if !s.ImportCurrent && !s.ImportPlanning && !s.ImportCompleted &&
		!s.ImportDropped && !s.ImportPaused && !s.ImportRepeating {
		out = append(out, importlists.TestFailure{PropertyName: "ImportCurrent", ErrorMessage: "At least one status type must be selected"})
	}
	return out
}

// buildStatusFilter assembles the GraphQL `status_in` variable from
// the six list-status toggles. Mirrors C#
// `AniListRequestGenerator.BuildStatusFilter` at `:52-87`.
func (s *Settings) buildStatusFilter() []string {
	var filters []string
	if s.ImportCompleted {
		filters = append(filters, MediaListStatusCompleted)
	}
	if s.ImportCurrent {
		filters = append(filters, MediaListStatusCurrent)
	}
	if s.ImportDropped {
		filters = append(filters, MediaListStatusDropped)
	}
	if s.ImportPaused {
		filters = append(filters, MediaListStatusPaused)
	}
	if s.ImportPlanning {
		filters = append(filters, MediaListStatusPlanning)
	}
	if s.ImportRepeating {
		filters = append(filters, MediaListStatusRepeating)
	}
	return filters
}

// allowMediaStatus reports whether a media.Status value is enabled
// in Settings. Mirrors C# `AniListParser.ValidateMediaStatus` at
// `:63-91`.
func (s *Settings) allowMediaStatus(status string) bool {
	switch status {
	case MediaStatusFinished:
		return s.ImportFinished
	case MediaStatusReleasing:
		return s.ImportReleasing
	case MediaStatusUnreleased:
		return s.ImportUnreleased
	case MediaStatusCancelled:
		return s.ImportCancelled
	case MediaStatusHiatus:
		return s.ImportHiatus
	default:
		return false
	}
}
