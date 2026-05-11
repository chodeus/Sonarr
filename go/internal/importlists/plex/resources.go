package plex

// API resource types ported from
// `src/NzbDrone.Core/Notifications/Plex/PlexTv/PlexWatchlistItem.cs:5-39`
// + `src/NzbDrone.Core/Notifications/Plex/Server/PlexResponse.cs:3-7`
// + the OAuth-handshake response shapes the C# code exposes through
// `IPlexTvService.GetPinUrl` / `GetSignInUrl`.

// watchlistEnvelope is the {"MediaContainer": ...} wrapper Plex's
// /library/sections/watchlist/all endpoint returns. Mirrors C#
// `PlexResponse<T>` at `PlexResponse.cs:3-7` (a generic single-
// property wrapper).
type watchlistEnvelope struct {
	MediaContainer WatchlistContainer `json:"MediaContainer"`
}

// WatchlistContainer mirrors C# `PlexWatchlistRespone` at
// `PlexWatchlistItem.cs:11-20`. Note: the C# class-name has a
// typo (the C# class is misspelled — single "n"); the Go port
// renames to WatchlistContainer since the typo serves no parity
// purpose at the Go-public level.
//
// The JSON wire key for the Items field is "Metadata" — Plex's
// envelope renames `Items` to that on the wire (C# `[JsonProperty
// ("Metadata")]` at `:13`).
type WatchlistContainer struct {
	Items []WatchlistItem `json:"Metadata"`
}

// WatchlistItem mirrors C# `PlexWatchlistItem` at
// `PlexWatchlistItem.cs:22-38`. JSON keys match the C#
// JsonProperty attributes — "ratingKey" → Id, "Guid" → Guids.
type WatchlistItem struct {
	// ID is the per-item ratingKey identifier (Plex internal id).
	// Currently unused by the Fetch path but carried for future
	// dedup / cross-reference use.
	ID string `json:"ratingKey"`

	// Title is the series title as the source list returned it.
	Title string `json:"title"`

	// Year is the series first-aired year (0 = unknown).
	Year int `json:"year"`

	// Guids carries the per-source GUID list — entries like
	// "tvdb://12345" / "imdb://tt6789012" / "tmdb://98765". The
	// parser extracts whichever ID prefixes match.
	Guids []ItemGuid `json:"Guid"`
}

// ItemGuid is one entry in WatchlistItem.Guids. Mirrors C#
// `PlexSectionItemGuid` at `PlexWatchlistItem.cs:6-9`.
type ItemGuid struct {
	ID string `json:"id"`
}

// PinURLResponse is the C# `PlexTvPinUrlResponse` shape returned
// by GetPinUrl. Carries the OAuth pin-poll URL + headers the v3 UI
// needs to launch the OAuth flow.
type PinURLResponse struct {
	Poll    bool              `json:"poll"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// SignInURLResponse is the C# `PlexTvSignInUrlResponse` shape
// returned by GetSignInUrl. The v3 UI redirects the browser to
// OauthUrl after the user clicks SignIn.
type SignInURLResponse struct {
	OauthURL string `json:"oauthUrl"`
	PinID    int    `json:"pinId"`
}

// pinResponse is the /api/v2/pins/{pinId} response containing the
// post-handshake authToken. Mirrors C# `PlexTvPinResponse`.
type pinResponse struct {
	AuthToken string `json:"authToken"`
}
