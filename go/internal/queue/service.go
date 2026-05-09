package queue

import (
	"sync"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service is the in-memory queue cache + read-only surface. Mirrors a
// subset of `Queue/QueueService.cs:14-44` — the
// `IQueueService.GetQueue / Find` methods plus the cache the
// TrackedDownloadRefreshedEvent handler will write through (sub-arc
// 11c).
//
// # Concurrency model
//
// The C# tree uses a `static List<Queue> _queue` mutated under
// implicit single-threaded access from the message bus. The Go-port
// supports concurrent reads (the v3 API handler can call GetQueue
// at any time) so an RWMutex protects the cache: writes through
// SetQueue (slice 11b) take the write lock; GetQueue / Find take
// the read lock. The cache is COPIED on read so callers can iterate
// without holding the lock.
//
// # Service-wrapper pattern
//
// Matches the slice-3.AQ history.Service / slice-3.BA blocklist.Service
// posture: NewService returns a non-nil zero-cache Service; nil is
// not a valid value. Callers wire it into the boot graph and the
// later sub-arcs subscribe / route through the same instance.
//
// Goroutine-safe. Do NOT call Service methods from inside a Service
// callback that takes the lock (e.g. the TrackedDownloadRefreshedEvent
// handler routing through SetQueue) — the lock is not re-entrant.
type Service struct {
	mu    sync.RWMutex
	queue []Queue
	// bus is the optional message bus used to publish
	// QueueUpdatedEvent on every successful HandleTrackedDownloadRefreshed
	// projection (slice 3.BE). nil when the Service is constructed
	// without a bus — the handler simply skips the publish in that
	// case (mirrors the slice-3.A history.Service pattern where bus
	// is optional). Captured by SubscribeOn at boot time.
	bus *messaging.Bus
}

// NewService returns a zero-cache Service ready for population. The
// initial GetQueue returns an empty slice; the cache populates when
// the IHandle<TrackedDownloadRefreshedEvent> handler (sub-arc 11c)
// fires its first refresh.
func NewService() *Service {
	return &Service{queue: []Queue{}}
}

// GetQueue ports `QueueService.cs:31-34` —
//
//	public List<Queue> GetQueue() { return _queue; }
//
// Returns a defensive copy of the cache so callers can iterate /
// mutate without holding the lock or mutating the underlying slice.
// The C# tree returns the live `_queue` reference; the Go-port
// snapshot pattern protects the cache against concurrent-mutation
// foot-guns (e.g. an API handler iterating while the
// TrackedDownloadRefreshedEvent handler reassigns).
//
// Returns a non-nil empty slice when the cache is empty.
func (s *Service) GetQueue() []Queue {
	if s == nil {
		return []Queue{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Queue, len(s.queue))
	copy(out, s.queue)
	return out
}

// SetQueue replaces the cache with the supplied rows under the write
// lock. nil input is treated as "empty cache" (cleared). The supplied
// slice is COPIED — callers can mutate their input after the call
// without affecting the cache.
//
// Slice 3.BD (sub-arc 11b) ships SetQueue as the cache-write surface
// the slice-3.BE (sub-arc 11c) IHandle<TrackedDownloadRefreshedEvent>
// will route through. The IHandle wraps SetQueue with the
// QueueUpdatedEvent publish per C# `:98-105`; SetQueue itself does
// NOT publish so test paths can replace the cache without bus side
// effects.
//
// nil-receiver is a no-op (no panic, no error returned) — matches
// the GetQueue / Find tolerance posture so callers wiring through a
// nil Service stack get silent dropping rather than an NPE.
func (s *Service) SetQueue(rows []Queue) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rows == nil {
		s.queue = []Queue{}
		return
	}
	s.queue = append([]Queue(nil), rows...)
}

// Remove ports `QueueService.cs:41-44` —
//
//	public void Remove(int id) { _queue.Remove(Find(id)); }
//
// Drops the cache row whose ID matches. Returns true when a row was
// removed, false otherwise. The C# tree returns void (silent on
// miss); the Go-port surfaces a bool so UI handlers can distinguish
// "removed N rows" from "no-op".
//
// Locks the write mutex for the duration. nil-receiver is a no-op
// returning false.
//
// Note: the C# Remove silently no-ops on a miss because
// `Find(id)` returns null and `List.Remove(null)` returns false
// without raising. The Go-port preserves the silent semantic but
// returns the boolean for caller diagnostics.
func (s *Service) Remove(id int) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.queue {
		if s.queue[i].ID == id {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return true
		}
	}
	return false
}

// Find ports `QueueService.cs:36-39` —
//
//	public Queue Find(int id) { return _queue.SingleOrDefault(q => q.Id == id); }
//
// Returns the queue row with the supplied ID, or nil when no row
// matches. The C# `SingleOrDefault` returns null on miss; the Go-
// port returns nil so callers can use `if q := svc.Find(id); q !=
// nil` for the canonical not-found check.
//
// Returns a defensive copy of the matched row. C# returns the live
// reference; the Go-port snapshot mirrors GetQueue's posture so a
// caller mutating the returned Queue can't bleed into the cache.
func (s *Service) Find(id int) *Queue {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.queue {
		if s.queue[i].ID == id {
			clone := s.queue[i]
			return &clone
		}
	}
	return nil
}
