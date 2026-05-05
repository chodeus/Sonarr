// CleanUpRecycleBin handler. Ports the IExecute<CleanUpRecycleBinCommand>
// implementation on RecycleBinProvider — AS A STUB. The real port
// (walk RecycleBin folder, delete files older than RecycleBinCleanupDays,
// remove empty sub-folders) depends on IDiskProvider + ConfigService,
// both Phase 4/5 territory. This slot ships so:
//
//   - The sixth IExecute<T> handler exists.
//   - The 24-hour scheduled-task entry materialises in the
//     ScheduledTasks table at boot (TaskManager.cs:108-113).
//   - API callers pushing `CleanUpRecycleBin` get a structured log
//     line instead of an "Unknown command" rejection.
//
// The C# `CleanUpRecycleBinCommand` is a bare `Command` subclass with
// no fields; the stub ignores the body entirely.

package handlers

import (
	"context"
	"log/slog"
)

// CleanUpRecycleBinName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `CleanUpRecycleBinCommand` → `CleanUpRecycleBin`.
const CleanUpRecycleBinName = "CleanUpRecycleBin"

// CleanUpRecycleBin is the IExecute<CleanUpRecycleBinCommand> port stub.
type CleanUpRecycleBin struct {
	log *slog.Logger
}

// NewCleanUpRecycleBin builds the handler. log defaults to slog.Default.
func NewCleanUpRecycleBin(log *slog.Logger) *CleanUpRecycleBin {
	if log == nil {
		log = slog.Default()
	}
	return &CleanUpRecycleBin{log: log}
}

// Execute logs that the command fired and returns nil. The real port
// will replace this with a delegation into a future
// recyclebin.Provider.Cleanup(ctx) call.
func (h *CleanUpRecycleBin) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"cleanup_recycle_bin: stub Execute (real RecycleBinProvider not yet ported)",
		slog.String("command", CleanUpRecycleBinName))
	return nil
}
