package signalr

import (
	"sync"
	"time"
)

// Debouncer ports `NzbDrone.Common.TPL.Debouncer` at
// `src/NzbDrone.Common/TPL/Debouncer.cs:5-65`. Coalesces many
// rapid Execute() calls into a single trailing-edge fire of the
// wrapped action after a quiet period.
//
// Semantics (lazy trailing-edge):
//
//   - First Execute() schedules a fire at now+Duration.
//   - Additional Execute()s during the pending window are absorbed
//     (NOT extending the window — same as the C# version where
//     System.Timers.Timer.Start() on a running timer is a no-op).
//   - When the timer elapses, the action fires once.
//   - Pause()/Resume() stack like the C# version: the action
//     doesn't fire while paused; calling Resume back to 0-paused
//     re-arms the timer if a pending trigger was recorded.
//
// Use cases (per go/analysis/signalr/inventory.md):
//
//   - CommandController: 100ms debounce on CommandUpdatedEvent to
//     coalesce bursts of in-flight command state changes.
//   - QueueStatusController: 5s debounce on queue events so the
//     full snapshot rebuild doesn't run more than once per 5s.
//
// Pause()/Resume() are used by the QueueStatusController to wrap
// an HTTP GET — the read shouldn't immediately retrigger a
// broadcast (the C# tree wraps GetQueueStatus in Pause/Resume to
// prevent that loop).
//
// Stop() is the Go-port addition — releases the underlying timer
// during graceful shutdown. The C# version relies on GC for
// cleanup since Timer.Dispose isn't called.
type Debouncer struct {
	delay time.Duration
	fn    func()

	mu        sync.Mutex
	timer     *time.Timer
	triggered bool
	paused    int
	stopped   bool
}

// NewDebouncer creates a Debouncer that calls fn at most once per
// quiet period of `delay`. fn must not be nil; delay must be > 0.
// Both pre-conditions are panics — boot-time misconfiguration
// should fail loud.
func NewDebouncer(fn func(), delay time.Duration) *Debouncer {
	if fn == nil {
		panic("signalr.NewDebouncer: nil fn")
	}
	if delay <= 0 {
		panic("signalr.NewDebouncer: delay must be positive")
	}
	return &Debouncer{fn: fn, delay: delay}
}

// Execute schedules a single deferred call to fn. Multiple
// Execute() calls within the debounce window coalesce into one
// trailing-edge fire. The first Execute after a fire (or after
// Resume) re-arms the timer.
//
// Mirrors C# `Debouncer.Execute` at `Debouncer.cs:32-41` — when
// paused records the trigger so Resume can fire it later.
func (d *Debouncer) Execute() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.triggered = true
	if d.paused == 0 && d.timer == nil {
		d.timer = time.AfterFunc(d.delay, d.fire)
	}
}

// fire is the internal timer callback. Re-checks the pause counter
// + fires the wrapped action while NOT holding d.mu (so the action
// can call back into Execute without deadlocking).
func (d *Debouncer) fire() {
	d.mu.Lock()
	if d.stopped || d.paused != 0 {
		d.mu.Unlock()
		return
	}
	d.triggered = false
	d.timer = nil
	fn := d.fn
	d.mu.Unlock()
	fn()
}

// Pause increments the pause counter. While paused (counter > 0),
// the timer is stopped + any in-flight fire is suppressed.
// Multiple Pause/Resume calls stack — Resume only re-arms when the
// counter returns to 0.
//
// Mirrors C# `Debouncer.Pause` at `:43-50`.
func (d *Debouncer) Pause() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused++
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

// Resume decrements the pause counter. When the counter returns
// to 0 AND a trigger was recorded while paused, re-arms the timer
// so the trailing-edge fire still happens.
//
// Mirrors C# `Debouncer.Resume` at `:52-62`. Calling Resume on a
// non-paused debouncer is a no-op (the counter just goes negative,
// matching C#'s lock-free decrement).
func (d *Debouncer) Resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.paused > 0 {
		d.paused--
	}
	if d.paused == 0 && d.triggered && d.timer == nil && !d.stopped {
		d.timer = time.AfterFunc(d.delay, d.fire)
	}
}

// Stop releases the underlying timer. The wrapped action will NOT
// fire after Stop returns. Idempotent: calling Stop multiple times
// is safe. Use during graceful shutdown to avoid a stray fire
// during teardown.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.triggered = false
}
