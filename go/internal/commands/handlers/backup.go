// Backup handler. Ports the IExecute<BackupCommand> implementation.
// As of slice 3.BN (queue [16] sub-arc 16b) this delegates to the
// real *backup.Service.Backup port that shipped in slice 3.BM.
//
// The C# `BackupCommand.Type` getter at
// `Backup/BackupCommand.cs:7-18` derives the BackupType from
// CommandTrigger (Scheduled / Manual). The Go-port dispatcher's
// Handler signature only carries the JSON body, not the Trigger
// — so the handler parses an optional `{"type":"manual"}` field
// from the body. Scheduler-triggered backups omit the field and
// default to TypeScheduled (which preserves the C# scheduled-only
// pruning gate at BackupService.cs:85-88).

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/backup"
)

// BackupName is the registry key + Commands.Name + ScheduledTasks.TypeName.
// Mirrors the C# short form: `BackupCommand` → `Backup`.
const BackupName = "Backup"

// BackupIntervalDefault is the seed interval used at boot before the
// live ConfigService is available. Mirrors ConfigService.BackupInterval
// default of 7 days (TaskManager.cs:168-182).
const BackupIntervalDefault = 7 * 24 * 60 // minutes

// BackupRunner narrows [*backup.Service] to the one method the handler
// reaches for. Defined locally so tests can stub the backup pipeline.
type BackupRunner interface {
	Backup(ctx context.Context, t backup.Type) (string, error)
}

// Backup is the IExecute<BackupCommand> port.
type Backup struct {
	svc BackupRunner
	log *slog.Logger
}

// NewBackup builds the handler. svc is required (slice 3.BN+); nil
// returns an error so the boot site fails loudly. log defaults to
// slog.Default.
func NewBackup(svc BackupRunner, log *slog.Logger) (*Backup, error) {
	if svc == nil {
		return nil, errors.New("commands.handlers.NewBackup: nil backup runner")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Backup{svc: svc, log: log}, nil
}

// backupBody is the JSON body shape this handler accepts. The C#
// BackupCommand has no fields; the Go-port adds an optional `type`
// override since the dispatcher doesn't surface CommandTrigger to
// handlers.
type backupBody struct {
	Type string `json:"type,omitempty"`
}

// Execute parses the body for an optional type override (default
// "scheduled") and delegates to [BackupRunner.Backup]. Returns the
// underlying error so the dispatcher persists it as Status=Failed.
func (h *Backup) Execute(ctx context.Context, body string) error {
	t := backup.TypeScheduled
	if strings.TrimSpace(body) != "" && body != "{}" {
		var parsed backupBody
		if err := json.Unmarshal([]byte(body), &parsed); err == nil {
			t = parseBackupType(parsed.Type)
		}
	}
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"backup: starting",
		slog.String("command", BackupName),
		slog.String("type", t.String()))
	path, err := h.svc.Backup(ctx, t)
	if err != nil {
		return err
	}
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"backup: complete",
		slog.String("command", BackupName),
		slog.String("path", path))
	return nil
}

// backupStub is the pre-3.BN log-only handler retained as a fallback
// for hosts that haven't yet wired a [backup.Service]. The real
// handler is the [Backup] type above.
type backupStub struct{ log *slog.Logger }

func newBackupStub(log *slog.Logger) *backupStub {
	if log == nil {
		log = slog.Default()
	}
	return &backupStub{log: log}
}

// Execute logs the firing event and returns nil. The dispatcher's
// scheduled-task tick still records the run for telemetry.
func (h *backupStub) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"backup: stub Execute (no backup.Service wired)",
		slog.String("command", BackupName))
	return nil
}

// parseBackupType maps the body-side string into the typed enum.
// Unknown / empty defaults to Scheduled.
func parseBackupType(s string) backup.Type {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "manual":
		return backup.TypeManual
	case "update":
		return backup.TypeUpdate
	default:
		return backup.TypeScheduled
	}
}
