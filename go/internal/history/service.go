package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/quality"
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
	AllBySeriesID(ctx context.Context, seriesID int) ([]Entry, error)
	Insert(ctx context.Context, e *Entry) (*Entry, error)
	DeleteForSeries(ctx context.Context, seriesIDs []int) (int64, error)
	Get(ctx context.Context, id int) (*Entry, error)
	Since(ctx context.Context, cutoff time.Time) ([]Entry, error)
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

// DeleteForSeries ports the SeriesDeletedEvent path at
// `HistoryService.cs:368-371` —
//
//	_historyRepository.DeleteForSeries(message.Series.Select(m => m.Id).ToList())
//
// Bulk-removes every History row whose SeriesId is in the supplied
// list. Returns rows affected; 0 with nil error when seriesIDs is
// empty (no-op short-circuit). Repo errors propagate wrapped.
//
// Mirrors the C# behaviour where a SeriesDeletedEvent fan-out cleans
// up audit-log rows alongside other per-series children — the
// History.SeriesId column is NOT a foreign key in the schema, so
// the cleanup is application-side rather than DB-cascade.
func (s *Service) DeleteForSeries(ctx context.Context, seriesIDs []int) (int64, error) {
	if s == nil || s.repo == nil {
		return 0, errors.New("history: DeleteForSeries on nil receiver")
	}
	if len(seriesIDs) == 0 {
		return 0, nil
	}
	n, err := s.repo.DeleteForSeries(ctx, seriesIDs)
	if err != nil {
		return 0, fmt.Errorf("history: DeleteForSeries: %w", err)
	}
	return n, nil
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

// Get returns a single history.Entry by Id. Mirrors C#
// `HistoryService.Get(int id)` at `HistoryService.cs:83-86` — direct
// repo lookup, ErrNotFound when missing.
func (s *Service) Get(ctx context.Context, id int) (*Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history: Get on nil receiver")
	}
	return s.repo.Get(ctx, id)
}

// Since ports `HistoryService.Since(DateTime, EpisodeHistoryEventType?)`
// at `HistoryService.cs:88-91`. Returns every history row newer than
// the cutoff, optionally filtered by event type (nil = no filter).
// Order: Date descending (same as the underlying repo guarantee).
func (s *Service) Since(ctx context.Context, cutoff time.Time, eventType *EventType) ([]Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history: Since on nil receiver")
	}
	rows, err := s.repo.Since(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("history: Since: %w", err)
	}
	if eventType == nil {
		return rows, nil
	}
	return filterByEventType(rows, *eventType), nil
}

// GetBySeries ports `HistoryService.GetBySeries(int seriesId,
// EpisodeHistoryEventType?)` at `HistoryService.cs:73-76`. Returns
// every history row for the series, optionally filtered by event
// type (nil = no filter). Repo.AllBySeriesID handles the underlying
// query (Date-descending order).
func (s *Service) GetBySeries(ctx context.Context, seriesID int, eventType *EventType) ([]Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history: GetBySeries on nil receiver")
	}
	rows, err := s.repo.AllBySeriesID(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("history: GetBySeries: %w", err)
	}
	if eventType == nil {
		return rows, nil
	}
	return filterByEventType(rows, *eventType), nil
}

// GetByEpisode ports `HistoryService.GetByEpisode(episodeId, eventType)`
// (`src/NzbDrone.Core/History/HistoryService.cs:86-89`). Returns every
// history row for the episode, optionally filtered by event type.
func (s *Service) GetByEpisode(ctx context.Context, episodeID int, eventType *EventType) ([]Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history: GetByEpisode on nil receiver")
	}
	rows, err := s.repo.AllByEpisodeID(ctx, episodeID)
	if err != nil {
		return nil, fmt.Errorf("history: GetByEpisode: %w", err)
	}
	if eventType == nil {
		return rows, nil
	}
	return filterByEventType(rows, *eventType), nil
}

// FindDownloadID ports `HistoryService.cs:101-142` —
// recovers a download-client identifier for an imported episode whose
// EpisodeImportedEvent carries no DownloadID of its own (manual /
// rescan imports of files Sonarr originally grabbed). The algorithm
// walks the series-scoped, quality-matched history rows and reasons
// about grab/import pairs to deduce a single still-downloading grab.
//
// Algorithm (C# `:103-142`):
//
//  1. Pull every history row for the supplied SeriesID via
//     AllBySeriesID, then filter to the quality-match subset whose
//     EventType is one of {Grabbed, DownloadFailed,
//     DownloadFolderImported}. Mirrors C#
//     `HistoryRepository.FindDownloadHistory(:105-114)` — the C# repo
//     pushes the filter into SQL; the Go port walks in memory because
//     the schema's `Quality` column is opaque JSON and the existing
//     repo surface doesn't expose a JSON-aware Where.
//  2. Restrict further to rows whose EpisodeID is in the supplied set.
//  3. Compute `processed = { row.DownloadID | row is non-Grabbed AND
//     has DownloadID }` — every downloadId that has already finished
//     a processing (import / fail) step. C# `:111-113`.
//  4. `stillDownloading = { row | row.EventType=Grabbed AND
//     row.DownloadID not in processed }`. The set of grabs that
//     haven't been resolved by an import/fail row yet. C# `:115`.
//  5. If `stillDownloading` is empty, return "". Otherwise iterate the
//     supplied episodeIDs in order: for each, the stillDownloading
//     rows must contain exactly one entry. Any episode with zero or
//     multiple matches → ambiguous → return "". Each per-episode
//     match must agree on the same DownloadID — disagreement → "".
//     C# `:119-138`.
//
// Returns the unanimous downloadID, or "" when the algorithm declines
// to guess. Repo errors propagate wrapped; JSON-decode errors on the
// Quality column are non-fatal (row skipped, walk continues) so a
// single corrupt row doesn't poison the lookup.
//
// Production caller: [historyhandlers.HandleEpisodeImported] via the
// LookupDownloadID interface, wired through the variadic
// WithDownloadIDLookup option (slice 5.J127).
func (s *Service) FindDownloadID(ctx context.Context, seriesID int, qualityModel quality.Model, episodeIDs []int) (string, error) {
	if s == nil || s.repo == nil {
		return "", errors.New("history: FindDownloadID on nil receiver")
	}
	if seriesID <= 0 || len(episodeIDs) == 0 {
		return "", nil
	}
	rows, err := s.repo.AllBySeriesID(ctx, seriesID)
	if err != nil {
		return "", fmt.Errorf("history: FindDownloadID: %w", err)
	}
	if len(rows) == 0 {
		return "", nil
	}
	episodeSet := make(map[int]struct{}, len(episodeIDs))
	for _, id := range episodeIDs {
		episodeSet[id] = struct{}{}
	}
	matched := make([]Entry, 0, len(rows))
	for _, r := range rows {
		if r.EventType == nil {
			continue
		}
		et := EventType(*r.EventType)
		if et != EventTypeGrabbed && et != EventTypeDownloadFailed && et != EventTypeDownloadFolderImported {
			continue
		}
		if _, ok := episodeSet[r.EpisodeID]; !ok {
			continue
		}
		if !qualityEquals(r.Quality, qualityModel) {
			continue
		}
		matched = append(matched, r)
	}
	if len(matched) == 0 {
		return "", nil
	}
	processed := make(map[string]struct{}, len(matched))
	for _, r := range matched {
		if EventType(*r.EventType) == EventTypeGrabbed {
			continue
		}
		if r.DownloadID == "" {
			continue
		}
		processed[r.DownloadID] = struct{}{}
	}
	stillDownloading := make([]Entry, 0, len(matched))
	for _, r := range matched {
		if EventType(*r.EventType) != EventTypeGrabbed {
			continue
		}
		if _, done := processed[r.DownloadID]; done {
			continue
		}
		stillDownloading = append(stillDownloading, r)
	}
	if len(stillDownloading) == 0 {
		return "", nil
	}
	var downloadID string
	for _, ep := range episodeIDs {
		count := 0
		var candidate string
		for _, r := range stillDownloading {
			if r.EpisodeID != ep {
				continue
			}
			count++
			candidate = r.DownloadID
		}
		if count != 1 {
			return "", nil
		}
		if downloadID == "" || downloadID == candidate {
			downloadID = candidate
		} else {
			return "", nil
		}
	}
	return downloadID, nil
}

// qualityEquals JSON-decodes a persisted Quality column and compares
// it against the supplied target via [quality.Model.Equal]. Decode
// failures yield `false` so a corrupt row is skipped, not propagated.
func qualityEquals(persisted string, target quality.Model) bool {
	if persisted == "" {
		return false
	}
	var m quality.Model
	if err := json.Unmarshal([]byte(persisted), &m); err != nil {
		return false
	}
	return m.Equal(target)
}

func filterByEventType(rows []Entry, eventType EventType) []Entry {
	out := make([]Entry, 0, len(rows))
	for _, e := range rows {
		if e.EventType != nil && EventType(*e.EventType) == eventType {
			out = append(out, e)
		}
	}
	return out
}

// Compile-time interface-satisfaction guard. A refactor that breaks
// the contract (Repo loses AllByDownloadID / AllByEpisodeID, signature
// changes) fails at build time rather than at the wiring callsite.
var _ EntryReader = (*Repo)(nil)
