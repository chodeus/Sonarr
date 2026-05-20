// Package-level Service for the Episodes domain. Mirrors
// `NzbDrone.Core.Tv.EpisodeService` at `src/NzbDrone.Core/Tv/EpisodeService.cs`.
//
// What this slice (Phase 2.63) ships:
//
//   - Service struct + NewService — repo + optional bus + optional
//     monitored-missing-from-disk cache (the C# `_cache` keyed by
//     SeriesId, holding episode IDs to unmonitor on the next
//     SeriesScannedEvent). The cache itself is wired but consumers
//     of EpisodeFileDeletedEvent / SeriesScannedEvent ship in Phase 3
//     mediafiles, so the cache is exercised here only via direct
//     test calls.
//   - Read-through accessors mirroring `IEpisodeService`:
//     GetEpisode/GetEpisodes/All*/Find*/EpisodesWithFiles/etc.
//   - FindEpisodeByTitle (uses parser.NormalizeEpisodeTitle +
//     parser.CleanSeriesTitle).
//   - FindByAirDateOnly (the C# `FindOneByAirDate` disambiguator).
//   - Mutation pass-throughs (SetEpisodeMonitored/SetMonitored/
//     SetEpisodeMonitoredBySeason/UpdateEpisode/UpdateMany/InsertMany/
//     DeleteMany/UpdateLastSearchTime/SetFileID/ClearFileID).
//
// What's deferred to Phase 3 (mediafiles):
//
//   - Handle(EpisodeFileDeletedEvent) / Handle(EpisodeFileAddedEvent) —
//     the events live in the mediafiles package; the cache plumbing is
//     ready but the wiring lands when EpisodeFile + DeleteMediaFileReason
//     port.
//   - HandleAsync(SeriesScannedEvent) — same; SeriesScannedEvent is a
//     mediafiles event, not a series event.
//   - HandleAsync(SeriesDeletedEvent) — SeriesDeletedEvent already
//     exists in the series package; the listener ports here when the
//     event-bus topology is finalised in Phase 3.
//
// No package-level events are published by this Service — the C# class
// doesn't publish any either; downstream consumers react to
// SeriesEditedEvent / EpisodeFileAddedEvent / EpisodeFileDeletedEvent
// from neighbouring packages.

package episodes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
)

// Service wraps the Repo with the higher-level surface the rest of the
// app consumes — the C# `EpisodeService` analog.
type Service struct {
	repo *Repo
	bus  *messaging.Bus

	// missingFromDiskCache mirrors the C# `_cache` field
	// (`ICached<HashSet<int>>` keyed by series-id-as-string). Holds
	// episode IDs whose files are missing from disk so a future
	// SeriesScannedEvent can unmonitor them. Populated by the (Phase 3)
	// EpisodeFileDeletedEvent handler with reason==MissingFromDisk;
	// drained by the (Phase 3) SeriesScannedEvent handler. Direct
	// test access is provided via PendingMissingFromDisk + Forget.
	missingMu            sync.Mutex
	missingFromDiskCache map[int]map[int]struct{} // seriesID → {episodeID}

	// autoUnmonitorMissing returns the user's
	// `AutoUnmonitorPreviouslyDownloadedEpisodes` config flag — read
	// fresh each time (matches C# `_configService.AutoUnmonitor...`).
	// May be nil; treat unset as `false`.
	autoUnmonitorMissing AutoUnmonitorMissingProvider
}

// AutoUnmonitorMissingProvider returns the current value of the
// `AutoUnmonitorPreviouslyDownloadedEpisodes` config flag. Indirected
// through an interface so the episodes package does NOT depend on
// the config package — the host wires a concrete adapter when the
// config service ports.
type AutoUnmonitorMissingProvider interface {
	AutoUnmonitorPreviouslyDownloadedEpisodes() bool
}

// NewService builds a Service. `bus` may be nil to skip publishing
// (matches the established Phase 2 pattern across other services).
func NewService(r *Repo, bus *messaging.Bus) (*Service, error) {
	if r == nil {
		return nil, errors.New("episodes: nil repo")
	}
	return &Service{
		repo:                 r,
		bus:                  bus,
		missingFromDiskCache: make(map[int]map[int]struct{}),
	}, nil
}

// SetAutoUnmonitorMissingProvider attaches the config-flag provider
// used by the (Phase 3) EpisodeFileDeletedEvent handler. When unset,
// the handler treats the flag as false.
func (s *Service) SetAutoUnmonitorMissingProvider(p AutoUnmonitorMissingProvider) {
	s.autoUnmonitorMissing = p
}

// GetEpisode ports `EpisodeService.GetEpisode(int id)` at
// `EpisodeService.cs:63-66`. Returns ErrNotFound on miss.
func (s *Service) GetEpisode(ctx context.Context, id int) (*Episode, error) {
	return s.repo.Get(ctx, id)
}

// GetEpisodes ports `EpisodeService.GetEpisodes(IEnumerable<int>)` at
// `:68-71`. Empty input returns empty slice.
func (s *Service) GetEpisodes(ctx context.Context, ids []int) ([]Episode, error) {
	return s.repo.GetMany(ctx, ids)
}

// FindEpisode ports `FindEpisode(seriesId, season, episodeNumber)` at
// `:73-76`.
func (s *Service) FindEpisode(ctx context.Context, seriesID, seasonNumber, episodeNumber int) (*Episode, error) {
	return s.repo.Find(ctx, seriesID, seasonNumber, episodeNumber)
}

// FindEpisodeByAbsolute ports
// `FindEpisode(seriesId, absoluteEpisodeNumber)` at `:78-81`.
func (s *Service) FindEpisodeByAbsolute(ctx context.Context, seriesID, absoluteEpisodeNumber int) (*Episode, error) {
	return s.repo.FindByAbsolute(ctx, seriesID, absoluteEpisodeNumber)
}

// FindEpisodesBySceneNumbering ports the (s,e) variant at `:83-86`.
func (s *Service) FindEpisodesBySceneNumbering(ctx context.Context, seriesID, sceneSeasonNumber, sceneEpisodeNumber int) ([]Episode, error) {
	return s.repo.FindBySceneNumbering(ctx, seriesID, sceneSeasonNumber, sceneEpisodeNumber)
}

// FindEpisodesBySceneAbsolute ports the absolute variant at `:88-91`.
func (s *Service) FindEpisodesBySceneAbsolute(ctx context.Context, seriesID, sceneAbsoluteEpisodeNumber int) ([]Episode, error) {
	return s.repo.FindBySceneAbsolute(ctx, seriesID, sceneAbsoluteEpisodeNumber)
}

// GetEpisodesBySeries ports `:98-101`. Mirrors C# naming asymmetry
// (the C# method is named `GetEpisodeBySeries` — singular — even
// though it returns a list); the Go port uses the more accurate
// `GetEpisodesBySeries` since the return shape is `[]Episode`.
func (s *Service) GetEpisodesBySeries(ctx context.Context, seriesID int) ([]Episode, error) {
	return s.repo.AllBySeriesID(ctx, seriesID)
}

// GetEpisodesByMultipleSeries ports `:103-106`. Plural-id variant.
func (s *Service) GetEpisodesByMultipleSeries(ctx context.Context, seriesIDs []int) ([]Episode, error) {
	return s.repo.AllBySeriesIDs(ctx, seriesIDs)
}

// GetEpisodesBySeason ports `:108-111`.
func (s *Service) GetEpisodesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]Episode, error) {
	return s.repo.AllBySeriesAndSeason(ctx, seriesID, seasonNumber)
}

// GetEpisodesBySceneSeason ports `:113-116`.
func (s *Service) GetEpisodesBySceneSeason(ctx context.Context, seriesID, sceneSeasonNumber int) ([]Episode, error) {
	return s.repo.AllBySceneSeason(ctx, seriesID, sceneSeasonNumber)
}

// EpisodesWithFiles ports `:156-159`. Returns episodes whose
// EpisodeFileId resolves to a real EpisodeFiles row.
func (s *Service) EpisodesWithFiles(ctx context.Context, seriesID int) ([]Episode, error) {
	return s.repo.EpisodesWithFiles(ctx, seriesID)
}

// GetEpisodesByFileID ports `:166-169`.
func (s *Service) GetEpisodesByFileID(ctx context.Context, fileID int) ([]Episode, error) {
	return s.repo.AllByFileID(ctx, fileID)
}

// EpisodesBetweenDates ports `:204-209`. Both bounds are converted to
// UTC at the call boundary (matches C# `start.ToUniversalTime()`).
func (s *Service) EpisodesBetweenDates(ctx context.Context, start, end time.Time, includeUnmonitored, includeSpecials bool) ([]Episode, error) {
	return s.repo.EpisodesBetweenDates(ctx, start.UTC(), end.UTC(), includeUnmonitored, includeSpecials)
}

// FindEpisodeByTitle ports the inexact title-matching path at
// `EpisodeService.cs:118-154`. Walks every episode in the series's
// season and ranks by where the (normalized + cleaned) candidate
// title appears in the (normalized + cleaned) release title.
//
// Algorithm parity:
//
//  1. NormalizeEpisodeTitle(releaseTitle) → A.
//  2. CleanSeriesTitle(A)               → B.
//  3. For each episode in (seriesID, seasonNumber):
//     - normEp = NormalizeEpisodeTitle(ep.Title); cleanEp = CleanSeriesTitle(normEp).
//     - Two candidate offsets: pos_norm = strings.Index(A, normEp);
//     pos_clean = strings.Index(B, cleanEp).
//     - Both contribute ranking entries with length = len(normEp)
//     (same value used in C# for both arms).
//  4. Filter out empty titles and pos < 0.
//  5. Sort ascending by (position), descending by (length).
//  6. Return the top match, or ErrNotFound if no entries survived.
//
// One tricky parity bit: the C# version's `IndexOf(...,
// CurrentCultureIgnoreCase)` is locale-aware. The Go port uses
// `strings.Index` after both inputs already pass through
// CleanSeriesTitle / NormalizeEpisodeTitle which lowercase + strip
// diacritics, so case-folding is already handled — the locale
// component is moot for the post-cleaned strings.
func (s *Service) FindEpisodeByTitle(ctx context.Context, seriesID, seasonNumber int, releaseTitle string) (*Episode, error) {
	episodesInSeason, err := s.repo.AllBySeriesAndSeason(ctx, seriesID, seasonNumber)
	if err != nil {
		return nil, err
	}
	if len(episodesInSeason) == 0 {
		return nil, ErrNotFound
	}
	a := parser.NormalizeEpisodeTitle(releaseTitle)
	b := parser.CleanSeriesTitle(a)

	type candidate struct {
		ep       Episode
		position int
		length   int
	}
	var ranks []candidate
	for _, ep := range episodesInSeason {
		if ep.Title == "" {
			continue
		}
		normEp := parser.NormalizeEpisodeTitle(ep.Title)
		cleanEp := parser.CleanSeriesTitle(normEp)
		length := len(normEp)
		if normEp != "" {
			if pos := strings.Index(a, normEp); pos >= 0 {
				ranks = append(ranks, candidate{ep: ep, position: pos, length: length})
			}
		}
		if cleanEp != "" {
			if pos := strings.Index(b, cleanEp); pos >= 0 {
				ranks = append(ranks, candidate{ep: ep, position: pos, length: length})
			}
		}
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
	winner := ranks[0].ep
	return &winner, nil
}

// ErrMultipleEpisodesSameAirDate mirrors the C# `InvalidOperationException`
// thrown by `FindOneByAirDate` at `EpisodeService.cs:258`. Surfaces when
// multiple non-special episodes share the same air date and the
// `part` disambiguator can't single one out.
var ErrMultipleEpisodesSameAirDate = errors.New("episodes: multiple non-special episodes share the air date")

// FindEpisodeByAirDate ports `EpisodeService.FindEpisode(int seriesId, string date, int? part)`
// at `:93-96` + `:226-259` (`FindOneByAirDate`).
//
// Disambiguation rules:
//
//   - 0 hits → ErrNotFound.
//   - 1 hit → return it.
//   - >1 hits, dropping specials yields 1 row AND part is unset → return that row.
//   - >1 hits, part is set AND part ≤ regularEpisodes count → return
//     regularEpisodes[part-1] (1-based, ordered by season+episode).
//   - Otherwise: ErrMultipleEpisodesSameAirDate.
//
// Pass `part = nil` for the no-part variant (the C# nullable int).
func (s *Service) FindEpisodeByAirDate(ctx context.Context, seriesID int, date string, part *int) (*Episode, error) {
	rows, err := s.repo.FindByAirDate(ctx, seriesID, date)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) == 1 {
		row := rows[0]
		return &row, nil
	}
	regular := make([]Episode, 0, len(rows))
	for _, r := range rows {
		if r.SeasonNumber > 0 {
			regular = append(regular, r)
		}
	}
	if len(regular) == 1 && part == nil {
		row := regular[0]
		return &row, nil
	}
	if part != nil && *part >= 1 && *part <= len(regular) {
		sort.SliceStable(regular, func(i, j int) bool {
			if regular[i].SeasonNumber != regular[j].SeasonNumber {
				return regular[i].SeasonNumber < regular[j].SeasonNumber
			}
			return regular[i].EpisodeNumber < regular[j].EpisodeNumber
		})
		row := regular[*part-1]
		return &row, nil
	}
	return nil, fmt.Errorf("%w: date=%s", ErrMultipleEpisodesSameAirDate, date)
}

// UpdateEpisode ports `:171-174`. Per-row update. Returns ErrNotFound
// when the row is missing.
func (s *Service) UpdateEpisode(ctx context.Context, e *Episode) error {
	if e == nil {
		return errors.New("episodes: cannot update nil")
	}
	return s.repo.Update(ctx, e)
}

// SetEpisodeMonitored ports `:176-182`. Loads the row first (so the
// in-place mutation has somewhere to land), flips the flag, and
// persists. Publishes [EpisodeMonitoredChangedEvent] on success.
func (s *Service) SetEpisodeMonitored(ctx context.Context, episodeID int, monitored bool) error {
	row, err := s.repo.Get(ctx, episodeID)
	if err != nil {
		return err
	}
	if err := s.repo.SetMonitoredFlat(ctx, row, monitored); err != nil {
		return err
	}
	s.publishMonitoredChanged(ctx, []int{episodeID}, monitored)
	return nil
}

// SetMonitored ports `:184-187`. Bulk variant; missing IDs are
// silently skipped (the WHERE Id IN (...) covers only existing rows).
// Publishes [EpisodeMonitoredChangedEvent] carrying the input IDs
// when the bulk update succeeds (even if some IDs miss — matches the
// C# ModelEvent fan-out which fires per affected row regardless of
// whether the caller's ID list was a subset).
func (s *Service) SetMonitored(ctx context.Context, ids []int, monitored bool) error {
	if _, err := s.repo.SetMonitoredByIDs(ctx, ids, monitored); err != nil {
		return err
	}
	if len(ids) > 0 {
		s.publishMonitoredChanged(ctx, ids, monitored)
	}
	return nil
}

// SetEpisodeMonitoredBySeason ports `:189-192`. Returns the number of
// rows actually flipped — surfaces in the SeriesService cascade as
// the EpisodesChanged signal in 2.64. Publishes
// [EpisodeMonitoredChangedEvent] when the underlying repo update
// affects at least one row; the carried IDs slice is nil because the
// repo's bulk WHERE-by-season path doesn't return affected IDs.
func (s *Service) SetEpisodeMonitoredBySeason(ctx context.Context, seriesID, seasonNumber int, monitored bool) (int64, error) {
	n, err := s.repo.SetMonitoredBySeason(ctx, seriesID, seasonNumber, monitored)
	if err != nil {
		return n, err
	}
	if n > 0 {
		s.publishMonitoredChanged(ctx, nil, monitored)
	}
	return n, nil
}

// publishMonitoredChanged emits [EpisodeMonitoredChangedEvent]
// best-effort. No-op when bus is nil (matches the established
// skip-publish pattern across the package).
func (s *Service) publishMonitoredChanged(ctx context.Context, ids []int, monitored bool) {
	if s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, EpisodeMonitoredChangedEvent{
		IDs:       ids,
		Monitored: monitored,
	})
}

// UpdateEpisodes ports `:194-197`. Bulk update (per-row).
func (s *Service) UpdateEpisodes(ctx context.Context, list []*Episode) error {
	return s.repo.UpdateMany(ctx, list)
}

// UpdateMany is the alias `IEpisodeService.UpdateMany` used at
// `:216-219` — same impl as UpdateEpisodes; both names exist on the
// C# interface.
func (s *Service) UpdateMany(ctx context.Context, list []*Episode) error {
	return s.repo.UpdateMany(ctx, list)
}

// UpdateLastSearchTime ports `:199-202`. Single-column bulk persist.
func (s *Service) UpdateLastSearchTime(ctx context.Context, list []Episode) error {
	_, err := s.repo.UpdateLastSearchTime(ctx, list)
	return err
}

// InsertMany ports `:211-214`.
func (s *Service) InsertMany(ctx context.Context, list []*Episode) error {
	return s.repo.InsertMany(ctx, list)
}

// DeleteMany ports `:221-224`.
func (s *Service) DeleteMany(ctx context.Context, list []Episode) error {
	return s.repo.DeleteMany(ctx, list)
}

// SetFileID ports the `_episodeRepository.SetFileId(episode, fileId)`
// path used by `Handle(EpisodeFileAddedEvent)` at
// `EpisodeService.cs:288-306`. Exposed as a service method so the
// (Phase 3) mediafiles package can call it directly without
// reaching into the repo. Also drains the missing-from-disk cache
// for that episode (mirrors the cache-bookkeeping at `:294-302`).
func (s *Service) SetFileID(ctx context.Context, e *Episode, fileID int) error {
	if e == nil {
		return errors.New("episodes: SetFileID: nil episode")
	}
	if err := s.repo.SetFileID(ctx, e, fileID); err != nil {
		return err
	}
	s.forgetMissingFromDisk(e.SeriesID, e.ID)
	return nil
}

// ClearFileID ports the `_episodeRepository.ClearFileId(episode, ...)`
// path used by `Handle(EpisodeFileDeletedEvent)` at `:282-285`.
func (s *Service) ClearFileID(ctx context.Context, e *Episode, unmonitor bool) error {
	return s.repo.ClearFileID(ctx, e, unmonitor)
}

// RememberMissingFromDisk records that `episodeID` (in series
// `seriesID`) had its file go missing from disk. Mirrors the C#
// cache write at `:274-281` — a future SeriesScannedEvent handler
// will drain the per-series set via DrainMissingFromDisk.
//
// Exposed publicly so the (Phase 3) mediafiles handler can plumb
// the bookkeeping without taking a private dependency on the
// service struct.
func (s *Service) RememberMissingFromDisk(seriesID, episodeID int) {
	s.missingMu.Lock()
	defer s.missingMu.Unlock()
	set, ok := s.missingFromDiskCache[seriesID]
	if !ok {
		set = make(map[int]struct{})
		s.missingFromDiskCache[seriesID] = set
	}
	set[episodeID] = struct{}{}
}

// DrainMissingFromDisk returns the set of episode IDs cached for the
// given series and clears the entry. Called by the (Phase 3)
// SeriesScannedEvent handler to unmonitor episodes whose files
// haven't reappeared.
func (s *Service) DrainMissingFromDisk(seriesID int) []int {
	s.missingMu.Lock()
	defer s.missingMu.Unlock()
	set, ok := s.missingFromDiskCache[seriesID]
	if !ok || len(set) == 0 {
		delete(s.missingFromDiskCache, seriesID)
		return nil
	}
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	delete(s.missingFromDiskCache, seriesID)
	sort.Ints(out)
	return out
}

// forgetMissingFromDisk removes one specific episode from the
// per-series cache. Used by SetFileID — when an episode picks up a
// new file, it shouldn't be auto-unmonitored on the next scan.
func (s *Service) forgetMissingFromDisk(seriesID, episodeID int) {
	s.missingMu.Lock()
	defer s.missingMu.Unlock()
	set, ok := s.missingFromDiskCache[seriesID]
	if !ok {
		return
	}
	delete(set, episodeID)
	if len(set) == 0 {
		delete(s.missingFromDiskCache, seriesID)
	}
}

// DeleteBySeriesID cascades episode deletion when a series is
// removed. Mirrors C# `EpisodeService.HandleAsync(SeriesDeletedEvent)`
// at `EpisodeService.cs` — which loads episodes by the deleted-
// series ids and calls `DeleteMany`.
//
// The Go port collapses the load + delete into a single bulk SQL
// `DELETE FROM "Episodes" WHERE "SeriesId" = ?` round-trip. The C#
// IHandle<EpisodeDeletedEvent> fan-out has no consumer in the Go
// port today, so per-row entity surfacing isn't needed.
//
// Also evicts the in-memory missingFromDiskCache for the series so
// the next scan starts fresh (the cache keys on seriesID; orphan
// entries would never be touched again).
//
// Returns the number of rows deleted (0 when the series had no
// episodes, e.g. a placeholder row created before the first scan).
// Slice 5.D3 port pre-work — the API-layer DELETE /api/v3/series/{id}?deleteFiles=true
// cascade lands in 5.D5.
func (s *Service) DeleteBySeriesID(ctx context.Context, seriesID int) (int64, error) {
	n, err := s.repo.DeleteBySeriesID(ctx, seriesID)
	if err != nil {
		return 0, err
	}
	s.missingMu.Lock()
	delete(s.missingFromDiskCache, seriesID)
	s.missingMu.Unlock()
	return n, nil
}
