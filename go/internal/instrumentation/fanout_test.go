package instrumentation

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// capturing is a minimal slog.Handler that records messages it handles.
type capturing struct {
	mu       sync.Mutex
	messages []string
	minLevel slog.Level
}

func (c *capturing) Enabled(_ context.Context, l slog.Level) bool { return l >= c.minLevel }
func (c *capturing) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, r.Message)
	return nil
}
func (c *capturing) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capturing) WithGroup(string) slog.Handler      { return c }
func (c *capturing) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

// emitMsg dispatches a record at the given level if the fanout is enabled
// for it (mirroring how slog.Logger gates on Enabled before Handle).
func emitMsg(t *testing.T, f *Fanout, level slog.Level, msg string) {
	t.Helper()
	if !f.Enabled(context.Background(), level) {
		return
	}
	rec := slog.NewRecord(time.Now(), level, msg, 0)
	if err := f.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func TestFanout_DispatchesToAll(t *testing.T) {
	a := &capturing{minLevel: slog.LevelInfo}
	b := &capturing{minLevel: slog.LevelInfo}
	f := NewFanout(a, b)
	emitMsg(t, f, slog.LevelWarn, "hello")
	if a.count() != 1 || b.count() != 1 {
		t.Fatalf("counts a=%d b=%d, want 1/1", a.count(), b.count())
	}
}

func TestFanout_AttachAddsHandler(t *testing.T) {
	a := &capturing{minLevel: slog.LevelInfo}
	f := NewFanout(a)
	emitMsg(t, f, slog.LevelInfo, "before")

	b := &capturing{minLevel: slog.LevelInfo}
	f.Attach(b)
	emitMsg(t, f, slog.LevelInfo, "after")

	if a.count() != 2 {
		t.Errorf("a saw %d, want 2 (both records)", a.count())
	}
	if b.count() != 1 {
		t.Errorf("b saw %d, want 1 (only post-attach)", b.count())
	}
}

func TestFanout_EnabledRespectsAnySubHandler(t *testing.T) {
	a := &capturing{minLevel: slog.LevelError}
	f := NewFanout(a)
	if f.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info should be disabled (only Error sub-handler)")
	}
	b := &capturing{minLevel: slog.LevelInfo}
	f.Attach(b)
	if !f.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info should be enabled after attaching an Info handler")
	}
}

// Per-sub-handler level gating: a record below one handler's threshold
// reaches only the handlers that accept it.
func TestFanout_PerHandlerLevelGate(t *testing.T) {
	info := &capturing{minLevel: slog.LevelInfo}
	errOnly := &capturing{minLevel: slog.LevelError}
	f := NewFanout(info, errOnly)
	emitMsg(t, f, slog.LevelInfo, "i")
	if info.count() != 1 {
		t.Errorf("info handler saw %d, want 1", info.count())
	}
	if errOnly.count() != 0 {
		t.Errorf("error-only handler saw %d, want 0 (Info below threshold)", errOnly.count())
	}
}

// WithAttrs derives a snapshot that still dispatches to current handlers.
func TestFanout_WithAttrsStillDispatches(t *testing.T) {
	a := &capturing{minLevel: slog.LevelInfo}
	f := NewFanout(a)
	derived, ok := f.WithAttrs([]slog.Attr{slog.String("k", "v")}).(*Fanout)
	if !ok {
		t.Fatal("WithAttrs should return *Fanout")
	}
	emitMsg(t, derived, slog.LevelInfo, "viaderived")
	if a.count() != 1 {
		t.Errorf("a saw %d, want 1 via derived handler", a.count())
	}
}
