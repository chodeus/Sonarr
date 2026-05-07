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
// Deferred:
//
//   - The 5-arg `ParseSpecialEpisodeTitle` overload at C# `:313-362`
//     that takes (tvdbId, tvRageId, imdbId, searchCriteria) — only
//     consumer is `ImportApprovedEpisodes` (writer slice, also
//     deferred). Adding it now would be speculative.
//
//   - Full Map (with FindSeries fallback) / GetSeries / IsTitleParsed
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
	"log/slog"

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

// Service ports a subset of `IParsingService` at
// `Parser/ParsingService.cs:1-712`. This slice ports
// [Service.ParseSpecialEpisodeTitle] + [Service.GetEpisodes]; see the
// package doc for the deferred-method list.
//
// Goroutine-safe post-construction: stateless beyond the wired deps,
// which themselves must be goroutine-safe (the production deps are).
type Service struct {
	episodes EpisodeFinder
	infos    ParsedEpisodeInfoBuilder
	scenemap SceneMappingFinder
	log      *slog.Logger
}

// NewService binds the dependencies. episodes is REQUIRED. infos,
// scenemap, and log are OPTIONAL:
//
//   - infos == nil → [defaultParsedEpisodeInfoBuilder] (delegates to
//     parser/quality free functions).
//   - scenemap == nil → [noopSceneMappingFinder] (returns nil from
//     both methods; matches "no scene mappings registered").
//   - log == nil → [slog.Default].
//
// Returns error rather than panicking for the required-nil arm so
// the caller's startup wiring path can surface the misuse cleanly.
// Mirrors the constructor pattern in [episodeimport.NewAggregationService]
// (slice 3.60) and the more recent service slices (3.63, 3.64, 3.65).
func NewService(episodes EpisodeFinder, infos ParsedEpisodeInfoBuilder, scenemap SceneMappingFinder, log *slog.Logger) (*Service, error) {
	if episodes == nil {
		return nil, errors.New("parsingservice: NewService: nil EpisodeFinder")
	}
	if infos == nil {
		infos = defaultParsedEpisodeInfoBuilder{}
	}
	if scenemap == nil {
		scenemap = noopSceneMappingFinder{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{episodes: episodes, infos: infos, scenemap: scenemap, log: log}, nil
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
