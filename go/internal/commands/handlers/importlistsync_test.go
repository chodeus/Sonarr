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
	"sonarr.tv/sonarr/internal/importlists"
)

var importListSyncFixedTime = time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

// J181 + J183: nil-runner falls back to the stub-log posture. Hosts
// that haven't yet wired the SyncService boot path still get the
// registry slot + scheduled-task entry; the per-tick Execute is a
// benign no-op + info line.
func TestImportListSync_NilRunner_LogsStub(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewImportListSync(nil, logger)

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
	h := handlers.NewImportListSync(nil, nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

// fakeRunner stubs the ImportListSyncRunner interface so the
// handler tests can drive the real-runner path without pulling the
// full importlists.SyncService construction chain into the test
// graph. The real *importlists.SyncService satisfies
// ImportListSyncRunner structurally via its existing
// Sync(ctx) (SyncResult, error) method.
type fakeRunner struct {
	result importlists.SyncResult
	err    error
	calls  int
}

func (f *fakeRunner) Sync(_ context.Context) (importlists.SyncResult, error) {
	f.calls++
	return f.result, f.err
}

// J183: when a runner is wired, Execute delegates to it and logs
// the result summary. The handler still returns nil (the scheduler
// must keep firing even on partial-failure ticks — matching the C#
// scheduled-task try/catch posture).
func TestImportListSync_RealRunner_DelegatesAndLogsResult(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	runner := &fakeRunner{result: importlists.SyncResult{
		ItemsFetched:        15,
		SeriesAdded:         3,
		SkippedExcluded:     2,
		SkippedExisting:     8,
		SkippedUnresolvedID: 2,
		AnyFailure:          false,
	}}
	h := handlers.NewImportListSync(runner, logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.calls != 1 {
		t.Errorf("runner.Sync calls = %d, want 1", runner.calls)
	}
	logged := buf.String()
	if !strings.Contains(logged, "importlistsync: Sync complete") {
		t.Errorf("expected real-runner log line; got: %q", logged)
	}
	if !strings.Contains(logged, "itemsFetched=15") || !strings.Contains(logged, "seriesAdded=3") {
		t.Errorf("expected result summary in log; got: %q", logged)
	}
}

// J183: Sync error degrades to a warn log + nil return. The
// scheduler must keep firing on subsequent intervals — matching the
// C# scheduled-task try/catch posture which swallows per-tick
// exceptions to keep the job pipeline alive.
func TestImportListSync_RealRunner_ErrorDegradesToWarn(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := &fakeRunner{err: errors.New("upstream borked")}
	h := handlers.NewImportListSync(runner, logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Errorf("Execute should return nil on Sync error, got: %v", err)
	}
	if runner.calls != 1 {
		t.Errorf("runner.Sync calls = %d, want 1", runner.calls)
	}
	logged := buf.String()
	if !strings.Contains(logged, "importlistsync: Sync completed with errors") {
		t.Errorf("expected warn log on error; got: %q", logged)
	}
	if !strings.Contains(logged, "upstream borked") {
		t.Errorf("expected error in log; got: %q", logged)
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
