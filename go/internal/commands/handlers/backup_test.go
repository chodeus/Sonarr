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

var backupFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func TestBackup_Execute_LogsAndReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := handlers.NewBackup(logger)

	if err := h.Execute(t.Context(), `{}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "backup: stub Execute") {
		t.Errorf("expected stub log line; got: %q", logged)
	}
	if !strings.Contains(logged, "command=Backup") {
		t.Errorf("expected command=Backup attr; got: %q", logged)
	}
}

func TestBackup_NewWithNilLogger_Defaults(t *testing.T) {
	t.Parallel()
	h := handlers.NewBackup(nil)
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Errorf("Execute with default logger: %v", err)
	}
}

func TestBackup_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, backupFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
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
