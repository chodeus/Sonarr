package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
)

type stubCleanupRunner struct {
	called int
	err    error
}

func (s *stubCleanupRunner) Cleanup(_ context.Context) error {
	s.called++
	return s.err
}

var cleanUpRecycleBinFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func TestCleanUpRecycleBin_Execute_NoRunner_LogsStubAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewCleanUpRecycleBin(nil, logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "cleanup_recycle_bin: stub Execute") {
		t.Errorf("expected stub log line; got: %q", logged)
	}
	if !strings.Contains(logged, "command=CleanUpRecycleBin") {
		t.Errorf("expected command=CleanUpRecycleBin attr; got: %q", logged)
	}
}

func TestCleanUpRecycleBin_Execute_DelegatesToRunner(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	runner := &stubCleanupRunner{}
	h := handlers.NewCleanUpRecycleBin(runner, logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.called != 1 {
		t.Errorf("runner.Cleanup called %d times, want 1", runner.called)
	}
	logged := buf.String()
	if !strings.Contains(logged, "delegating to recyclebin.Service.Cleanup") {
		t.Errorf("expected delegation log line; got: %q", logged)
	}
}

func TestCleanUpRecycleBin_Execute_RunnerErrorPropagates(t *testing.T) {
	t.Parallel()
	bottom := errors.New("recycle-bin not configured")
	runner := &stubCleanupRunner{err: bottom}
	h := handlers.NewCleanUpRecycleBin(runner, slog.Default())

	err := h.Execute(t.Context(), "")
	if !errors.Is(err, bottom) {
		t.Errorf("Execute err = %v, want it to wrap %v", err, bottom)
	}
}

func TestCleanUpRecycleBin_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewCleanUpRecycleBin(nil, nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

func TestCleanUpRecycleBin_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, cleanUpRecycleBinFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
		RescanSeries: noopRescanScanner{},
		Log:          slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.CleanUpRecycleBinName); !ok {
		t.Errorf("CleanUpRecycleBin not registered under name %q", handlers.CleanUpRecycleBinName)
	}
}

func TestCleanUpRecycleBin_DefaultScheduledTasks_Has24HourInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.CleanUpRecycleBinName {
			if task.Interval != 24*60 {
				t.Errorf("CleanUpRecycleBin Interval: %d minutes, want %d (24h)", task.Interval, 24*60)
			}
			return
		}
	}
	t.Errorf("CleanUpRecycleBin not in DefaultScheduledTasks")
}
