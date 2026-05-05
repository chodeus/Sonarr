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

var checkHealthFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func TestCheckHealth_Execute_LogsAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewCheckHealth(logger)

	if err := h.Execute(t.Context(), `{"some":"body"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "healthcheck: stub Execute") {
		t.Errorf("expected stub log line; got: %q", logged)
	}
	if !strings.Contains(logged, "command=CheckHealth") {
		t.Errorf("expected command=CheckHealth attr; got: %q", logged)
	}
}

func TestCheckHealth_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewCheckHealth(nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

// TestCheckHealth_RegisteredInRegistry verifies RegisterAll wires the
// handler under the canonical Commands.Name. Catches a regression in
// the registry-key convention (CheckHealth, not CheckHealthCommand).
func TestCheckHealth_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()

	// Construct a minimal Deps with the empty house service helper from
	// messagingcleanup_test.go.
	cmdSvc := newCleanupHarness(t, checkHealthFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		Log:          slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.CheckHealthName); !ok {
		t.Errorf("CheckHealth not registered under name %q", handlers.CheckHealthName)
	}
}

// TestCheckHealth_DefaultScheduledTasks_Has6HourInterval pins the
// scheduled-task interval against TaskManager.cs:96-99 (6h).
func TestCheckHealth_DefaultScheduledTasks_Has6HourInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.CheckHealthName {
			if task.Interval != 6*60 {
				t.Errorf("CheckHealth Interval: %d minutes, want %d (6h)", task.Interval, 6*60)
			}
			return
		}
	}
	t.Errorf("CheckHealth not in DefaultScheduledTasks")
}
