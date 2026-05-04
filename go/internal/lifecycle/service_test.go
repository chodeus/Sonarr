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

func TestNewService_NilBusErrors(t *testing.T) {
	t.Parallel()
	if _, err := lifecycle.NewService(nil); err == nil {
		t.Error("NewService(nil) should error")
	}
}

func TestService_Shutdown_PublishesNonRestarting(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var (
		got      atomic.Int32
		captured atomic.Bool // captures Restarting flag from the received event
	)
	messaging.Subscribe(bus, "lifecycle.test.shutdown",
		func(_ context.Context, e lifecycle.ApplicationShutdownRequested) error {
			got.Add(1)
			captured.Store(e.Restarting)
			return nil
		})

	svc, err := lifecycle.NewService(bus)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 ApplicationShutdownRequested, got %d", got.Load())
	}
	if captured.Load() {
		t.Error("Shutdown(): Restarting flag should be false")
	}
	if svc.RestartPending() {
		t.Error("Shutdown() should not set RestartPending")
	}
}

func TestService_Restart_SetsFlagAndPublishesRestarting(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var (
		got        atomic.Int32
		restarting atomic.Bool
		// pendingDuringDeliver captures whether RestartPending() was
		// already true at the moment the subscriber ran. The C#
		// behaviour sets the flag BEFORE PublishEvent, so subscribers
		// that immediately query the flag see the correct value.
		pendingDuringDeliver atomic.Bool
	)
	svc, err := lifecycle.NewService(bus)
	if err != nil {
		t.Fatal(err)
	}
	messaging.Subscribe(bus, "lifecycle.test.restart",
		func(_ context.Context, e lifecycle.ApplicationShutdownRequested) error {
			got.Add(1)
			restarting.Store(e.Restarting)
			pendingDuringDeliver.Store(svc.RestartPending())
			return nil
		})

	if err := svc.Restart(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 ApplicationShutdownRequested, got %d", got.Load())
	}
	if !restarting.Load() {
		t.Error("Restart(): Restarting flag should be true")
	}
	if !svc.RestartPending() {
		t.Error("Restart() should set RestartPending")
	}
	if !pendingDuringDeliver.Load() {
		t.Error("RestartPending should be true when subscriber runs (set BEFORE Publish)")
	}
}

func TestService_Shutdown_PostShutdownIsErr(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.New(slog.DiscardHandler))
	svc, err := lifecycle.NewService(bus)
	if err != nil {
		t.Fatal(err)
	}
	bus.BeginShutdown()
	if err := svc.Shutdown(t.Context()); !errors.Is(err, messaging.ErrShutdown) {
		t.Errorf("Shutdown after BeginShutdown: %v, want ErrShutdown", err)
	}
}
