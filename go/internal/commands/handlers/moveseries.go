// MoveSeries + BulkMoveSeries handlers. Port the
// IExecute<MoveSeriesCommand> + IExecute<BulkMoveSeriesCommand>
// implementations at `src/NzbDrone.Core/Tv/MoveSeriesService.cs`.
//
// The disk-move orchestration (validate / skip-if-equal / create-parent
// / move / revert-on-failure / destination-folder naming for the bulk
// path) lives in the SeriesMover implementation (the cmd/sonarr
// seriesMoveAdapter); these handlers decode the command body and
// dispatch.

package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// MoveSeriesName / BulkMoveSeriesName are the registry keys + Commands.Name.
const (
	MoveSeriesName     = "MoveSeries"
	BulkMoveSeriesName = "BulkMoveSeries"
)

// BulkMoveSeriesItem mirrors the C# `BulkMoveSeries` entry
// (`BulkMoveSeriesCommand.cs`): a series id + its current folder.
type BulkMoveSeriesItem struct {
	SeriesID   int    `json:"seriesId"`
	SourcePath string `json:"sourcePath"`
}

// SeriesMover moves series folders on disk (recycling the source on
// failure). Satisfied by the cmd/sonarr seriesMoveAdapter (ports
// MoveSeriesService). Per-series failures are logged-and-skipped inside
// the implementation so one bad series doesn't abort the command.
type SeriesMover interface {
	MoveSeries(ctx context.Context, seriesID int, source, dest string) error
	BulkMoveSeries(ctx context.Context, items []BulkMoveSeriesItem, destRoot string) error
}

// MoveSeries is the IExecute<MoveSeriesCommand> port.
type MoveSeries struct {
	mover SeriesMover
	log   *slog.Logger
}

// NewMoveSeries builds the handler. mover MUST be non-nil.
func NewMoveSeries(mover SeriesMover, log *slog.Logger) (*MoveSeries, error) {
	if mover == nil {
		return nil, errors.New("handlers: NewMoveSeries: nil SeriesMover")
	}
	if log == nil {
		log = slog.Default()
	}
	return &MoveSeries{mover: mover, log: log}, nil
}

type moveSeriesBody struct {
	SeriesID        int    `json:"seriesId"`
	SourcePath      string `json:"sourcePath"`
	DestinationPath string `json:"destinationPath"`
}

// Execute moves a single series folder (C# `Execute(MoveSeriesCommand)`).
func (h *MoveSeries) Execute(ctx context.Context, body string) error {
	var cmd moveSeriesBody
	if err := decodeCommandBody(body, &cmd); err != nil {
		return fmt.Errorf("handlers: MoveSeries: parse body: %w", err)
	}
	if cmd.SeriesID <= 0 || cmd.SourcePath == "" || cmd.DestinationPath == "" {
		return nil
	}
	return h.mover.MoveSeries(ctx, cmd.SeriesID, cmd.SourcePath, cmd.DestinationPath)
}

// BulkMoveSeries is the IExecute<BulkMoveSeriesCommand> port.
type BulkMoveSeries struct {
	mover SeriesMover
	log   *slog.Logger
}

// NewBulkMoveSeries builds the handler. mover MUST be non-nil.
func NewBulkMoveSeries(mover SeriesMover, log *slog.Logger) (*BulkMoveSeries, error) {
	if mover == nil {
		return nil, errors.New("handlers: NewBulkMoveSeries: nil SeriesMover")
	}
	if log == nil {
		log = slog.Default()
	}
	return &BulkMoveSeries{mover: mover, log: log}, nil
}

type bulkMoveSeriesBody struct {
	Series                []BulkMoveSeriesItem `json:"series"`
	DestinationRootFolder string               `json:"destinationRootFolder"`
}

// Execute moves each series into the destination root (C#
// `Execute(BulkMoveSeriesCommand)`).
func (h *BulkMoveSeries) Execute(ctx context.Context, body string) error {
	var cmd bulkMoveSeriesBody
	if err := decodeCommandBody(body, &cmd); err != nil {
		return fmt.Errorf("handlers: BulkMoveSeries: parse body: %w", err)
	}
	if len(cmd.Series) == 0 || cmd.DestinationRootFolder == "" {
		return nil
	}
	return h.mover.BulkMoveSeries(ctx, cmd.Series, cmd.DestinationRootFolder)
}
