// RefreshSeries handler. Ports the IExecute<RefreshSeriesCommand>
// implementation — AS A STUB. The real port is deferred until the
// heavy RefreshSeriesService subsystem lands (SkyHook metadata fetch
// + episode reconciliation + image-cache trigger). This slot ships so:
//
//   - The fourth IExecute<T> handler exists.
//   - The 12-hour scheduled-task entry materialises in the
//     ScheduledTasks table at boot.
//   - API callers pushing `RefreshSeries` get a structured log line
//     instead of an "Unknown command" rejection.
//
// The C# `RefreshSeriesCommand` carries optional `SeriesId` and
// `LastStartTime` fields; the stub ignores the body entirely.

package handlers

import (
	"context"
	"log/slog"
)

// RefreshSeriesName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `RefreshSeriesCommand` → `RefreshSeries`.
const RefreshSeriesName = "RefreshSeries"

// RefreshSeries is the IExecute<RefreshSeriesCommand> port stub.
type RefreshSeries struct {
	log *slog.Logger
}

// NewRefreshSeries builds the handler. log defaults to slog.Default.
func NewRefreshSeries(log *slog.Logger) *RefreshSeries {
	if log == nil {
		log = slog.Default()
	}
	return &RefreshSeries{log: log}
}

// Execute logs that the command fired and returns nil. The real port
// will replace this with a delegation into a future
// refreshseries.Service.Execute(ctx, seriesId) call.
func (h *RefreshSeries) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"refreshseries: stub Execute (real RefreshSeriesService not yet ported)",
		slog.String("command", RefreshSeriesName))
	return nil
}
