package indexersearch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// SearchSettings carries the per-call flags every public entry point
// accepts. Mirrors the C# parameter triplet (monitoredOnly,
// userInvokedSearch, interactiveSearch) on every ISearchForReleases
// method — bundled here so the Go signatures don't grow long parameter
// lists.
//
// MonitoredOnly: when true, exclude unmonitored episodes from the
// search target list. Mirrors `monitoredOnly` /
// `MonitoredEpisodesOnly` at SearchCriteriaBase.cs:21 +
// ReleaseSearchService.cs uses.
//
// UserInvokedSearch: stamps the criteria so downstream specs
// (RetentionSpecification, MinimumAgeSpecification) can honour the
// user-invoked-bypass paths.
//
// InteractiveSearch: stamps the criteria so the orchestrator dispatches
// to EnableInteractiveSearch indexers instead of EnableAutomaticSearch.
type SearchSettings struct {
	MonitoredOnly     bool
	UserInvokedSearch bool
	InteractiveSearch bool
}

// EpisodeSearch ports `ISearchForReleases.EpisodeSearch(int episodeId,
// ...)` at ReleaseSearchService.cs:52-57 — the by-id entry point that
// loads the episode then dispatches to EpisodeSearchByEpisode.
//
// Returns aggregated raw releases. Decision conversion + dedup is
// deferred to queue [7c] (decisionengine wire-through); mirroring the
// C# DownloadDecision return shape requires the parsing/aggregation
// front-end which hasn't landed yet.
func (s *Service) EpisodeSearch(ctx context.Context, episodeID int, settings SearchSettings) ([]decisionengine.Decision, error) {
	episode, err := s.episodeGet.GetEpisode(ctx, episodeID)
	if err != nil {
		return nil, fmt.Errorf("indexersearch: load episode %d: %w", episodeID, err)
	}
	return s.EpisodeSearchByEpisode(ctx, episode, settings)
}

// EpisodeSearchByEpisode ports the (Episode episode, ...) overload at
// ReleaseSearchService.cs:59-94. Selects between SearchSingle /
// SearchDaily / SearchAnime / SearchSpecial based on the series type
// and the episode's season-zero / scene-numbering shape.
//
// Errors mirror the C# SearchFailedException for daily-without-airdate
// — returned via plain `errors.New` here since the Go port lacks a
// typed search-exception hierarchy at this layer.
func (s *Service) EpisodeSearchByEpisode(ctx context.Context, episode *episodes.Episode, settings SearchSettings) ([]decisionengine.Decision, error) {
	if episode == nil {
		return nil, errors.New("indexersearch: nil episode")
	}
	ser, err := s.seriesGet.GetSeries(ctx, episode.SeriesID)
	if err != nil {
		return nil, fmt.Errorf("indexersearch: load series %d: %w", episode.SeriesID, err)
	}

	switch ser.SeriesType {
	case series.SeriesTypeDaily:
		if strings.TrimSpace(episode.AirDate) == "" {
			s.log.Error("indexersearch: daily episode missing air date",
				slog.Int("episodeId", episode.ID))
			return nil, errors.New("indexersearch: air date is missing")
		}
		return s.searchDaily(ctx, ser, episode, false, settings)
	case series.SeriesTypeAnime:
		if episode.SeasonNumber == 0 &&
			episode.SceneAbsoluteEpisodeNumber == nil &&
			episode.AbsoluteEpisodeNumber == nil {
			// Specials in season 0 without absolute episode numbers
			// fall back to the SearchSpecial path. Mirrors C# :76-82.
			return s.searchSpecial(ctx, ser, []episodes.Episode{*episode}, false, settings)
		}
		return s.searchAnime(ctx, ser, episode, false, settings, false)
	}

	if episode.SeasonNumber == 0 {
		return s.searchSpecial(ctx, ser, []episodes.Episode{*episode}, false, settings)
	}
	return s.searchSingle(ctx, ser, episode, false, settings)
}

// SeasonSearch ports the (seriesId, seasonNumber, missingOnly,
// monitoredOnly, ...) overload at ReleaseSearchService.cs:96-112 —
// loads the season's episodes, optionally filters to !HasFile, and
// hands off to SeasonSearchByEpisodes.
func (s *Service) SeasonSearch(ctx context.Context, seriesID, seasonNumber int, missingOnly bool, settings SearchSettings) ([]decisionengine.Decision, error) {
	eps, err := s.episodeGet.GetEpisodesBySeason(ctx, seriesID, seasonNumber)
	if err != nil {
		return nil, fmt.Errorf("indexersearch: load season %d/%d: %w", seriesID, seasonNumber, err)
	}
	if missingOnly {
		filtered := eps[:0]
		for _, e := range eps {
			// HasFile equivalent: C# Episode.HasFile is the
			// derived `EpisodeFileId.HasValue && Value > 0` check —
			// inlined here since the Go port doesn't expose a
			// HasFile method on episodes.Episode.
			if e.EpisodeFileID == nil || *e.EpisodeFileID == 0 {
				filtered = append(filtered, e)
			}
		}
		eps = filtered
	}
	if len(eps) == 0 {
		s.log.Debug("indexersearch: no wanted episodes for season",
			slog.Int("seriesId", seriesID), slog.Int("seasonNumber", seasonNumber))
		return nil, nil
	}
	return s.SeasonSearchByEpisodes(ctx, seriesID, seasonNumber, eps, settings)
}

// SeasonSearchByEpisodes ports the (seriesId, seasonNumber, episodes,
// monitoredOnly, ...) overload at ReleaseSearchService.cs:114-161.
// Branches on the series type, then for standard series iterates the
// per-mapping search using single/season criteria.
func (s *Service) SeasonSearchByEpisodes(ctx context.Context, seriesID, seasonNumber int, eps []episodes.Episode, settings SearchSettings) ([]decisionengine.Decision, error) {
	ser, err := s.seriesGet.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("indexersearch: load series %d: %w", seriesID, err)
	}

	switch ser.SeriesType {
	case series.SeriesTypeAnime:
		return s.searchAnimeSeason(ctx, ser, eps, settings)
	case series.SeriesTypeDaily:
		return s.searchDailySeason(ctx, ser, eps, settings)
	}

	mappings, err := getSceneSeasonMappings(ctx, s.sceneMapper, ser, eps)
	if err != nil {
		return nil, fmt.Errorf("indexersearch: scene-season mappings: %w", err)
	}

	var aggregated []decisionengine.Decision
	for _, m := range mappings {
		if m.SeasonNumber == 0 {
			rs, err := s.searchSpecial(ctx, ser, m.Episodes, settings.MonitoredOnly, settings)
			if err != nil {
				return nil, err
			}
			aggregated = append(aggregated, rs...)
			continue
		}

		if len(m.Episodes) == 1 {
			spec := SingleEpisodeCriteria{
				CriteriaBase:  s.criteriaForSeasonMapping(ser, m, settings),
				SeasonNumber:  m.SeasonNumber,
				EpisodeNumber: m.EpisodeMapping.EpisodeNumber,
			}
			rs, err := s.dispatchSearch(ctx, spec)
			if err != nil {
				return nil, err
			}
			aggregated = append(aggregated, rs...)
			continue
		}

		spec := SeasonCriteria{
			CriteriaBase: s.criteriaForSeasonMapping(ser, m, settings),
			SeasonNumber: m.SeasonNumber,
		}
		rs, err := s.dispatchSearch(ctx, spec)
		if err != nil {
			return nil, err
		}
		aggregated = append(aggregated, rs...)
	}
	return aggregated, nil
}

// searchSingle ports SearchSingle :323-340 — iterate the per-episode
// mappings, build a SingleEpisodeCriteria per mapping, dispatch.
func (s *Service) searchSingle(ctx context.Context, ser *series.Series, episode *episodes.Episode, monitoredOnly bool, settings SearchSettings) ([]decisionengine.Decision, error) {
	mappings, err := getSceneEpisodeMappingsForEpisode(ctx, s.sceneMapper, ser, episode)
	if err != nil {
		return nil, err
	}
	settings.MonitoredOnly = monitoredOnly
	var aggregated []decisionengine.Decision
	for _, m := range mappings {
		spec := SingleEpisodeCriteria{
			CriteriaBase:  s.criteriaForEpisodeMapping(ser, m, settings),
			SeasonNumber:  m.SeasonNumber,
			EpisodeNumber: m.EpisodeNumber,
		}
		rs, err := s.dispatchSearch(ctx, spec)
		if err != nil {
			return nil, err
		}
		aggregated = append(aggregated, rs...)
	}
	return aggregated, nil
}

// searchDaily ports SearchDaily :342-351.
func (s *Service) searchDaily(ctx context.Context, ser *series.Series, episode *episodes.Episode, monitoredOnly bool, settings SearchSettings) ([]decisionengine.Decision, error) {
	settings.MonitoredOnly = monitoredOnly
	airDate, err := time.Parse("2006-01-02", episode.AirDate)
	if err != nil {
		return nil, fmt.Errorf("indexersearch: parse airDate %q: %w", episode.AirDate, err)
	}
	base, err := s.criteriaForEpisodes(ctx, ser, []episodes.Episode{*episode}, settings)
	if err != nil {
		return nil, err
	}
	spec := DailyEpisodeCriteria{CriteriaBase: base, AirDate: airDate}
	return s.dispatchSearch(ctx, spec)
}

// searchAnime ports SearchAnime :353-366.
func (s *Service) searchAnime(ctx context.Context, ser *series.Series, episode *episodes.Episode, monitoredOnly bool, settings SearchSettings, isSeasonSearch bool) ([]decisionengine.Decision, error) {
	settings.MonitoredOnly = monitoredOnly
	base, err := s.criteriaForEpisodes(ctx, ser, []episodes.Episode{*episode}, settings)
	if err != nil {
		return nil, err
	}
	spec := AnimeEpisodeCriteria{
		CriteriaBase:   base,
		IsSeasonSearch: isSeasonSearch,
	}
	if episode.SceneSeasonNumber != nil {
		spec.SeasonNumber = *episode.SceneSeasonNumber
	} else {
		spec.SeasonNumber = episode.SeasonNumber
	}
	if episode.SceneEpisodeNumber != nil {
		spec.EpisodeNumber = *episode.SceneEpisodeNumber
	} else {
		spec.EpisodeNumber = episode.EpisodeNumber
	}
	switch {
	case episode.SceneAbsoluteEpisodeNumber != nil:
		spec.AbsoluteEpisodeNumber = *episode.SceneAbsoluteEpisodeNumber
	case episode.AbsoluteEpisodeNumber != nil:
		spec.AbsoluteEpisodeNumber = *episode.AbsoluteEpisodeNumber
	default:
		spec.AbsoluteEpisodeNumber = 0
	}
	return s.dispatchSearch(ctx, spec)
}

// searchSpecial ports SearchSpecial :368-396 — builds the
// "<series> <episode-title>" query list per :375-379, dispatches the
// SpecialEpisodeCriteria, then per-episode falls through to
// searchSingle for season/episode-number coverage.
func (s *Service) searchSpecial(ctx context.Context, ser *series.Series, eps []episodes.Episode, monitoredOnly bool, settings SearchSettings) ([]decisionengine.Decision, error) {
	settings.MonitoredOnly = monitoredOnly
	base, err := s.criteriaForEpisodes(ctx, ser, eps, settings)
	if err != nil {
		return nil, err
	}
	spec := SpecialEpisodeCriteria{CriteriaBase: base}

	// Build episode-title query list per C# :375-379.
	cleaned := base.CleanSceneTitles()
	queries := make([]string, 0, len(eps)*len(cleaned))
	seen := make(map[string]struct{}, len(eps)*len(cleaned))
	for _, e := range eps {
		title := strings.TrimSpace(e.Title)
		if title == "" {
			continue
		}
		if !settings.InteractiveSearch && monitoredOnly && !e.Monitored {
			continue
		}
		for _, sceneTitle := range cleaned {
			q := sceneTitle + " " + GetCleanSceneTitle(e.Title)
			lower := strings.ToLower(q)
			if _, ok := seen[lower]; ok {
				continue
			}
			seen[lower] = struct{}{}
			queries = append(queries, q)
		}
	}
	spec.EpisodeQueryTitles = queries

	aggregated, err := s.dispatchSearch(ctx, spec)
	if err != nil {
		return nil, err
	}

	for i := range eps {
		e := eps[i]
		if !settings.InteractiveSearch && monitoredOnly && !e.Monitored {
			continue
		}
		rs, err := s.searchSingle(ctx, ser, &e, monitoredOnly, settings)
		if err != nil {
			return nil, err
		}
		aggregated = append(aggregated, rs...)
	}
	return aggregated, nil
}

// searchAnimeSeason ports SearchAnimeSeason :398-430.
func (s *Service) searchAnimeSeason(ctx context.Context, ser *series.Series, eps []episodes.Episode, settings SearchSettings) ([]decisionengine.Decision, error) {
	base, err := s.criteriaForEpisodes(ctx, ser, eps, settings)
	if err != nil {
		return nil, err
	}
	spec := AnimeSeasonCriteria{CriteriaBase: base}

	now := time.Now().UTC()
	episodesToSearch := make([]episodes.Episode, 0, len(eps))
	for _, e := range eps {
		if !settings.InteractiveSearch && settings.MonitoredOnly && !e.Monitored {
			continue
		}
		if e.AirDateUtc == nil || !e.AirDateUtc.Before(now) {
			continue
		}
		episodesToSearch = append(episodesToSearch, e)
	}

	seasonsToSearch, err := getSceneSeasonMappings(ctx, s.sceneMapper, ser, episodesToSearch)
	if err != nil {
		return nil, err
	}
	// C# at :413: GroupBy(SeasonNumber).Select(g => g.First()).
	// Walk in order, skip duplicates.
	seenSeason := make(map[int]struct{}, len(seasonsToSearch))
	var aggregated []decisionengine.Decision
	for _, sm := range seasonsToSearch {
		if _, ok := seenSeason[sm.SeasonNumber]; ok {
			continue
		}
		seenSeason[sm.SeasonNumber] = struct{}{}
		seasonSpec := spec
		seasonSpec.SeasonNumber = sm.SeasonNumber
		rs, err := s.dispatchSearch(ctx, seasonSpec)
		if err != nil {
			return nil, err
		}
		aggregated = append(aggregated, rs...)
	}

	for i := range episodesToSearch {
		e := episodesToSearch[i]
		rs, err := s.searchAnime(ctx, ser, &e, settings.MonitoredOnly, settings, true)
		if err != nil {
			return nil, err
		}
		aggregated = append(aggregated, rs...)
	}
	return aggregated, nil
}

// searchDailySeason ports SearchDailySeason :432-461.
func (s *Service) searchDailySeason(ctx context.Context, ser *series.Series, eps []episodes.Episode, settings SearchSettings) ([]decisionengine.Decision, error) {
	episodesToSearch := make([]episodes.Episode, 0, len(eps))
	for _, e := range eps {
		if !settings.InteractiveSearch && settings.MonitoredOnly && !e.Monitored {
			continue
		}
		if strings.TrimSpace(e.AirDate) == "" {
			continue
		}
		episodesToSearch = append(episodesToSearch, e)
	}

	// Group by AirDate-year; preserve first-seen ordering.
	type yearGroup struct {
		year int
		eps  []episodes.Episode
	}
	var groups []yearGroup
	yearIdx := make(map[int]int)
	for _, e := range episodesToSearch {
		t, err := time.Parse("2006-01-02", e.AirDate)
		if err != nil {
			return nil, fmt.Errorf("indexersearch: parse airDate %q: %w", e.AirDate, err)
		}
		y := t.Year()
		idx, ok := yearIdx[y]
		if !ok {
			yearIdx[y] = len(groups)
			groups = append(groups, yearGroup{year: y, eps: []episodes.Episode{e}})
			continue
		}
		groups[idx].eps = append(groups[idx].eps, e)
	}

	var aggregated []decisionengine.Decision
	for _, g := range groups {
		if len(g.eps) > 1 {
			base, err := s.criteriaForEpisodes(ctx, ser, g.eps, settings)
			if err != nil {
				return nil, err
			}
			spec := DailySeasonCriteria{CriteriaBase: base, Year: g.year}
			rs, err := s.dispatchSearch(ctx, spec)
			if err != nil {
				return nil, err
			}
			aggregated = append(aggregated, rs...)
			continue
		}
		// Single-episode-in-year fallback to searchDaily.
		first := g.eps[0]
		rs, err := s.searchDaily(ctx, ser, &first, settings.MonitoredOnly, settings)
		if err != nil {
			return nil, err
		}
		aggregated = append(aggregated, rs...)
	}
	return aggregated, nil
}

// criteriaForEpisodes ports the C# Get<TSpec>(series, List<Episode>,
// monitoredOnly, ...) overload at :463-484 — fetches the deduped scene
// names from the SceneMapper, then unions the series.Title in if it's
// not already in the list (case-insensitive).
func (s *Service) criteriaForEpisodes(ctx context.Context, ser *series.Series, eps []episodes.Episode, settings SearchSettings) (CriteriaBase, error) {
	seasons := distinctInts(func() []int {
		out := make([]int, 0, len(eps))
		for _, e := range eps {
			out = append(out, e.SeasonNumber)
		}
		return out
	}())
	sceneSeasons := distinctInts(func() []int {
		out := make([]int, 0, len(eps))
		for _, e := range eps {
			if e.SceneSeasonNumber != nil {
				out = append(out, *e.SceneSeasonNumber)
				continue
			}
			out = append(out, e.SeasonNumber)
		}
		return out
	}())
	sceneTitles, err := s.sceneMapper.GetSceneNames(ctx, ser.TvdbID, seasons, sceneSeasons)
	if err != nil {
		return CriteriaBase{}, fmt.Errorf("indexersearch: GetSceneNames(%d): %w", ser.TvdbID, err)
	}
	if !containsCaseInsensitive(sceneTitles, ser.Title) {
		sceneTitles = append(sceneTitles, ser.Title)
	}
	return CriteriaBase{
		Series:                ser,
		SceneTitles:           sceneTitles,
		Episodes:              append([]episodes.Episode(nil), eps...),
		MonitoredEpisodesOnly: settings.MonitoredOnly,
		UserInvokedSearch:     settings.UserInvokedSearch,
		InteractiveSearch:     settings.InteractiveSearch,
	}, nil
}

// criteriaForEpisodeMapping ports Get<TSpec>(series, SceneEpisodeMapping,
// monitoredOnly, ...) at :486-501 — uses mapping.SceneTitles +
// mapping.SearchMode, and a singleton episode list.
func (s *Service) criteriaForEpisodeMapping(ser *series.Series, m SceneEpisodeMapping, settings SearchSettings) CriteriaBase {
	var eps []episodes.Episode
	if m.Episode != nil {
		eps = []episodes.Episode{*m.Episode}
	}
	return CriteriaBase{
		Series:                ser,
		SceneTitles:           append([]string(nil), m.SceneTitles...),
		SearchMode:            m.SearchMode,
		Episodes:              eps,
		MonitoredEpisodesOnly: settings.MonitoredOnly,
		UserInvokedSearch:     settings.UserInvokedSearch,
		InteractiveSearch:     settings.InteractiveSearch,
	}
}

// criteriaForSeasonMapping ports Get<TSpec>(series, SceneSeasonMapping,
// monitoredOnly, ...) at :503-518.
func (s *Service) criteriaForSeasonMapping(ser *series.Series, m SceneSeasonMapping, settings SearchSettings) CriteriaBase {
	return CriteriaBase{
		Series:                ser,
		SceneTitles:           append([]string(nil), m.SceneTitles...),
		SearchMode:            m.SearchMode,
		Episodes:              append([]episodes.Episode(nil), m.Episodes...),
		MonitoredEpisodesOnly: settings.MonitoredOnly,
		UserInvokedSearch:     settings.UserInvokedSearch,
		InteractiveSearch:     settings.InteractiveSearch,
	}
}

// dispatchSearch is the per-criteria dispatch helper. Wraps
// Service.Dispatch with a SearchAction that calls FetchSearch on each
// indexer, stamps LastSearchTime on the criteria's episodes after
// dispatch (mirrors C# :540-547), then runs the collected raw reports
// through [DecisionMaker.GetSearchDecision] to produce typed
// [decisionengine.Decision] values (slice 4.BV / C-cleanup [C13]).
// Mirrors C# `ReleaseSearchService.Dispatch` at :520-550 — the C#
// version returns `List<DownloadDecision>` after the same sequence.
func (s *Service) dispatchSearch(ctx context.Context, criteria criteriaWithBase) ([]decisionengine.Decision, error) {
	base := criteria.Base()
	releases, err := s.Dispatch(ctx, func(ctx context.Context, idx Indexer) ([]any, error) {
		return idx.FetchSearch(ctx, criteria)
	}, base)
	if err != nil {
		return nil, err
	}

	if len(base.Episodes) > 0 && s.episodeGet != nil {
		now := time.Now().UTC()
		stamped := make([]episodes.Episode, len(base.Episodes))
		for i := range base.Episodes {
			stamped[i] = base.Episodes[i]
			stamped[i].LastSearchTime = &now
		}
		if err := s.episodeGet.UpdateLastSearchTime(ctx, stamped); err != nil {
			s.log.Warn("indexersearch: UpdateLastSearchTime failed",
				slog.Int("seriesId", seriesID(base)),
				slog.Any("err", err),
			)
		}
	}

	// Slice 4.BV (C-cleanup [C13]): wrap raw reports → Decisions.
	// The C# equivalent at `ReleaseSearchService.cs:549` is
	// `_makeDownloadDecision.GetSearchDecision(reports, criteriaBase).ToList()`.
	// Slice 4.BW (C-cleanup [C14]) merged CriteriaBase into
	// decisionengine.SearchCriteria; pass `&base` directly now that
	// the two are the same underlying type.
	return s.decisionMaker.GetSearchDecision(ctx, releases, &base), nil
}

// criteriaWithBase is the small "I'm a typed criteria value with a
// CriteriaBase + a String()" interface that dispatchSearch consumes.
// Every concrete criteria struct (SingleEpisodeCriteria, etc.) gets a
// .Base() method via embedding-helper below so it can be passed
// through dispatchSearch without an explicit type assertion at the
// call site.
type criteriaWithBase interface {
	Criteria
	Base() CriteriaBase
}

// Base returns the embedded CriteriaBase so dispatchSearch can read
// the typed criteria's shared fields generically. The three search
// shapes the search-match download-decision specs discriminate on
// (single-episode, season, anime-episode) stamp the criteria's Kind +
// season/episode numbers onto the returned base so those specs — which
// only ever see the unified CriteriaBase via the decision engine — can
// recover the C# subtype + numbers (J504). The other shapes leave Kind
// at SearchKindOther (both specs Accept). Pure projection otherwise.
func (c SingleEpisodeCriteria) Base() CriteriaBase {
	b := c.CriteriaBase
	b.Kind = decisionengine.SearchKindSingleEpisode
	b.SeasonNumber = c.SeasonNumber
	b.EpisodeNumber = c.EpisodeNumber
	return b
}

// Base — see SingleEpisodeCriteria.Base. Stamps SearchKindSeason +
// the searched season (C# SeasonSearchCriteria).
func (c SeasonCriteria) Base() CriteriaBase {
	b := c.CriteriaBase
	b.Kind = decisionengine.SearchKindSeason
	b.SeasonNumber = c.SeasonNumber
	return b
}

// Base — see SingleEpisodeCriteria.Base.
func (c DailyEpisodeCriteria) Base() CriteriaBase { return c.CriteriaBase }

// Base — see SingleEpisodeCriteria.Base.
func (c DailySeasonCriteria) Base() CriteriaBase { return c.CriteriaBase }

// Base — see SingleEpisodeCriteria.Base. Stamps SearchKindAnimeEpisode +
// the searched season/episode + IsSeasonSearch (C# AnimeEpisodeSearchCriteria).
func (c AnimeEpisodeCriteria) Base() CriteriaBase {
	b := c.CriteriaBase
	b.Kind = decisionengine.SearchKindAnimeEpisode
	b.SeasonNumber = c.SeasonNumber
	b.EpisodeNumber = c.EpisodeNumber
	b.AnimeIsSeasonSearch = c.IsSeasonSearch
	return b
}

// Base — see SingleEpisodeCriteria.Base. AnimeSeasonCriteria maps to the
// C# AnimeSeasonSearchCriteria, which is NOT a SeasonSearchCriteria — so
// it stays SearchKindOther and SeasonMatch does NOT act on it.
func (c AnimeSeasonCriteria) Base() CriteriaBase { return c.CriteriaBase }

// Base — see SingleEpisodeCriteria.Base.
func (c SpecialEpisodeCriteria) Base() CriteriaBase { return c.CriteriaBase }

func seriesID(c CriteriaBase) int {
	if c.Series == nil {
		return 0
	}
	return c.Series.ID
}

func distinctInts(in []int) []int {
	seen := make(map[int]struct{}, len(in))
	out := make([]int, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func containsCaseInsensitive(haystack []string, needle string) bool {
	lower := strings.ToLower(needle)
	for _, h := range haystack {
		if strings.ToLower(h) == lower {
			return true
		}
	}
	return false
}
