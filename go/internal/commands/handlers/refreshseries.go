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
//   - UpdateTags + RescanSeries.
//
// Back-compat: when [Deps.RefreshSeries] is nil the handler falls
// back to the J166 stub log so hosts that haven't wired the runner
// still register the slot. When the bus is nil, the
// SeriesRefreshStarting/Complete event publishes are silently
// skipped — the refresh itself still runs.
//
// Slice 5.J194 added the bus dep + the SeriesRefreshStartingEvent /
// SeriesRefreshCompleteEvent emit pair that wraps every refresh
// batch (both the per-id and the all-series sweep paths). Mirrors
// C# RefreshSeriesService.cs:215 + :284.

package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// RefreshSeriesName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `RefreshSeriesCommand` → `RefreshSeries`.
const RefreshSeriesName = "RefreshSeries"

// RefreshSeriesRunner is the narrow slice of refreshseries.Service
// the handler reaches for. Refresh handles the per-id path; RefreshAll
// (added 5.J193) handles the C# `Execute` all-series sweep that fires
// when SeriesIds is empty.
//
// J198 grew both methods with an `isManual` bool — true when the
// triggering command was user-initiated (Trigger=Manual on the row),
// false for scheduled-task ticks. Refreshseries uses it to (a) bypass
// the [ShouldRefresh] staleness gate in the all-series sweep (C#
// `RefreshSeriesService.cs:256`) and (b) honour the "AfterManual"
// RescanAfterRefresh config branch in the rescan tail (C# `:183-189`).
type RefreshSeriesRunner interface {
	Refresh(ctx context.Context, seriesID int, isManual bool) error
	RefreshAll(ctx context.Context, isManual bool) error
}

// RefreshSeries is the IExecute<RefreshSeriesCommand> port.
type RefreshSeries struct {
	runner RefreshSeriesRunner // optional; nil falls back to stub log
	bus    *messaging.Bus      // optional; nil silently skips refresh-event publish
	log    *slog.Logger
}

// NewRefreshSeries builds the handler. `runner` is optional — nil
// preserves the J166 stub posture so old wiring still boots. `bus` is
// optional — nil silently skips SeriesRefreshStarting/Complete event
// publishes (the refresh itself still runs).
func NewRefreshSeries(runner RefreshSeriesRunner, bus *messaging.Bus, log *slog.Logger) *RefreshSeries {
	if log == nil {
		log = slog.Default()
	}
	return &RefreshSeries{runner: runner, bus: bus, log: log}
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
	var cmd refreshSeriesCommandBody
	allSeriesSweep := false
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		// J193 — empty body now fires the all-series sweep (was a
		// stub-log no-op pre-J193). The 12h scheduled-task tick
		// pushes RefreshSeries with no body, so this is the path
		// the periodic refresh actually exercises.
		allSeriesSweep = true
	} else if err := json.Unmarshal([]byte(trimmed), &cmd); err != nil {
		h.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshseries: malformed body — skipping refresh",
			slog.String("body", trimmed),
			slog.Any("err", err))
		return nil
	} else if len(cmd.SeriesIDs) == 0 {
		// J193 — empty seriesIds list also fires the all-series
		// sweep, matching C# Execute :249-282 where SeriesIds.Any()
		// == false jumps to the all-series branch.
		allSeriesSweep = true
	}

	// J194 — wrap the batch with the C# SeriesRefreshStarting /
	// SeriesRefreshComplete event pair (C# :215, :284).
	//
	// J198 — read the trigger from ctx (attached by the dispatcher's
	// [commands.WithTrigger] call) and pass `isManual` through to the
	// refreshseries runner. Mirrors C# `RefreshSeriesService.Execute`
	// at `:213` (`var trigger = message.Trigger`).
	isManual := commands.TriggerFromContext(ctx) == commands.TriggerManual
	h.publishRefreshStart(ctx, isManual)
	defer h.publishRefreshComplete(ctx)

	if allSeriesSweep {
		return h.runner.RefreshAll(ctx, isManual)
	}

	for _, id := range cmd.SeriesIDs {
		if err := h.runner.Refresh(ctx, id, isManual); err != nil {
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

func (h *RefreshSeries) publishRefreshStart(ctx context.Context, manualTrigger bool) {
	if h.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, h.bus, series.SeriesRefreshStartingEvent{ManualTrigger: manualTrigger})
}

func (h *RefreshSeries) publishRefreshComplete(ctx context.Context) {
	if h.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, h.bus, series.SeriesRefreshCompleteEvent{})
}
