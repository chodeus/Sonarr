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
