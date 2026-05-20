// CheckForFinishedDownloads handler. Ports the deprecated
// IExecute<CheckForFinishedDownloadCommand> implementation at
// `src/NzbDrone.Core/Download/TrackedDownloads/DownloadMonitoringService.cs:166-170`.
//
// In the C# tree (post-deprecation) this command is an alias for
// RefreshMonitoredDownloads with a warn log nudging third-party
// callers to migrate. The Go port matches that posture — slice 5.J38
// delegates to the same [Refresher] interface as
// RefreshMonitoredDownloads (`*downloadmonitoring.Service`).
//
// When no Refresher is wired the handler falls back to the original
// stub-shaped log so hosts that haven't yet wired a refresher still
// satisfy the registry slot — the 1-minute scheduled-task entry
// depends on the registry key existing.

package handlers

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// CheckForFinishedDownloadsName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form
// `CheckForFinishedDownloadCommand` → `CheckForFinishedDownload` (the
// scheduled-task name uses the singular form per
// `TaskManager.cs:74`).
const CheckForFinishedDownloadsName = "CheckForFinishedDownload"

// CheckForFinishedDownloadsIntervalDefault is the 1-minute scheduler
// cadence. Matches C# `TaskManager.cs:74`. Hardcoded here pending the
// config getter port (same idiom as RssSync slice 4.Q).
const CheckForFinishedDownloadsIntervalDefault = 1

// CheckForFinishedDownloads is the IExecute<CheckForFinishedDownloadCommand>
// port. Delegates to the configured [Refresher] (same instance as
// RefreshMonitoredDownloads) with a deprecation warn-log per call.
type CheckForFinishedDownloads struct {
	refresher Refresher
	log       *slog.Logger
}

// NewCheckForFinishedDownloads builds the handler with an optional
// refresher. When refresher is nil, Execute logs the stub-shaped
// "command fired" line and returns nil (matches the pre-5.J38
// behaviour so hosts without a wired downloadmonitoring.Service
// still register the slot).
//
// log defaults to slog.Default.
func NewCheckForFinishedDownloads(refresher Refresher, log *slog.Logger) *CheckForFinishedDownloads {
	if log == nil {
		log = slog.Default()
	}
	return &CheckForFinishedDownloads{refresher: refresher, log: log}
}

// Execute logs a deprecation warning (matches C# `:168`) and
// delegates to the Refresher. When no Refresher is wired, falls
// back to the stub fired-event log.
func (h *CheckForFinishedDownloads) Execute(ctx context.Context, _ string) error {
	if h.refresher == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"checkforfinisheddownloads: stub Execute (no DownloadMonitoringService wired)",
			slog.String("command", CheckForFinishedDownloadsName))
		return nil
	}
	// Mirrors C# `_logger.Warn("A third party app used the deprecated
	// CheckForFinishedDownload command, it should be updated
	// RefreshMonitoredDownloads instead");` at `:168`.
	h.log.LogAttrs(ctx, slog.LevelWarn,
		"checkforfinisheddownloads: deprecated command — use RefreshMonitoredDownloads instead",
		slog.String("command", CheckForFinishedDownloadsName))
	items, err := h.refresher.Refresh(ctx)
	if err != nil {
		h.log.LogAttrs(ctx, slog.LevelWarn,
			"checkforfinisheddownloads: Refresh completed with errors (per-client poll failure absorbed)",
			slog.String("command", CheckForFinishedDownloadsName),
			slog.Int("items", items),
			slog.Any("err", err))
		return nil
	}
	h.log.LogAttrs(ctx, slog.LevelDebug,
		"checkforfinisheddownloads: Refresh complete",
		slog.String("command", CheckForFinishedDownloadsName),
		slog.Int("items", items))
	return nil
}

// checkForFinishedDownloadsDefaultTask returns the
// DefaultScheduledTasks entry for CheckForFinishedDownloads. Hoisted
// into a function so the constants stay co-located with the rest of
// this file.
func checkForFinishedDownloadsDefaultTask() *scheduledtasks.Task {
	return &scheduledtasks.Task{
		TypeName: CheckForFinishedDownloadsName,
		Interval: CheckForFinishedDownloadsIntervalDefault,
	}
}
