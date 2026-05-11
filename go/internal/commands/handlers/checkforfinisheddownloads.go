// CheckForFinishedDownloads handler. Ports the
// IExecute<CheckForFinishedDownloadCommand> implementation — AS A STUB.
//
// Slice 4.Z (queue [15]). The real-world job in C# at
// `src/NzbDrone.Core/Download/DownloadProcessingService.cs` (the
// `CheckForFinishedDownload` command handler — bare class at
// `Download/CheckForFinishedDownloadCommand.cs:1-8` triggers the
// service's recurring scan) iterates every download client's
// GetItems output, filters by category, and routes completed items
// through the import pipeline.
//
// The Go port stubs because the consumer chain requires:
//
//   - Provider.GetItems implementations across SAB + Transmission
//     (sub-arcs 14a/14b shipped only Download + Test; GetItems is
//     ErrNotImplementedYet today).
//   - TrackedDownloadService — the in-flight queue + history join
//     not yet ported.
//   - Import pipeline integration — connects to
//     downloadedimport.Service which is ported but expects the
//     full scan input.
//
// What this slice DOES close:
//
//   - The CheckForFinishedDownloads command is push-routable via the
//     registry (the API/v3 command-push path + the scheduler tick).
//   - The 1-minute scheduled-task entry materialises at boot
//     (matches C# `TaskManager.cs:74` — same frequency tier as
//     RefreshMonitoredDownloads).
//   - API callers pushing CheckForFinishedDownloads get a structured
//     log line instead of an "Unknown command" rejection.

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
// port stub. Logs and returns nil.
type CheckForFinishedDownloads struct {
	log *slog.Logger
}

// NewCheckForFinishedDownloads builds the handler. log defaults to
// slog.Default.
func NewCheckForFinishedDownloads(log *slog.Logger) *CheckForFinishedDownloads {
	if log == nil {
		log = slog.Default()
	}
	return &CheckForFinishedDownloads{log: log}
}

// Execute logs the command fire-event and returns nil. The real port
// will replace this with a delegation into a future
// downloadprocessing.Service that iterates every Provider.GetItems
// and dispatches the completed-import branch.
func (h *CheckForFinishedDownloads) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"checkforfinisheddownloads: stub Execute (DownloadProcessingService not yet ported)",
		slog.String("command", CheckForFinishedDownloadsName))
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
