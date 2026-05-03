package lifecycle_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/messaging"
)

func TestEvents_AreLifecycle(t *testing.T) {
	t.Parallel()
	events := []messaging.Lifecycle{
		lifecycle.ApplicationStartingEvent{},
		lifecycle.ApplicationStartedEvent{},
		lifecycle.ApplicationShutdownRequested{},
	}
	for _, e := range events {
		if !e.Lifecycle() {
			t.Errorf("%T.Lifecycle() = false, want true", e)
		}
	}
}

// TestStartedEvent_FiresAndShutdownBlocks confirms the events flow
// through messaging.Publish on the happy path and are blocked once
// BeginShutdown has been called — the integration that justifies
// tagging them as Lifecycle.
func TestStartedEvent_FiresAndShutdownBlocks(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.New(slog.DiscardHandler))
	var startedCalls int64
	messaging.Subscribe(bus, "lifecycle.test.started",
		func(_ context.Context, _ lifecycle.ApplicationStartedEvent) error {
			atomic.AddInt64(&startedCalls, 1)
			return nil
		})
	if err := messaging.Publish(t.Context(), bus, lifecycle.ApplicationStartedEvent{}); err != nil {
		t.Fatalf("Publish (pre-shutdown): %v", err)
	}
	if got := atomic.LoadInt64(&startedCalls); got != 1 {
		t.Errorf("started fires=%d, want 1", got)
	}
	bus.BeginShutdown()
	err := messaging.Publish(t.Context(), bus, lifecycle.ApplicationStartedEvent{})
	if !errors.Is(err, messaging.ErrShutdown) {
		t.Errorf("post-shutdown Publish: %v, want ErrShutdown", err)
	}
	// And shutdown-requested itself is blocked once shutdown has
	// already begun (subsequent shutdown signals are no-ops).
	if err := messaging.Publish(t.Context(), bus, lifecycle.ApplicationShutdownRequested{Restarting: true}); !errors.Is(err, messaging.ErrShutdown) {
		t.Errorf("post-shutdown Shutdown Publish: %v, want ErrShutdown", err)
	}
}
