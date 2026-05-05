// Backup handler. Ports the IExecute<BackupCommand> implementation —
// AS A STUB. The real port (config+DB tarball with rotation) is
// deferred until the Backup subsystem Phase 3 slice lands. This slot
// ships so:
//
//   - The fifth IExecute<T> handler exists.
//   - The scheduled-task entry materialises in the ScheduledTasks
//     table at boot. Interval is config-driven in the C# tree
//     (BackupInterval defaults to 7 days; GetBackupInterval at
//     TaskManager.cs:168 clamps to [1,7] days and converts to
//     minutes). The stub seeds the default 7-day value; when the
//     real ConfigService lands, HandleAsync(ConfigSavedEvent) will
//     update the interval live.
//   - API callers pushing `Backup` get a structured log line instead
//     of an "Unknown command" rejection.
//
// The C# `BackupCommand` is a bare `Command` subclass with no fields;
// the stub ignores the body entirely.

package handlers

import (
	"context"
	"log/slog"
)

// BackupName is the registry key + Commands.Name + ScheduledTasks.TypeName.
// Mirrors the C# short form: `BackupCommand` → `Backup`.
const BackupName = "Backup"

// BackupIntervalDefault is the seed interval used at boot before the
// live ConfigService is available. Mirrors ConfigService.BackupInterval
// default of 7 days (TaskManager.cs:168-182).
const BackupIntervalDefault = 7 * 24 * 60 // minutes

// Backup is the IExecute<BackupCommand> port stub.
type Backup struct {
	log *slog.Logger
}

// NewBackup builds the handler. log defaults to slog.Default.
func NewBackup(log *slog.Logger) *Backup {
	if log == nil {
		log = slog.Default()
	}
	return &Backup{log: log}
}

// Execute logs that the command fired and returns nil. The real port
// will replace this with a delegation into a future
// backup.Service.Backup(ctx) call.
func (h *Backup) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"backup: stub Execute (real BackupService not yet ported)",
		slog.String("command", BackupName))
	return nil
}
