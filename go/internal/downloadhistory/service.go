package downloadhistory

import (
	"context"
	"errors"
	"fmt"
)

// EventType is the wire-stable enum for DownloadHistory.EventType.
// Mirrors `src/NzbDrone.Core/Download/History/DownloadHistory.cs:27-34`.
//
// Numeric order is frozen — rows persist the int and the v3 frontend's
// per-int filter UI reads it. Adding new entries APPENDS at the end;
// reordering / removing entries breaks existing user data.
type EventType int

// EventType values. Names + ordinals match the C# enum at
// `DownloadHistory.cs:29-33` exactly.
const (
	// EventTypeGrabbed — release was grabbed from an indexer and
	// handed to the download client. Persisted by the EpisodeGrabbedEvent
	// handler (deferred to a later slice when the bus wiring ports).
	EventTypeGrabbed EventType = 1

	// EventTypeImported — every file from the download was moved into
	// the library and the download is considered finished. Set by the
	// DownloadCompletedEvent handler.
	EventTypeImported EventType = 2

	// EventTypeFailed — the download client reported a terminal
	// failure (corrupt, unavailable, retry-exhausted). Set by the
	// DownloadFailedEvent handler.
	EventTypeFailed EventType = 3

	// EventTypeIgnored — the user explicitly told Sonarr to forget
	// this download. Set by the DownloadIgnoredEvent handler.
	EventTypeIgnored EventType = 4

	// EventTypeFileImported — one file from a multi-file download
	// was moved into the library. Multiple FileImported events can
	// fire before the eventual DownloadImported summary event.
	EventTypeFileImported EventType = 5
)

// String returns the C# enum-identifier form for log lines. Matches
// the C# `Enum.ToString()` shape at `DownloadHistory.cs:29-33`.
//
// Out-of-range values fall through to `EventType(N)` so log lines
// never silently swap a fresh-but-not-yet-named entry for one of the
// known names.
func (t EventType) String() string {
	switch t {
	case EventTypeGrabbed:
		return "DownloadGrabbed"
	case EventTypeImported:
		return "DownloadImported"
	case EventTypeFailed:
		return "DownloadFailed"
	case EventTypeIgnored:
		return "DownloadIgnored"
	case EventTypeFileImported:
		return "FileImported"
	default:
		return fmt.Sprintf("EventType(%d)", int(t))
	}
}

// EntryReader is the slice of [Repo] the [Service] reaches for. Pulled
// out so callers can inject a stub in tests without dragging the full
// CRUD surface; the real repo satisfies the interface.
//
// AllByDownloadID feeds the readonly three (DownloadAlreadyImported /
// GetLatestDownloadHistoryItem / GetLatestGrab). DeleteBySeriesIDs
// feeds the SeriesDeletedEvent IHandle subscriber (slice 5.J135).
// Insert feeds the slice 5.J136 [Service.Add] wrapper that the
// downloadhistoryhandlers IHandle subscribers (EpisodeGrabbedEvent +
// the four still-deferred event-bus handlers) route through.
type EntryReader interface {
	AllByDownloadID(ctx context.Context, downloadID string) ([]Entry, error)
	DeleteBySeriesIDs(ctx context.Context, seriesIDs []int) (int64, error)
	Insert(ctx context.Context, e *Entry) (*Entry, error)
}

// Service ports the readonly half of `IDownloadHistoryService` at
// `Download/History/DownloadHistoryService.cs:12-93`. Three methods —
// `DownloadAlreadyImported`, `GetLatestDownloadHistoryItem`,
// `GetLatestGrab` — each walking the repo's date-DESC event stream
// and short-circuiting on the first event that matches.
//
// The C# class also implements seven `IHandle<...>` event-bus
// handlers that INSERT new rows on grab/import/fail/ignore. Those
// ride on a different subsystem (the message bus + EpisodeGrabbedEvent
// shape), so they're deferred to a later slice — production wiring of
// `LatestGrabFinder` (the [episodeimport.LatestGrabFinder] consumer)
// only needs the readonly half.
//
// Goroutine-safe: stateless after construction.
type Service struct {
	repo EntryReader
}

// NewService binds an EntryReader. nil reader returns nil so a
// caller wiring a service stack at startup can skip optional
// subscribers without an explicit guard. Mirrors the nil-tolerance
// pattern in [episodeimport.NewAggregationService].
func NewService(repo EntryReader) *Service {
	if repo == nil {
		return nil
	}
	return &Service{repo: repo}
}

// DownloadAlreadyImported ports `DownloadHistoryService.cs:36-56` —
// returns true iff the most recent event in the lifecycle is
// `DownloadImported` (i.e. there's no later `DownloadGrabbed` that
// would mean "grabbed again, needs reprocessing").
//
// Walks the date-DESC event list, returns the verdict from the FIRST
// Grabbed-or-Imported event encountered. Other event types are
// skipped — a Failed event between two grab/imports doesn't change
// the answer.
//
// Repo errors propagate verbatim. Empty result list returns
// (false, nil) — matches C# `:55` `return false`.
func (s *Service) DownloadAlreadyImported(ctx context.Context, downloadID string) (bool, error) {
	if s == nil || s.repo == nil {
		return false, errors.New("downloadhistory: DownloadAlreadyImported on nil receiver")
	}
	events, err := s.repo.AllByDownloadID(ctx, downloadID)
	if err != nil {
		return false, fmt.Errorf("downloadhistory: DownloadAlreadyImported: %w", err)
	}
	for _, e := range events {
		switch EventType(e.EventType) {
		case EventTypeGrabbed:
			// A grab newer than any import means re-download in
			// progress — not yet imported.
			return false, nil
		case EventTypeImported:
			// An import with no later grab means stable-imported.
			return true, nil
		}
	}
	return false, nil
}

// GetLatestDownloadHistoryItem ports `DownloadHistoryService.cs:58-86`.
// Returns the most recent event of any of the four "interesting"
// types (Ignored, Grabbed, Imported, Failed). FileImported events
// are skipped (they're per-file noise; the summary is what callers
// want).
//
// Returns (nil, nil) for downloads with no matching events. Repo
// errors propagate.
//
// The C# arm-order at `:65-83` is preserved literally — Ignored,
// Grabbed, Imported, Failed. Order doesn't change semantics (the
// loop returns the FIRST-encountered event regardless of which arm
// fires) but matching the C# arm-order keeps the audit-trail clean.
func (s *Service) GetLatestDownloadHistoryItem(ctx context.Context, downloadID string) (*Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("downloadhistory: GetLatestDownloadHistoryItem on nil receiver")
	}
	events, err := s.repo.AllByDownloadID(ctx, downloadID)
	if err != nil {
		return nil, fmt.Errorf("downloadhistory: GetLatestDownloadHistoryItem: %w", err)
	}
	for i := range events {
		switch EventType(events[i].EventType) {
		case EventTypeIgnored, EventTypeGrabbed, EventTypeImported, EventTypeFailed:
			// Index-into-slice + take address: caller may want to
			// mutate the returned Entry without affecting future
			// reads; copying is the simpler choice for now.
			out := events[i]
			return &out, nil
		}
	}
	return nil, nil
}

// GetLatestGrab ports `DownloadHistoryService.cs:88-93`. Returns the
// most recent `DownloadGrabbed` event for the given download, or
// (nil, nil) when no grab event exists.
//
// Used in production by [episodeimport.LatestGrabFinder] (slice 3.61's
// AugmentQualityFromReleaseName aggregator). The aggregator filters
// for Grabbed events the same way C# does at `:92` — `FirstOrDefault`
// over the date-DESC list.
//
// Repo errors propagate. The aggregator's own slice already treats a
// nil/error return as "no data, leave LocalEpisode unchanged".
func (s *Service) GetLatestGrab(ctx context.Context, downloadID string) (*Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("downloadhistory: GetLatestGrab on nil receiver")
	}
	events, err := s.repo.AllByDownloadID(ctx, downloadID)
	if err != nil {
		return nil, fmt.Errorf("downloadhistory: GetLatestGrab: %w", err)
	}
	for i := range events {
		if EventType(events[i].EventType) == EventTypeGrabbed {
			out := events[i]
			return &out, nil
		}
	}
	return nil, nil
}

// Add inserts a new DownloadHistory row. Mirrors the C# pattern at
// `Download/History/DownloadHistoryService.cs:121, 162, 183, 208, 227`
// — every IHandle subscriber builds a DownloadHistory locally and
// calls `_repository.Insert(history)` directly. The Go port surfaces
// the write as a public Service.Add so the slice 5.J136+ subscribers
// can route through one wrapper rather than each importing the Repo.
//
// Validation expectations match the C# Insert callsites:
//   - DownloadID required (the column is NOT NULL; the C# tree gates
//     on `IsNullOrWhiteSpace` at the subscriber level — surface a
//     typed error here as a defence in depth).
//   - SeriesID > 0 required (NOT NULL column; orphan-row UI filter
//     would drop zero-SeriesID rows anyway).
//   - SourceTitle / Date required (both NOT NULL).
//
// Release / Data are JSON-shape strings the per-event handler builds
// before calling Add — opaque text columns on the C# side too, so
// Add does not validate them.
//
// Returns the inserted Entry (with ID populated by the underlying
// Repo.Insert auto-increment).
func (s *Service) Add(ctx context.Context, e *Entry) (*Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("downloadhistory: Add on nil receiver")
	}
	if e == nil {
		return nil, errors.New("downloadhistory: Add: nil entry")
	}
	if e.DownloadID == "" {
		return nil, errors.New("downloadhistory: Add: DownloadID required")
	}
	if e.SeriesID <= 0 {
		return nil, fmt.Errorf("downloadhistory: Add: SeriesID required, got %d", e.SeriesID)
	}
	if e.SourceTitle == "" {
		return nil, errors.New("downloadhistory: Add: SourceTitle required")
	}
	if e.Date.IsZero() {
		return nil, errors.New("downloadhistory: Add: Date required")
	}
	out, err := s.repo.Insert(ctx, e)
	if err != nil {
		return nil, fmt.Errorf("downloadhistory: Add: %w", err)
	}
	return out, nil
}

// DeleteForSeries ports the SeriesDeletedEvent path at
// `DownloadHistoryService.cs:230-233` —
//
//	_repository.DeleteBySeriesIds(message.Series.Select(m => m.Id).ToList())
//
// Bulk-removes every DownloadHistory row whose SeriesId is in the
// supplied list. Returns rows affected; 0 with nil error when
// seriesIDs is empty (no-op short-circuit). Repo errors propagate
// wrapped.
//
// Mirrors the C# behaviour where a SeriesDeletedEvent fan-out cleans
// up DownloadHistory rows alongside other per-series children — the
// DownloadHistory.SeriesId column is NOT a foreign key in the schema,
// so the cleanup is application-side rather than DB-cascade (same
// posture as [history.Service.DeleteForSeries]).
func (s *Service) DeleteForSeries(ctx context.Context, seriesIDs []int) (int64, error) {
	if s == nil || s.repo == nil {
		return 0, errors.New("downloadhistory: DeleteForSeries on nil receiver")
	}
	if len(seriesIDs) == 0 {
		return 0, nil
	}
	n, err := s.repo.DeleteBySeriesIDs(ctx, seriesIDs)
	if err != nil {
		return 0, fmt.Errorf("downloadhistory: DeleteForSeries: %w", err)
	}
	return n, nil
}

// Compile-time interface-satisfaction guard. A refactor that breaks
// the contract (e.g. Repo loses AllByDownloadID, or the signature
// changes) fails at build time rather than at the wiring callsite.
var _ EntryReader = (*Repo)(nil)
