package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/backup"
	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
)

var backupFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

type stubBackupRunner struct {
	calls   int
	gotType backup.Type
	path    string
	err     error
}

func (s *stubBackupRunner) Backup(_ context.Context, t backup.Type) (string, error) {
	s.calls++
	s.gotType = t
	return s.path, s.err
}

func TestNewBackup_NilSvc(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewBackup(nil, nil); err == nil {
		t.Fatal("want error for nil runner")
	}
}

func TestBackup_Execute_DefaultsToScheduled(t *testing.T) {
	t.Parallel()
	runner := &stubBackupRunner{path: "/x.zip"}
	h, err := handlers.NewBackup(runner, nil)
	if err != nil {
		t.Fatalf("NewBackup: %v", err)
	}
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.calls != 1 {
		t.Errorf("calls: %d want 1", runner.calls)
	}
	if runner.gotType != backup.TypeScheduled {
		t.Errorf("default type: %v want Scheduled", runner.gotType)
	}
}

func TestBackup_Execute_EmptyBraces(t *testing.T) {
	t.Parallel()
	runner := &stubBackupRunner{path: "/x.zip"}
	h, _ := handlers.NewBackup(runner, nil)
	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.gotType != backup.TypeScheduled {
		t.Errorf("type: %v", runner.gotType)
	}
}

func TestBackup_Execute_ManualOverride(t *testing.T) {
	t.Parallel()
	runner := &stubBackupRunner{path: "/x.zip"}
	h, _ := handlers.NewBackup(runner, nil)
	if err := h.Execute(t.Context(), `{"type":"manual"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.gotType != backup.TypeManual {
		t.Errorf("type: %v want Manual", runner.gotType)
	}
}

func TestBackup_Execute_UpdateOverride(t *testing.T) {
	t.Parallel()
	runner := &stubBackupRunner{path: "/x.zip"}
	h, _ := handlers.NewBackup(runner, nil)
	if err := h.Execute(t.Context(), `{"type":"Update"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.gotType != backup.TypeUpdate {
		t.Errorf("type: %v want Update", runner.gotType)
	}
}

func TestBackup_Execute_UnknownTypeFallsBackToScheduled(t *testing.T) {
	t.Parallel()
	runner := &stubBackupRunner{path: "/x.zip"}
	h, _ := handlers.NewBackup(runner, nil)
	if err := h.Execute(t.Context(), `{"type":"weird"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.gotType != backup.TypeScheduled {
		t.Errorf("type: %v want Scheduled", runner.gotType)
	}
}

func TestBackup_Execute_BadJSONFallsBackToScheduled(t *testing.T) {
	t.Parallel()
	runner := &stubBackupRunner{path: "/x.zip"}
	h, _ := handlers.NewBackup(runner, nil)
	// Invalid JSON shouldn't fail the handler — it just falls through.
	if err := h.Execute(t.Context(), `{not json`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.gotType != backup.TypeScheduled {
		t.Errorf("type: %v want Scheduled", runner.gotType)
	}
}

func TestBackup_Execute_PropagatesError(t *testing.T) {
	t.Parallel()
	want := errors.New("disk full")
	runner := &stubBackupRunner{err: want}
	h, _ := handlers.NewBackup(runner, nil)
	if err := h.Execute(t.Context(), ""); !errors.Is(err, want) {
		t.Errorf("want propagated err, got %v", err)
	}
}

func TestBackup_RegisteredInRegistry_WithStub(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, backupFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
		RescanSeries: noopRescanScanner{},
		// Backup nil — falls back to log-only stub.
		Log: slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.BackupName); !ok {
		t.Errorf("Backup not registered under name %q (stub fallback)", handlers.BackupName)
	}
}

func TestBackup_RegisteredInRegistry_WithService(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, backupFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
		RescanSeries: noopRescanScanner{},
		Backup:       &stubBackupRunner{},
		Log:          slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.BackupName); !ok {
		t.Errorf("Backup not registered under name %q", handlers.BackupName)
	}
}

func TestBackup_DefaultScheduledTasks_Has7DayInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.BackupName {
			if task.Interval != handlers.BackupIntervalDefault {
				t.Errorf("Backup Interval: %d minutes, want %d (7 days)", task.Interval, handlers.BackupIntervalDefault)
			}
			return
		}
	}
	t.Errorf("Backup not in DefaultScheduledTasks")
}
