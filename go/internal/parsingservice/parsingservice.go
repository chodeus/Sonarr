// Package parsingservice ports `NzbDrone.Core.Parser.ParsingService`
// (`src/NzbDrone.Core/Parser/ParsingService.cs:1-712`) — the Series-
// + Episode-aware orchestration layer that sits BETWEEN the regex-
// driven [parser] package (which produces `ParsedEpisodeInfo` from
// raw release titles) and the matched-episode list every downstream
// import / decision pipeline consumes.
//
// This slice (3.66) lands ONLY [Service.ParseSpecialEpisodeTitle] —
// the 3-arg overload at `ParsingService.cs:364-403`. It's the
// smallest viable subset that closes one half of the
// [episodeimport.EpisodeParsingService] interface (slice 3.61's
// AggregateEpisodes consumer).
//
// Deferred to slice 3.67:
//
//   - `Service.GetEpisodes(parsedInfo, series, sceneSource)` — the
//     other half of [episodeimport.EpisodeParsingService]. The
//     private `getEpisodes` 4-arg dispatcher and its three helpers
//     (GetDailyEpisode / GetAnimeEpisodes / GetStandardEpisodes)
//     port alongside; the sceneSource=true path additionally
//     requires Map (which depends on a SceneMappingFinder narrow
//     interface).
//
//   - The 5-arg `ParseSpecialEpisodeTitle` overload at C# `:313-362`
//     that takes (tvdbId, tvRageId, imdbId, searchCriteria) — that
//     overload's only caller is `ImportApprovedEpisodes` (writer
//     slice, also deferred). Adding it now would be speculative.
//
//   - Map / FindSeries / GetSeries / IsTitleParsed-style methods —
//     none have ported consumers yet.
//
//   - The seven `IHandle<...>` event-bus subscribers — defer with
//     the unported event shapes.
package parsingservice

import (
	"context"
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
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
	// mapped specials at C# `:369`.
	FindEpisodesBySceneNumbering(ctx context.Context, seriesID, sceneSeasonNumber, sceneEpisodeNumber int) ([]episodes.Episode, error)

	// FindEpisodeByTitle returns the (single) episode in
	// (seriesID, seasonNumber) whose title appears as a substring
	// of the release title. Used by ParseSpecialEpisodeTitle to
	// resolve special episodes by name match against season 0 at
	// C# `:376`.
	//
	// nil result with nil error means "no match" — the C# version
	// returns null in the same case (`:376` `_episodeService.FindEpisodeByTitle(...)`
	// uses `FirstOrDefault`).
	FindEpisodeByTitle(ctx context.Context, seriesID, seasonNumber int, releaseTitle string) (*episodes.Episode, error)
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

// Service ports a subset of `IParsingService` at
// `Parser/ParsingService.cs:1-712`. This slice ports
// [Service.ParseSpecialEpisodeTitle] only; see the package doc for
// the deferred-method list.
//
// Goroutine-safe post-construction: stateless beyond the wired deps,
// which themselves must be goroutine-safe (the production deps are).
type Service struct {
	episodes EpisodeFinder
	infos    ParsedEpisodeInfoBuilder
	log      *slog.Logger
}

// NewService binds the dependencies. episodes is REQUIRED. infos is
// OPTIONAL (nil → [defaultParsedEpisodeInfoBuilder]); log is OPTIONAL
// (nil → [slog.Default]).
//
// Returns error rather than panicking for the required-nil arm so
// the caller's startup wiring path can surface the misuse cleanly.
// Mirrors the constructor pattern in [episodeimport.NewAggregationService]
// (slice 3.60) and the more recent service slices (3.63, 3.64, 3.65).
func NewService(episodes EpisodeFinder, infos ParsedEpisodeInfoBuilder, log *slog.Logger) (*Service, error) {
	if episodes == nil {
		return nil, errors.New("parsingservice: NewService: nil EpisodeFinder")
	}
	if infos == nil {
		infos = defaultParsedEpisodeInfoBuilder{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{episodes: episodes, infos: infos, log: log}, nil
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
