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

var importListSyncFixedTime = time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

// J181: stub handler logs at info + returns nil. Mirrors the
// RefreshSeries stub pattern. Replaces the "Unknown command" rejection
// the dispatcher would otherwise give API callers pushing
// `{"name":"ImportListSync"}`.
func TestImportListSync_Execute_LogsAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewImportListSync(logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "importlistsync: stub Execute") {
		t.Errorf("expected stub log line; got: %q", logged)
	}
	if !strings.Contains(logged, "command=ImportListSync") {
		t.Errorf("expected command=ImportListSync attr; got: %q", logged)
	}
}

func TestImportListSync_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewImportListSync(nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

// J181: RegisterAll registers the ImportListSync slot — no Deps
// field needed because the stub is log-only. API callers pushing
// `{"name":"ImportListSync"}` now hit the registered handler instead
// of the "Unknown command" rejection.
func TestImportListSync_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, importListSyncFixedTime).svc
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
	if _, ok := registry.Lookup(handlers.ImportListSyncName); !ok {
		t.Errorf("ImportListSync not registered under name %q", handlers.ImportListSyncName)
	}
}

// J181: ImportListSync is in DefaultScheduledTasks at the 5-minute
// cadence the C# `TaskManager.cs:118-122` `ImportListSyncCommand`
// entry runs. Closes the v3.system.tasks "missing Import List Sync"
// half of the task-set drift.
func TestImportListSync_DefaultScheduledTasks_HasFiveMinuteInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.ImportListSyncName {
			if task.Interval != handlers.ImportListSyncIntervalDefault {
				t.Errorf("ImportListSync Interval: %d minutes, want %d", task.Interval, handlers.ImportListSyncIntervalDefault)
			}
			if task.Interval != 5 {
				t.Errorf("ImportListSync Interval: %d minutes, want 5 (C# TaskManager.cs:120)", task.Interval)
			}
			return
		}
	}
	t.Errorf("ImportListSync not in DefaultScheduledTasks")
}

// J181: CheckForFinishedDownload is now ABSENT from
// DefaultScheduledTasks — the slice 4.Z scheduled-task entry was
// based on a misread C# cite (TaskManager.cs:74 is the Priority
// field of RefreshMonitoredDownloads, not a separate task). The
// handler stays registered so API callers can still push the
// deprecated alias; only the scheduler stops auto-firing it.
func TestCheckForFinishedDownload_NotInDefaultScheduledTasks(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.CheckForFinishedDownloadsName {
			t.Errorf("CheckForFinishedDownload should NOT be in DefaultScheduledTasks (C# TaskManager.cs doesn't schedule it — :74 is the Priority field of RefreshMonitoredDownloads); slice 5.J181 removed the entry")
			return
		}
	}
}
