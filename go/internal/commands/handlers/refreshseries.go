// RefreshSeries handler. Ports the IExecute<RefreshSeriesCommand>
// implementation at `src/NzbDrone.Core/Tv/RefreshSeriesService.cs:211-285`.
//
// Slice 5.J191 promotes the J166-era stub to a real handler:
//   - Parses the RefreshSeriesCommand body into seriesIds (+
//     isNewSeries when present; currently unused — the deferred
//     disk-rescan + episode-reconcile arms will consume it).
//   - For each id, delegates to a [RefreshSeriesRunner] (production
//     binds *refreshseries.Service).
//   - Per-id failures absorbed via warn-log so one bad row doesn't
//     poison the batch. Mirrors C# `:228-246` catch-and-continue
//     posture; the C# SeriesNotFoundException path marks the result
//     Indeterminate via ICommandResultReporter — the Go-port logs +
//     continues (CommandResult.Indeterminate plumbing is a deferred
//     follow-up).
//
// Deferred:
//   - All-series sweep when SeriesIds is empty (C# `:249-282`). The
//     C# implementation walks every row and checks ShouldRefresh; the
//     Go-port currently no-ops on an empty list and logs.
//   - SeriesRefreshStartingEvent / SeriesRefreshCompleteEvent.
//   - UpdateTags + RescanSeries.
//
// Back-compat: when [Deps.RefreshSeries] is nil the handler falls
// back to the J166 stub log so hosts that haven't wired the runner
// still register the slot.

package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
)

// RefreshSeriesName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `RefreshSeriesCommand` → `RefreshSeries`.
const RefreshSeriesName = "RefreshSeries"

// RefreshSeriesRunner is the narrow slice of refreshseries.Service
// the handler reaches for. Refresh handles the per-id path; RefreshAll
// (added 5.J193) handles the C# `Execute` all-series sweep that fires
// when SeriesIds is empty.
type RefreshSeriesRunner interface {
	Refresh(ctx context.Context, seriesID int) error
	RefreshAll(ctx context.Context) error
}

// RefreshSeries is the IExecute<RefreshSeriesCommand> port.
type RefreshSeries struct {
	runner RefreshSeriesRunner // optional; nil falls back to stub log
	log    *slog.Logger
}

// NewRefreshSeries builds the handler. `runner` is optional — nil
// preserves the J166 stub posture so old wiring still boots.
func NewRefreshSeries(runner RefreshSeriesRunner, log *slog.Logger) *RefreshSeries {
	if log == nil {
		log = slog.Default()
	}
	return &RefreshSeries{runner: runner, log: log}
}

// refreshSeriesCommandBody mirrors the wire shape of C#
// `RefreshSeriesCommand` at `Tv/Commands/RefreshSeriesCommand.cs`.
// Only the fields the Go-port currently uses are mapped; UpdateScheduled
// + IsNewSeries can be added when the deferred arms (disk rescan,
// scheduled-vs-manual trigger fork) land.
type refreshSeriesCommandBody struct {
	// SeriesIds is the explicit list of ids to refresh. Empty triggers
	// the all-series sweep in C# (deferred in Go-port).
	SeriesIDs []int `json:"seriesIds"`

	// IsNewSeries is set when the AddSeries flow pushed the command
	// to seed metadata for a freshly-added row (C# `:214`). Currently
	// unused; will gate the forced rescan when the disk-rescan arm
	// lands.
	IsNewSeries bool `json:"isNewSeries"`
}

// Execute parses the JSON body and dispatches per-id refreshes.
// Empty bodies + JSON-decode failures degrade to the stub-log path so
// the registry slot stays well-behaved.
func (h *RefreshSeries) Execute(ctx context.Context, body string) error {
	if h.runner == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"refreshseries: stub Execute (RefreshSeriesRunner not wired)",
			slog.String("command", RefreshSeriesName))
		return nil
	}

	trimmed := strings.TrimSpace(body)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		// J193 — empty body now fires the all-series sweep (was a
		// stub-log no-op pre-J193). The 12h scheduled-task tick
		// pushes RefreshSeries with no body, so this is the path
		// the periodic refresh actually exercises.
		return h.runner.RefreshAll(ctx)
	}

	var cmd refreshSeriesCommandBody
	if err := json.Unmarshal([]byte(trimmed), &cmd); err != nil {
		h.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshseries: malformed body — skipping refresh",
			slog.String("body", trimmed),
			slog.Any("err", err))
		return nil
	}
	if len(cmd.SeriesIDs) == 0 {
		// J193 — empty seriesIds list also fires the all-series
		// sweep, matching C# `Execute` :249-282 where
		// `message.SeriesIds.Any()` returning false jumps to the
		// all-series branch.
		return h.runner.RefreshAll(ctx)
	}

	for _, id := range cmd.SeriesIDs {
		if err := h.runner.Refresh(ctx, id); err != nil {
			// Matches C# `RefreshSeriesService.cs:228-246` catch-and-
			// continue stance: log + keep going so one missing/dead
			// series doesn't abort the whole batch.
			h.log.LogAttrs(ctx, slog.LevelWarn,
				"refreshseries: per-id refresh failed (batch continues)",
				slog.Int("seriesId", id),
				slog.Any("err", err))
		}
	}
	return nil
}
