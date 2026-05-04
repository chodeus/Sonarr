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
