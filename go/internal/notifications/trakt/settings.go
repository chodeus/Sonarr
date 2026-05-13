// Package trakt ports the C# `NzbDrone.Core.Notifications.Trakt`
// concrete notification provider. Slice 4.CM (C-cleanup [C3]
// sub-arc C3l).
//
// Surface: Settings + Validate + Resource types + Proxy (Bearer-auth
// POST to api.trakt.tv/sync/collection (+/remove); GET users/settings
// for verification; auto-renewal via auth.servarr.com when token is
// near expiry) + the 5 OnX hooks Trakt overrides
// (OnDownload, OnImportComplete, OnEpisodeFileDelete, OnSeriesAdd,
// OnSeriesDelete). The other 6 are no-ops with SupportsOnX false.
//
// Deferred (matches C5 #11):
//   - Refresh-token persistence: the C# code calls
//     `_notificationRepository.UpdateSettings(...)` to persist the
//     refreshed token. The Go port refreshes in-memory only — next
//     provider construction reads the old token and refreshes again.
//     Acceptable given the orchestrator caches per-process.
//   - MediaInfo-derived fields (resolution/hdr/audio/audioChannels)
//     on the episode-file collection payload — EpisodeFile.MediaInfo
//     hasn't ported (it's a raw JSON blob); episodefile.Quality is
//     also a raw JSON string. The Go port emits the bare
//     show/season/episode payload without these enrichments.
//   - The OAuth-handshake admin path (startOAuth + getOAuthToken via
//     Trakt.RequestAction) — no equivalent Provider slot yet.
package trakt

import (
	"time"

	"sonarr.tv/sonarr/internal/notifications"
)

// Settings ports `TraktSettings` at
// `Notifications/Trakt/TraktSettings.cs:18-46`.
type Settings struct {
	// AccessToken is the per-user OAuth2 access token — REQUIRED.
	// C# `:28`.
	AccessToken string `json:"accessToken"`
	// RefreshToken is the OAuth2 refresh token — REQUIRED. C# `:31`.
	RefreshToken string `json:"refreshToken"`
	// Expires is the absolute expiry timestamp for AccessToken —
	// REQUIRED (non-zero). C# `:34`.
	Expires time.Time `json:"expires"`
	// AuthUser is the cached Trakt username slug from the last
	// successful GetUserName call. C# `:37`.
	AuthUser string `json:"authUser"`
	// SignIn is the placeholder for the OAuth field-type UI element.
	// C# `:40`.
	SignIn string `json:"signIn"`
}

// Validate ports the C# `TraktSettingsValidator` at `:8-15`.
//
// Rules:
//   - AccessToken non-empty.
//   - RefreshToken non-empty.
//   - Expires non-zero.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if s.AccessToken == "" {
		out = append(out, notifications.TestFailure{PropertyName: "AccessToken", ErrorMessage: "'AccessToken' must not be empty."})
	}
	if s.RefreshToken == "" {
		out = append(out, notifications.TestFailure{PropertyName: "RefreshToken", ErrorMessage: "'RefreshToken' must not be empty."})
	}
	if s.Expires.IsZero() {
		out = append(out, notifications.TestFailure{PropertyName: "Expires", ErrorMessage: "'Expires' must not be empty."})
	}
	return out
}
