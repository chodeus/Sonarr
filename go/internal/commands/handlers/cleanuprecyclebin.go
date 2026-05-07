// CleanUpRecycleBin handler. Ports the IExecute<CleanUpRecycleBinCommand>
// implementation on RecycleBinProvider. Slice 3.77 wires the handler
// to the real `recyclebin.Service.Cleanup` (ported in the same slice)
// via an optional [CleanupRunner] dep — when nil the handler falls
// back to the original stub-shaped log line so the registry slot
// continues to materialise the 24-hour scheduled-task entry even
// before production wiring composes a recyclebin Service.
//
// The C# `CleanUpRecycleBinCommand` is a bare `Command` subclass with
// no fields; the handler ignores the body.

package handlers

import (
	"context"
	"log/slog"
)

// CleanupRunner narrows `recyclebin.Service.Cleanup(ctx) error` —
// the one method this handler delegates to. Defined locally so the
// commands/handlers package doesn't import internal/recyclebin
// (avoids a cycle once the recyclebin package starts importing
// command-bus types for its IHandle<MediaCoversUpdatedEvent> family).
//
// Production binding wires `*recyclebin.Service` directly via Go's
// structural typing.
type CleanupRunner interface {
	Cleanup(ctx context.Context) error
}

// CleanUpRecycleBinName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `CleanUpRecycleBinCommand` → `CleanUpRecycleBin`.
const CleanUpRecycleBinName = "CleanUpRecycleBin"

// CleanUpRecycleBin is the IExecute<CleanUpRecycleBinCommand> port.
// Holds an optional [CleanupRunner] — when nil the Execute method
// logs at info-level and returns nil (the original 3.13-era stub
// shape). When non-nil, Execute delegates to runner.Cleanup(ctx).
type CleanUpRecycleBin struct {
	runner CleanupRunner
	log    *slog.Logger
}

// NewCleanUpRecycleBin builds the handler. runner is optional — pass
// nil to keep the original stub behaviour (log + return nil) for
// hosts that haven't yet wired a recyclebin.Service. log defaults to
// slog.Default when nil.
//
// The two-arg signature lets RegisterAll thread a recyclebin.Service
// through Deps when production composes it, without breaking the
// existing tests that pass log only.
func NewCleanUpRecycleBin(runner CleanupRunner, log *slog.Logger) *CleanUpRecycleBin {
	if log == nil {
		log = slog.Default()
	}
	return &CleanUpRecycleBin{runner: runner, log: log}
}

// Execute delegates to [CleanupRunner.Cleanup] when wired; otherwise
// logs the stub-shaped line for hosts that haven't yet wired a real
// recyclebin Service. Errors from the runner propagate so the
// dispatcher can mark the command failed.
func (h *CleanUpRecycleBin) Execute(ctx context.Context, _ string) error {
	if h.runner == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"cleanup_recycle_bin: stub Execute (no CleanupRunner wired)",
			slog.String("command", CleanUpRecycleBinName))
		return nil
	}
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"cleanup_recycle_bin: delegating to recyclebin.Service.Cleanup",
		slog.String("command", CleanUpRecycleBinName))
	return h.runner.Cleanup(ctx)
}
