// Package downloadhistoryhandlers ports the six IHandle<...>
// subscribers on `DownloadHistoryService` at
// `src/NzbDrone.Core/Download/History/DownloadHistoryService.cs:19-25`.
//
// Slice 5.J135 ships the SeriesDeletedEvent handler — the simplest of
// the six and the only DELETE-shaped one. The other five (Episode
// Grabbed / EpisodeImported / DownloadCompleted / DownloadFailed /
// DownloadIgnored) all INSERT new rows and have more dependencies
// (event payloads with embedded ReleaseInfo / DownloadClientInfo) so
// they port as separate slices once those payloads are needed at the
// audit-log layer.
//
// Mirrors the historyhandlers/ package layout — narrow per-handler
// interface seam, package-level handler function (not a struct
// method), variadic options where new tuning slots land in the
// future.
package downloadhistoryhandlers

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/series"
)

// SeriesHistoryDeleter narrows [*downloadhistory.Service] to the
// bulk-delete method this handler needs (DeleteForSeries). Pulled
// out so tests can inject a stub without standing up the full
// service stack.
type SeriesHistoryDeleter interface {
	DeleteForSeries(ctx context.Context, seriesIDs []int) (int64, error)
}

// HandleSeriesDeleted ports
// `Download/History/DownloadHistoryService.cs:230-233` —
// `Handle(SeriesDeletedEvent)`. Collects every deleted series Id from
// the event payload and dispatches a single bulk
// [SeriesHistoryDeleter.DeleteForSeries] call.
//
// Mirrors [historyhandlers.HandleSeriesDeleted] (slice 3.AY) exactly
// — both packages run cleanup against the SAME event, just against
// different audit-log tables. The two handlers run side-by-side at
// the composition root.
//
// # Defensive arms
//
// nil-deleter → typed error. nil-event / empty-Series → no-op (no
// rows to delete, no error). Deleter error propagates wrapped.
//
// The handler discards the affected-row count from the deleter —
// the C# tree doesn't surface it either, and SeriesDeletedEvent
// publishers don't have a use for it.
func HandleSeriesDeleted(ctx context.Context, d SeriesHistoryDeleter, ev *series.SeriesDeletedEvent) error {
	if d == nil {
		return errors.New("downloadhistoryhandlers: HandleSeriesDeleted: nil deleter")
	}
	if ev == nil || len(ev.Series) == 0 {
		return nil
	}
	ids := make([]int, 0, len(ev.Series))
	for _, s := range ev.Series {
		ids = append(ids, s.ID)
	}
	if _, err := d.DeleteForSeries(ctx, ids); err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleSeriesDeleted: %w", err)
	}
	return nil
}
