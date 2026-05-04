// In-memory CommandQueue. Ports
// `NzbDrone.Core.Messaging.Commands.CommandQueue` at
// `src/NzbDrone.Core/Messaging/Commands/CommandQueue.cs:1-224`.
//
// The C# implementation is a list guarded by a mutex with
// `Monitor.PulseAll` for signalling. The Go-port keeps the same shape:
// a slice of in-flight rows behind a `sync.Mutex` + `sync.Cond` so the
// dispatcher's [Queue.Pull] can block until either fresh work arrives,
// a started command frees up, or the supplied ctx is cancelled.
//
// Eligibility logic mirrors `CommandQueue.cs:147-222` verbatim:
//
//   - If any started command sets RequiresDiskAccess, only return a
//     queued command that does NOT require disk access.
//   - Else if any started command sets IsLongRunning, only return a
//     queued command that does NOT set IsExclusive.
//   - If any started command is exclusive, return nothing (block all).
//   - If the candidate is exclusive and any command is started, return
//     nothing (wait for the started set to drain).
//   - Sort by Priority DESC, then QueuedAt ASC.
//
// The three behaviour flags (RequiresDiskAccess / IsExclusive /
// IsLongRunning) live on the C# Command base class as virtual
// properties. The Go-port stores Body as opaque JSON and looks the
// flags up by command Name through a host-supplied registry — see
// [CommandSpec] in dispatcher.go.

package commands

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// QueueProperties carries the dispatch-eligibility flags the queue
// needs for [Queue.Pull]. Mirrors the three virtual properties on the
// C# `Command` base class. Provided per command Name through the
// [CommandSpec] registry that backs both [Queue] and [Dispatcher].
type QueueProperties struct {
	RequiresDiskAccess bool
	IsExclusive        bool
	IsLongRunning      bool
}

// QueueClock returns the timestamp stamped on a Command when it
// transitions Queued → Started inside [Queue.Pull]. Defaults to
// `time.Now().UTC` when not overridden.
type QueueClock = func() time.Time

// ErrQueueClosed is returned by [Queue.Pull] when [Queue.Close] has
// been called and no eligible work is left.
var ErrQueueClosed = errors.New("commands: queue is closed")

// Queue is the in-memory dispatcher buffer. Goroutine-safe. One Queue
// per process; the dispatcher Pulls from it and the host (via
// [Service]) Adds to it.
type Queue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []*Command
	props  func(name string) QueueProperties
	now    QueueClock
	closed bool
}

// NewQueue builds an empty queue. propsFn returns the eligibility
// flags for a given command Name; nil is treated as a registry that
// reports zero-value properties for every name (no disk / not
// exclusive / not long-running) — useful in tests where dispatch
// ordering is the only thing under test.
func NewQueue(propsFn func(name string) QueueProperties) *Queue {
	q := &Queue{
		props: propsFn,
		now:   func() time.Time { return time.Now().UTC() },
	}
	if q.props == nil {
		q.props = func(string) QueueProperties { return QueueProperties{} }
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// SetClock overrides the clock used to stamp StartedAt when a queued
// row transitions to Started inside [Queue.Pull]. Tests pin a
// deterministic value; production leaves the default.
func (q *Queue) SetClock(f QueueClock) {
	if f == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.now = f
}

// Add appends a command to the queue and wakes any Pull waiters.
// Mirrors `CommandQueue.cs:21-29`. The C# implementation does not
// dedup on Add — DB-side dedup at [Service.Push] is the contract — and
// neither does the Go port. Passing a nil command is a no-op.
func (q *Queue) Add(c *Command) {
	if c == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, c)
	q.cond.Broadcast()
}

// AddMany is a convenience wrapper for boot-time re-push from
// [Service.HandleApplicationStarted]. Wakes waiters once.
func (q *Queue) AddMany(cs []*Command) {
	if len(cs) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, c := range cs {
		if c == nil {
			continue
		}
		q.items = append(q.items, c)
	}
	q.cond.Broadcast()
}

// RemoveIfQueued drops the row with the given id IFF it's currently
// in the Queued state. Returns true on success. Mirrors
// `CommandQueue.cs:78-96`. Used by [Service.Cancel] to stop the
// dispatcher from picking up a Cancelled row.
func (q *Queue) RemoveIfQueued(id int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, c := range q.items {
		if c.ID != id {
			continue
		}
		if Status(c.Status) != StatusQueued {
			return false
		}
		q.items = append(q.items[:i], q.items[i+1:]...)
		q.cond.Broadcast()
		return true
	}
	return false
}

// RemoveCompleted drops a row that has transitioned into a terminal
// state (Completed/Failed/Cancelled/Aborted/Orphaned). Called by the
// dispatcher after [Service.Complete] / [Service.Fail] returns. The
// C# version doesn't explicitly remove — it relies on the row already
// being out of the queue at consume time — but the Go-port leaves
// rows in the slice while Started so [Queue.Find] can surface them,
// then drops them once terminal. Returns true if removed.
func (q *Queue) RemoveCompleted(id int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, c := range q.items {
		if c.ID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			q.cond.Broadcast()
			return true
		}
	}
	return false
}

// Find returns the command with the given id, or nil if absent.
// Mirrors `CommandQueue.cs:60-63`. The returned pointer is the same
// instance held by the queue; callers must not mutate it.
func (q *Queue) Find(id int) *Command {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, c := range q.items {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// All returns a snapshot copy of every row currently held by the
// queue (Queued and Started). Mirrors `CommandQueue.cs:48-58` minus
// the C# pointer-aliased return — the Go port copies so callers can't
// race against the dispatcher.
func (q *Queue) All() []Command {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Command, len(q.items))
	for i, c := range q.items {
		out[i] = *c
	}
	return out
}

// Len reports the in-memory queue size.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// QueuedOrStarted returns a snapshot copy of every row in either of
// those two states. Mirrors `CommandQueue.cs:98-102`.
func (q *Queue) QueuedOrStarted() []Command {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Command, 0, len(q.items))
	for _, c := range q.items {
		st := Status(c.Status)
		if st == StatusQueued || st == StatusStarted {
			out = append(out, *c)
		}
	}
	return out
}

// Close marks the queue as drained. Subsequent [Queue.Pull] calls
// return ErrQueueClosed once they observe an empty eligible set.
// Existing waiters are woken so they can return.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}

// Pull blocks until an eligible Queued command is available, marks
// it Started (StartedAt = clock-now, Status = StatusStarted) in
// place, and returns it. Returns ctx.Err() on cancellation,
// ErrQueueClosed when [Queue.Close] is called.
//
// Eligibility logic ports `CommandQueue.cs:147-222`:
//
//   - When any Started command requires disk access, only non-disk
//     queued commands are eligible.
//   - Otherwise, when any Started command is long-running, only
//     non-exclusive queued commands are eligible.
//   - Sort eligibles by Priority DESC then QueuedAt ASC.
//   - If any Started command is exclusive, hold (return no row, wait).
//   - If the chosen candidate is exclusive but Started is non-empty,
//     hold (return no row, wait).
func (q *Queue) Pull(ctx context.Context) (*Command, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// One-shot watcher: when ctx cancels, broadcast so the cond
	// loop wakes and observes ctx.Err.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			q.cond.L.Lock()
			q.cond.Broadcast()
			q.cond.L.Unlock()
		case <-done:
		}
	}()

	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c := q.tryGetLocked(); c != nil {
			return c, nil
		}
		if q.closed {
			return nil, ErrQueueClosed
		}
		q.cond.Wait()
	}
}

// tryGetLocked is the C# `TryGet` body. Caller holds q.mu.
func (q *Queue) tryGetLocked() *Command {
	if len(q.items) == 0 {
		return nil
	}
	// Snapshot started set + flags so we don't re-read per candidate.
	var (
		startedCount int
		hasDisk      bool
		hasLong      bool
		hasExclusive bool
	)
	for _, c := range q.items {
		if Status(c.Status) != StatusStarted {
			continue
		}
		startedCount++
		p := q.props(c.Name)
		if p.RequiresDiskAccess {
			hasDisk = true
		}
		if p.IsLongRunning {
			hasLong = true
		}
		if p.IsExclusive {
			hasExclusive = true
		}
	}

	// Build the eligible candidate list per the C# filter rules.
	var eligible []*Command
	for _, c := range q.items {
		if Status(c.Status) != StatusQueued {
			continue
		}
		p := q.props(c.Name)
		if hasDisk && p.RequiresDiskAccess {
			continue
		}
		if !hasDisk && hasLong && p.IsExclusive {
			continue
		}
		eligible = append(eligible, c)
	}
	if len(eligible) == 0 {
		return nil
	}

	// Priority DESC, then QueuedAt ASC. SliceStable so equal-priority
	// rows preserve insertion order on the QueuedAt tie.
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].Priority != eligible[j].Priority {
			return eligible[i].Priority > eligible[j].Priority
		}
		return eligible[i].QueuedAt.Before(eligible[j].QueuedAt)
	})
	candidate := eligible[0]

	// Exclusivity gates after we know what we'd pick.
	if hasExclusive {
		return nil
	}
	candidateProps := q.props(candidate.Name)
	if candidateProps.IsExclusive && startedCount > 0 {
		return nil
	}

	// Mark Started in place. The dispatcher mutates the same pointer
	// when it later calls Service.Complete/Fail; the queue holds the
	// row until the dispatcher calls RemoveCompleted.
	now := q.now()
	candidate.StartedAt = &now
	candidate.Status = int(StatusStarted)
	return candidate
}
