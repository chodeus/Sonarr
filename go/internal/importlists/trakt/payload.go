package trakt

// seriesIDs mirrors C# `TraktSeriesIdsResource` at `TraktAPI.cs:6-13`.
// Tvdb/Tmdb are nullable in C# — represented as *int here.
type seriesIDs struct {
	Trakt *int   `json:"trakt"`
	Slug  string `json:"slug"`
	Imdb  string `json:"imdb"`
	Tmdb  *int   `json:"tmdb"`
	Tvdb  *int   `json:"tvdb"`
}

// seriesResource mirrors C# `TraktSeriesResource` at `:15-22`.
type seriesResource struct {
	Title         string    `json:"title"`
	Year          *int      `json:"year"`
	IDs           seriesIDs `json:"ids"`
	AiredEpisodes int       `json:"aired_episodes"`
}

// showResponse mirrors C# `TraktResponse` at `:24-27`. Each entry
// wraps a TraktSeriesResource in a `.show` field.
type showResponse struct {
	Show seriesResource `json:"show"`
}

// watchedEpisode mirrors C# `TraktWatchedEpisodeResource` at `:29-32`.
type watchedEpisode struct {
	Plays *int `json:"plays"`
}

// watchedSeason mirrors C# `TraktWatchedSeasonResource` at `:34-38`.
type watchedSeason struct {
	Number   *int             `json:"number"`
	Episodes []watchedEpisode `json:"episodes"`
}

// watchedResponse mirrors C# `TraktWatchedResponse` at `:40-43`.
type watchedResponse struct {
	showResponse
	Seasons []watchedSeason `json:"seasons"`
}

// authToken mirrors C# `RefreshRequestResponse` at `:45-53`. Returned
// by the renew endpoint after refresh.
type authToken struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}
