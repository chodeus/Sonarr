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

var refreshMonitoredDownloadsFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func TestRefreshMonitoredDownloads_Execute_LogsAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewRefreshMonitoredDownloads(logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "refreshmonitoreddownloads: stub Execute") {
		t.Errorf("expected stub log line; got: %q", logged)
	}
	if !strings.Contains(logged, "command=RefreshMonitoredDownloads") {
		t.Errorf("expected command=RefreshMonitoredDownloads attr; got: %q", logged)
	}
}

func TestRefreshMonitoredDownloads_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewRefreshMonitoredDownloads(nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

func TestRefreshMonitoredDownloads_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, refreshMonitoredDownloadsFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		Log:          slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.RefreshMonitoredDownloadsName); !ok {
		t.Errorf("RefreshMonitoredDownloads not registered under name %q", handlers.RefreshMonitoredDownloadsName)
	}
}

func TestRefreshMonitoredDownloads_DefaultScheduledTasks_Has1MinInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.RefreshMonitoredDownloadsName {
			if task.Interval != 1 {
				t.Errorf("RefreshMonitoredDownloads Interval: %d minutes, want 1", task.Interval)
			}
			return
		}
	}
	t.Errorf("RefreshMonitoredDownloads not in DefaultScheduledTasks")
}
