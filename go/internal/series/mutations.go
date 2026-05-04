package series

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/messaging"
)

// AddSeries ports `SeriesService.AddSeries(Series newSeries)` at
// `SeriesService.cs:73-79`. Inserts the row, re-loads the persisted
// shape (the C# version calls `GetSeries(newSeries.Id)` to surface
// any DB-side defaults) and publishes [SeriesAddedEvent].
//
// On nil bus, the publish is silently skipped (matches the established
// Phase 2 pattern across the other services).
func (s *Service) AddSeries(ctx context.Context, ns *Series) (*Series, error) {
	if ns == nil {
		return nil, errors.New("series: cannot add nil series")
	}
	if _, err := s.repo.Insert(ctx, ns); err != nil {
		return nil, fmt.Errorf("series: AddSeries: insert: %w", err)
	}
	// Re-fetch to pick up any DB-side defaults (e.g. NULL columns
	// coerced through the Scan path). Mirrors C# `GetSeries(newSeries.Id)`.
	stored, err := s.repo.Get(ctx, ns.ID)
	if err != nil {
		return nil, fmt.Errorf("series: AddSeries: refetch: %w", err)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, SeriesAddedEvent{Series: stored})
	}
	return stored, nil
}

// UpdateOptions encapsulates the optional flags `SeriesService.UpdateSeries`
// takes in C# (`updateEpisodesToMatchSeason`, `publishUpdatedEvent`).
// Zero value = the C# defaults: episode-monitored cascade ON,
// publishing ON.
type UpdateOptions struct {
	// SkipEpisodeMonitoredCascade flips the C#
	// `updateEpisodesToMatchSeason` flag (default true): when set,
	// season-monitored toggle propagation to episodes is skipped.
	// Used by EpisodeMonitoredService to avoid cascade loops.
	SkipEpisodeMonitoredCascade bool

	// SkipPublishUpdatedEvent flips the C# `publishUpdatedEvent`
	// flag (default true): when set, no SeriesEditedEvent is fired.
	SkipPublishUpdatedEvent bool
}

// UpdateSeries ports `SeriesService.UpdateSeries(Series, bool, bool)` at
// `SeriesService.cs:203-234` — minus the still-deferred side effects:
//
//   - Episode-monitored cascade (the season loop calling
//     `_episodeService.SetEpisodeMonitoredBySeason`) is NOT applied
//     in this slice; lands when EpisodeService ports. The
//     `EpisodesChanged` flag in the published event is currently
//     always false.
//   - UpdateTags (the autotagging delta-application) is NOT applied
//     either; lands in the next slice that wires the autotagging
//     and rootfolders providers.
//
// What the slice DOES do, faithfully:
//
//  1. Re-fetch the stored row (so the caller can't mutate AddOptions).
//  2. Force-restore `AddOptions` from the stored row (mirrors `:224`).
//  3. Update via the repo.
//  4. If `!SkipPublishUpdatedEvent`, publish [SeriesEditedEvent]
//     with the (new, old, episodesChanged: false) payload.
//
// Returns the updated row (re-loaded from the DB, not the input
// pointer — matches the C# `_seriesRepository.Update(series)` return
// shape).
func (s *Service) UpdateSeries(ctx context.Context, ns *Series, opts UpdateOptions) (*Series, error) {
	if ns == nil {
		return nil, errors.New("series: cannot update nil series")
	}
	stored, err := s.repo.Get(ctx, ns.ID)
	if err != nil {
		return nil, fmt.Errorf("series: UpdateSeries: load stored: %w", err)
	}
	// AddOptions is never updated through this path — preserve the
	// stored value verbatim. Mirrors C# `:224`
	// (`series.AddOptions = storedSeries.AddOptions;`).
	ns.AddOptions = stored.AddOptions

	if err := s.repo.Update(ctx, ns); err != nil {
		return nil, fmt.Errorf("series: UpdateSeries: persist: %w", err)
	}
	updated, err := s.repo.Get(ctx, ns.ID)
	if err != nil {
		return nil, fmt.Errorf("series: UpdateSeries: refetch: %w", err)
	}
	if !opts.SkipPublishUpdatedEvent && s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, SeriesEditedEvent{
			Series:          updated,
			OldSeries:       stored,
			EpisodesChanged: false,
		})
	}
	return updated, nil
}

// RemoveAddOptions ports `SeriesService.RemoveAddOptions(Series)` at
// `:270-273`. The C# version uses
// `_seriesRepository.SetFields(series, s => s.AddOptions)` —
// a generic-repo "update only these columns" helper that the Go
// port doesn't have yet. We approximate with a Get + targeted
// modify + Update; the field-set guarantees only AddOptions is
// touched at the SQL layer.
//
// No event published (the C# version doesn't either).
func (s *Service) RemoveAddOptions(ctx context.Context, seriesID int) error {
	stored, err := s.repo.Get(ctx, seriesID)
	if err != nil {
		return fmt.Errorf("series: RemoveAddOptions: load: %w", err)
	}
	stored.AddOptions = ""
	if err := s.repo.Update(ctx, stored); err != nil {
		return fmt.Errorf("series: RemoveAddOptions: persist: %w", err)
	}
	return nil
}

// DeleteSeries ports `SeriesService.DeleteSeries(List<int>, bool, bool)`
// at `SeriesService.cs:163-168`. Loads each series before delete (so
// the published event carries the deleted rows), then deletes via
// per-id Repo.Delete, then publishes [SeriesDeletedEvent].
//
// Missing IDs are silently dropped from the load step (matches the
// C# `_seriesRepository.Get(seriesIds)` shape, which returns only the
// matching rows). The Delete step is a per-id loop; idempotent
// double-deletes (ErrNotFound) are tolerated.
func (s *Service) DeleteSeries(ctx context.Context, seriesIDs []int, deleteFiles, addImportListExclusion bool) error {
	if len(seriesIDs) == 0 {
		return nil
	}
	rows := make([]Series, 0, len(seriesIDs))
	for _, id := range seriesIDs {
		row, err := s.repo.Get(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return fmt.Errorf("series: DeleteSeries: load %d: %w", id, err)
		}
		rows = append(rows, *row)
	}
	for _, id := range seriesIDs {
		if err := s.repo.Delete(ctx, id); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return fmt.Errorf("series: DeleteSeries: delete %d: %w", id, err)
		}
	}
	if s.bus != nil && len(rows) > 0 {
		_ = messaging.Publish(ctx, s.bus, SeriesDeletedEvent{
			Series:                 rows,
			DeleteFiles:            deleteFiles,
			AddImportListExclusion: addImportListExclusion,
		})
	}
	return nil
}

// AddMany ports `SeriesService.AddSeries(List<Series> newSeries)` at
// `:81-87`. The C# version uses `_seriesRepository.InsertMany(...)`
// (one transaction); the Go port issues per-row Inserts since the
// generic `*repo.Repository` doesn't yet expose a bulk-insert helper.
//
// For typical batch sizes (import-list CSV imports of dozens of
// shows) the round-trip cost is acceptable; the C# transactional
// semantics matter more for tens of thousands. Worth revisiting when
// migrations 1.x land that need bulk inserts.
//
// Publishes a single [SeriesImportedEvent] carrying every assigned ID
// — mirrors C# `:84` exactly.
func (s *Service) AddMany(ctx context.Context, list []*Series) ([]*Series, error) {
	if len(list) == 0 {
		return nil, nil
	}
	ids := make([]int, 0, len(list))
	for _, ns := range list {
		if ns == nil {
			return nil, errors.New("series: AddMany: encountered nil series")
		}
		if _, err := s.repo.Insert(ctx, ns); err != nil {
			return nil, fmt.Errorf("series: AddMany: insert (idx=%d): %w", len(ids), err)
		}
		ids = append(ids, ns.ID)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, SeriesImportedEvent{SeriesIDs: ids})
	}
	return list, nil
}
