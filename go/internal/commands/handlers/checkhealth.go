// CheckHealth handler. Ports the IExecute<CheckHealthCommand> port at
// `src/NzbDrone.Core/HealthCheck/HealthCheckService.cs:137-150` —
// AS A STUB. The real port is deferred until the heavy
// HealthCheckService subsystem lands (the C# version is 230 LOC + 20+
// check implementations + a debouncer + an ApplicationStarted grace
// period). This slot ships only so:
//
//   - The third IExecute<T> handler exists, proving the registry +
//     scheduledtasks defaults pattern accepts new handlers without
//     touching the dispatcher core.
//   - The 6-hour scheduled-task entry materialises in the
//     ScheduledTasks table at boot, so when the real service lands
//     the row is already there — switching to the real Execute is
//     a code-only change.
//   - Operators who push `CheckHealth` via the API get a structured
//     "stub fired" log line instead of a "Unknown command" rejection.
//
// Body is ignored. The C# `CheckHealthCommand` carries a `Trigger`
// field (Manual vs Scheduled) that gates which check set runs; the
// stub doesn't differentiate.

package handlers

import (
	"context"
	"log/slog"
)

// CheckHealthName is the registry key + Commands.Name +
// ScheduledTasks.TypeName the scheduler pushes against. Mirrors the
// C# `Command.Name` short form — `CheckHealthCommand` → `CheckHealth`.
const CheckHealthName = "CheckHealth"

// CheckHealth is the IExecute<CheckHealthCommand> port stub.
type CheckHealth struct {
	log *slog.Logger
}

// NewCheckHealth builds the handler. log defaults to slog.Default.
func NewCheckHealth(log *slog.Logger) *CheckHealth {
	if log == nil {
		log = slog.Default()
	}
	return &CheckHealth{log: log}
}

// Execute logs that the command fired and returns nil. The real port
// will replace this with a delegation into a future
// healthcheck.Service.Run(ctx, trigger) call.
func (h *CheckHealth) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"healthcheck: stub Execute (real HealthCheckService not yet ported)",
		slog.String("command", CheckHealthName))
	return nil
}
