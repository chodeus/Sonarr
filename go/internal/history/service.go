package history

import (
	"context"
	"errors"
	"fmt"
)

// EventType is the wire-stable enum for `History.EventType`. Mirrors
// `src/NzbDrone.Core/History/EpisodeHistory.cs:37-47` exactly.
//
// Numeric order is frozen — old rows that predate the EventType column
// in the schema persist as NULL (modelled as nil *int on [Entry]); rows
// with a value persist the int and the v3 frontend filters per-int.
// Adding new entries APPENDS at the end; reordering / removing entries
// breaks existing user data.
type EventType int

// EventType values. Names + ordinals match the C# enum at
// `EpisodeHistory.cs:39-46` exactly.
const (
	// EventTypeUnknown — explicit Unknown=0. Not the same as a NULL
	// EventType column (NULL maps to a nil [Entry.EventType]); the
	// 0 ordinal is for rows with EventType set explicitly to Unknown.
	EventTypeUnknown EventType = 0

	// EventTypeGrabbed — release was grabbed from an indexer. Set by
	// the EpisodeGrabbedEvent handler (deferred along with the bus
	// surface).
	EventTypeGrabbed EventType = 1

	// EventTypeSeriesFolderImported — file was imported from inside
	// the series folder (manual import / library rescan flow).
	EventTypeSeriesFolderImported EventType = 2

	// EventTypeDownloadFolderImported — file was imported from a
	// download client's output folder. The most common import path.
	EventTypeDownloadFolderImported EventType = 3

	// EventTypeDownloadFailed — download client reported a terminal
	// failure (corrupt, retry-exhausted).
	EventTypeDownloadFailed EventType = 4

	// EventTypeEpisodeFileDeleted — an EpisodeFile was removed (manual
	// or housekeeping). Recorded so the user can audit deletions.
	EventTypeEpisodeFileDeleted EventType = 5

	// EventTypeEpisodeFileRenamed — an EpisodeFile was renamed in
	// place (post-rename naming-config change, etc.). Recorded so
	// the rename audit log is complete.
	EventTypeEpisodeFileRenamed EventType = 6

	// EventTypeDownloadIgnored — user explicitly told Sonarr to forget
	// the download. Recorded so the ignore action shows up in history.
	EventTypeDownloadIgnored EventType = 7
)

// String returns the C# enum-identifier form for log lines. Matches
// the C# `Enum.ToString()` shape at `EpisodeHistory.cs:39-46`.
//
// Out-of-range values fall through to `EventType(N)` so log lines
// never silently swap a fresh-but-not-yet-named entry for "Unknown".
func (t EventType) String() string {
	switch t {
	case EventTypeUnknown:
		return "Unknown"
	case EventTypeGrabbed:
		return "Grabbed"
	case EventTypeSeriesFolderImported:
		return "SeriesFolderImported"
	case EventTypeDownloadFolderImported:
		return "DownloadFolderImported"
	case EventTypeDownloadFailed:
		return "DownloadFailed"
	case EventTypeEpisodeFileDeleted:
		return "EpisodeFileDeleted"
	case EventTypeEpisodeFileRenamed:
		return "EpisodeFileRenamed"
	case EventTypeDownloadIgnored:
		return "DownloadIgnored"
	default:
		return fmt.Sprintf("EventType(%d)", int(t))
	}
}

// EntryReader is the slice of [Repo] the [Service] reaches for. Pulled
// out so callers can inject a stub in tests without dragging the full
// CRUD surface; the real repo satisfies the interface.
//
// Method coverage:
//   - AllByDownloadID / AllByEpisodeID feed the per-id read paths
//     (FindByDownloadID + Find + FindByEpisodeId).
//   - Insert feeds the slice-3.AQ Service.Add wrapper. The seven
//     IHandle subscribers (sub-arcs 9b-9h) write through Add.
//
// Future slices that port heavier C# methods (`Paged`,
// `MostRecentForEpisode`, `GetBySeason`, `FindDownloadHistory`) will
// append to this interface as their needs land.
//
// The "Reader" name is preserved despite Insert appearing here —
// renaming would cascade into every test stub. The Go-port's
// pattern across packages is to grow these interfaces as needs land
// rather than splitting; the slice-3.62 stubReader pattern continues
// to apply.
type EntryReader interface {
	AllByDownloadID(ctx context.Context, downloadID string) ([]Entry, error)
	AllByEpisodeID(ctx context.Context, episodeID int) ([]Entry, error)
	Insert(ctx context.Context, e *Entry) (*Entry, error)
}

// Service ports a subset of `IHistoryService` at
// `History/HistoryService.cs:33-99`. This slice lands the two
// downloadID-driven readonly methods that unblock production wiring
// of [episodeimport.HistoryGrabsFinder] (slice 3.61's
// AggregateReleaseInfo aggregator) — `FindByDownloadId` and `Find`.
//
// Other readonly methods on the C# interface (`Paged`,
// `MostRecentForEpisode`, `FindByEpisodeId`, `MostRecentForDownloadId`,
// `Get`, `GetBySeries`, `GetBySeason`, `GetByEpisode`, `FindDownloadId`,
// `Since`) are deferred — each needs additional repo methods that
// haven't ported yet (`GetPaged`, `MostRecentForX`, `FindDownloadHistory`)
// and the consumers haven't ported either, so adding them now would
// be speculative.
//
// The seven `IHandle<...>` event-bus subscribers at the C# class are
// also deferred — they require event shapes (EpisodeGrabbedEvent,
// EpisodeImportedEvent, DownloadFailedEvent, EpisodeFileDeletedEvent,
// EpisodeFileRenamedEvent, SeriesDeletedEvent, DownloadIgnoredEvent)
// that haven't ported yet.
//
// Goroutine-safe: stateless after construction.
type Service struct {
	repo EntryReader
}

// NewService binds an EntryReader. nil reader returns nil so a caller
// wiring a service stack at startup can skip optional subscribers
// without an explicit guard. Mirrors the nil-tolerance pattern in
// [downloadhistory.NewService] (slice 3.63).
func NewService(repo EntryReader) *Service {
	if repo == nil {
		return nil
	}
	return &Service{repo: repo}
}

// FindByDownloadID ports `HistoryService.cs:96-99` —
// returns every history entry for the supplied downloadID, ordered
// by Date descending (the order the underlying repo guarantees).
//
// Direct delegate to the repo; no filtering. Empty slice is the
// "no rows" signal — propagates verbatim. Repo errors propagate
// wrapped.
//
// Production binding for [episodeimport.HistoryGrabsFinder] which
// applies the `EventType==Grabbed` filter at the aggregator. The
// aggregator chose the unfiltered surface so Service stays narrow;
// the alternative (a typed `FindByDownloadIDAndEventType`) is what
// `Find` below covers as a separate code path that mirrors C# `:91-94`.
func (s *Service) FindByDownloadID(ctx context.Context, downloadID string) ([]Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history: FindByDownloadID on nil receiver")
	}
	out, err := s.repo.AllByDownloadID(ctx, downloadID)
	if err != nil {
		return nil, fmt.Errorf("history: FindByDownloadID: %w", err)
	}
	return out, nil
}

// Find ports `HistoryService.cs:91-94` —
// returns every history entry for the supplied downloadID whose
// EventType matches the supplied filter, preserving the date-DESC
// order from the repo.
//
// Filter semantics:
//   - The C# EventType is a value type so a `null` arg isn't possible
//     at the language level; the C# overload that takes
//     `EpisodeHistoryEventType?` lives elsewhere. The Go port mirrors
//     the value-typed C# method signature and applies a strict equality
//     filter against the typed [EventType] arg.
//   - [Entry.EventType] is `*int` (NULL-safe — old rows predating the
//     column read as nil). Rows with nil EventType are skipped from
//     the filter regardless of the requested type — even for
//     [EventTypeUnknown] (=0), since "no value" and "value=0" carry
//     different audit-log meanings. C# uses `c.EventType == eventType`
//     against an enum value, which would also exclude NULL rows after
//     SQL-to-CLR null coalescing.
//
// Returns empty slice when no rows match. Repo errors propagate
// wrapped.
func (s *Service) Find(ctx context.Context, downloadID string, eventType EventType) ([]Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history: Find on nil receiver")
	}
	rows, err := s.repo.AllByDownloadID(ctx, downloadID)
	if err != nil {
		return nil, fmt.Errorf("history: Find: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		if r.EventType == nil {
			continue
		}
		if EventType(*r.EventType) == eventType {
			out = append(out, r)
		}
	}
	return out, nil
}

// Add inserts a new History row. Mirrors the C# pattern at
// `HistoryService.cs:213-373` — every IHandle subscriber builds an
// EpisodeHistory locally and calls `_historyRepository.Insert(history)`
// directly. The Go port surfaces the write as a public Service.Add
// so the future IHandle subscribers (sub-arcs 9b-9h) can route
// through one wrapper rather than each importing the Repo.
//
// Validation expectations match the C# Insert call sites:
//
//   - EpisodeID / SeriesID required; the C# repo allows zero but
//     the History UI filters them out, so the Go port surfaces a
//     typed error instead of silently inserting an orphan row.
//   - SourceTitle / Date required for the same reason.
//
// Quality / Languages / Data are JSON-shape strings the per-event
// handler builds before calling Add. Add does not validate them
// — the column is opaque text on the C# side too.
//
// Returns the inserted Entry (with ID populated by the underlying
// Repo.Insert auto-increment) on success.
//
// Future slices may add a ConfigSavedEvent-shaped audit-log emit
// here; for now Add is a thin pass-through.
func (s *Service) Add(ctx context.Context, e *Entry) (*Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history: Add on nil receiver")
	}
	if e == nil {
		return nil, errors.New("history: Add: nil entry")
	}
	if e.EpisodeID <= 0 {
		return nil, fmt.Errorf("history: Add: EpisodeID required, got %d", e.EpisodeID)
	}
	if e.SeriesID <= 0 {
		return nil, fmt.Errorf("history: Add: SeriesID required, got %d", e.SeriesID)
	}
	if e.SourceTitle == "" {
		return nil, errors.New("history: Add: SourceTitle required")
	}
	if e.Date.IsZero() {
		return nil, errors.New("history: Add: Date required")
	}
	out, err := s.repo.Insert(ctx, e)
	if err != nil {
		return nil, fmt.Errorf("history: Add: %w", err)
	}
	return out, nil
}

// FindByEpisodeId ports `HistoryService.cs:101-104` —
// returns every history entry for the supplied EpisodeId, preserving
// the date-DESC order the underlying repo guarantees.
//
// Direct delegate to the repo; no filtering. Empty slice is the
// "no rows" signal — propagates verbatim. Repo errors propagate
// wrapped.
//
// Production binding for [decisionengine.AlreadyImportedSpec] (slice
// 3.AL) which walks the per-episode history rows looking for
// matching grab/import pairs. The C# enum spelling
// `FindByEpisodeId` (note: lowercase 'd') is preserved verbatim for
// grep parity with the C# tree.
func (s *Service) FindByEpisodeId(ctx context.Context, episodeID int) ([]Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history: FindByEpisodeId on nil receiver")
	}
	out, err := s.repo.AllByEpisodeID(ctx, episodeID)
	if err != nil {
		return nil, fmt.Errorf("history: FindByEpisodeId: %w", err)
	}
	return out, nil
}

// Compile-time interface-satisfaction guard. A refactor that breaks
// the contract (Repo loses AllByDownloadID / AllByEpisodeID, signature
// changes) fails at build time rather than at the wiring callsite.
var _ EntryReader = (*Repo)(nil)
