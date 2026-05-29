// DeleteSeriesFiles handler. Ports the
// IExecute<DeleteSeriesFilesCommand> arm of
// `src/NzbDrone.Core/MediaFiles/MediaFileDeletionService.cs:62-130`.
//
// Routes a `{"name":"DeleteSeriesFiles","seriesIds":[...]}` API command
// into the recycle-bin + DB delete of every episode file for each
// series. The orchestration (root-folder validation, recycle, DB
// delete) lives in the SeriesFilesDeleter implementation (the cmd
// adapter shared with the API episodefile-delete endpoints); this
// handler just decodes the body and dispatches.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// DeleteSeriesFilesName is the registry key + Commands.Name. Mirrors the
// C# short form `DeleteSeriesFilesCommand` → `DeleteSeriesFiles`.
const DeleteSeriesFilesName = "DeleteSeriesFiles"

// SeriesFilesDeleter recycles + DB-deletes every episode file for the
// supplied series. Satisfied by the cmd/sonarr episodeFileDeleteAdapter
// (which ports MediaFileDeletionService). Per-series failures are
// logged-and-skipped inside the implementation, matching the C#
// per-series try/catch → CommandResult.Indeterminate posture, so a
// single bad series doesn't abort the whole command.
type SeriesFilesDeleter interface {
	DeleteSeriesFiles(ctx context.Context, seriesIDs []int) error
}

// DeleteSeriesFiles is the IExecute<DeleteSeriesFilesCommand> port.
type DeleteSeriesFiles struct {
	deleter SeriesFilesDeleter
	log     *slog.Logger
}

// NewDeleteSeriesFiles builds the handler. deleter MUST be non-nil.
func NewDeleteSeriesFiles(deleter SeriesFilesDeleter, log *slog.Logger) (*DeleteSeriesFiles, error) {
	if deleter == nil {
		return nil, errors.New("handlers: NewDeleteSeriesFiles: nil SeriesFilesDeleter")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DeleteSeriesFiles{deleter: deleter, log: log}, nil
}

// deleteSeriesFilesBody mirrors the C# `DeleteSeriesFilesCommand`
// properties (`DeleteSeriesFilesCommand.cs:8`).
type deleteSeriesFilesBody struct {
	SeriesIDs []int `json:"seriesIds"`
}

// Execute parses the JSON body and dispatches to the deleter. An empty
// seriesIds list is a no-op success (C# loops over an empty list).
func (h *DeleteSeriesFiles) Execute(ctx context.Context, body string) error {
	var cmd deleteSeriesFilesBody
	body = strings.TrimSpace(body)
	if body != "" && body != "{}" {
		if err := json.Unmarshal([]byte(body), &cmd); err != nil {
			return fmt.Errorf("handlers: DeleteSeriesFiles: parse body: %w", err)
		}
	}
	if len(cmd.SeriesIDs) == 0 {
		return nil
	}
	h.log.LogAttrs(ctx, slog.LevelInfo, "deleteseriesfiles: deleting files",
		slog.Int("series_count", len(cmd.SeriesIDs)))
	return h.deleter.DeleteSeriesFiles(ctx, cmd.SeriesIDs)
}
