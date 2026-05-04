package series

import (
	"context"
	"errors"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
)

// Service ports `NzbDrone.Core.Tv.SeriesService` at
// `src/NzbDrone.Core/Tv/SeriesService.cs`.
//
// This slice ships the read-only surface — the lookup/search helpers
// and the bulk-list accessors — which is enough to unblock
// RootFolderService's [SeriesPaths] stub (via [Service.AllSeriesPaths])
// and provide the foundation for the mutation-side methods landing in
// follow-up slices.
//
// What ships in 2.48:
//
//   - GetSeries / GetMany / GetAllSeries — id-keyed lookups.
//   - FindByTvdbID / FindByTvRageID / FindByImdbID / FindByPath —
//     unique-ish lookups; surface ErrNotFound on miss.
//   - FindByTitle / FindByTitleAndYear — wrap the parser-cleaned
//     title (matches `SeriesService.cs:106` calling
//     `title.CleanSeriesTitle()` before delegating to repo).
//   - FindByTitleInexact — full port of the longest-leftmost match
//     scoring at `:109-151`.
//   - AllSeriesPaths / AllSeriesTvdbIDs / AllSeriesTags /
//     AllSeriesQualityProfiles / AllForTag — bulk accessors used by
//     downstream services.
//   - SeriesPathExists — the duplicate-add guard.
//
// What's deferred to follow-up slices:
//
//   - AddSeries / AddMany — needs SeriesAddedEvent +
//     SeriesImportedEvent (lifecycle events; the bus is already wired).
//   - UpdateSeries (single + bulk) — needs SeriesEditedEvent +
//     SeriesBulkEditedEvent + IBuildSeriesPaths.
//   - DeleteSeries — needs SeriesDeletedEvent.
//   - UpdateTags — needs the autotagging.Service.GetTagChanges
//     plumbing.
//   - RemoveAddOptions — needs the SetFields helper on the generic
//     Repo.
//
// No cache: the C# class doesn't cache anything either. Lookups go
// straight to the repo; All() is uncached.
type Service struct {
	repo        *Repo
	bus         *messaging.Bus
	tagProvider TagChangeProvider
	pathBuilder SeriesPathBuilder
}

// NewService binds a Repo + Bus into a Series Service. Pass `bus = nil`
// to skip event publishing (useful for migrations and tests that don't
// care about the wiring).
func NewService(r *Repo, bus *messaging.Bus) (*Service, error) {
	if r == nil {
		return nil, errors.New("series: nil repo")
	}
	return &Service{repo: r, bus: bus}, nil
}

// GetSeries ports `SeriesService.GetSeries(int seriesId)` at `:63-66`.
// Returns [ErrNotFound] on miss.
func (s *Service) GetSeries(ctx context.Context, id int) (*Series, error) {
	return s.repo.Get(ctx, id)
}

// GetMany ports `SeriesService.GetSeries(IEnumerable<int>)` at `:68-71`.
// Returns the rows for the given Ids in arbitrary order; missing Ids
// are silently dropped (matches `_seriesRepository.Get(ids)` semantics
// — the C# `BasicRepository.Get(IEnumerable<int>)` returns only the
// matching rows).
//
// Implementation note: the C# version uses a single SQL `IN` clause; the
// Go port issues per-Id Get calls because the generic repo doesn't
// expose a bulk-get yet. For typical batch sizes (tens of series for
// dashboard widgets) the round-trip overhead is negligible.
func (s *Service) GetMany(ctx context.Context, ids []int) ([]Series, error) {
	out := make([]Series, 0, len(ids))
	for _, id := range ids {
		row, err := s.repo.Get(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, *row)
	}
	return out, nil
}

// GetAllSeries ports `SeriesService.GetAllSeries()` at `:170-173`.
func (s *Service) GetAllSeries(ctx context.Context) ([]Series, error) {
	return s.repo.All(ctx)
}

// FindByTvdbID ports `SeriesService.FindByTvdbId(int tvdbId)` at `:89-92`.
// Returns ErrNotFound on miss.
func (s *Service) FindByTvdbID(ctx context.Context, tvdbID int) (*Series, error) {
	return s.repo.GetByTvdbID(ctx, tvdbID)
}

// FindByTvRageID ports `SeriesService.FindByTvRageId(int tvRageId)` at
// `:94-97`. Returns ErrNotFound on miss.
func (s *Service) FindByTvRageID(ctx context.Context, tvRageID int) (*Series, error) {
	return s.repo.FindByTvRageID(ctx, tvRageID)
}

// FindByImdbID ports `SeriesService.FindByImdbId(string imdbId)` at
// `:99-102`. Returns ErrNotFound on miss.
func (s *Service) FindByImdbID(ctx context.Context, imdbID string) (*Series, error) {
	return s.repo.FindByImdbID(ctx, imdbID)
}

// FindByPath ports `SeriesService.FindByPath(string path)` at `:153-156`.
func (s *Service) FindByPath(ctx context.Context, path string) (*Series, error) {
	return s.repo.FindByPath(ctx, path)
}

// FindByTitle ports `SeriesService.FindByTitle(string title)` at
// `:104-107`. Cleans the title via `parser.CleanSeriesTitle` before
// looking it up — matches the C# `title.CleanSeriesTitle()` extension
// call at `:106`.
func (s *Service) FindByTitle(ctx context.Context, title string) (*Series, error) {
	return s.repo.FindByTitle(ctx, parser.CleanSeriesTitle(title))
}

// FindByTitleAndYear ports `SeriesService.FindByTitle(string, int)` at
// `:158-161`.
func (s *Service) FindByTitleAndYear(ctx context.Context, title string, year int) (*Series, error) {
	return s.repo.FindByTitleAndYear(ctx, parser.CleanSeriesTitle(title), year)
}

// FindByTitleInexact ports `SeriesService.FindByTitleInexact(string)` at
// `:109-151`. Tries to match an in-corpus series whose CleanTitle is a
// substring of the cleaned input.
//
// Algorithm (line-aligned with C#):
//
//  1. Clean the input title via `parser.CleanSeriesTitle`.
//  2. Pull every candidate whose CleanTitle is a substring of the
//     cleaned input via [Repo.FindByTitleInexact].
//  3. If 0 candidates → return ErrNotFound (C# returns null at `:117`).
//  4. If 1 candidate → return it (C# at `:122`).
//  5. Otherwise: rank candidates by `(position, -length)` — leftmost
//     position wins; on ties, longest match wins. Return the top.
//     Mirrors `:128-142`.
func (s *Service) FindByTitleInexact(ctx context.Context, title string) (*Series, error) {
	clean := parser.CleanSeriesTitle(title)
	if clean == "" {
		return nil, ErrNotFound
	}
	candidates, err := s.repo.FindByTitleInexact(ctx, clean)
	if err != nil {
		return nil, err
	}
	switch len(candidates) {
	case 0:
		return nil, ErrNotFound
	case 1:
		first := candidates[0]
		return &first, nil
	}
	type ranked struct {
		row      Series
		position int
		length   int
	}
	ranks := make([]ranked, 0, len(candidates))
	for _, c := range candidates {
		pos := strings.Index(clean, c.CleanTitle)
		if pos < 0 {
			continue
		}
		ranks = append(ranks, ranked{row: c, position: pos, length: len(c.CleanTitle)})
	}
	if len(ranks) == 0 {
		return nil, ErrNotFound
	}
	sort.SliceStable(ranks, func(i, j int) bool {
		if ranks[i].position != ranks[j].position {
			return ranks[i].position < ranks[j].position
		}
		return ranks[i].length > ranks[j].length
	})
	winner := ranks[0].row
	return &winner, nil
}

// AllSeriesTvdbIDs ports `SeriesService.AllSeriesTvdbIds()` at
// `:175-178`.
func (s *Service) AllSeriesTvdbIDs(ctx context.Context) ([]int, error) {
	return s.repo.AllSeriesTvdbIDs(ctx)
}

// AllSeriesPaths ports `SeriesService.GetAllSeriesPaths()` at
// `:180-183`. Satisfies the [rootfolders.SeriesPaths] interface
// signature exactly — once SeriesService is wired at the host, the
// RootFolderService stub can be replaced with this method.
func (s *Service) AllSeriesPaths(ctx context.Context) (map[int]string, error) {
	return s.repo.AllSeriesPaths(ctx)
}

// AllSeriesTags ports `SeriesService.GetAllSeriesTags()` at
// `:185-188`.
func (s *Service) AllSeriesTags(ctx context.Context) (map[int][]int, error) {
	return s.repo.AllSeriesTags(ctx)
}

// AllSeriesQualityProfiles ports
// `SeriesService.GetAllSeriesQualityProfiles()` at `:190-193`. Returns
// `Id → QualityProfileId`. Rows whose QualityProfileId is NULL surface
// as 0 in the map (matches C# default-int unmarshalling of a NULL
// column).
//
// QualityProfileService.Delete will use this when the in-use validator
// lands.
func (s *Service) AllSeriesQualityProfiles(ctx context.Context) (map[int]int, error) {
	return s.repo.AllSeriesQualityProfiles(ctx)
}

// AllForTag ports `SeriesService.AllForTag(int tagId)` at `:195-199`.
// Returns every series whose Tags slice contains `tagID`. Filtering
// happens in-memory after fetching `All()` — matches the C# in-memory
// `.Where(s => s.Tags.Contains(tagId))` semantic.
func (s *Service) AllForTag(ctx context.Context, tagID int) ([]Series, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Series, 0)
	for _, row := range all {
		for _, t := range row.Tags {
			if t == tagID {
				out = append(out, row)
				break
			}
		}
	}
	return out, nil
}

// SeriesPathExists ports `SeriesService.SeriesPathExists(string)` at
// `:265-268`.
func (s *Service) SeriesPathExists(ctx context.Context, folder string) (bool, error) {
	return s.repo.SeriesPathExists(ctx, folder)
}
