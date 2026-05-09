package historyhandlers

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/series"
)

// SeriesHistoryDeleter narrows [*history.Service] to the bulk-delete
// method this handler needs (DeleteForSeries). Pulled out so tests
// can inject a stub without standing up the full service stack.
//
// Distinct from [EntryWriter] (which exposes Add) because the
// SeriesDeletedEvent path doesn't write entries — it removes them.
// Future delete-side handlers (none planned in queue [9]) would
// extend this interface rather than EntryWriter.
type SeriesHistoryDeleter interface {
	DeleteForSeries(ctx context.Context, seriesIDs []int) (int64, error)
}

// HandleSeriesDeleted ports
// `src/NzbDrone.Core/History/HistoryService.cs:368-371` —
// `Handle(SeriesDeletedEvent)`. Collects every deleted series Id
// from the event payload and dispatches a single bulk
// [SeriesHistoryDeleter.DeleteForSeries] call. Unlike the seven
// other history subscribers, this one DELETES rows rather than
// inserting them — the audit log loses its history when the parent
// series is removed (the History.SeriesId column is not declared
// as a foreign key in the schema, so the cleanup is application-
// side; the C# tree handles it the same way).
//
// # Defensive arms
//
// nil-deleter → typed error. nil-event / empty-Series → no-op (no
// rows to delete, no error). Deleter error propagates wrapped.
//
// The handler discards the affected-row count from the deleter —
// the C# tree doesn't surface it either, and SeriesDeletedEvent
// publishers don't have a use for it. A future log-line could pull
// the count back out by capturing the return.
func HandleSeriesDeleted(ctx context.Context, d SeriesHistoryDeleter, ev *series.SeriesDeletedEvent) error {
	if d == nil {
		return errors.New("historyhandlers: HandleSeriesDeleted: nil deleter")
	}
	if ev == nil || len(ev.Series) == 0 {
		return nil
	}
	ids := make([]int, 0, len(ev.Series))
	for _, s := range ev.Series {
		ids = append(ids, s.ID)
	}
	if _, err := d.DeleteForSeries(ctx, ids); err != nil {
		return fmt.Errorf("historyhandlers: HandleSeriesDeleted: %w", err)
	}
	return nil
}
