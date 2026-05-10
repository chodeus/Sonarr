package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
)

var lifecycleFixedTime = time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

type stubLifecycle struct {
	shutdownCalls int
	restartCalls  int
	shutdownErr   error
	restartErr    error
}

func (s *stubLifecycle) Shutdown(_ context.Context) error {
	s.shutdownCalls++
	return s.shutdownErr
}
func (s *stubLifecycle) Restart(_ context.Context) error {
	s.restartCalls++
	return s.restartErr
}

func TestNewShutdown_NilSvc(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewShutdown(nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestShutdown_Execute_Delegates(t *testing.T) {
	t.Parallel()
	svc := &stubLifecycle{}
	h, err := handlers.NewShutdown(svc, nil)
	if err != nil {
		t.Fatalf("NewShutdown: %v", err)
	}
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if svc.shutdownCalls != 1 {
		t.Errorf("shutdown calls: %d want 1", svc.shutdownCalls)
	}
}

func TestShutdown_Execute_PropagatesError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	svc := &stubLifecycle{shutdownErr: want}
	h, _ := handlers.NewShutdown(svc, nil)
	if err := h.Execute(t.Context(), ""); !errors.Is(err, want) {
		t.Errorf("want propagated err, got %v", err)
	}
}

func TestNewRestart_NilSvc(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewRestart(nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestRestart_Execute_Delegates(t *testing.T) {
	t.Parallel()
	svc := &stubLifecycle{}
	h, err := handlers.NewRestart(svc, nil)
	if err != nil {
		t.Fatalf("NewRestart: %v", err)
	}
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if svc.restartCalls != 1 {
		t.Errorf("restart calls: %d want 1", svc.restartCalls)
	}
}

func TestRestart_Execute_PropagatesError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	svc := &stubLifecycle{restartErr: want}
	h, _ := handlers.NewRestart(svc, nil)
	if err := h.Execute(t.Context(), ""); !errors.Is(err, want) {
		t.Errorf("want propagated err, got %v", err)
	}
}

func TestRegisterAll_LifecycleNilSkipsRegistration(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, lifecycleFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
		RescanSeries: noopRescanScanner{},
		// Lifecycle nil — Shutdown/Restart NOT registered.
		Log: slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.ShutdownName); ok {
		t.Errorf("Shutdown should NOT be registered when Deps.Lifecycle is nil")
	}
	if _, ok := registry.Lookup(handlers.RestartName); ok {
		t.Errorf("Restart should NOT be registered when Deps.Lifecycle is nil")
	}
}

func TestRegisterAll_LifecycleWiredRegistersBoth(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, lifecycleFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
		RescanSeries: noopRescanScanner{},
		Lifecycle:    &stubLifecycle{},
		Log:          slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.ShutdownName); !ok {
		t.Errorf("Shutdown not registered with non-nil Lifecycle")
	}
	if _, ok := registry.Lookup(handlers.RestartName); !ok {
		t.Errorf("Restart not registered with non-nil Lifecycle")
	}
}
