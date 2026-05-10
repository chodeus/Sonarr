// Package parsingservice ports `NzbDrone.Core.Parser.ParsingService`
// (`src/NzbDrone.Core/Parser/ParsingService.cs:1-712`) — the Series-
// + Episode-aware orchestration layer that sits BETWEEN the regex-
// driven [parser] package (which produces `ParsedEpisodeInfo` from
// raw release titles) and the matched-episode list every downstream
// import / decision pipeline consumes.
//
// Slice 3.66 landed [Service.ParseSpecialEpisodeTitle] (3-arg
// overload at `ParsingService.cs:364-403`). Slice 3.67 lands
// [Service.GetEpisodes] (3-arg public form at `:247-257`) plus the
// private `getEpisodes` 4-arg dispatcher (`:259-311`), a simplified
// `mapForSeries` (the series-passed branch of Map at `:176-245`),
// and the three helpers GetDailyEpisode / GetAnimeEpisodes /
// GetStandardEpisodes (`:557-710`). Together they close the
// [episodeimport.EpisodeParsingService] interface (slice 3.61's
// AggregateEpisodes consumer).
//
// Slice 3.81 lands [Service.GetSeriesFromTitle] — the
// `IParsingService.GetSeries(string title)` port at C# `:43-73`. This
// is the consumer-facing surface
// `downloadedimport.SeriesParser.GetSeriesFromTitle` (slice 3.79a)
// reaches for; landing the production type here unblocks the eventual
// cmd/sonarr/main.go wiring slice that constructs a
// `*downloadedimport.Service` against the real production stack.
//
// Deferred:
//
//   - The 5-arg `ParseSpecialEpisodeTitle` overload at C# `:313-362`
//     that takes (tvdbId, tvRageId, imdbId, searchCriteria) — only
//     consumer is `ImportApprovedEpisodes` (writer slice, also
//     deferred). Adding it now would be speculative.
//
//   - Full Map (with FindSeries fallback) / IsTitleParsed
//     — no ported consumers yet.
//
//   - The seven `IHandle<...>` event-bus subscribers — defer with
//     the unported event shapes.
//
//   - SearchCriteria-driven short-circuits inside GetEpisodes /
//     GetDailyEpisode / GetAnimeEpisodes / GetStandardEpisodes — the
//     C# code uses these for indexer-search-driven paths the writer
//     slice will need; the Aggregate-driven consumer always passes
//     null. The Go port omits these arms; a future overload adds them.
//
//   - scenemappings.Service — the [SceneMappingFinder] narrow
//     interface ships here so production binding can stub with a
//     no-op until scenemappings.Service ports.
package parsingservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/scenemappings"
	"sonarr.tv/sonarr/internal/series"
)

// EpisodeFinder is the slice of [episodes.Service] the parsing
// service reaches for in `ParseSpecialEpisodeTitle`. Two methods
// today; future slices append per their needs:
//
//   - `FindEpisodesBySceneNumbering` — the C# `:369` "is the SxxE00
//     scene-numbered" arm, used to short-circuit when an episode
//     already exists in the series' scene-numbered season.
//   - `FindEpisodeByTitle` — the C# `:376` Levenshtein-style match
//     against episode titles in season 0. The actual fuzzy match
//     lives in [episodes.Service]; the parsing service just
//     forwards the call.
//
// Production wires `*episodes.Service`; tests pass a stub.
type EpisodeFinder interface {
	// FindEpisodesBySceneNumbering returns every episode whose
	// SceneSeasonNumber + SceneEpisodeNumber match the supplied
	// pair. Used by ParseSpecialEpisodeTitle to detect TheXEM-
	// mapped specials at C# `:369`, and by GetStandardEpisodes /
	// GetAnimeEpisodes to resolve scene-numbered queries at
	// `:597,607,617,671`.
	FindEpisodesBySceneNumbering(ctx context.Context, seriesID, sceneSeasonNumber, sceneEpisodeNumber int) ([]episodes.Episode, error)

	// FindEpisodesBySceneAbsolute returns every episode whose
	// SceneAbsoluteEpisodeNumber matches. Maps to the C# overload
	// `_episodeService.FindEpisodesBySceneNumbering(seriesId, absoluteEpisodeNumber)`
	// at `:617`.
	FindEpisodesBySceneAbsolute(ctx context.Context, seriesID, sceneAbsoluteEpisodeNumber int) ([]episodes.Episode, error)

	// FindEpisodeByTitle returns the (single) episode in
	// (seriesID, seasonNumber) whose title appears as a substring
	// of the release title. Used by ParseSpecialEpisodeTitle at
	// C# `:376`.
	//
	// nil result with nil error means "no match" — the C# version
	// returns null in the same case (`:376` `_episodeService.FindEpisodeByTitle(...)`
	// uses `FirstOrDefault`).
	FindEpisodeByTitle(ctx context.Context, seriesID, seasonNumber int, releaseTitle string) (*episodes.Episode, error)

	// FindEpisode returns the single episode at
	// (seriesID, seasonNumber, episodeNumber). Used by GetDailyEpisode
	// helpers + GetStandardEpisodes / GetAnimeEpisodes at
	// `:587,601,611,629,696`.
	//
	// nil result with nil error means "no match".
	FindEpisode(ctx context.Context, seriesID, seasonNumber, episodeNumber int) (*episodes.Episode, error)

	// FindEpisodeByAbsolute returns the single episode whose
	// AbsoluteEpisodeNumber matches. Maps to C# overload
	// `_episodeService.FindEpisode(seriesId, absoluteEpisodeNumber)`
	// at `:629`.
	//
	// nil result with nil error means "no match".
	FindEpisodeByAbsolute(ctx context.Context, seriesID, absoluteEpisodeNumber int) (*episodes.Episode, error)

	// FindEpisodeByAirDate returns the single episode that aired on
	// the supplied date (with optional DailyPart for double-feature
	// disambiguation). Used by GetDailyEpisode at `:569`.
	//
	// nil result with nil error means "no match" — the underlying
	// repo returns nil for a missing date row.
	FindEpisodeByAirDate(ctx context.Context, seriesID int, airDate string, part *int) (*episodes.Episode, error)

	// GetEpisodesBySeason returns every episode in a season ordered
	// by EpisodeNumber. Used by getEpisodes at `:274`.
	GetEpisodesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error)

	// GetEpisodesBySceneSeason returns every episode whose
	// SceneSeasonNumber matches. Used by getEpisodes for the
	// FullSeason+sceneSource path at `:265`.
	GetEpisodesBySceneSeason(ctx context.Context, seriesID, sceneSeasonNumber int) ([]episodes.Episode, error)
}

// ParsedEpisodeInfoBuilder is a thin compositional layer over the
// existing [parser] package's free-function helpers (`ParseQuality`,
// `ParseReleaseGroup`, `ParseLanguages`). Pulled behind a narrow
// interface SOLELY for testability — production binds
// [defaultParsedEpisodeInfoBuilder] which delegates to the regex
// engines, while tests pin synthetic Quality/ReleaseGroup/Languages
// without depending on the regex tables.
//
// The methods are deliberately scoped to "what
// ParseSpecialEpisodeTitle constructs" — the larger parser API
// stays where it lives.
type ParsedEpisodeInfoBuilder interface {
	// ParseQuality wraps `quality.ParseQuality(releaseTitle)`
	// (`internal/quality/parse.go:23`).
	ParseQuality(releaseTitle string) quality.Model
	// ParseReleaseGroup wraps `parser.ParseReleaseGroup(title)`
	// (`internal/parser/release_group.go:212`).
	ParseReleaseGroup(releaseTitle string) string
	// ParseLanguages wraps `parser.ParseLanguages(title)`
	// (`internal/parser/parse_languages.go:97`).
	ParseLanguages(releaseTitle string) []language.Language
}

// defaultParsedEpisodeInfoBuilder delegates to the per-package
// free functions. Stateless; safe to share.
type defaultParsedEpisodeInfoBuilder struct{}

// ParseQuality delegates to [quality.ParseQuality].
func (defaultParsedEpisodeInfoBuilder) ParseQuality(t string) quality.Model {
	return quality.ParseQuality(t)
}

// ParseReleaseGroup delegates to [parser.ParseReleaseGroup].
func (defaultParsedEpisodeInfoBuilder) ParseReleaseGroup(t string) string {
	return parser.ParseReleaseGroup(t)
}

// ParseLanguages delegates to [parser.ParseLanguages] but maps the
// returned `[]language.Language` to the parser-internal [language.Language]
// alias the ParsedEpisodeInfo struct uses.
func (defaultParsedEpisodeInfoBuilder) ParseLanguages(t string) []language.Language {
	return parser.ParseLanguages(t)
}

// SceneMappingFinder is the slice of `ISceneMappingService` the
// parsing service reaches for in `Map` (sceneSource path) and
// `GetAnimeEpisodes`. Two methods:
//
//   - `FindSceneMapping(seriesTitle, releaseTitle, seasonNumber)`
//     returns the matched [scenemappings.Mapping] or nil. Used by
//     Map at C# `:178` to derive MappedSeasonNumber + decide whether
//     the parse is tvdb-numbered (Origin="tvdb") rather than scene-
//     numbered.
//   - `GetSceneSeasonNumber(seriesTitle, releaseTitle)` returns the
//     scene-season override or nil. Used by GetAnimeEpisodes at
//     `:579` to remap absolute episode numbers when a scene/anime
//     title doesn't carry an explicit season.
//
// `internal/scenemappings` ships only the Repo today; the Service
// that would back this interface ports in a separate slice. Until
// then, production wiring passes a no-op stub (returns nil from both
// methods) — the consumers tolerate nil returns gracefully (the C#
// code's `if (sceneMapping != null)` arms cover the same case).
//
// nil dep on the [Service] struct triggers the same nil-returning
// behaviour automatically — see [noopSceneMappingFinder] below.
type SceneMappingFinder interface {
	// FindSceneMapping returns the scene mapping matching the
	// supplied (seriesTitle, releaseTitle, seasonNumber) triple, or
	// nil if no mapping exists. Mirrors C# `:178`.
	//
	// Implementations swallow per-call errors (returning nil) rather
	// than propagating; the C# version's underlying engine never
	// throws on a missing match.
	FindSceneMapping(seriesTitle, releaseTitle string, seasonNumber int) *scenemappings.Mapping

	// GetSceneSeasonNumber returns the scene-season override for the
	// supplied (seriesTitle, releaseTitle), or nil. Mirrors C# `:579`.
	//
	// Returns *int because the C# value is `int?` — null means "no
	// override; use the parsed season number as-is".
	GetSceneSeasonNumber(seriesTitle, releaseTitle string) *int
}

// noopSceneMappingFinder returns nil from both methods. Bound by
// [NewService] when the caller passes nil; matches the C# tree's
// "no scene mapping registered" baseline at `:178,579` (both return
// null on no-match, and every consumer arm has a `!= null` guard).
type noopSceneMappingFinder struct{}

// FindSceneMapping always returns nil.
func (noopSceneMappingFinder) FindSceneMapping(_, _ string, _ int) *scenemappings.Mapping {
	return nil
}

// GetSceneSeasonNumber always returns nil.
func (noopSceneMappingFinder) GetSceneSeasonNumber(_, _ string) *int { return nil }

// SeriesFinder narrows `ISeriesService` for the lookup methods the
// parsing service reaches for. The C# class at
// `Parser/ParsingService.cs:43-73, 405-555` calls five `ISeriesService`
// methods: FindByTitle (1-arg + 2-arg overloads), FindByTvdbId,
// FindByTvRageId, and FindByImdbId — that's the entire surface needed
// to resolve a release-title string + ID hints + (optional)
// SearchCriteria to a [series.Series].
//
// Production binds [series.Service] directly via Go's structural typing:
// every method signature matches exactly. Tests pass a per-method stub.
//
// Method semantics — all five return (nil, nil) on no-match (mirrors
// C# `null`) and propagate any underlying lookup error:
//
//   - FindByTitle takes the RAW (uncleaned) title; the production
//     binding at [series.Service.FindByTitle] applies
//     `parser.CleanSeriesTitle` internally before the repo lookup,
//     matching `SeriesService.cs:104-107`.
//   - FindByTitleAndYear takes a raw title + year; the production
//     binding cleans the title before the repo lookup. Matches
//     `SeriesService.cs:158-161`.
//   - FindByTvdbID takes the integer TvdbId; matches the C# overload
//     used at `ParsingService.cs:55,499`.
//   - FindByTvRageID takes the integer TvRageId; matches the C#
//     overload used at `ParsingService.cs:516`. Slice 4.S added.
//   - FindByImdbID takes the IMDb id string (tt-prefixed); matches the
//     C# overload used at `ParsingService.cs:533`. Slice 4.S added.
//
// Returns [series.ErrNotFound] (not a sentinel nil) when the row
// isn't present — both [Service.GetSeriesFromTitle] and
// [Service.findSeries] classify via `errors.Is` and treat it as the
// C# null arm.
type SeriesFinder interface {
	FindByTitle(ctx context.Context, title string) (*series.Series, error)
	FindByTitleAndYear(ctx context.Context, title string, year int) (*series.Series, error)
	FindByTvdbID(ctx context.Context, tvdbID int) (*series.Series, error)
	FindByTvRageID(ctx context.Context, tvRageID int) (*series.Series, error)
	FindByImdbID(ctx context.Context, imdbID string) (*series.Series, error)
}

// noopSeriesFinder satisfies [SeriesFinder] with five nil-returning
// methods. Bound by [NewService] when the caller passes nil. Mirrors
// the noop-tolerant convention from [noopSceneMappingFinder] above —
// the consumer ([Service.GetSeriesFromTitle] / [Service.findSeries])
// treats every (nil, nil) arm as "no series in catalog" so a
// parsingservice.Service constructed without a real seriesFinder is
// still usable for the ParseSpecialEpisodeTitle / GetEpisodes paths
// that don't need it. A brand-new install with zero series rows would
// behave the same way against a real binding, so the noop preserves
// semantic equivalence.
type noopSeriesFinder struct{}

// FindByTitle always returns (nil, nil).
func (noopSeriesFinder) FindByTitle(_ context.Context, _ string) (*series.Series, error) {
	return nil, nil
}

// FindByTitleAndYear always returns (nil, nil).
func (noopSeriesFinder) FindByTitleAndYear(_ context.Context, _ string, _ int) (*series.Series, error) {
	return nil, nil
}

// FindByTvdbID always returns (nil, nil).
func (noopSeriesFinder) FindByTvdbID(_ context.Context, _ int) (*series.Series, error) {
	return nil, nil
}

// FindByTvRageID always returns (nil, nil).
func (noopSeriesFinder) FindByTvRageID(_ context.Context, _ int) (*series.Series, error) {
	return nil, nil
}

// FindByImdbID always returns (nil, nil).
func (noopSeriesFinder) FindByImdbID(_ context.Context, _ string) (*series.Series, error) {
	return nil, nil
}

// Service ports a subset of `IParsingService` at
// `Parser/ParsingService.cs:1-712`. Slice 3.66 landed
// [Service.ParseSpecialEpisodeTitle], 3.67 landed [Service.GetEpisodes],
// 3.81 lands [Service.GetSeriesFromTitle]; see the package doc for the
// deferred-method list.
//
// Goroutine-safe post-construction: stateless beyond the wired deps,
// which themselves must be goroutine-safe (the production deps are).
type Service struct {
	episodes     EpisodeFinder
	infos        ParsedEpisodeInfoBuilder
	scenemap     SceneMappingFinder
	seriesFinder SeriesFinder
	log          *slog.Logger
}

// NewService binds the dependencies. episodes is REQUIRED. infos,
// scenemap, seriesFinder, and log are OPTIONAL:
//
//   - infos == nil → [defaultParsedEpisodeInfoBuilder] (delegates to
//     parser/quality free functions).
//   - scenemap == nil → [noopSceneMappingFinder] (returns nil from
//     both methods; matches "no scene mappings registered").
//   - seriesFinder == nil → [noopSeriesFinder] (returns (nil, nil)
//     from all three methods; matches "no series rows in catalog" —
//     see the [SeriesFinder] doc for why this is semantically safe).
//   - log == nil → [slog.Default].
//
// Returns error rather than panicking for the required-nil arm so
// the caller's startup wiring path can surface the misuse cleanly.
// Mirrors the constructor pattern in [episodeimport.NewAggregationService]
// (slice 3.60) and the more recent service slices (3.63, 3.64, 3.65).
func NewService(
	episodes EpisodeFinder,
	infos ParsedEpisodeInfoBuilder,
	scenemap SceneMappingFinder,
	seriesFinder SeriesFinder,
	log *slog.Logger,
) (*Service, error) {
	if episodes == nil {
		return nil, errors.New("parsingservice: NewService: nil EpisodeFinder")
	}
	if infos == nil {
		infos = defaultParsedEpisodeInfoBuilder{}
	}
	if scenemap == nil {
		scenemap = noopSceneMappingFinder{}
	}
	if seriesFinder == nil {
		seriesFinder = noopSeriesFinder{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		episodes:     episodes,
		infos:        infos,
		scenemap:     scenemap,
		seriesFinder: seriesFinder,
		log:          log,
	}, nil
}

// GetSeriesFromTitle ports `Parser/ParsingService.cs:43-73` — the
// `IParsingService.GetSeries(string title)` method that resolves a
// release-title (or folder-name, or any free-form string the
// downloaded-import orchestrator hands the parsing service) to a
// [series.Series]. Used by [downloadedimport.Service] (slice 3.79a
// SeriesParser narrow interface) on the path's last segment when no
// download-client item supplies a series ID directly.
//
// Decision tree (line-aligned with C# `:43-73`):
//
//  1. `parser.ParseTitle(title)` — if the regex engine can't extract
//     a [parser.ParsedEpisodeInfo] (returns nil), fall back to
//     `seriesFinder.FindByTitle(title)` against the raw input. The
//     production binding ([series.Service.FindByTitle]) cleans the
//     title via `parser.CleanSeriesTitle` before the repo lookup, so
//     the raw string is the right input shape.
//  2. Otherwise, ask [SceneMappingFinder.FindSceneMapping] for a
//     scene-mapping match against
//     (parsedInfo.SeriesTitle, parsedInfo.ReleaseTitle, parsedInfo.SeasonNumber).
//     The C# `_sceneMappingService.FindTvdbId` is just a wrapper over
//     FindSceneMapping that pulls `.TvdbId` off the result; the Go
//     port inlines that pull. If the mapping is non-nil and carries a
//     non-zero TvdbID, dispatch to `seriesFinder.FindByTvdbID(tvdbID)`
//     and return its result directly.
//  3. Else, look up `seriesFinder.FindByTitle(parsedInfo.SeriesTitle)`.
//  4. If step 3 missed AND `parsedInfo.SeriesTitleInfo.AllTitles` is
//     non-empty, dispatch to [Service.getSeriesByAllTitles] (the C#
//     `GetSeriesByAllTitles` private helper at `:75-138`).
//  5. If still missing AND parsedInfo.SeriesTitleInfo is non-nil, try
//     `seriesFinder.FindByTitleAndYear(SeriesTitleInfo.TitleWithoutYear,
//     SeriesTitleInfo.Year)` — matches C# `:69-70`.
//  6. Return whatever step 3-5 produced (possibly nil).
//
// Errors from any [SeriesFinder] call propagate wrapped EXCEPT
// [series.ErrNotFound], which is treated as the C# `null` arm and
// classified via `errors.Is`. Production [series.Service] returns
// ErrNotFound on no-match (see `internal/series/queries.go:267-277`);
// other unexpected errors (DB unreachable, multi-match, etc.) propagate.
//
// nil receiver returns (nil, error) — surfacing wiring misuse loudly
// matches the convention from [Service.ParseSpecialEpisodeTitle] and
// [Service.GetEpisodes] above.
//
// The caller's "no series resolved" arm is the (nil, nil) return —
// downloadedimport.processFolder / processFile turn that into an
// UnknownSeries rejection at C# `DownloadedEpisodesImportService.cs:162-164`.
func (s *Service) GetSeriesFromTitle(ctx context.Context, title string) (*series.Series, error) {
	if s == nil {
		return nil, errors.New("parsingservice: GetSeriesFromTitle on nil receiver")
	}

	parsedEpisodeInfo := parser.ParseTitle(title)

	// Step 1: regex couldn't extract a ParsedEpisodeInfo — fall back
	// to a direct title lookup. C# `:46-49`.
	if parsedEpisodeInfo == nil {
		ser, err := s.seriesFinder.FindByTitle(ctx, title)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, fmt.Errorf("parsingservice: GetSeriesFromTitle: FindByTitle(%q): %w", title, err)
		}
		return ser, nil
	}

	// Step 2: scene-mapping pre-pass. C# `:51-56` —
	// `_sceneMappingService.FindTvdbId(...)` is `FindSceneMapping(...)?.TvdbId`.
	if mapping := s.scenemap.FindSceneMapping(
		parsedEpisodeInfo.SeriesTitle,
		parsedEpisodeInfo.ReleaseTitle,
		parsedEpisodeInfo.SeasonNumber,
	); mapping != nil && mapping.TvdbID != 0 {
		ser, err := s.seriesFinder.FindByTvdbID(ctx, mapping.TvdbID)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, fmt.Errorf("parsingservice: GetSeriesFromTitle: FindByTvdbID(%d): %w", mapping.TvdbID, err)
		}
		return ser, nil
	}

	// Step 3: parsed-series-title lookup. C# `:58`.
	ser, err := s.seriesFinder.FindByTitle(ctx, parsedEpisodeInfo.SeriesTitle)
	if err != nil && !errors.Is(err, series.ErrNotFound) {
		return nil, fmt.Errorf("parsingservice: GetSeriesFromTitle: FindByTitle(%q): %w", parsedEpisodeInfo.SeriesTitle, err)
	}

	// Step 4: AllTitles fallback. C# `:60-63`.
	if ser == nil &&
		parsedEpisodeInfo.SeriesTitleInfo != nil &&
		len(parsedEpisodeInfo.SeriesTitleInfo.AllTitles) > 0 {
		ser, err = s.getSeriesByAllTitles(ctx, parsedEpisodeInfo)
		if err != nil {
			return nil, err
		}
	}

	// Step 5: TitleWithoutYear + Year fallback. C# `:65-70`.
	if ser == nil && parsedEpisodeInfo.SeriesTitleInfo != nil {
		ser, err = s.seriesFinder.FindByTitleAndYear(
			ctx,
			parsedEpisodeInfo.SeriesTitleInfo.TitleWithoutYear,
			parsedEpisodeInfo.SeriesTitleInfo.Year,
		)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, fmt.Errorf("parsingservice: GetSeriesFromTitle: FindByTitleAndYear(%q,%d): %w",
				parsedEpisodeInfo.SeriesTitleInfo.TitleWithoutYear,
				parsedEpisodeInfo.SeriesTitleInfo.Year, err)
		}
	}

	return ser, nil
}

// getSeriesByAllTitles ports the private
// `ParsingService.GetSeriesByAllTitles` helper at C# `:75-138`. Walks
// each title in parsedInfo.SeriesTitleInfo.AllTitles, attempts to
// resolve each one to a series (with year + scene-mapping fallbacks),
// then verifies every successful resolution maps to the same TvdbId.
//
// Algorithm (line-aligned with C# `:79-130`):
//
//  1. For each title in AllTitles:
//     a. If year > 0: try FindByTitleAndYear(title, year). If miss,
//     retry FindByTitle(title + " <year>") — the C# fallback at
//     `:88-95` lets a series whose Title contains the year as a
//     suffix still match.
//     b. Else: FindByTitle(title).
//     c. If we got a series, capture series.TvdbId.
//     d. If we DIDN'T get a series, fall back to scene-mapping
//     FindTvdbId(title, ReleaseTitle, SeasonNumber).
//     e. If still no TvdbId, log + skip this title.
//     f. If foundTvdbId is set AND the new tvdbId disagrees, return
//     nil (ambiguous match — C# `:115-119`).
//     g. Track foundSeries (first non-nil) + foundTvdbId.
//  2. If foundSeries is nil but foundTvdbId is set, look up
//     FindByTvdbID(foundTvdbId). C# `:131-134`.
//  3. Return foundSeries (possibly nil).
//
// Errors from any seriesFinder call propagate wrapped (except
// ErrNotFound, treated as nil match).
func (s *Service) getSeriesByAllTitles(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo) (*series.Series, error) {
	year := parsedInfo.SeriesTitleInfo.Year

	var foundSeries *series.Series
	foundTvdbID := 0

	for _, title := range parsedInfo.SeriesTitleInfo.AllTitles {
		var ser *series.Series
		var err error

		if year > 0 {
			ser, err = s.seriesFinder.FindByTitleAndYear(ctx, title, year)
			if err != nil && !errors.Is(err, series.ErrNotFound) {
				return nil, fmt.Errorf("parsingservice: getSeriesByAllTitles: FindByTitleAndYear(%q,%d): %w", title, year, err)
			}
			// C# `:88-95`: title-plus-year fallback.
			if ser == nil {
				combined := fmt.Sprintf("%s %d", title, year)
				ser, err = s.seriesFinder.FindByTitle(ctx, combined)
				if err != nil && !errors.Is(err, series.ErrNotFound) {
					return nil, fmt.Errorf("parsingservice: getSeriesByAllTitles: FindByTitle(%q): %w", combined, err)
				}
			}
		} else {
			ser, err = s.seriesFinder.FindByTitle(ctx, title)
			if err != nil && !errors.Is(err, series.ErrNotFound) {
				return nil, fmt.Errorf("parsingservice: getSeriesByAllTitles: FindByTitle(%q): %w", title, err)
			}
		}

		// Derive a candidate tvdbId — either from the matched series
		// (C# `:103`) or via scene-mapping fallback (C# `:107`).
		tvdbID := 0
		if ser != nil {
			tvdbID = ser.TvdbID
		} else if mapping := s.scenemap.FindSceneMapping(title, parsedInfo.ReleaseTitle, parsedInfo.SeasonNumber); mapping != nil {
			tvdbID = mapping.TvdbID
		}

		if tvdbID == 0 {
			s.log.Debug("parsingservice: getSeriesByAllTitles: title not matching any series",
				"title", title)
			continue
		}

		// Conflict check — C# `:115-119`.
		if foundTvdbID != 0 && tvdbID != foundTvdbID {
			s.log.Debug("parsingservice: getSeriesByAllTitles: title matches multiple tvdbids — abandoning",
				"seriesTitle", parsedInfo.SeriesTitle, "found", foundTvdbID, "alt", tvdbID)
			return nil, nil
		}

		if foundSeries == nil {
			foundSeries = ser
		}
		foundTvdbID = tvdbID
	}

	// Final fallback — scene-mapping resolved a tvdbId but none of the
	// FindBy* calls produced a series row directly. C# `:131-134`.
	if foundSeries == nil && foundTvdbID != 0 {
		ser, err := s.seriesFinder.FindByTvdbID(ctx, foundTvdbID)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, fmt.Errorf("parsingservice: getSeriesByAllTitles: FindByTvdbID(%d): %w", foundTvdbID, err)
		}
		foundSeries = ser
	}

	return foundSeries, nil
}

// ParseSpecialEpisodeTitle ports
// `Parser/ParsingService.cs:364-403` — the 3-arg overload that
// resolves a special-episode `ParsedEpisodeInfo` from a release
// title given a known [series.Series].
//
// Decision tree (mirrors C# exactly):
//
//  1. If the input ParsedEpisodeInfo is non-nil AND the parse looks
//     like a "scene SxxE00 special" (per
//     [parser.ParsedEpisodeInfo.IsPossibleSceneSeasonSpecial]) AND
//     the series uses scene numbering, ask the episode finder for
//     any episodes with the matching (sceneSeason, sceneEpisode=0)
//     pair. If at least one exists, the parse is already correct
//     (TheXEM-mapped special) — return parsedEpisodeInfo unchanged.
//  2. Otherwise, ask the episode finder for a season-0 episode
//     whose title appears in the release title. If found,
//     construct a fresh ParsedEpisodeInfo:
//     - ReleaseTitle, SeriesTitle, SeriesTitleInfo from input
//     - SeasonNumber + EpisodeNumber from the resolved episode
//     - FullSeason=false, Special=true
//     - Quality / ReleaseGroup / Languages from the release-title
//     regex helpers (delegated through the
//     ParsedEpisodeInfoBuilder narrow interface so tests can pin)
//     Return the new info.
//  3. Otherwise, return nil — the caller (AggregateEpisodes /
//     private GetEpisodes) treats nil as "couldn't resolve a
//     special; fall back to the regular dispatch arm".
//
// Errors from EpisodeFinder propagate wrapped.
//
// The C# version takes parsedEpisodeInfo by reference (may be null
// in step 1) but never mutates it — the Go port mirrors by treating
// the input as read-only and returning either the same pointer
// (step 1) or a fresh value (step 2).
//
// nil series returns (nil, error) — the C# version would NPE at
// `series.UseSceneNumbering` / `series.Id` / `series.Title`. Surfacing
// the misuse is preferred over silent failure.
func (s *Service) ParseSpecialEpisodeTitle(ctx context.Context, parsedEpisodeInfo *parser.ParsedEpisodeInfo, releaseTitle string, ser *series.Series) (*parser.ParsedEpisodeInfo, error) {
	if s == nil {
		return nil, errors.New("parsingservice: ParseSpecialEpisodeTitle on nil receiver")
	}
	if ser == nil {
		return nil, errors.New("parsingservice: ParseSpecialEpisodeTitle: nil series")
	}

	// Step 1: TheXEM scene-numbered special check.
	if parsedEpisodeInfo != nil &&
		parsedEpisodeInfo.IsPossibleSceneSeasonSpecial() &&
		ser.UseSceneNumbering {
		eps, err := s.episodes.FindEpisodesBySceneNumbering(ctx, ser.ID, parsedEpisodeInfo.SeasonNumber, 0)
		if err != nil {
			return nil, err
		}
		if len(eps) > 0 {
			// Already a known scene-numbered special; the parsed
			// info is correct as-is.
			return parsedEpisodeInfo, nil
		}
	}

	// Step 2: title-substring lookup against season 0.
	episode, err := s.episodes.FindEpisodeByTitle(ctx, ser.ID, 0, releaseTitle)
	if err != nil {
		return nil, err
	}
	if episode == nil {
		return nil, nil
	}

	// Step 3: build the synthetic ParsedEpisodeInfo. Mirrors C# `:381-396`.
	out := &parser.ParsedEpisodeInfo{
		ReleaseTitle: releaseTitle,
		SeriesTitle:  ser.Title,
		SeriesTitleInfo: &parser.SeriesTitleInfo{
			Title: ser.Title,
		},
		SeasonNumber:   episode.SeasonNumber,
		EpisodeNumbers: []int{episode.EpisodeNumber},
		FullSeason:     false,
		Quality:        s.infos.ParseQuality(releaseTitle),
		ReleaseGroup:   s.infos.ParseReleaseGroup(releaseTitle),
		Languages:      s.infos.ParseLanguages(releaseTitle),
		Special:        true,
	}
	s.log.Debug("parsingservice: resolved special episode",
		"series", ser.Title, "season", out.SeasonNumber,
		"episode", episode.EpisodeNumber, "release", releaseTitle)
	return out, nil
}

// GetEpisodes ports `Parser/ParsingService.cs:247-257` — the public
// 3-arg overload that resolves a [parser.ParsedEpisodeInfo] +
// [series.Series] + sceneSource flag into the matched-episode list.
//
// Two paths:
//   - sceneSource=true → equivalent to calling Map and returning
//     remoteEpisode.Episodes. Map applies the scene-mapping pre-pass
//     (FindSceneMapping) to derive MappedSeasonNumber + an effective
//     sceneSource (a tvdb-origin mapping flips it to false), then
//     dispatches via the private getEpisodes helper.
//   - sceneSource=false → calls private getEpisodes directly with
//     mappedSeasonNumber=parsedInfo.SeasonNumber and sceneSource=false.
//
// nil parsedInfo / nil series returns (nil, error) — the C# version
// would NPE; surfacing the misuse loudly is preferred over silent
// failure.
//
// The 4-arg C# overload that takes a SearchCriteriaBase isn't ported;
// AggregateEpisodes (the only consumer at this slice) always passes
// nil. When ImportApprovedEpisodes (writer slice) ports, it can take
// a separate overload.
func (s *Service) GetEpisodes(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, ser *series.Series, sceneSource bool) ([]episodes.Episode, error) {
	if s == nil {
		return nil, errors.New("parsingservice: GetEpisodes on nil receiver")
	}
	if parsedInfo == nil {
		return nil, errors.New("parsingservice: GetEpisodes: nil parsedEpisodeInfo")
	}
	if ser == nil {
		return nil, errors.New("parsingservice: GetEpisodes: nil series")
	}
	if sceneSource {
		return s.mapForSeries(ctx, parsedInfo, ser)
	}
	return s.getEpisodes(ctx, parsedInfo, ser, parsedInfo.SeasonNumber, false)
}

// mapForSeries ports the series-passed branch of `Map` at C#
// `:176-245`, simplified for the always-nil searchCriteria + always-
// non-nil series case (the only call shape `GetEpisodes(sceneSource=true)`
// actually uses).
//
// Goes through FindSceneMapping → MappedSeasonNumber adjustment →
// scene-origin override (tvdb flips sceneSource to false) → XemService
// override → ValidateForSeriesType → private getEpisodes.
//
// Returns the resolved episode list (empty slice, never nil, when the
// dispatch arms don't match). Errors propagate from
// the underlying episode-finder calls.
func (s *Service) mapForSeries(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, ser *series.Series) ([]episodes.Episode, error) {
	sceneMapping := s.scenemap.FindSceneMapping(parsedInfo.SeriesTitle, parsedInfo.ReleaseTitle, parsedInfo.SeasonNumber)
	mappedSeason := parsedInfo.SeasonNumber
	sceneSource := true

	if sceneMapping != nil {
		// C# `:191-195`: SceneMapping.SeasonNumber is `int?`; HasValue +
		// Value >= 0 means "explicit non-negative override". The
		// SceneSeasonNumber comparison `<= parsedInfo.SeasonNumber` —
		// when SceneSeasonNumber is null, the C# `null <= int` returns
		// false, so the whole compound check is false. Mirror by
		// explicit nil check.
		if sceneMapping.SeasonNumber != nil && *sceneMapping.SeasonNumber >= 0 &&
			sceneMapping.SceneSeasonNumber != nil && *sceneMapping.SceneSeasonNumber <= parsedInfo.SeasonNumber {
			mappedSeason += *sceneMapping.SeasonNumber - *sceneMapping.SceneSeasonNumber
		}

		if sceneMapping.SceneOrigin == "tvdb" {
			// tvdb-origin mappings disable scene-source semantics —
			// the parsed numbers are already TVDB-numbered.
			sceneSource = false
		} else if sceneMapping.Type == "XemService" &&
			sceneMapping.SceneSeasonNumber != nil && *sceneMapping.SceneSeasonNumber >= 0 &&
			parsedInfo.SeasonNumber == 1 &&
			*sceneMapping.SceneSeasonNumber != parsedInfo.SeasonNumber {
			// XemService remap: the parsed S01 is actually the
			// scene-named series's first season, but the TVDB
			// mapping says it lives under a different season number.
			mappedSeason = *sceneMapping.SceneSeasonNumber
		}
	}

	if !parser.ValidateForSeriesType(parsedInfo, ser.SeriesType) {
		// C# `:225-229`: when ValidateForSeriesType returns false the
		// remoteEpisode.Episodes is left null which the `:233-236`
		// arm coerces to empty. Mirror with empty slice.
		return []episodes.Episode{}, nil
	}

	return s.getEpisodes(ctx, parsedInfo, ser, mappedSeason, sceneSource)
}

// getEpisodes is the private 4-arg dispatcher at C# `:259-311`.
// Branches on the parse shape (FullSeason / IsDaily / IsAbsoluteNumbering
// / IsPossibleSceneSeasonSpecial / Special / default-Standard) to route
// to the matching helper.
//
// Returns empty slice (not nil) for every "couldn't resolve" terminal
// — matches C# `new List<Episode>()` returns. Errors from the
// episode-finder propagate.
func (s *Service) getEpisodes(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, ser *series.Series, mappedSeasonNumber int, sceneSource bool) ([]episodes.Episode, error) {
	if parsedInfo.FullSeason {
		// C# `:261-275`: scene-numbered season → scene-numbered season
		// fallback to plain season.
		if ser.UseSceneNumbering && sceneSource {
			eps, err := s.episodes.GetEpisodesBySceneSeason(ctx, ser.ID, mappedSeasonNumber)
			if err != nil {
				return nil, err
			}
			if len(eps) > 0 {
				return eps, nil
			}
		}
		return s.episodes.GetEpisodesBySeason(ctx, ser.ID, mappedSeasonNumber)
	}

	if parsedInfo.IsDaily() {
		ep, err := s.getDailyEpisode(ctx, ser, parsedInfo.AirDate, parsedInfo.DailyPart)
		if err != nil {
			return nil, err
		}
		if ep != nil {
			return []episodes.Episode{*ep}, nil
		}
		return []episodes.Episode{}, nil
	}

	if parsedInfo.IsAbsoluteNumbering() {
		return s.getAnimeEpisodes(ctx, ser, parsedInfo, mappedSeasonNumber, sceneSource)
	}

	if parsedInfo.IsPossibleSceneSeasonSpecial() {
		// Recurse into ParseSpecialEpisodeTitle — the same release
		// title parses as a special if a season-0 episode title
		// matches. C# `:296-302`.
		spec, err := s.ParseSpecialEpisodeTitle(ctx, parsedInfo, parsedInfo.ReleaseTitle, ser)
		if err != nil {
			return nil, err
		}
		if spec != nil {
			// Use the resolved special's season number AND disable
			// scene-source — the resolved season/episode numbers are
			// TVDB-numbered, not scene-numbered. C# `:301`.
			return s.getStandardEpisodes(ctx, ser, spec, spec.SeasonNumber, false)
		}
	}

	if parsedInfo.Special && mappedSeasonNumber != 0 {
		// A specials parse against a non-zero season is a
		// contradiction — the C# code returns empty here.
		return []episodes.Episode{}, nil
	}

	return s.getStandardEpisodes(ctx, ser, parsedInfo, mappedSeasonNumber, sceneSource)
}

// getDailyEpisode ports C# `:557-573` — resolves a daily episode by
// AirDate (with optional DailyPart for double-feature
// disambiguation).
//
// SearchCriteria-driven short-circuit at C# `:561-565` is omitted —
// callers don't pass searchCriteria at this slice. Goes straight to
// the episode finder.
//
// nil result with nil error means "no episode aired on that date" —
// the caller wraps this in an empty list. Errors propagate.
func (s *Service) getDailyEpisode(ctx context.Context, ser *series.Series, airDate string, part *int) (*episodes.Episode, error) {
	return s.episodes.FindEpisodeByAirDate(ctx, ser.ID, airDate, part)
}

// getAnimeEpisodes ports C# `:575-646` — anime / absolute-numbered
// episode resolution.
//
// Three nested arms per absolute-episode-number:
//
//   - Special arm: FindEpisode(seriesID, 0, absoluteEpisodeNumber).
//   - sceneSource arm: 3 sub-cases driven by the scene-season override
//     and the parse's SeasonNumber + EpisodeNumbers state.
//   - Default fallback: FindEpisodeByAbsolute(seriesID, n).
//
// Errors propagate. The aggregator concatenates per-absolute-number
// results in declaration order — C# uses `result.Add(episode)` per
// loop iteration; the Go port does the same.
func (s *Service) getAnimeEpisodes(ctx context.Context, ser *series.Series, parsedInfo *parser.ParsedEpisodeInfo, _ int, sceneSource bool) ([]episodes.Episode, error) {
	result := []episodes.Episode{}

	// C# `:579`: scene-season-number is unconditional but only used
	// inside the sceneSource arm. Skip the call when sceneSource=false
	// — the value would never be read.
	var sceneSeasonNumber *int
	if sceneSource {
		sceneSeasonNumber = s.scenemap.GetSceneSeasonNumber(parsedInfo.SeriesTitle, parsedInfo.ReleaseTitle)
	}

	for _, absoluteEpisodeNumber := range parsedInfo.AbsoluteEpisodeNumbers {
		var matched []episodes.Episode

		switch {
		case parsedInfo.Special:
			// C# `:587-588`: specials map to season 0 + the absolute
			// number (the special-pack convention).
			ep, err := s.episodes.FindEpisode(ctx, ser.ID, 0, absoluteEpisodeNumber)
			if err != nil {
				return nil, err
			}
			if ep != nil {
				matched = []episodes.Episode{*ep}
			}

		case sceneSource && sceneSeasonNumber != nil:
			// C# `:595-604`: explicit scene-season override → scene-
			// numbered lookup with fallback to TVDB-numbered.
			eps, err := s.episodes.FindEpisodesBySceneNumbering(ctx, ser.ID, *sceneSeasonNumber, absoluteEpisodeNumber)
			if err != nil {
				return nil, err
			}
			if len(eps) > 0 {
				matched = eps
			} else {
				ep, err := s.episodes.FindEpisode(ctx, ser.ID, *sceneSeasonNumber, absoluteEpisodeNumber)
				if err != nil {
					return nil, err
				}
				if ep != nil {
					matched = []episodes.Episode{*ep}
				}
			}

		case sceneSource && parsedInfo.SeasonNumber > 1 && len(parsedInfo.EpisodeNumbers) == 0:
			// C# `:605-614`: parse has a season but no episode numbers
			// → use the parsed season number for scene-numbered lookup.
			eps, err := s.episodes.FindEpisodesBySceneNumbering(ctx, ser.ID, parsedInfo.SeasonNumber, absoluteEpisodeNumber)
			if err != nil {
				return nil, err
			}
			if len(eps) > 0 {
				matched = eps
			} else {
				ep, err := s.episodes.FindEpisode(ctx, ser.ID, parsedInfo.SeasonNumber, absoluteEpisodeNumber)
				if err != nil {
					return nil, err
				}
				if ep != nil {
					matched = []episodes.Episode{*ep}
				}
			}

		case sceneSource:
			// C# `:615-624`: bare absolute-number scene query.
			// "Don't allow multiple results without a scene name
			// mapping" — clear when len > 1.
			eps, err := s.episodes.FindEpisodesBySceneAbsolute(ctx, ser.ID, absoluteEpisodeNumber)
			if err != nil {
				return nil, err
			}
			if len(eps) <= 1 {
				matched = eps
			}
		}

		if len(matched) == 0 {
			// C# `:627-631`: default fallback — TVDB-numbered absolute
			// lookup. Runs whenever no earlier arm produced a match.
			ep, err := s.episodes.FindEpisodeByAbsolute(ctx, ser.ID, absoluteEpisodeNumber)
			if err != nil {
				return nil, err
			}
			if ep != nil {
				matched = []episodes.Episode{*ep}
			}
		}

		result = append(result, matched...)
	}

	return result, nil
}

// getStandardEpisodes ports C# `:648-710` — standard SxxExx episode
// resolution.
//
// For each parsedInfo.EpisodeNumber:
//   - sceneNumbering arm (UseSceneNumbering + sceneSource):
//     FindEpisodesBySceneNumbering(mappedSeason, episodeNumber); if
//     found, take all results. C# `:659-684`.
//   - Default arm: FindEpisode(mappedSeason, episodeNumber).
//
// nil EpisodeNumbers returns empty slice early — matches C# `:652-655`
// `new List<Episode>()`.
//
// SearchCriteria-driven short-circuits at C# `:663-666` and
// `:689-692` are omitted — callers don't pass searchCriteria.
func (s *Service) getStandardEpisodes(ctx context.Context, ser *series.Series, parsedInfo *parser.ParsedEpisodeInfo, mappedSeasonNumber int, sceneSource bool) ([]episodes.Episode, error) {
	if parsedInfo.EpisodeNumbers == nil {
		return []episodes.Episode{}, nil
	}
	result := []episodes.Episode{}

	for _, episodeNumber := range parsedInfo.EpisodeNumbers {
		matched := false

		if ser.UseSceneNumbering && sceneSource {
			eps, err := s.episodes.FindEpisodesBySceneNumbering(ctx, ser.ID, mappedSeasonNumber, episodeNumber)
			if err != nil {
				return nil, err
			}
			if len(eps) > 0 {
				result = append(result, eps...)
				matched = true
			}
		}

		if !matched {
			ep, err := s.episodes.FindEpisode(ctx, ser.ID, mappedSeasonNumber, episodeNumber)
			if err != nil {
				return nil, err
			}
			if ep != nil {
				result = append(result, *ep)
			} else {
				s.log.Debug("parsingservice: unable to find episode",
					"series", ser.Title, "season", mappedSeasonNumber,
					"episode", episodeNumber, "release", parsedInfo.ReleaseTitle)
			}
		}
	}

	return result, nil
}

// Map ports the public `IParsingService.Map(parsedEpisodeInfo, tvdbId,
// tvRageId, imdbId, searchCriteria)` overload at C# `Parser/ParsingService.cs:156-159`
// (which thin-wraps the private 6-arg overload at `:176-245`).
//
// Slice 4.S (sub-arc 11a of queue [11]). The end-to-end release-info
// → matched-series + matched-episodes pipeline that the decisionengine
// front-end (DownloadDecisionMaker.GetRssDecision / GetSearchDecision —
// sub-arc 11c) feeds Subject by. Returns a fully-populated
// [*decisionengine.Subject] with ParsedEpisodeInfo + SceneMapping +
// MappedSeasonNumber + Series + Episodes + SeriesMatchType +
// EpisodeRequested + Languages set; Release / CustomFormats /
// CustomFormatScore / SeedConfiguration / DownloadAllowed /
// ReleaseSource remain zero — the caller (RemoteEpisodeAggregationService,
// sub-arc 11b) attaches those.
//
// Direction-of-imports note: parsingservice → decisionengine. The
// natural C# layering is the inverse (DecisionEngine consumes
// ParsingService); the Go port reverses ONLY at the Subject return
// type so the orchestrator doesn't need a glue conversion layer.
// decisionengine.Subject is the canonical "release under decision"
// shape and lives in decisionengine for read-by-spec ergonomics;
// parsingservice is the only producer, so the dep edge is one-way
// and acyclic.
//
// Decision tree (line-aligned with C# `:176-245`):
//
//  1. FindSceneMapping(seriesTitle, releaseTitle, seasonNumber). The
//     result populates Subject.SceneMapping + drives the
//     MappedSeasonNumber adjustment + scene/tvdb origin override.
//     Mirrors C# `:178`.
//
//  2. MappedSeasonNumber = parsedInfo.SeasonNumber, sceneSource = true
//     by default. If the mapping exists and supplies a non-negative
//     SeasonNumber AND its SceneSeasonNumber <= parsedInfo.SeasonNumber,
//     add the season-offset. If SceneOrigin == "tvdb", flip sceneSource
//     to false. Else if Type == "XemService" and the XemService remap
//     fires, set MappedSeasonNumber to the scene-season number.
//     Mirrors C# `:189-208`.
//
//  3. findSeries(parsedInfo, tvdbId, tvRageId, imdbId, sceneMapping,
//     searchCriteria) → matched series + match type. Mirrors C#
//     `:212-217`.
//
//  4. If series is non-nil AND ValidateForSeriesType passes, dispatch
//     to getEpisodes(parsedInfo, series, mappedSeasonNumber, sceneSource)
//     to populate Subject.Episodes. Mirrors C# `:221-228`.
//
//  5. Languages always populated from parsedInfo.Languages. Mirrors C#
//     `:231`.
//
//  6. If searchCriteria != nil, set EpisodeRequested true iff any
//     matched episode appears in searchCriteria.Episodes (by ID).
//     Mirrors C# `:238-242`.
//
// Errors from the seriesFinder / episodeFinder / scenemap helpers
// propagate wrapped; nil parsedInfo / nil receiver returns
// (nil, error).
//
// Production callers: indexersearch.Service.Dispatch search arm and
// commands/handlers/rsssync.go (both wired in sub-arc 11c when
// DownloadDecisionMaker.GetRssDecision + GetSearchDecision land).
func (s *Service) Map(
	ctx context.Context,
	parsedInfo *parser.ParsedEpisodeInfo,
	tvdbID int,
	tvRageID int,
	imdbID string,
	searchCriteria *decisionengine.SearchCriteria,
) (*decisionengine.Subject, error) {
	if s == nil {
		return nil, errors.New("parsingservice: Map on nil receiver")
	}
	if parsedInfo == nil {
		return nil, errors.New("parsingservice: Map: nil parsedEpisodeInfo")
	}

	subject := decisionengine.NewSubject()
	subject.ParsedEpisodeInfo = parsedInfo
	subject.MappedSeasonNumber = parsedInfo.SeasonNumber

	// Step 1: scene-mapping pre-pass. Mirrors C# `:178`.
	sceneMapping := s.scenemap.FindSceneMapping(
		parsedInfo.SeriesTitle,
		parsedInfo.ReleaseTitle,
		parsedInfo.SeasonNumber,
	)
	subject.SceneMapping = sceneMapping
	sceneSource := true

	if sceneMapping != nil {
		// C# `:191-195`. SceneMapping.SeasonNumber is `int?` and
		// SceneSeasonNumber is `int?` — the C# `null <= int` returns
		// false, so the compound check is false when either is null.
		// Mirror with explicit nil checks.
		if sceneMapping.SeasonNumber != nil && *sceneMapping.SeasonNumber >= 0 &&
			sceneMapping.SceneSeasonNumber != nil && *sceneMapping.SceneSeasonNumber <= parsedInfo.SeasonNumber {
			subject.MappedSeasonNumber += *sceneMapping.SeasonNumber - *sceneMapping.SceneSeasonNumber
		}

		switch {
		case sceneMapping.SceneOrigin == "tvdb":
			// tvdb-origin mappings disable scene-source semantics — the
			// parsed numbers are already TVDB-numbered. C# `:197-200`.
			sceneSource = false
		case sceneMapping.Type == "XemService" &&
			sceneMapping.SceneSeasonNumber != nil && *sceneMapping.SceneSeasonNumber >= 0 &&
			parsedInfo.SeasonNumber == 1 &&
			*sceneMapping.SceneSeasonNumber != parsedInfo.SeasonNumber:
			// XemService remap: parsed S01 actually lives under a
			// different TVDB-mapped season. C# `:201-207`.
			subject.MappedSeasonNumber = *sceneMapping.SceneSeasonNumber
		}
	}

	// Step 2: series resolution. C# `:210-219`.
	resolvedSeries, matchType, err := s.findSeries(ctx, parsedInfo, tvdbID, tvRageID, imdbID, sceneMapping, searchCriteria)
	if err != nil {
		return nil, err
	}
	if resolvedSeries != nil {
		subject.Series = resolvedSeries
		subject.SeriesMatchType = matchType
	}

	// Step 3: episode resolution. C# `:221-228`.
	if subject.Series != nil && parser.ValidateForSeriesType(parsedInfo, subject.Series.SeriesType) {
		eps, err := s.getEpisodes(ctx, parsedInfo, subject.Series, subject.MappedSeasonNumber, sceneSource)
		if err != nil {
			return nil, err
		}
		subject.Episodes = eps
	}

	// Languages always set from the parser output. C# `:231`.
	subject.Languages = parsedInfo.Languages

	// Episodes guaranteed non-nil empty slice (NewSubject + the
	// getEpisodes contract both return empty-not-nil); the C# `:233-236`
	// null-coercion arm is implicit.

	// Step 4: searchCriteria-driven EpisodeRequested tag. C# `:238-242`.
	if searchCriteria != nil && len(subject.Episodes) > 0 {
		requested := make(map[int]struct{}, len(searchCriteria.Episodes))
		for _, ep := range searchCriteria.Episodes {
			requested[ep.ID] = struct{}{}
		}
		for _, ep := range subject.Episodes {
			if _, ok := requested[ep.ID]; ok {
				subject.EpisodeRequested = true
				break
			}
		}
	}

	return subject, nil
}

// findSeries ports the private `ParsingService.FindSeries` helper at
// C# `Parser/ParsingService.cs:405-555`. Resolves a parsed-info +
// (tvdbId, tvRageId, imdbId) + (optional sceneMapping, optional
// searchCriteria) to a [*series.Series] + [series.MatchType], or
// (nil, MatchTypeUnknown, nil) for "couldn't resolve".
//
// Decision tree:
//
//  1. sceneMapping non-nil: if searchCriteria.Series.TvdbId matches
//     sceneMapping.TvdbId, return (searchCriteria.Series, Alias). Else
//     dispatch FindByTvdbID(sceneMapping.TvdbId). Either way, the
//     match type is Alias. C# `:409-425`.
//
//  2. searchCriteria non-nil + Series non-nil: try CleanTitle match
//     (Title), tvdbId match (ID), tvRageId match (ID; requires
//     tvdbId <= 0), imdbId match (ID; requires tvdbId <= 0). Each
//     short-circuits with the searchCriteria.Series row. C# `:427-468`.
//
//  3. FindByTitle(parsedInfo.SeriesTitle) — match type Title. C# `:472`.
//
//  4. If still nil AND parsedInfo.SeriesTitleInfo.AllTitles non-empty,
//     dispatch [Service.getSeriesByAllTitles] — match type Title.
//     C# `:479-482`.
//
//  5. If still nil AND parsedInfo.SeriesTitleInfo.Year > 0, try
//     FindByTitleAndYear(TitleWithoutYear, Year) — match type Title.
//     If THAT misses, fall back to scene-mapping FindTvdbId on
//     TitleWithoutYear and then FindByTvdbID — match type Alias.
//     C# `:485-495`.
//
//  6. If still nil AND tvdbId > 0, FindByTvdbID(tvdbId) — match type ID.
//     C# `:497-512`.
//
//  7. If still nil AND tvRageId > 0 AND tvdbId <= 0,
//     FindByTvRageID(tvRageId) — match type ID. C# `:514-529`.
//
//  8. If still nil AND imdbId non-empty AND tvdbId <= 0,
//     FindByImdbID(imdbId) — match type ID. C# `:531-546`.
//
//  9. Return (series, matchType) — nil + Unknown when every arm missed.
//     C# `:548-554`.
//
// Errors from the seriesFinder calls propagate wrapped (except
// [series.ErrNotFound], treated as the C# null arm).
//
// The C# `getSeriesAliasTitleAndYear` private helper at
// `:638-678` (the title-by-AllTitles + Year + scene-mapping fallback
// used by step 5b) is NOT separately ported — its logic is inlined
// into step 5 here; the AllTitles iteration in step 5b is identical
// to the existing [Service.getSeriesByAllTitles] which is shared.
func (s *Service) findSeries(
	ctx context.Context,
	parsedInfo *parser.ParsedEpisodeInfo,
	tvdbID, tvRageID int,
	imdbID string,
	sceneMapping *scenemappings.Mapping,
	searchCriteria *decisionengine.SearchCriteria,
) (*series.Series, series.MatchType, error) {
	// Step 1: scene-mapping short-circuit. C# `:409-425`.
	if sceneMapping != nil {
		if searchCriteria != nil && searchCriteria.Series != nil &&
			searchCriteria.Series.TvdbID == sceneMapping.TvdbID {
			return searchCriteria.Series, series.MatchTypeAlias, nil
		}

		ser, err := s.seriesFinder.FindByTvdbID(ctx, sceneMapping.TvdbID)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, series.MatchTypeUnknown, fmt.Errorf("parsingservice: findSeries: FindByTvdbID(%d): %w", sceneMapping.TvdbID, err)
		}
		if ser == nil {
			s.log.Debug("parsingservice: findSeries: no matching series for scene mapping",
				"seriesTitle", parsedInfo.SeriesTitle, "tvdbId", sceneMapping.TvdbID)
			return nil, series.MatchTypeUnknown, nil
		}
		return ser, series.MatchTypeAlias, nil
	}

	// Step 2: searchCriteria short-circuits. C# `:427-468`.
	if searchCriteria != nil && searchCriteria.Series != nil {
		critSer := searchCriteria.Series
		if critSer.CleanTitle == parser.CleanSeriesTitle(parsedInfo.SeriesTitle) {
			return critSer, series.MatchTypeTitle, nil
		}
		if tvdbID > 0 && tvdbID == critSer.TvdbID {
			return critSer, series.MatchTypeID, nil
		}
		if tvRageID > 0 && tvRageID == critSer.TvRageID && tvdbID <= 0 {
			return critSer, series.MatchTypeID, nil
		}
		if imdbID != "" && imdbID == critSer.ImdbID && tvdbID <= 0 {
			return critSer, series.MatchTypeID, nil
		}
	}

	// Step 3: plain title lookup. C# `:472-477`.
	matchType := series.MatchTypeUnknown
	ser, err := s.seriesFinder.FindByTitle(ctx, parsedInfo.SeriesTitle)
	if err != nil && !errors.Is(err, series.ErrNotFound) {
		return nil, series.MatchTypeUnknown, fmt.Errorf("parsingservice: findSeries: FindByTitle(%q): %w", parsedInfo.SeriesTitle, err)
	}
	if ser != nil {
		matchType = series.MatchTypeTitle
	}

	// Step 4: AllTitles fallback. C# `:479-483`.
	if ser == nil && parsedInfo.SeriesTitleInfo != nil && len(parsedInfo.SeriesTitleInfo.AllTitles) > 0 {
		ser, err = s.getSeriesByAllTitles(ctx, parsedInfo)
		if err != nil {
			return nil, series.MatchTypeUnknown, err
		}
		if ser != nil {
			matchType = series.MatchTypeTitle
		}
	}

	// Step 5: TitleWithoutYear + Year fallback. C# `:485-495`.
	if ser == nil && parsedInfo.SeriesTitleInfo != nil && parsedInfo.SeriesTitleInfo.Year > 0 {
		ser, err = s.seriesFinder.FindByTitleAndYear(
			ctx,
			parsedInfo.SeriesTitleInfo.TitleWithoutYear,
			parsedInfo.SeriesTitleInfo.Year,
		)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, series.MatchTypeUnknown, fmt.Errorf("parsingservice: findSeries: FindByTitleAndYear(%q,%d): %w",
				parsedInfo.SeriesTitleInfo.TitleWithoutYear,
				parsedInfo.SeriesTitleInfo.Year, err)
		}
		if ser != nil {
			matchType = series.MatchTypeTitle
		} else {
			// 5b: scene-mapping FindTvdbId(TitleWithoutYear, ReleaseTitle, SeasonNumber)
			// → FindByTvdbID. Mirrors the C# `GetSeriesAliasTitleAndYear`
			// helper at `:638-678` — the alias-by-AllTitles path that
			// `FindByTitleAndYear` doesn't cover.
			aliasMapping := s.scenemap.FindSceneMapping(
				parsedInfo.SeriesTitleInfo.TitleWithoutYear,
				parsedInfo.ReleaseTitle,
				parsedInfo.SeasonNumber,
			)
			if aliasMapping != nil && aliasMapping.TvdbID != 0 {
				aliasSer, aliasErr := s.seriesFinder.FindByTvdbID(ctx, aliasMapping.TvdbID)
				if aliasErr != nil && !errors.Is(aliasErr, series.ErrNotFound) {
					return nil, series.MatchTypeUnknown, fmt.Errorf("parsingservice: findSeries: FindByTvdbID(alias %d): %w", aliasMapping.TvdbID, aliasErr)
				}
				// The C# `GetSeriesAliasTitleAndYear` cross-checks the
				// resolved series's Year against parsedInfo.Year — if
				// they disagree, abandon the alias resolution. Mirror
				// that guard so the "wrong year" MapFixture case ends
				// with series=nil even though FindByTvdbID returned a
				// row. series.Series.Year is *int (nullable); a nil
				// Year on the resolved series counts as mismatch (no
				// year to cross-check against).
				if aliasSer != nil && aliasSer.Year != nil && *aliasSer.Year == parsedInfo.SeriesTitleInfo.Year {
					ser = aliasSer
					matchType = series.MatchTypeAlias
				}
			}
		}
	}

	// Step 6: TvdbID fallback. C# `:497-512`.
	if ser == nil && tvdbID > 0 {
		ser, err = s.seriesFinder.FindByTvdbID(ctx, tvdbID)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, series.MatchTypeUnknown, fmt.Errorf("parsingservice: findSeries: FindByTvdbID(%d): %w", tvdbID, err)
		}
		if ser != nil {
			matchType = series.MatchTypeID
		}
	}

	// Step 7: TvRageID fallback. C# `:514-529`.
	if ser == nil && tvRageID > 0 && tvdbID <= 0 {
		ser, err = s.seriesFinder.FindByTvRageID(ctx, tvRageID)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, series.MatchTypeUnknown, fmt.Errorf("parsingservice: findSeries: FindByTvRageID(%d): %w", tvRageID, err)
		}
		if ser != nil {
			matchType = series.MatchTypeID
		}
	}

	// Step 8: ImdbID fallback. C# `:531-546`.
	if ser == nil && imdbID != "" && tvdbID <= 0 {
		ser, err = s.seriesFinder.FindByImdbID(ctx, imdbID)
		if err != nil && !errors.Is(err, series.ErrNotFound) {
			return nil, series.MatchTypeUnknown, fmt.Errorf("parsingservice: findSeries: FindByImdbID(%q): %w", imdbID, err)
		}
		if ser != nil {
			matchType = series.MatchTypeID
		}
	}

	if ser == nil {
		s.log.Debug("parsingservice: findSeries: no matching series",
			"seriesTitle", parsedInfo.SeriesTitle)
		return nil, series.MatchTypeUnknown, nil
	}
	return ser, matchType, nil
}
