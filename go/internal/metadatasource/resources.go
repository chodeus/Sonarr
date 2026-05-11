package metadatasource

import "time"

// This file ports the wire-format DTOs at
// `src/NzbDrone.Core/MetadataSource/SkyHook/Resource/`. These are the
// shapes SkyHook returns over HTTP (`GET /v1/tvdb/shows/en/{id}/`
// returns `ShowResource`; `GET /v1/tvdb/search/en/?term=...` returns
// `[]ShowResource`). The C# class names round-trip via
// System.Text.Json's PascalCase-as-default + camelCase property
// renaming; the SkyHook server actually emits camelCase JSON, so the
// Go struct tags use camelCase verbatim.
//
// All zero-value-tolerant — every nullable field on the C# side is
// modeled as a Go pointer or a slice that may be nil. The MapSeries /
// MapEpisode mapping pipeline (slice 4.AT / queue [M3a]) is the layer
// that converts these DTOs into the typed series.Series + episodes.Episode
// records the rest of the system consumes.
//
// PROV-Q31 cache-key note: a future LRU cache keyed on
// (route, tvdbId, lang) is documented in the analysis doc; this layer
// stays cache-agnostic. The DTOs are decoded fresh from each HTTP
// response.

// ShowResource ports `ShowResource` at
// `src/NzbDrone.Core/MetadataSource/SkyHook/Resource/ShowResource.cs:5-48`.
//
// The `GET /v1/tvdb/shows/en/{id}/` endpoint returns one of these;
// the `GET /v1/tvdb/search/en/?term=` endpoint returns a `[]ShowResource`.
//
// Nullable handling:
//
//   - TvRageID / TvMazeID / TmdbID / Runtime are `int?` on C# →
//     `*int` on Go so the MapSeries pass can distinguish "absent"
//     from "zero" (the C# `if (show.TvRageId.HasValue) series.TvRageId
//     = show.TvRageId.Value` pattern relies on the Nullable<int>
//     marker — Go pointer preserves the same).
//
//   - Title / Overview / Slug / FirstAired / LastAired / Status /
//     Network / ImdbId / OriginalLanguage / OriginalCountry /
//     ContentRating are non-null strings on the C# side but the JSON
//     wire may legitimately omit them (older SkyHook responses) so
//     the Go DTO uses plain string with the empty-string-is-empty
//     convention. The map pass checks IsNotNullOrWhiteSpace per C#
//     `SkyHookProxy.cs:209-247`.
//
//   - MalIDs / AniListIDs are `HashSet<int>` on C#; Go uses `[]int`
//     since the wire is a JSON array and downstream consumers
//     (series.Series.MalIDs / AniListIDs) are also `[]int`. JSON
//     decode of a duplicate-containing array would produce a slice
//     with duplicates — the upstream SkyHook server is responsible
//     for emitting a set-like representation; the Go port does not
//     deduplicate here.
//
//   - TimeOfDay is a sub-object (Hours, Minutes) that's null when
//     SkyHook has no air-time. *TimeOfDayResource preserves the
//     null marker so MapSeries can choose between formatting
//     `series.AirTime = "HH:mm"` vs leaving it empty (matches C#
//     `SkyHookProxy.cs:233-236`).
//
//   - Rating is a sub-object that's null when SkyHook has no
//     ratings. *RatingResource preserves the null marker so
//     [mapRatings] returns the zero Ratings struct on null
//     (matches C# `SkyHookProxy.cs:335-347`).
type ShowResource struct {
	TvdbID           int                `json:"tvdbId"`
	Title            string             `json:"title"`
	Overview         string             `json:"overview"`
	Slug             string             `json:"slug"`
	FirstAired       string             `json:"firstAired"`
	LastAired        string             `json:"lastAired"`
	TvRageID         *int               `json:"tvRageId"`
	TvMazeID         *int               `json:"tvMazeId"`
	TmdbID           *int               `json:"tmdbId"`
	MalIDs           []int              `json:"malIds"`
	AniListIDs       []int              `json:"aniListIds"`
	Status           string             `json:"status"`
	Runtime          *int               `json:"runtime"`
	TimeOfDay        *TimeOfDayResource `json:"timeOfDay"`
	Network          string             `json:"network"`
	ImdbID           string             `json:"imdbId"`
	OriginalLanguage string             `json:"originalLanguage"`
	OriginalCountry  string             `json:"originalCountry"`
	Actors           []ActorResource    `json:"actors"`
	Genres           []string           `json:"genres"`
	ContentRating    string             `json:"contentRating"`
	Rating           *RatingResource    `json:"rating"`
	Images           []ImageResource    `json:"images"`
	Seasons          []SeasonResource   `json:"seasons"`
	Episodes         []EpisodeResource  `json:"episodes"`
}

// EpisodeResource ports `EpisodeResource` at
// `src/NzbDrone.Core/MetadataSource/SkyHook/Resource/EpisodeResource.cs:5-22`.
//
// Nullable handling mirrors ShowResource:
//
//   - AbsoluteEpisodeNumber / AiredAfterSeasonNumber /
//     AiredBeforeSeasonNumber / AiredBeforeEpisodeNumber are `int?`
//     on C# → `*int` on Go. The MapEpisode pass copies the pointer
//     into the persisted episodes.Episode (which uses `*int` for
//     the same nullable columns).
//
//   - AirDateUtc is `DateTime?` on C# → `*time.Time` on Go. The
//     wire format is RFC3339 (SkyHook emits e.g. "2008-01-20T03:00:00Z").
//     The MapEpisode pass writes it into episodes.Episode.AirDateUtc.
//
//   - AirDate is a plain string ("yyyy-MM-dd") that the
//     episodes.Episode persists as TEXT; no nullability marker
//     needed (empty string round-trips as NULL).
//
//   - Rating is null-tolerated identical to ShowResource.Rating.
//
//   - Image is the per-episode screenshot URL (single string, no
//     CoverType wrapper). MapEpisode wraps it into a
//     mediacover.Cover with CoverTypeScreenshot when non-empty
//     (matches C# `SkyHookProxy.cs:301-305`).
//
//   - Runtime is `int` (NOT `int?`) on C# — required field on the
//     wire. JSON omission decodes to 0 which matches the
//     `episodes.Episode.Runtime INT NOT NULL DEFAULT 0` schema.
type EpisodeResource struct {
	TvdbID                   int             `json:"tvdbId"`
	SeasonNumber             int             `json:"seasonNumber"`
	EpisodeNumber            int             `json:"episodeNumber"`
	AbsoluteEpisodeNumber    *int            `json:"absoluteEpisodeNumber"`
	AiredAfterSeasonNumber   *int            `json:"airedAfterSeasonNumber"`
	AiredBeforeSeasonNumber  *int            `json:"airedBeforeSeasonNumber"`
	AiredBeforeEpisodeNumber *int            `json:"airedBeforeEpisodeNumber"`
	Title                    string          `json:"title"`
	AirDate                  string          `json:"airDate"`
	AirDateUtc               *time.Time      `json:"airDateUtc"`
	Runtime                  int             `json:"runtime"`
	FinaleType               string          `json:"finaleType"`
	Rating                   *RatingResource `json:"rating"`
	Overview                 string          `json:"overview"`
	Image                    string          `json:"image"`
}

// SeasonResource ports `SeasonResource` at
// `src/NzbDrone.Core/MetadataSource/SkyHook/Resource/SeasonResource.cs:5-14`.
//
// Wire shape: { "seasonNumber": N, "images": [...] }. The MapSeason
// pass at C# `SkyHookProxy.cs:310-318` constructs a Season with
// `Monitored = SeasonNumber > 0` (specials default-unmonitored) —
// the Go port replicates this in slice 4.AT / queue [M3a].
type SeasonResource struct {
	SeasonNumber int             `json:"seasonNumber"`
	Images       []ImageResource `json:"images"`
}

// ActorResource ports `ActorResource` at
// `src/NzbDrone.Core/MetadataSource/SkyHook/Resource/ActorResource.cs:3-8`.
//
// Image is the optional headshot URL — MapActors wraps it into a
// mediacover.Cover with CoverTypeHeadshot when non-empty (matches
// C# `SkyHookProxy.cs:270-276`). Empty string → no Images on the
// resulting Actor.
type ActorResource struct {
	Name      string `json:"name"`
	Character string `json:"character"`
	Image     string `json:"image"`
}

// ImageResource ports `ImageResource` at
// `src/NzbDrone.Core/MetadataSource/SkyHook/Resource/ImageResource.cs:3-7`.
//
// CoverType is the lower-cased name SkyHook emits ("poster", "banner",
// "fanart", "clearlogo", or anything else which MapCoverType collapses
// to mediacover.CoverTypeUnknown per C# `SkyHookProxy.cs:358-373`).
type ImageResource struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
}

// RatingResource ports `RatingResource` at
// `src/NzbDrone.Core/MetadataSource/SkyHook/Resource/RatingResource.cs:3-6`.
//
// Count is the C# `int` (NOT nullable on the wire). Value is the C#
// `decimal` — SkyHook emits it as a JSON number which the Go port
// decodes into a `float64` to preserve fractional values like 8.7.
// The downstream series.Ratings JSON blob also stores Value as a
// decimal-like field; the conversion is lossless for the 0-10 range
// SkyHook uses.
type RatingResource struct {
	Count int     `json:"count"`
	Value float64 `json:"value"`
}

// TimeOfDayResource ports `TimeOfDayResource` at
// `src/NzbDrone.Core/MetadataSource/SkyHook/Resource/TimeOfDayResource.cs:3-7`.
//
// Carries the air-time in 24h split-fields. The MapSeries pass
// formats this into a "HH:mm" string for the series.Series.AirTime
// column when the pointer is non-nil (matches C# `SkyHookProxy.cs:233-236`).
//
// Also load-bearing for queue [M5] (SystemTimeCheck) — that
// healthcheck reads SkyHook's reported time-of-day and compares it
// against the local clock; the Go port piggybacks on this same
// DTO once the SkyHookProxy is wired.
type TimeOfDayResource struct {
	Hours   int `json:"hours"`
	Minutes int `json:"minutes"`
}
