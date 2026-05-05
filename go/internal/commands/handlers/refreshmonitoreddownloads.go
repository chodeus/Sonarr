// RefreshMonitoredDownloads handler. Ports the
// IExecute<RefreshMonitoredDownloadsCommand> implementation — AS A STUB.
// The real port depends on Phase 4 download-client integration. This
// slot ships so:
//
//   - The fifth IExecute<T> handler exists.
//   - The 1-minute scheduled-task entry materialises in the
//     ScheduledTasks table at boot (TaskManager.cs:71 — highest-frequency
//     scheduled task; drives the "Downloading" queue refresh).
//   - API callers pushing `RefreshMonitoredDownloads` get a structured
//     log line instead of an "Unknown command" rejection.
//
// The C# `RefreshMonitoredDownloadsCommand` is a bare `Command`
// subclass with no fields; the stub ignores the body entirely.

package handlers

import (
	"context"
	"log/slog"
)

// RefreshMonitoredDownloadsName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `RefreshMonitoredDownloadsCommand` → `RefreshMonitoredDownloads`.
const RefreshMonitoredDownloadsName = "RefreshMonitoredDownloads"

// RefreshMonitoredDownloads is the IExecute<RefreshMonitoredDownloadsCommand>
// port stub.
type RefreshMonitoredDownloads struct {
	log *slog.Logger
}

// NewRefreshMonitoredDownloads builds the handler. log defaults to
// slog.Default.
func NewRefreshMonitoredDownloads(log *slog.Logger) *RefreshMonitoredDownloads {
	if log == nil {
		log = slog.Default()
	}
	return &RefreshMonitoredDownloads{log: log}
}

// Execute logs that the command fired and returns nil. The real port
// will replace this with a delegation into a future
// downloadmonitoring.Service.Execute(ctx) call.
func (h *RefreshMonitoredDownloads) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"refreshmonitoreddownloads: stub Execute (real DownloadMonitoringService not yet ported)",
		slog.String("command", RefreshMonitoredDownloadsName))
	return nil
}
