package trakt

import "time"

// CollectShowsPayload ports the C# `TraktCollectShowsResource` at
// `Notifications/Trakt/Resource/TraktCollectShowsResource.cs`.
type CollectShowsPayload struct {
	Shows []CollectShow `json:"shows"`
}

// CollectShow ports `TraktCollectShow` at
// `Resource/TraktCollectShowResource.cs`.
type CollectShow struct {
	Title   string        `json:"title"`
	Year    int           `json:"year"`
	IDs     ShowIDs       `json:"ids"`
	Seasons []SeasonResrc `json:"seasons,omitempty"`
}

// ShowIDs ports `TraktShowIdsResource` at
// `Resource/TraktShowIdsResource.cs`. Tvdb is an int per Trakt API;
// Imdb is the tt-prefixed string slug.
type ShowIDs struct {
	Tvdb int    `json:"tvdb"`
	Imdb string `json:"imdb"`
}

// SeasonResrc ports `TraktSeasonResource` at
// `Resource/TraktSeasonResource.cs`.
type SeasonResrc struct {
	Number   int               `json:"number"`
	Episodes []EpisodeResource `json:"episodes"`
}

// EpisodeResource ports `TraktEpisodeResource` at
// `Resource/TraktEpisodeResource.cs`. The MediaInfo-derived fields
// (Resolution / Hdr / MediaType / Audio / AudioChannels) are DEFERRED
// — emitted as empty strings (omitempty drops them) until the
// upstream MediaInfo decode ports.
type EpisodeResource struct {
	Number        int        `json:"number"`
	CollectedAt   *time.Time `json:"collected_at,omitempty"`
	Resolution    string     `json:"resolution,omitempty"`
	Hdr           string     `json:"hdr,omitempty"`
	MediaType     string     `json:"media_type,omitempty"`
	AudioChannels string     `json:"audio_channels,omitempty"`
	Audio         string     `json:"audio,omitempty"`
}

// AuthRefreshResource ports `TraktAuthRefreshResource` at
// `Resource/TraktAuthRefreshResource.cs`. Token-renewal response
// from auth.servarr.com.
type AuthRefreshResource struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// UserSettingsResource ports `TraktUserSettingsResource` at
// `Resource/TraktUserSettingsResource.cs`.
type UserSettingsResource struct {
	User UserResource `json:"user"`
}

// UserResource ports `TraktUserResource` at
// `Resource/TraktUserResource.cs`. Only the slug is consumed by the
// Go port (used by Test() to verify auth).
type UserResource struct {
	IDs UserIDs `json:"ids"`
}

// UserIDs ports `TraktUserIdsResource` at
// `Resource/TraktUserIdsResource.cs`.
type UserIDs struct {
	Slug string `json:"slug"`
}
