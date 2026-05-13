// Package mediaserverqueue ports the C# `MediaServerUpdateQueue<THost,TInfo>`
// at `Notifications/MediaServerUpdateQueue.cs`. Slice 4.CX (B2 of
// the follow-up C-cleanup deferred-surface backfill).
//
// The C# queue is a per-(provider-class, host-identifier) coalescing
// buffer for library-refresh events. MediaBrowser uses it to batch
// "Created"/"Modified"/"Deleted" series updates per host; Xbmc + Plex
// use it to batch series-needs-refresh + clean-flag pairs. Without
// this buffer, a 20-episode import fires 20 separate per-series
// library-refresh calls; with it, the orchestrator's periodic
// ProcessQueue tick drains them all in one batched call.
//
// Go-port differences from the C# original:
//   - Generic [T comparable] for the per-item info type (string for
//     MediaBrowser, bool for Xbmc/Plex). C# used a 2-arg generic;
//     the Go port only parameterises the info type since the
//     "provider class" type was C#-specific (per-class cache scoping
//     via typeof(TQueueHost) — each Go provider package instantiates
//     its own *Queue instance instead).
//   - In-process map keyed by host identifier (no rolling-cache TTL
//     — the orchestrator owns the lifecycle, and queue entries
//     don't survive process restarts in either C# or the Go port).
//   - sync.Mutex-guarded buckets; the C# code uses lock(queue)
//     on the per-host bucket and a Concurrent dictionary at the top.
//     The Go port uses one top-level mutex for bucket-map ops + a
//     per-bucket mutex for the pending map (matches the C# locking
//     graph exactly: cache access is briefly locked, then the
//     per-host bucket is locked for the duration of add/drain).
//
// Goroutine-safety: Queue.Add and Queue.Process may be called
// concurrently from any goroutine. Process serialises drain against
// itself per-host (the C# Refreshing flag) — a concurrent Process
// call against the same host returns immediately.
package mediaserverqueue

import (
	"sync"

	"sonarr.tv/sonarr/internal/series"
)

// Queue is a per-host coalescing buffer of series-update items.
// T is the per-item info type (string for MediaBrowser, bool for
// Xbmc/Plex). Constructed via [New]; the zero value is unusable.
type Queue[T comparable] struct {
	mu      sync.Mutex
	buckets map[string]*bucket[T]
}

// bucket is the per-host pending-updates set + refresh-in-progress
// gate. Mirrors the C# `UpdateQueue` nested class at `:13-17`.
type bucket[T comparable] struct {
	mu         sync.Mutex
	pending    map[int]*Item[T]
	refreshing bool
}

// Item is a single per-series pending entry. The Info field is a
// set of T values: MediaBrowser accumulates "Created" + "Modified"
// strings for the same series across multiple Add calls; Xbmc/Plex
// accumulate bool flags (true = needs library-clean after the scan).
//
// Mirrors C# `UpdateQueueItem<TItemInfo>` at `:94-104`.
type Item[T comparable] struct {
	Series *series.Series
	Info   map[T]struct{}
}

// New constructs an empty *Queue[T].
func New[T comparable]() *Queue[T] {
	return &Queue[T]{buckets: make(map[string]*bucket[T])}
}

// Add records the (series, info) pair against the per-host bucket.
// Concurrent Add calls for the same (host, series.ID) tuple
// accumulate Info values into the same bucket entry — the set
// semantic dedups identical T values.
//
// Mirrors C# `MediaServerUpdateQueue.Add` at `:26-40`.
func (q *Queue[T]) Add(host string, ser *series.Series, info T) {
	if ser == nil {
		return
	}
	b := q.bucketFor(host)

	b.mu.Lock()
	defer b.mu.Unlock()

	item, ok := b.pending[ser.ID]
	if !ok {
		item = &Item[T]{Series: ser, Info: make(map[T]struct{})}
		b.pending[ser.ID] = item
	}
	item.Info[info] = struct{}{}
}

// Process drains the per-host bucket through update. When the bucket
// is empty or another Process call is already draining the same
// host, this returns immediately (no-op). Per-batch drains are
// repeated until the pending map is empty — handles the case where
// new items arrive while a previous batch is being processed.
//
// The update callback receives the items captured for the current
// batch; subsequent Add calls during the callback go into the next
// batch.
//
// Returning from Process always clears the refreshing flag, even if
// update panics. Mirrors C# `ProcessQueue` at `:42-91` (the C# code
// uses try/catch to clear the flag on exception; the Go port uses
// defer).
func (q *Queue[T]) Process(host string, update func(items []*Item[T])) {
	q.mu.Lock()
	b, ok := q.buckets[host]
	q.mu.Unlock()
	if !ok {
		return
	}

	// Acquire the refresh gate. Mirrors C# `:51-59`.
	b.mu.Lock()
	if b.refreshing {
		b.mu.Unlock()
		return
	}
	b.refreshing = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.refreshing = false
		b.mu.Unlock()
	}()

	for {
		// Snapshot + clear the per-batch slice. Mirrors C# `:63-77`.
		b.mu.Lock()
		if len(b.pending) == 0 {
			b.mu.Unlock()
			return
		}
		items := make([]*Item[T], 0, len(b.pending))
		for _, item := range b.pending {
			items = append(items, item)
		}
		b.pending = make(map[int]*Item[T])
		b.mu.Unlock()

		update(items)
	}
}

// bucketFor returns the per-host bucket, creating one on first
// access. The top-level mutex covers the map check/insert; the
// returned bucket carries its own mutex for pending-map ops.
func (q *Queue[T]) bucketFor(host string) *bucket[T] {
	q.mu.Lock()
	defer q.mu.Unlock()
	b, ok := q.buckets[host]
	if !ok {
		b = &bucket[T]{pending: make(map[int]*Item[T])}
		q.buckets[host] = b
	}
	return b
}
