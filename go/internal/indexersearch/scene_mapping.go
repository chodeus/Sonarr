package indexersearch

import (
	"context"
	"strings"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/scenemappings"
	"sonarr.tv/sonarr/internal/series"
)

// SceneEpisodeMapping is the per-episode scene-mapping record produced
// by getSceneEpisodeMappings. Each entry corresponds to one
// SceneMappings row that survived the season-filter, plus a synthetic
// "global" entry when no row name-shadows the series title.
//
// C# cite: src/NzbDrone.Core/IndexerSearch/Definitions/SceneEpisodeMapping.cs:7-32.
//
// Equality (used to dedup across multiple SceneMappings rows that all
// resolve to the same scene-search shape) is keyed on
// (SeasonNumber, EpisodeNumber, SearchMode) per the C# Equals override
// at :22-30. The Go port models this with the small comparable struct
// `sceneEpisodeKey` rather than overriding any equality method, since
// Go's `map[K]V` natively handles struct keys.
type SceneEpisodeMapping struct {
	Episode               *episodes.Episode
	SearchMode            SearchMode
	SceneTitles           []string
	SeasonNumber          int
	EpisodeNumber         int
	AbsoluteEpisodeNumber *int
}

// SceneSeasonMapping is the per-season scene-mapping record produced
// by getSceneSeasonMappings. C# cite:
// src/NzbDrone.Core/IndexerSearch/Definitions/SceneSeasonMapping.cs:7-31.
//
// Equality (per C# :20-29) keys on (SeasonNumber, SearchMode); the Go
// port models this with `sceneSeasonKey` below.
type SceneSeasonMapping struct {
	Episodes       []episodes.Episode
	EpisodeMapping SceneEpisodeMapping
	SearchMode     SearchMode
	SceneTitles    []string
	SeasonNumber   int
}

type sceneEpisodeKey struct {
	SeasonNumber  int
	EpisodeNumber int
	SearchMode    SearchMode
}

type sceneSeasonKey struct {
	SeasonNumber int
	SearchMode   SearchMode
}

// nonNegative ports the C# `int?.NonNegative()` extension at
// `NzbDrone.Common.Extensions.IntExtensions.NonNegative` — returns nil
// for negative values, otherwise the value as-is.
//
// The C# tree uses negative SeasonNumber values (-1) as "missing"
// sentinels in some persisted scene-mapping rows; .NonNegative() lets
// the search code treat them like nullable ints.
func nonNegative(p *int) *int {
	if p == nil || *p < 0 {
		return nil
	}
	return p
}

// getSceneEpisodeMappings ports
// ReleaseSearchService.GetSceneEpisodeMappings(series, episode,
// sceneMappings) at :236-321 — the inner "iterate every scene-mapping
// row, decide whether it applies to this episode" loop.
//
// Returns the per-episode mapping list, including the synthetic
// "global" entry (line :309-320) when no row name-shadows the series
// title. Walking the C# logic carefully:
//
//  1. For each SceneMappings row:
//     a. ignoreSceneNumbering = SceneOrigin in {"tvdb", "unknown:tvdb"}.
//     b. mappingSceneSeasonNumber = .SceneSeasonNumber.NonNegative().
//     c. mappingSeasonNumber = .SeasonNumber.NonNegative().
//     d. mappedSeasonNumber = ignoreSceneNumbering ? episode.SeasonNumber
//     : (episode.SceneSeasonNumber ?? episode.SeasonNumber).
//     e. releaseSeasonNumber = .SceneSeasonNumber.NonNegative()
//     ?? mappedSeasonNumber.
//     f. Two filter branches:
//     - If mapping has SceneSeasonNumber: mappedAlt = .SeasonNumber
//     ?? .SceneSeasonNumber ?? mappedSeasonNumber. Skip when
//     mappedAlt != mappedSeasonNumber.
//     - Else: skip when mappingSeasonNumber.HasValue and
//     mappingSeasonNumber.Value != episode.SeasonNumber.
//     g. If mapping.SearchTerm == series.Title and FilterRegex empty,
//     set includeGlobal = false.
//     h. Compute searchMode (Default fall-through, or SearchTitle when
//     SceneSeasonNumber and a title mismatch).
//     i. Yield a SceneEpisodeMapping with SeasonNumber =
//     releaseSeasonNumber and EpisodeNumber/Absolute set per the
//     ignoreSceneNumbering branch.
//  2. If includeGlobal: yield the implicit (series.Title) mapping.
//
// The Go port mirrors the C# branch shapes verbatim — re-ordering or
// short-circuiting any step risks divergence on edge-case mappings.
func getSceneEpisodeMappings(s *series.Series, episode *episodes.Episode, sceneMappings []scenemappings.Mapping) []SceneEpisodeMapping {
	var out []SceneEpisodeMapping
	includeGlobal := true

	for i := range sceneMappings {
		m := &sceneMappings[i]

		ignoreSceneNumbering := m.SceneOrigin == "tvdb" || m.SceneOrigin == "unknown:tvdb"
		mappingSceneSeasonNumber := nonNegative(m.SceneSeasonNumber)
		mappingSeasonNumber := nonNegative(m.SeasonNumber)

		var mappedSeasonNumber int
		switch {
		case ignoreSceneNumbering:
			mappedSeasonNumber = episode.SeasonNumber
		case episode.SceneSeasonNumber != nil:
			mappedSeasonNumber = *episode.SceneSeasonNumber
		default:
			mappedSeasonNumber = episode.SeasonNumber
		}

		releaseSeasonNumber := mappedSeasonNumber
		if mappingSceneSeasonNumber != nil {
			releaseSeasonNumber = *mappingSceneSeasonNumber
		}

		if mappingSceneSeasonNumber != nil {
			// Apply the alternative mapping (release to scene/tvdb).
			mappedAltSeasonNumber := mappedSeasonNumber
			switch {
			case mappingSeasonNumber != nil:
				mappedAltSeasonNumber = *mappingSeasonNumber
			case mappingSceneSeasonNumber != nil:
				mappedAltSeasonNumber = *mappingSceneSeasonNumber
			}
			if mappedAltSeasonNumber != mappedSeasonNumber {
				continue
			}
		} else if mappingSeasonNumber != nil && *mappingSeasonNumber != episode.SeasonNumber {
			continue
		}

		if m.SearchTerm == s.Title && m.FilterRegex == "" {
			// Disable the implied mapping when an explicit row by the
			// same name exists.
			includeGlobal = false
		}

		searchMode := computeSearchMode(m, s, mappingSceneSeasonNumber)

		mapping := SceneEpisodeMapping{
			Episode:      episode,
			SearchMode:   searchMode,
			SceneTitles:  []string{m.SearchTerm},
			SeasonNumber: releaseSeasonNumber,
		}
		if ignoreSceneNumbering {
			mapping.EpisodeNumber = episode.EpisodeNumber
			mapping.AbsoluteEpisodeNumber = episode.AbsoluteEpisodeNumber
		} else {
			if episode.SceneEpisodeNumber != nil {
				mapping.EpisodeNumber = *episode.SceneEpisodeNumber
			} else {
				mapping.EpisodeNumber = episode.EpisodeNumber
			}
			if episode.SceneAbsoluteEpisodeNumber != nil {
				mapping.AbsoluteEpisodeNumber = episode.SceneAbsoluteEpisodeNumber
			} else {
				mapping.AbsoluteEpisodeNumber = episode.AbsoluteEpisodeNumber
			}
		}
		out = append(out, mapping)
	}

	if includeGlobal {
		// Implicit fallback: search by series.Title.
		mapping := SceneEpisodeMapping{
			Episode:     episode,
			SearchMode:  SearchModeDefault,
			SceneTitles: []string{s.Title},
		}
		if episode.SceneSeasonNumber != nil {
			mapping.SeasonNumber = *episode.SceneSeasonNumber
		} else {
			mapping.SeasonNumber = episode.SeasonNumber
		}
		if episode.SceneEpisodeNumber != nil {
			mapping.EpisodeNumber = *episode.SceneEpisodeNumber
		} else {
			mapping.EpisodeNumber = episode.EpisodeNumber
		}
		// Note: the C# tree at :318 has a ?: chain that selects
		// SceneSeasonNumber as the AbsoluteEpisodeNumber fallback when
		// SceneSeasonNumber is set — that's a C# bug (the season-number
		// shadowing the absolute slot), but we mirror it verbatim per
		// the FAITHFULNESS rule.
		if episode.SceneSeasonNumber != nil {
			mapping.AbsoluteEpisodeNumber = episode.SceneSeasonNumber
		} else {
			mapping.AbsoluteEpisodeNumber = episode.AbsoluteEpisodeNumber
		}
		out = append(out, mapping)
	}

	return out
}

// computeSearchMode mirrors the C# `searchMode = ... ?? ((mapping
// SceneSeasonNumber.HasValue && series.CleanTitle != searchTerm
// .CleanSeriesTitle()) ? SearchTitle : Default)` ternary at :281.
//
// The C# tree calls .CleanSeriesTitle() — which performs the same
// regex-strip-then-lowercase the Go port already implements via
// series.CleanTitle (the persisted column). To match the C# comparison
// without re-deriving, we lowercase + strip-non-alphanum the SearchTerm
// inline.
func computeSearchMode(m *scenemappings.Mapping, s *series.Series, mappingSceneSeasonNumber *int) SearchMode {
	if m.SearchMode != nil {
		return SearchMode(*m.SearchMode)
	}
	if mappingSceneSeasonNumber != nil && s.CleanTitle != cleanSeriesTitle(m.SearchTerm) {
		return SearchModeSearchTitle
	}
	return SearchModeDefault
}

// cleanSeriesTitle ports `Parser.CleanSeriesTitle` at
// src/NzbDrone.Core/Parser/Parser.cs:CleanSeriesTitle. Lowercase +
// strip non-alphanumeric. Used here (and only here) to match the C#
// derived comparison against series.CleanTitle.
func cleanSeriesTitle(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range title {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// getSceneEpisodeMappingsForEpisode ports the public overload at
// ReleaseSearchService.cs:208-234 — fetches scene mappings from the
// SceneMapper, runs the inner getSceneEpisodeMappings, then dedups
// the result by sceneEpisodeKey, merging SceneTitles distinctively
// (case-insensitive).
func getSceneEpisodeMappingsForEpisode(ctx context.Context, mapper SceneMapper, s *series.Series, episode *episodes.Episode) ([]SceneEpisodeMapping, error) {
	rows, err := mapper.FindByTvdbID(ctx, s.TvdbID)
	if err != nil {
		return nil, err
	}
	mappings := getSceneEpisodeMappings(s, episode, rows)

	dict := make(map[sceneEpisodeKey]*SceneEpisodeMapping, len(mappings))
	order := make([]sceneEpisodeKey, 0, len(mappings))
	for i := range mappings {
		em := mappings[i]
		key := sceneEpisodeKey{em.SeasonNumber, em.EpisodeNumber, em.SearchMode}
		if existing, ok := dict[key]; ok {
			existing.SceneTitles = append(existing.SceneTitles, em.SceneTitles...)
			continue
		}
		// Copy SceneTitles slice so the merge above doesn't alias the
		// caller's slice in-place.
		em.SceneTitles = append([]string(nil), em.SceneTitles...)
		dict[key] = &em
		order = append(order, key)
	}
	out := make([]SceneEpisodeMapping, 0, len(order))
	for _, k := range order {
		v := dict[k]
		v.SceneTitles = distinctTitles(v.SceneTitles)
		out = append(out, *v)
	}
	return out, nil
}

// getSceneSeasonMappings ports
// ReleaseSearchService.GetSceneSeasonMappings(series, episodes) at
// :163-206. The episodes are grouped by ((SceneSeasonNumber ??
// SeasonNumber)*100000 + SeasonNumber); each group's first episode is
// fed through getSceneEpisodeMappings; each per-episode mapping is
// merged into a sceneSeasonKey-keyed dict, and Episodes/SceneTitles
// are dedup-merged per :199-203.
func getSceneSeasonMappings(ctx context.Context, mapper SceneMapper, s *series.Series, eps []episodes.Episode) ([]SceneSeasonMapping, error) {
	if len(eps) == 0 {
		return nil, nil
	}
	rows, err := mapper.FindByTvdbID(ctx, s.TvdbID)
	if err != nil {
		return nil, err
	}

	// Group episodes by ((SceneSeasonNumber ?? SeasonNumber)*100000 +
	// SeasonNumber). C# uses Lookup; Go uses an ordered slice-of-groups
	// keyed by an int so iteration stays deterministic.
	type group struct {
		key int
		eps []episodes.Episode
	}
	var groups []group
	keyIdx := make(map[int]int)
	for _, e := range eps {
		sceneSN := e.SeasonNumber
		if e.SceneSeasonNumber != nil {
			sceneSN = *e.SceneSeasonNumber
		}
		k := sceneSN*100000 + e.SeasonNumber
		idx, ok := keyIdx[k]
		if !ok {
			keyIdx[k] = len(groups)
			groups = append(groups, group{key: k, eps: []episodes.Episode{e}})
			continue
		}
		groups[idx].eps = append(groups[idx].eps, e)
	}

	dict := make(map[sceneSeasonKey]*SceneSeasonMapping)
	order := make([]sceneSeasonKey, 0)
	for _, g := range groups {
		first := g.eps[0]
		episodeMappings := getSceneEpisodeMappings(s, &first, rows)
		for i := range episodeMappings {
			em := episodeMappings[i]
			seasonMapping := SceneSeasonMapping{
				Episodes:       append([]episodes.Episode(nil), g.eps...),
				EpisodeMapping: em,
				SceneTitles:    append([]string(nil), em.SceneTitles...),
				SearchMode:     em.SearchMode,
				SeasonNumber:   em.SeasonNumber,
			}
			key := sceneSeasonKey{em.SeasonNumber, em.SearchMode}
			if existing, ok := dict[key]; ok {
				existing.Episodes = append(existing.Episodes, seasonMapping.Episodes...)
				existing.SceneTitles = append(existing.SceneTitles, seasonMapping.SceneTitles...)
				continue
			}
			cp := seasonMapping
			dict[key] = &cp
			order = append(order, key)
		}
	}

	// Per C# :199-203 — dedupe Episodes (by reference equality, ported
	// here as id-equality) and SceneTitles (case-insensitive).
	out := make([]SceneSeasonMapping, 0, len(order))
	for _, k := range order {
		v := dict[k]
		v.Episodes = distinctEpisodes(v.Episodes)
		v.SceneTitles = distinctTitles(v.SceneTitles)
		out = append(out, *v)
	}
	return out, nil
}

// distinctTitles dedupes a slice of strings case-insensitively,
// preserving the first occurrence's casing. Mirrors the C#
// `Distinct(StringComparer.InvariantCultureIgnoreCase)` at :202.
func distinctTitles(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		k := strings.ToLower(t)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, t)
	}
	return out
}

// distinctEpisodes dedupes by Episode.ID, preserving order. Mirrors
// the C# `.Distinct().ToList()` at :201 — C# default reference-equality
// + Episode being a class makes that an identity dedupe; the Go port
// keys on ID since Episode is a value type.
func distinctEpisodes(in []episodes.Episode) []episodes.Episode {
	seen := make(map[int]struct{}, len(in))
	out := make([]episodes.Episode, 0, len(in))
	for _, e := range in {
		if _, ok := seen[e.ID]; ok {
			continue
		}
		seen[e.ID] = struct{}{}
		out = append(out, e)
	}
	return out
}
