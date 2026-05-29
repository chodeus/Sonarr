package instrumentation

import (
	"context"
	"log/slog"
	"sync"
)

// Fanout is an slog.Handler that dispatches each record to every
// registered sub-handler, and—crucially—supports attaching a new
// sub-handler at runtime via [Fanout.Attach]. This mirrors the C#
// pattern where the NLog `DatabaseTarget` is registered dynamically
// (`DatabaseTarget.Register`) once the log database is available, well
// after the console/file targets are already running: the Go process
// builds its logger at startup (before any DB is open) and attaches the
// DB sink later, once the RoleLog datastore is migrated.
//
// Concurrency: the handler slice is read on every record and mutated by
// Attach. Attach is copy-on-write under a write lock, so readers take a
// brief read lock to grab the current slice and never observe a partial
// mutation.
//
// WithAttrs / WithGroup return a SNAPSHOT Fanout whose sub-handlers are
// derived from the handlers present at derivation time. A handler later
// Attach()ed to the ROOT Fanout therefore does NOT propagate into
// already-derived loggers — acceptable because the only runtime Attach
// (the DB sink) happens during boot, before the services that derive
// loggers via Logger.With(...) start logging. Early-boot logs (before
// the log DB exists) can't reach the DB anyway, matching the C# target's
// post-availability registration.
type Fanout struct {
	mu       *sync.RWMutex
	handlers []slog.Handler
}

// NewFanout builds a Fanout over the given handlers.
func NewFanout(handlers ...slog.Handler) *Fanout {
	return &Fanout{mu: &sync.RWMutex{}, handlers: handlers}
}

// Attach adds a sub-handler to the (root) Fanout at runtime.
func (f *Fanout) Attach(h slog.Handler) {
	if h == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Copy-on-write: readers holding the old slice are unaffected.
	next := make([]slog.Handler, len(f.handlers)+1)
	copy(next, f.handlers)
	next[len(f.handlers)] = h
	f.handlers = next
}

func (f *Fanout) snapshot() []slog.Handler {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.handlers
}

// Enabled reports whether any sub-handler is enabled for the level.
func (f *Fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.snapshot() {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle dispatches the record to every enabled sub-handler. Each gets a
// clone so a handler that mutates the record (AddAttrs, etc.) can't
// corrupt another's view. The first non-nil error is returned, after all
// handlers have run.
func (f *Fanout) Handle(ctx context.Context, r slog.Record) error {
	var first error
	for _, h := range f.snapshot() {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// WithAttrs returns a snapshot Fanout whose sub-handlers carry attrs.
func (f *Fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return f
	}
	hs := f.snapshot()
	derived := make([]slog.Handler, len(hs))
	for i, h := range hs {
		derived[i] = h.WithAttrs(attrs)
	}
	return &Fanout{mu: &sync.RWMutex{}, handlers: derived}
}

// WithGroup returns a snapshot Fanout whose sub-handlers carry the group.
func (f *Fanout) WithGroup(name string) slog.Handler {
	if name == "" {
		return f
	}
	hs := f.snapshot()
	derived := make([]slog.Handler, len(hs))
	for i, h := range hs {
		derived[i] = h.WithGroup(name)
	}
	return &Fanout{mu: &sync.RWMutex{}, handlers: derived}
}
