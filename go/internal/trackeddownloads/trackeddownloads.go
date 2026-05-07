// Package trackeddownloads is the Go port of
// `NzbDrone.Core.Download.TrackedDownloads` — the in-memory cache
// that maps a DownloadID to its parsed RemoteEpisode + lifecycle
// state, populated by the queue / import workflow.
//
// This slice (3.69) lands the data shape + an in-memory cache +
// the readonly Find / write-side Track / StopTracking surface that
// production wiring needs to satisfy
// [episodeimport.TrackedDownloadFinder] (slice 3.62's narrow
// interface used by the ImportDecisionMaker orchestrator).
//
// Deferred to later slices:
//
//   - [Service.TrackDownload(downloadClient, downloadItem)] — the
//     C# 4-arg method at `TrackedDownloadService.cs:91-...` that
//     parses an unknown DownloadClientItem into a TrackedDownload
//     by walking ParsingService + HistoryService + the
//     remote-aggregation pipeline. Heavy: depends on ParsingService
//     (now ported), HistoryService (now ported), DownloadHistoryService
//     (now ported), CustomFormatCalculationService (in-tree but no
//     LocalEpisode adapter yet), and the remote-aggregation slice
//     which hasn't started. The current Track method takes a
//     pre-built [TrackedDownload] instead — appropriate for the
//     queue / monitoring slices that have done their own parsing.
//
//   - The five `IHandle<...>` event-bus subscribers
//     (EpisodeInfoRefreshedEvent / SeriesAddedEvent / SeriesEditedEvent /
//     SeriesBulkEditedEvent / SeriesDeletedEvent) — defer with the
//     unported event shapes.
//
//   - [TrackedDownloadStatusMessage] payload — the status-message
//     format for surfacing per-download warnings to the UI. Not yet
//     ported; the [TrackedDownload.StatusMessages] field is a
//     placeholder string slice for now.
package trackeddownloads

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/parser"
)

// State is the wire-stable enum for [TrackedDownload.State]. Mirrors
// `TrackedDownloadState` at `TrackedDownload.cs:49-59`.
//
// Numeric order is frozen — the v3 frontend filters per-int. Adding
// new entries APPENDS at the end.
type State int

// State values. Names + ordinals match the C# enum at
// `TrackedDownload.cs:51-58` exactly.
const (
	// StateDownloading — the download is in progress at the client.
	StateDownloading State = 0
	// StateImportBlocked — download finished but the import pipeline
	// rejected it (sample / no episodes / etc.).
	StateImportBlocked State = 1
	// StateImportPending — finished, queued for import.
	StateImportPending State = 2
	// StateImporting — import in progress.
	StateImporting State = 3
	// StateImported — files moved into the library.
	StateImported State = 4
	// StateFailedPending — failure detected, awaiting client cleanup.
	StateFailedPending State = 5
	// StateFailed — terminal failure.
	StateFailed State = 6
	// StateIgnored — user told Sonarr to forget this download.
	StateIgnored State = 7
)

// String returns the C# enum-identifier form for log lines. Out-of-
// range values fall through to `State(N)`.
func (s State) String() string {
	switch s {
	case StateDownloading:
		return "Downloading"
	case StateImportBlocked:
		return "ImportBlocked"
	case StateImportPending:
		return "ImportPending"
	case StateImporting:
		return "Importing"
	case StateImported:
		return "Imported"
	case StateFailedPending:
		return "FailedPending"
	case StateFailed:
		return "Failed"
	case StateIgnored:
		return "Ignored"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Status is the wire-stable enum for [TrackedDownload.Status]. Mirrors
// `TrackedDownloadStatus` at `TrackedDownload.cs:61-66`.
type Status int

// Status values. Names + ordinals match the C# enum at
// `TrackedDownload.cs:63-65` exactly.
const (
	// StatusOK — no warnings or errors.
	StatusOK Status = 0
	// StatusWarning — a per-download warning is set (StatusMessages).
	StatusWarning Status = 1
	// StatusError — the download is failed; client will be told to
	// remove it (Fail() at C# `:39`).
	StatusError Status = 2
)

// String returns the C# enum-identifier form for log lines.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "Ok"
	case StatusWarning:
		return "Warning"
	case StatusError:
		return "Error"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// TrackedDownload is the per-DownloadID cached payload. Mirrors
// `TrackedDownload.cs:7-46`. Direct field access is allowed; the
// Service is the only place where mutation crosses goroutines, so
// callers either own a freshly-cloned value (returned by Find) or
// lock the Service themselves.
//
// RemoteEpisode is `*decisionengine.Subject` — the Go-port equivalent
// of C# `RemoteEpisode` (renamed in slice 3.31 because the value
// carries parsed release + matched series + episodes + CustomFormats
// rather than "an episode living somewhere remote"). nil when the
// TrackedDownload predates parsing or the parse failed.
//
// The C# class has an `Indexer` string field; the Go port keeps it
// for parity but production wiring uses
// `RemoteEpisode.Release.Indexer` as the source of truth.
type TrackedDownload struct {
	// DownloadID is the cache key. Required.
	DownloadID string

	// DownloadClient is the row id of the configured download client
	// the item came from. Mirrors C# `:9`.
	DownloadClient int

	// DownloadItem is the live download-client view of the file —
	// progress, output paths, etc. Mirrors C# `:10`.
	DownloadItem *downloadclients.Item

	// ImportItem is the post-rename view if the script-import flow
	// produced a different shape than the original DownloadItem.
	// Mirrors C# `:11`.
	ImportItem *downloadclients.Item

	// State is the lifecycle stage. Mirrors C# `:12`.
	State State

	// Status is the warn/error tag. Mirrors C# `:13` (note: C# made
	// the setter private; the Go port allows direct mutation since
	// callers either own a freshly-cloned value or lock the Service).
	Status Status

	// RemoteEpisode is the parsed remote-release subject. nil when
	// not yet parsed.
	RemoteEpisode *decisionengine.Subject

	// StatusMessages carry per-download warnings for the UI. Mirrors
	// C# `:15` `TrackedDownloadStatusMessage[]`. This slice ships a
	// simplified `[]string` shape — the typed status-message struct
	// ports alongside the queue / UI integration slice that
	// consumes it.
	StatusMessages []string

	// Protocol is the download protocol (Usenet / Torrent). Mirrors
	// `indexers.DownloadProtocol`.
	Protocol int

	// Indexer is the indexer name string. Mirrors C# `:17`.
	Indexer string

	// Added is when the download was first tracked. Mirrors C# `:18`
	// `DateTime?`.
	Added *time.Time

	// IsTrackable controls whether the downloads tab surfaces this
	// entry. Mirrors C# `:19`.
	IsTrackable bool

	// HasNotifiedManualInteractionRequired tracks whether the user
	// has been pinged for manual intervention. Mirrors C# `:20`.
	HasNotifiedManualInteractionRequired bool
}

// IndexerFlags returns the per-release IndexerFlags from the wrapped
// RemoteEpisode.Release, or zero when no remote episode / release is
// attached.
//
// Convenience for the [episodeimport.TrackedDownloadFinder]
// production binding which calls
// `tracked.RemoteEpisode.Release.IndexerFlags` in the C# tree at
// `ImportDecisionMaker.cs:147-150`. Wrapping the chain here keeps
// the binding code at the host trivial.
//
// `Subject.Release` is `any` — the field carries a `*parser.ReleaseInfo`
// or a `*parser.TorrentInfo` depending on protocol. Both shapes embed
// IndexerFlags identically; the type-switch covers both.
func (t *TrackedDownload) IndexerFlags() parser.IndexerFlags {
	if t == nil || t.RemoteEpisode == nil || t.RemoteEpisode.Release == nil {
		return 0
	}
	switch r := t.RemoteEpisode.Release.(type) {
	case *parser.ReleaseInfo:
		if r == nil {
			return 0
		}
		return r.IndexerFlags
	case *parser.TorrentInfo:
		if r == nil {
			return 0
		}
		return r.IndexerFlags
	default:
		return 0
	}
}

// Service is the in-memory cache of TrackedDownloads keyed by
// DownloadID. Mirrors the read+write surface of
// `TrackedDownloadService.cs:64-89`.
//
// Goroutine-safe: every mutation takes the RWMutex's write lock;
// every read takes the read lock. Concurrent readers don't block
// each other.
type Service struct {
	log *slog.Logger

	mu    sync.RWMutex
	cache map[string]*TrackedDownload
}

// NewService constructs an empty cache. log may be nil
// (defaults to [slog.Default]). Mirrors the slice-3.55+ optional-log
// pattern.
func NewService(log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		log:   log,
		cache: make(map[string]*TrackedDownload),
	}
}

// Track inserts or replaces the tracked download for its DownloadID.
// Mirrors the cache-insert paths inside `TrackedDownloadService.cs`
// (e.g. `:213` `_cache.Set(...)` inside TrackDownload).
//
// nil receiver / nil td / empty DownloadID return an error so the
// caller's misuse surfaces loudly — the cache invariant is "every
// row keyed by a non-empty DownloadID points at a non-nil TrackedDownload".
//
// The supplied TrackedDownload pointer is stored by reference. The
// caller must not mutate the value afterward; for safety, prefer
// passing a freshly-built struct.
func (s *Service) Track(td *TrackedDownload) error {
	if s == nil {
		return errors.New("trackeddownloads: Track on nil receiver")
	}
	if td == nil {
		return errors.New("trackeddownloads: Track: nil TrackedDownload")
	}
	if td.DownloadID == "" {
		return errors.New("trackeddownloads: Track: empty DownloadID")
	}
	s.mu.Lock()
	s.cache[td.DownloadID] = td
	s.mu.Unlock()
	return nil
}

// Find returns the cached TrackedDownload for the supplied
// DownloadID, or nil when no row exists. Mirrors C# `:64-67`
// `_cache.Find(downloadId)`.
//
// Returns the SAME pointer as is stored in the cache (no defensive
// copy). The caller must not mutate the returned value — the
// service holds the same pointer for subsequent reads. C# semantics
// match (the C# `_cache.Find` returns the live cached reference).
//
// nil receiver returns nil.
func (s *Service) Find(downloadID string) *TrackedDownload {
	if s == nil || downloadID == "" {
		return nil
	}
	s.mu.RLock()
	td := s.cache[downloadID]
	s.mu.RUnlock()
	return td
}

// StopTracking removes the supplied DownloadID from the cache.
// Mirrors C# `:69-75` (the single-id form).
//
// Returns true when a row was removed; false when no row existed
// or the receiver / id is empty. The C# version publishes a
// TrackedDownloadsRemovedEvent on every call — that event-bus
// dispatch is deferred with the unported event shapes.
func (s *Service) StopTracking(downloadID string) bool {
	if s == nil || downloadID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cache[downloadID]; !ok {
		return false
	}
	delete(s.cache, downloadID)
	return true
}

// StopTrackingMany removes every supplied DownloadID. Mirrors C#
// `:77-89` (the batch form). Returns the count of rows actually
// removed. The C# version's event-bus dispatch is deferred.
//
// Empty / nil ids slice is a no-op (returns 0).
func (s *Service) StopTrackingMany(downloadIDs []string) int {
	if s == nil || len(downloadIDs) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for _, id := range downloadIDs {
		if id == "" {
			continue
		}
		if _, ok := s.cache[id]; ok {
			delete(s.cache, id)
			removed++
		}
	}
	return removed
}

// GetTrackedDownloads returns a snapshot of every cached
// TrackedDownload. Mirrors C# `GetTrackedDownloads` at the bottom
// of the C# class (re-uses the cache iterator).
//
// Returns a fresh slice (independent of the cache map's internal
// storage) so caller iteration is safe under concurrent mutation.
// The slice is in iteration-order of the underlying map — Go's map
// iteration is randomised, so callers expecting a stable order
// should sort by their preferred key (most do this already).
func (s *Service) GetTrackedDownloads() []*TrackedDownload {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*TrackedDownload, 0, len(s.cache))
	for _, td := range s.cache {
		out = append(out, td)
	}
	return out
}

// FindIndexerFlags is the prod-wiring adapter for
// [episodeimport.TrackedDownloadFinder]. Returns the cached
// TrackedDownload's IndexerFlags + ok=true on hit; (0, false) on
// miss.
//
// Wraps Find + [TrackedDownload.IndexerFlags] so the host wiring
// can pass `*Service` directly to [episodeimport.NewImportDecisionMaker]
// without an explicit adapter shim — the method signature here
// matches the [episodeimport.TrackedDownloadFinder] surface exactly.
func (s *Service) FindIndexerFlags(downloadID string) (parser.IndexerFlags, bool) {
	td := s.Find(downloadID)
	if td == nil {
		return 0, false
	}
	flags := td.IndexerFlags()
	if flags == 0 {
		// No flags AND td was found is an interesting state: the
		// remote episode parsed but didn't carry indexer flags. Tell
		// the caller "we had a row, no flags" via ok=true + zero
		// flags; the orchestrator's `if flags, ok := ...; ok {
		// le.IndexerFlags = flags }` arm writes zero, which matches
		// the C# behaviour where the field is unconditionally set
		// when a tracked download exists.
		return 0, true
	}
	return flags, true
}
