package handlers_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
)

var cleanUpRecycleBinFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func TestCleanUpRecycleBin_Execute_LogsAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewCleanUpRecycleBin(logger)

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

func TestCleanUpRecycleBin_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewCleanUpRecycleBin(nil)
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
