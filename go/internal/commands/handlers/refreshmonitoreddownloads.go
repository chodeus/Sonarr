// RefreshMonitoredDownloads handler. Ports the
// IExecute<RefreshMonitoredDownloadsCommand> implementation at
// `src/NzbDrone.Core/Download/TrackedDownloads/DownloadMonitoringService.cs`.
//
// Slice 5.J37 (audit B.3) replaces the original stub-shape (logged +
// returned nil) with a real delegate to
// [downloadmonitoring.Service.Refresh] when a [Refresher] is wired
// at Deps.MonitoredDownloadRefresher. When unset the handler falls
// back to the stub log line so hosts that haven't yet wired a
// refresher still satisfy the registry slot — important because the
// 1-minute scheduled-task entry depends on the registry key
// existing.
//
// The C# `RefreshMonitoredDownloadsCommand` is a bare `Command`
// subclass with no fields; the body string is ignored.

package handlers

import (
	"context"
	"log/slog"
)

// RefreshMonitoredDownloadsName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `RefreshMonitoredDownloadsCommand` → `RefreshMonitoredDownloads`.
const RefreshMonitoredDownloadsName = "RefreshMonitoredDownloads"

// Refresher is the narrow interface RefreshMonitoredDownloads
// delegates to. `*downloadmonitoring.Service` satisfies this via
// Go's structural typing — the handler doesn't import the
// downloadmonitoring package directly to avoid a cycle (the service
// imports trackeddownloads which transitively imports commands at
// the host wiring tier).
type Refresher interface {
	Refresh(ctx context.Context) (int, error)
}

// RefreshMonitoredDownloads is the IExecute<RefreshMonitoredDownloadsCommand>
// port.
type RefreshMonitoredDownloads struct {
	refresher Refresher
	log       *slog.Logger
}

// NewRefreshMonitoredDownloads builds the handler with an optional
// refresher. When refresher is nil, Execute logs the stub-shaped
// "command fired" line and returns nil (matches the pre-5.J37
// behaviour so hosts without a wired downloadmonitoring.Service
// still register the slot).
//
// log defaults to slog.Default.
func NewRefreshMonitoredDownloads(refresher Refresher, log *slog.Logger) *RefreshMonitoredDownloads {
	if log == nil {
		log = slog.Default()
	}
	return &RefreshMonitoredDownloads{refresher: refresher, log: log}
}

// Execute delegates to the configured Refresher when set; otherwise
// logs the stub fired-event line. Per-client poll failures are
// already absorbed by the Refresher (Refresh returns a non-fatal
// error after warn-logging each failure); the handler logs the
// summary error at Warn level but returns nil so the scheduler
// doesn't classify the command as failed (the C# tree's
// `IExecute<>` posture is the same — partial poll failures don't
// fail the command).
func (h *RefreshMonitoredDownloads) Execute(ctx context.Context, _ string) error {
	if h.refresher == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"refreshmonitoreddownloads: stub Execute (no DownloadMonitoringService wired)",
			slog.String("command", RefreshMonitoredDownloadsName))
		return nil
	}
	items, err := h.refresher.Refresh(ctx)
	if err != nil {
		h.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshmonitoreddownloads: Refresh completed with errors (per-client poll failure absorbed)",
			slog.String("command", RefreshMonitoredDownloadsName),
			slog.Int("items", items),
			slog.Any("err", err))
		return nil
	}
	h.log.LogAttrs(ctx, slog.LevelDebug,
		"refreshmonitoreddownloads: Refresh complete",
		slog.String("command", RefreshMonitoredDownloadsName),
		slog.Int("items", items))
	return nil
}
