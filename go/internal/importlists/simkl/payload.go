package simkl

// listResponse mirrors C# `SimklResponse` at `SimklAPI.cs:33-37`.
// The /sync/all-items endpoint may return EITHER a Shows-array OR an
// Anime-array depending on the show-type segment.
type listResponse struct {
	Shows []seriesResource `json:"shows"`
	Anime []seriesResource `json:"anime"`
}

// seriesResource mirrors C# `SimklSeriesResource` at `:25-31`.
type seriesResource struct {
	Show      seriesPropsResource `json:"show"`
	AnimeType string              `json:"anime_type,omitempty"`
}

// seriesPropsResource mirrors C# `SimklSeriesPropsResource` at `:18-23`.
type seriesPropsResource struct {
	Title string            `json:"title"`
	Year  int               `json:"year"`
	IDs   seriesIDsResource `json:"ids"`
}

// seriesIDsResource mirrors C# `SimklSeriesIdsResource` at `:8-16`.
// Tvdb/Imdb/Tmdb/Mal are STRINGS per the Simkl API even when they
// represent integer IDs.
type seriesIDsResource struct {
	Simkl int    `json:"simkl"`
	Slug  string `json:"slug"`
	Imdb  string `json:"imdb"`
	Tmdb  string `json:"tmdb"`
	Tvdb  string `json:"tvdb"`
	Mal   string `json:"mal"`
}

// authToken mirrors C# `RefreshRequestResponse` at `:39-47`.
type authToken struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// AnimeType values are the recognised C# `SimklAnimeType` enum
// values at `:77-88`. The C# parser filters anime entries to TV /
// ONA / OVA / Special — anything else is skipped with a log warn
// at `:46-59`.
const (
	animeTypeTV      = "tv"
	animeTypeONA     = "ona"
	animeTypeOVA     = "ova"
	animeTypeSpecial = "special"
)

// isSupportedAnimeType reports whether the SimklAnimeType is one of
// the TV-show-like categories the C# parser accepts.
func isSupportedAnimeType(at string) bool {
	switch at {
	case animeTypeTV, animeTypeONA, animeTypeOVA, animeTypeSpecial:
		return true
	default:
		return false
	}
}
