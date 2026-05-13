package pendingreleaseshandlers

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/series"
)

// SeriesDeleter narrows [*pendingreleases.Service] to the bulk
// delete-by-series method this handler needs. Pulled out so tests
// can inject a stub without standing up the full Service +
// Repo + DB stack.
type SeriesDeleter interface {
	DeleteBySeriesIDs(ctx context.Context, seriesIDs []int) error
}

// HandleSeriesDeleted ports C# `PendingReleaseService.Handle(SeriesDeletedEvent)`
// at `PendingReleaseService.cs:660-665`. Walks every deleted series
// row in the event payload and dispatches a single bulk
// [SeriesDeleter.DeleteBySeriesIDs] call. The C# tree's
// UpdatePendingReleases cache-refresh tail is unnecessary here — the
// Go port has no in-process cache (slice 4.BQ documented this
// divergence; the DB read-per-call posture trades a small perf hit
// for goroutine-safety + simpler cache invalidation).
//
// Defensive arms: nil deleter → typed error. nil event /
// empty Series → no-op. Deleter error propagates wrapped.
//
// Slice 4.BZ (sub-arc C7b-ii).
func HandleSeriesDeleted(ctx context.Context, d SeriesDeleter, ev *series.SeriesDeletedEvent) error {
	if d == nil {
		return errors.New("pendingreleaseshandlers: HandleSeriesDeleted: nil deleter")
	}
	if ev == nil || len(ev.Series) == 0 {
		return nil
	}
	ids := make([]int, 0, len(ev.Series))
	for _, s := range ev.Series {
		ids = append(ids, s.ID)
	}
	if err := d.DeleteBySeriesIDs(ctx, ids); err != nil {
		return fmt.Errorf("pendingreleaseshandlers: HandleSeriesDeleted: %w", err)
	}
	return nil
}
