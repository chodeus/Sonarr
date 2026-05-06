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

var refreshSeriesFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func TestRefreshSeries_Execute_LogsAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewRefreshSeries(logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "refreshseries: stub Execute") {
		t.Errorf("expected stub log line; got: %q", logged)
	}
	if !strings.Contains(logged, "command=RefreshSeries") {
		t.Errorf("expected command=RefreshSeries attr; got: %q", logged)
	}
}

func TestRefreshSeries_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewRefreshSeries(nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

func TestRefreshSeries_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, refreshSeriesFixedTime).svc
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
	if _, ok := registry.Lookup(handlers.RefreshSeriesName); !ok {
		t.Errorf("RefreshSeries not registered under name %q", handlers.RefreshSeriesName)
	}
}

func TestRefreshSeries_DefaultScheduledTasks_Has12HourInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.RefreshSeriesName {
			if task.Interval != 12*60 {
				t.Errorf("RefreshSeries Interval: %d minutes, want %d (12h)", task.Interval, 12*60)
			}
			return
		}
	}
	t.Errorf("RefreshSeries not in DefaultScheduledTasks")
}
