package series

// SeriesAddedEvent ports `Tv/Events/SeriesAddedEvent.cs`. Carries the
// inserted series (with its assigned ID) so listeners (refresh-info
// scheduler, downloads, etc.) can react.
type SeriesAddedEvent struct {
	Series *Series
}

// SeriesImportedEvent ports `Tv/Events/SeriesImportedEvent.cs`.
// Carries the IDs of the bulk-imported series — listeners typically
// schedule a downstream task per ID rather than dragging in the full
// Series objects.
type SeriesImportedEvent struct {
	SeriesIDs []int
}

// SeriesEditedEvent ports `Tv/Events/SeriesEditedEvent.cs`. Carries
// the new + old series rows so listeners can diff (e.g. detect path
// or quality-profile changes) and the EpisodesChanged flag indicating
// a season-level monitored toggle that fanned out to episodes.
type SeriesEditedEvent struct {
	Series          *Series
	OldSeries       *Series
	EpisodesChanged bool
}

// SeriesBulkEditedEvent ports `Tv/Events/SeriesBulkEditedEvent.cs`.
// Single-event-per-bulk-update analogue of SeriesEditedEvent — the
// bulk path doesn't carry diff data per row (consumers of bulk
// edits are coarser-grained).
type SeriesBulkEditedEvent struct {
	Series []Series
}

// SeriesDeletedEvent ports `Tv/Events/SeriesDeletedEvent.cs`. Carries
// the deleted series rows + the user-driven flags
// (deleteFiles + addImportListExclusion) that downstream cleanup
// tasks need.
type SeriesDeletedEvent struct {
	Series                 []Series
	DeleteFiles            bool
	AddImportListExclusion bool
}

// SeriesRefreshStartingEvent ports `Tv/Events/SeriesRefreshStartingEvent.cs`.
// Published by the RefreshSeries command handler at the start of a
// refresh batch (whether per-id or all-series sweep). ManualTrigger is
// true when the command came from a user-initiated action; false for
// scheduled tasks. Subscribers (XemService, SceneMappingService in C#)
// use this to drop caches before the refresh repopulates them.
type SeriesRefreshStartingEvent struct {
	ManualTrigger bool
}

// SeriesRefreshCompleteEvent ports `Tv/Events/SeriesRefreshCompleteEvent.cs`.
// Published by the RefreshSeries command handler when the batch ends
// (after all per-id or sweep iterations complete). C# carries no
// payload; the Go-port mirrors. Useful for orchestration that needs to
// run after refresh activity quiesces.
type SeriesRefreshCompleteEvent struct{}

// SeriesAddCompletedEvent ports `Tv/Events/SeriesAddCompletedEvent.cs`.
// Published by the SeriesScannedHandler post-add-actions arm (C#
// `Tv/SeriesScannedHandler.cs:49`) after a freshly-added series
// finishes its first disk scan AND the per-episode monitor cascade +
// search-command pushes complete. Distinct from [SeriesAddedEvent]
// (which fires immediately on insert from `series.Service.Add`):
// SeriesAddCompletedEvent fires only after the post-add lifecycle
// fully settles — the right moment for notification providers to
// announce "series added" to users.
//
// C# subscriber: NotificationService at
// `src/NzbDrone.Core/Notifications/NotificationService.cs:25 +
// :420`. The Go-port adds the type for slice 5.J216 (post-add-actions
// arm port) — the notification subscriber lands when the notification
// service ports its IHandle<SeriesAddCompletedEvent> arm.
type SeriesAddCompletedEvent struct {
	Series *Series
}

// SeriesUpdatedEvent ports `Tv/Events/SeriesUpdatedEvent.cs`. Distinct
// from [SeriesEditedEvent]: SeriesUpdatedEvent fires after a successful
// RefreshSeries metadata sync (C# `RefreshSeriesService.cs:132` +
// `:77` on the SeriesNotFound soft-delete arm), while SeriesEditedEvent
// fires after `SeriesService.UpdateSeries(publishUpdatedEvent:true)`.
// The two events have non-overlapping subscriber sets in C#
// (MediaCoverService, PendingReleaseService, XemService, V3 SeriesController
// listen on SeriesUpdated; the cascade + diff-aware consumers listen on
// SeriesEdited). Slice 5.J192 adds the type so the refreshseries package
// can publish a faithful equivalent.
type SeriesUpdatedEvent struct {
	Series *Series
}
