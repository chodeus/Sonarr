// RescanSeries handler. Ports the IExecute<RescanSeriesCommand>
// implementation at `src/NzbDrone.Core/MediaFiles/DiskScanService.cs:301-317`.
//
// The body parsing for `{"seriesId": N}` and `{}` lives here; the
// actual two-branch dispatch (single-series GetSeries+Scan vs
// all-series iteration) lives on [mediafiles.Service.Execute] —
// keeping the handler a thin transport wrapper and the iteration
// logic where it can be exercised by Service-level tests.
//
// Lineage: this slot ships REAL — slice 3.50 ported the command +
// events shape, slice 3.51 ported the Service struct + Scan body,
// and this slice closes the dispatcher surface. The dispatcher
// receives the {"name":"RescanSeries"} command from the API or from
// upstream RescanSeries triggers (RefreshSeries follow-on, manual UI
// button) and routes it here.
//
// The C# RescanSeriesCommand is NOT in the ScheduledTasks default
// set (TaskManager.cs:67-135 doesn't include it) — RescanSeries fires
// only from API calls or from the RefreshSeries handler at the tail
// of a refresh. handlers.DefaultScheduledTasks() therefore skips it
// too; pinned by a test.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/mediafiles"
)

// RescanSeriesName is the registry key + Commands.Name. Mirrors the
// C# `Command.Name` short form: `RescanSeriesCommand` → `RescanSeries`.
// Defined in `mediafiles.RescanSeriesName` (slice 3.50); aliased here
// so the handlers package's RegisterAll wiring stays self-contained.
const RescanSeriesName = mediafiles.RescanSeriesName

// RescanSeriesScanner is the thin slice of [mediafiles.Service] the
// handler depends on. Lets the test suite drive the dispatch surface
// with a stub instead of constructing a fully-wired Service. The
// production wiring at cmd/sonarr passes `*mediafiles.Service`
// directly (Go's structural typing satisfies the interface without
// an explicit declaration on the Service side).
type RescanSeriesScanner interface {
	Execute(ctx context.Context, cmd mediafiles.RescanSeriesCommand) error
}

// RescanSeries is the IExecute<RescanSeriesCommand> port. Holds a
// reference to the scan-orchestration service and a logger for the
// "command fired" trace line.
type RescanSeries struct {
	scanner RescanSeriesScanner
	log     *slog.Logger
}

// NewRescanSeries builds the handler. The scanner MUST be non-nil —
// running RescanSeries without a scan service has no meaningful
// behaviour and a nil panic would surface as a goroutine crash on
// first dispatch. Log defaults to [slog.Default].
func NewRescanSeries(scanner RescanSeriesScanner, log *slog.Logger) (*RescanSeries, error) {
	if scanner == nil {
		return nil, errors.New("handlers: NewRescanSeries: nil scanner")
	}
	if log == nil {
		log = slog.Default()
	}
	return &RescanSeries{scanner: scanner, log: log}, nil
}

// Execute parses the command body's optional `seriesId` field and
// delegates to the scanner. Body parsing is forgiving:
//
//   - empty / whitespace / "{}" → SeriesID nil → all-series scan
//   - `{"seriesId": 42}` → SeriesID 42 → single-series scan
//   - garbage / non-JSON → returns an error rather than silently
//     scanning everything (a body parse failure is a programming
//     bug, not a missing field)
func (h *RescanSeries) Execute(ctx context.Context, body string) error {
	body = strings.TrimSpace(body)
	var cmd mediafiles.RescanSeriesCommand
	if body != "" && body != "{}" {
		if err := json.Unmarshal([]byte(body), &cmd); err != nil {
			return fmt.Errorf("RescanSeries: parse body: %w", err)
		}
	}
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"RescanSeries: dispatching",
		slog.String("command", RescanSeriesName),
		slog.Any("seriesId", cmd.SeriesID))
	return h.scanner.Execute(ctx, cmd)
}
