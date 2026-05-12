package xbmc

import (
	"strconv"

	"sonarr.tv/sonarr/internal/series"
)

// KodiEpisodeGuide ports `KodiEpisodeGuide` at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Xbmc/KodiEpisodeGuide.cs:6-35`.
//
// Emitted as a JSON sub-document inside the `<episodeguide>` element
// of `tvshow.nfo` when [Settings.SeriesMetadataEpisodeGuide] is true.
// Kodi parses this body to bootstrap its own scraper with the
// series's external IDs.
//
// Field shapes match the C# `[JsonPropertyName(...)]` attributes
// (lowercase identifier-keys: "tvdb", "tvmaze", "tvrage", "tmdb",
// "imdb"). All five fields are `string` even when the underlying ID
// is numeric — the C# code calls `.ToString()` on each. A nil pointer
// or unset value emits `null`.
//
// NULLABLE-FIELD CONVENTION (Option A — see go/JSON_CONVENTIONS.md):
// the C# `string` properties default to null and are emitted as
// `null` when the source ID is missing. The Go port uses `*string`
// with NO `omitempty` to preserve byte-equal output. `Tvdb` is the
// one exception — the C# constructor at `KodiEpisodeGuide.cs:29`
// always populates it (TVDB ID is the primary key of the entire
// series row), so the Go port keeps it as a plain `string`.
type KodiEpisodeGuide struct {
	// Tvdb is the TheTVDB series ID as a string. Always populated;
	// the constructor at C# `:29` calls `series.TvdbId.ToString()`
	// unconditionally.
	Tvdb string `json:"tvdb"`

	// TvMaze is the TVmaze series ID as a string. nil when
	// series.TvMazeID <= 0 — C# `:30` `series.TvMazeId > 0` gate.
	TvMaze *string `json:"tvmaze"`

	// TvRage is the TVRage series ID as a string.
	//
	// FAITHFUL-TO-C#-BUG: the C# constructor at `KodiEpisodeGuide.cs:31`
	// gates on `series.TvRageId > 0` but writes
	// `series.TvMazeId.ToString()` (a copy-paste typo that has
	// shipped in Sonarr for years). The Go port preserves the
	// behaviour byte-for-byte — when TvRageId is non-zero, the
	// emitted "tvrage" value carries the TvMazeId number. Fixing this
	// would change the Kodi-side wire payload and break v3 API
	// parity (Option A wire-shape policy at
	// go/JSON_CONVENTIONS.md); raise a separate ticket if a future
	// Sonarr release fixes the C# side.
	TvRage *string `json:"tvrage"`

	// Tmdb is the TheMovieDB series ID as a string. nil when
	// series.TmdbID <= 0 — C# `:32` `series.TmdbId > 0` gate.
	Tmdb *string `json:"tmdb"`

	// Imdb is the IMDb series ID (the "tt"-prefixed string). nil
	// when series.ImdbID is empty — but the C# code at `:33`
	// assigns unconditionally, so the Go port encodes an empty IMDb
	// ID as the empty string (not nil) to preserve exact parity.
	Imdb *string `json:"imdb"`
}

// NewKodiEpisodeGuide builds the per-series episode-guide payload.
// Mirrors the C# constructor at `KodiEpisodeGuide.cs:27-34`:
//
//   - Tvdb is always set;
//   - TvMaze is set when series.TvMazeID > 0;
//   - TvRage is set when series.TvRageID > 0 — VALUE COPIED FROM
//     TvMazeID to preserve the upstream typo (see [KodiEpisodeGuide.TvRage]);
//   - Tmdb is set when series.TmdbID > 0;
//   - Imdb is set to series.ImdbID unconditionally (matches the C#
//     `Imdb = series.ImdbId;` assignment).
//
// nil series returns the zero value.
func NewKodiEpisodeGuide(s *series.Series) KodiEpisodeGuide {
	if s == nil {
		return KodiEpisodeGuide{}
	}
	g := KodiEpisodeGuide{
		Tvdb: strconv.Itoa(s.TvdbID),
	}
	if s.TvMazeID > 0 {
		v := strconv.Itoa(s.TvMazeID)
		g.TvMaze = &v
	}
	if s.TvRageID > 0 {
		// PRESERVED-C#-BUG (`KodiEpisodeGuide.cs:31`): the gate is
		// on TvRageId but the value comes from TvMazeId. Match
		// byte-for-byte.
		v := strconv.Itoa(s.TvMazeID)
		g.TvRage = &v
	}
	if s.TmdbID > 0 {
		v := strconv.Itoa(s.TmdbID)
		g.Tmdb = &v
	}
	// C# `:33` assigns ImdbId unconditionally, even when empty.
	// Mirror by always setting a pointer, even when the string is "".
	imdb := s.ImdbID
	g.Imdb = &imdb
	return g
}
