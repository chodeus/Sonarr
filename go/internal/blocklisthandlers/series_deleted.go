package blocklisthandlers

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/series"
)

// HandleSeriesDeleted ports
// `src/NzbDrone.Core/Blocklisting/BlocklistService.cs:160-163` —
// `HandleAsync(SeriesDeletedEvent)`. Collects every deleted series
// Id from the event payload and dispatches a single bulk
// [SeriesDeleter.DeleteForSeries] call. Like its slice-3.AY history
// sibling this handler DELETES rather than inserts — the
// Blocklist.SeriesId column is not declared as a foreign key in the
// schema, so the cascade-on-series-delete is application-side.
//
// # IHandleAsync vs IHandle
//
// The C# class implements IHandleAsync (NOT IHandle) for this event
// — the dispatcher invokes the handler off the publisher's thread.
// The Go-port messaging.Subscribe lambda runs synchronously on the
// caller goroutine in the same way the slice-3.AY history sibling
// does; producers that need true async fire-and-forget should
// migrate to messaging.SubscribeAsync. The audit-log effect is
// identical either way — rows get bulk-deleted; the only divergence
// is back-pressure shape.
//
// # Defensive arms
//
// nil-deleter → typed error. nil-event / empty-Series → no-op.
// Deleter error propagates wrapped. Discards the affected-row count
// (the C# tree doesn't surface it either; SeriesDeletedEvent
// publishers don't have a use for it).
func HandleSeriesDeleted(ctx context.Context, d SeriesDeleter, ev *series.SeriesDeletedEvent) error {
	if d == nil {
		return errors.New("blocklisthandlers: HandleSeriesDeleted: nil deleter")
	}
	if ev == nil || len(ev.Series) == 0 {
		return nil
	}
	ids := make([]int, 0, len(ev.Series))
	for _, s := range ev.Series {
		ids = append(ids, s.ID)
	}
	if _, err := d.DeleteForSeries(ctx, ids); err != nil {
		return fmt.Errorf("blocklisthandlers: HandleSeriesDeleted: %w", err)
	}
	return nil
}
