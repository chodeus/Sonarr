// Package plex ports the C# `NzbDrone.Core.ImportLists.Plex`
// concrete import-list provider — Plex watchlist via plex.tv
// OAuth. Slice 4.AN (sub-arc I3b of queue [I3]).
//
// Scope at sub-arc I3b:
//   - [Settings] + Validate ports `PlexListSettings.cs:7-15` —
//     AccessToken NotEmpty with PropertyName="SignIn" (the C# code
//     overrides the failure property so the v3 UI surfaces the
//     SignIn OAuth button as the failing field).
//   - [Proxy] ports `PlexTvService` at
//     `Notifications/Plex/PlexTv/PlexTvService.cs:22-139` — the
//     subset the import-list family needs: GetPinUrl,
//     GetSignInUrl, GetAuthToken, GetWatchlist, Ping. The Plex
//     server-management subset (GetServers / GetResources / etc.)
//     is out of scope for queue [I3]; it lives in the
//     notifications/plex follow-up.
//   - [parseResponse] ports `PlexParser.cs:12-78` — extracts
//     tvdb/tmdb/imdb GUID values from each PlexWatchlistItem and
//     returns the matching [importlists.ItemInfo].
//   - [PlexImport] satisfies [importlists.Provider]. Fetch paginates
//     up to MaxPages=10 with PageSize=100, builds the per-page
//     []ItemInfo, runs through importlists.Dedup, stamps in
//     ImportListID/ImportList. RecordSuccess/RecordFailure call-
//     sites match the C# HttpImportListBase.FetchItems behaviour
//     even though the Go port inlines the pagination.
//   - RequestAction handles 4 OAuth actions startOAuth /
//     continueOAuth / pollOAuth / getOAuthToken per
//     `PlexImport.cs:55-118`.
//
// Family pattern validated by this sub-arc + queue [I3]:
//   - The (Implementation, ConfigContract) catalog now ships both
//     a non-OAuth provider (SonarrImport) and an OAuth-flow
//     provider (PlexImport) — the queue [I4] sync orchestrator can
//     consume either without further per-family branching.
//
// Out-of-scope:
//   - PlexClientIdentifier: C# reads it from IConfigService —
//     a per-install GUID Sonarr generates at first start. The Go
//     port reads it from deps.ConfigProvider via a narrow
//     interface; nil-tolerated, falls back to "sonarr" (Plex does
//     not validate the value strictly).
//   - Ping with 24-hour cache: C# wraps GetWatchlist + GetServers
//     with a Ping() that's coalesced via ICacheManager. The Go
//     port ports Ping but does NOT add the cache layer — every
//     GetWatchlist call invokes Ping. Acceptable because the
//     orchestrator's MinRefreshInterval is 6h, so the worst case
//     is 4 Ping calls per day per list — well under any rate-
//     limit. A cache layer can be added when ICacheManager ports.
//   - BadRequestException: C# throws BadRequestException on bad
//     query params. The Go port surfaces this as a typed
//     [ErrBadRequest] return for caller-side classification.
//   - PlexWatchlistRespone preserves the C# typo (single "n")
//     at the public-name level for grep parity; internal Go
//     naming uses watchlistResponse.
package plex

import (
	"strings"

	"sonarr.tv/sonarr/internal/importlists"
)

// Implementation is the value stored in the ImportLists.Implementation
// column for rows wired to this provider. Mirrors the C# class
// name `PlexImport` at `ImportLists/Plex/PlexImport.cs:15`.
const Implementation = "PlexImport"

// ConfigContract is the value stored in the ImportLists.ConfigContract
// column. Mirrors the C# `PlexListSettings` type-name at
// `ImportLists/Plex/PlexListSettings.cs:17`.
const ConfigContract = "PlexListSettings"

// Settings ports `PlexListSettings` at
// `src/NzbDrone.Core/ImportLists/Plex/PlexListSettings.cs:17-40`.
//
// Field order + JSON tag names match the persisted Settings JSON.
// Defaults mirror the C# constructor at `:21-24` — SignIn defaults
// to "startOAuth" (the v3 React UI keys the OAuth button off this
// value).
//
// BaseURL is intentionally NOT exposed in the v3 UI (C# marks it
// hidden in the FieldDefinition attributes); plex.tv hosts are
// hardcoded in the proxy.
type Settings struct {
	// BaseURL is the C# base-class hidden field. Persisted but
	// not user-visible.
	BaseURL string `json:"baseUrl"`

	// AccessToken is the Plex OAuth bearer token. Empty until the
	// user completes the OAuth handshake via the SignIn UI button.
	// C# `:31` — Hidden=HiddenIfNotSet.
	AccessToken string `json:"accessToken"`

	// SignIn is the C# `:34` OAuth-button field. Defaults to
	// "startOAuth"; the v3 UI mutates this through the RequestAction
	// flow during the handshake.
	SignIn string `json:"signIn"`
}

// DefaultSettings returns a Settings struct populated with the C#
// constructor defaults at `PlexListSettings.cs:21-24` — SignIn =
// "startOAuth", every other field empty.
func DefaultSettings() Settings {
	return Settings{SignIn: "startOAuth"}
}

// Validate ports the C# `PlexListSettingsValidator` at
// `PlexListSettings.cs:7-15`.
//
// C# defines one rule: `RuleFor(c => c.AccessToken).NotEmpty()
// .OverridePropertyName("SignIn").WithMessage("Must authenticate
// with Plex")`. The OverridePropertyName surfaces the failure on
// the SignIn field so the v3 UI highlights the OAuth-button row.
//
// The Go port mirrors the override exactly — PropertyName is
// "SignIn", not "AccessToken".
func (s *Settings) Validate() []importlists.TestFailure {
	if strings.TrimSpace(s.AccessToken) == "" {
		return []importlists.TestFailure{{
			PropertyName: "SignIn",
			ErrorMessage: "Must authenticate with Plex",
		}}
	}
	return nil
}
