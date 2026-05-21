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
// Read surface (this file):
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
// Write surface (in [mutations.go]):
//
//   - AddSeries / AddMany — publish SeriesAddedEvent +
//     SeriesImportedEvent (Phase 2.49).
//   - UpdateSeries / UpdateMany — publish SeriesEditedEvent +
//     SeriesBulkEditedEvent; uses [SeriesPathBuilder] +
//     [EpisodeMonitorCascade] bridges (Phase 2.50 / 2.52 / 2.54).
//   - DeleteSeries — publish SeriesDeletedEvent (Phase 2.50).
//   - UpdateTags — uses [TagChangeProvider] bridge (Phase 2.52).
//   - RemoveAddOptions — Get + targeted-update approximation of the
//     C# SetFields helper (Phase 2.50).
//
// Closes Phase 2 SeriesService porting modulo the
// IBuildSeriesPaths/autotagging bridges (interfaces here, real
// implementations in their respective Phase 3+ subsystems).
//
// No cache: the C# class doesn't cache anything either. Lookups go
// straight to the repo; All() is uncached.
type Service struct {
	repo        *Repo
	bus         *messaging.Bus
	tagProvider TagChangeProvider
	pathBuilder SeriesPathBuilder
	episodes    EpisodeMonitorCascade
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

// SeriesExists reports whether a Series row with the given id is
// persisted. Returns (false, nil) on a clean miss (no row),
// (true, nil) on a hit, and propagates any other repo error
// untouched. Mirrors the C# `BasicRepository.Find(id) != null`
// idiom callers use when they only care about existence (the
// C# tree itself uses `GetSeries` + try/catch on
// `ModelNotFoundException`; the bool return is the Go-port
// idiomatic translation).
//
// Added at 5.J180 for the [api/v3.EpisodeFileListHandler] 404-on-
// missing-series path that mirrors C# `EpisodeFileController.cs:72`
// (`_seriesService.GetSeries(seriesId.Value)` throws when the row
// is gone, surfacing as a 404). Reusable from any handler that needs
// the same precondition check without pulling the full Series row.
func (s *Service) SeriesExists(ctx context.Context, id int) (bool, error) {
	if _, err := s.repo.Get(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
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

// Insert persists a new Series row through the repo. This is the
// service-layer wrapper added in slice 5.D1a so the v3 API write
// handlers don't have to import series.Repo directly.
//
// IMPORTANT: this method is a thin pass-through to the repo only.
// The richer C# AddSeriesService.AddSeries pipeline (TVDB lookup-
// then-merge, folder creation via DiskProvider, season-pass seeding,
// image download, ImportListExclusion clearing) is NOT replicated.
// Callers that need the full pipeline must wait for the
// AddSeriesService port.
func (s *Service) Insert(ctx context.Context, row *Series) (*Series, error) {
	return s.repo.Insert(ctx, row)
}

// Update persists a Series row through the repo. Service-layer
// wrapper added in slice 5.D1a.
//
// IMPORTANT: the C# `SeriesService.UpdateSeries` performs several
// side-effects this thin wrapper does NOT replicate yet:
//
//   - MoveSeriesCommand dispatch when Path changed (the on-disk
//     move). The v3 `?moveFiles=true` query param is parsed but
//     ignored until the cascade lands.
//   - SeriesEditedEvent + SeriesMovedEvent publication on the bus
//     (these flow to downstream subscribers like media-info
//     refresh).
//
// Callers that depend on the cascade behaviour must wait for the
// follow-up slice. For the v3 PUT happy path (rename Title, change
// monitoring, swap quality profile) without a Path change, the
// thin wrapper is sufficient.
func (s *Service) Update(ctx context.Context, row *Series) error {
	return s.repo.Update(ctx, row)
}

// Delete removes the Series row through the repo. Service-layer
// wrapper added in slice 5.D1a.
//
// IMPORTANT: the C# `SeriesService.DeleteSeries(ids, deleteFiles,
// addImportListExclusion)` performs:
//
//   - On-disk delete of the series folder when deleteFiles=true.
//   - Adds the TVDB id to ImportListExclusion when
//     addImportListExclusion=true.
//   - Publishes SeriesDeletedEvent which downstream subscribers
//     consume to clean up Episode rows + EpisodeFile rows + History
//     entries.
//
// None of those side-effects are replicated here. Callers must wait
// for the cascade port; for now the row vanishes from the Series
// table but child Episode/EpisodeFile rows remain orphaned.
func (s *Service) Delete(ctx context.Context, id int) error {
	return s.repo.Delete(ctx, id)
}
