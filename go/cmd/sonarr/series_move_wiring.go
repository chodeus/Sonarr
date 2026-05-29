// Boot-time adapter porting C# `MoveSeriesService` (the
// IExecute<MoveSeriesCommand> + IExecute<BulkMoveSeriesCommand> arms at
// `src/NzbDrone.Core/Tv/MoveSeriesService.cs`). Moves a series folder on
// disk; reverts the series.Path to the source on failure. Implements the
// handlers.SeriesMover surface the move command handlers depend on.
//
// Note vs C#: the C# editor sets series.Path to the destination BEFORE
// queuing the command (so the command only moves the folder + reverts on
// failure). The Go port additionally persists Path=destination on success
// so a direct API command push leaves the DB consistent regardless of
// whether the caller pre-set the path; this is a harmless superset of the
// C# behaviour (idempotent when Path already equals destination).

package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/series"
)

// seriesMoveDisk narrows the disk.Provider surface the move uses.
type seriesMoveDisk interface {
	FolderExists(path string) bool
	CreateFolder(path string) error
	MoveFolder(src, dst string) error
}

type seriesMoveAdapter struct {
	series *series.Service
	naming seriesPathBuilderNamingReader
	disk   seriesMoveDisk
	log    *slog.Logger
}

// MoveSeries ports `Execute(MoveSeriesCommand)` (`MoveSeriesService.cs`):
// move a single series folder from source to the explicit destination.
func (a *seriesMoveAdapter) MoveSeries(ctx context.Context, seriesID int, source, dest string) error {
	ser, err := a.series.GetSeries(ctx, seriesID)
	if err != nil || ser == nil {
		return fmt.Errorf("move series: resolve series %d: %w", seriesID, err)
	}
	a.moveSingleSeries(ctx, ser, source, dest)
	return nil
}

// BulkMoveSeries ports `Execute(BulkMoveSeriesCommand)`: move each series
// into `destRoot`, computing its folder name from the naming config.
func (a *seriesMoveAdapter) BulkMoveSeries(ctx context.Context, items []handlers.BulkMoveSeriesItem, destRoot string) error {
	opts, err := a.folderOptions(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		ser, err := a.series.GetSeries(ctx, item.SeriesID)
		if err != nil || ser == nil {
			a.log.Warn("bulk move series: resolve series failed", "seriesId", item.SeriesID, "err", err)
			continue
		}
		dest := filepath.Join(destRoot, organizer.GetSeriesFolder(ser, opts))
		a.moveSingleSeries(ctx, ser, item.SourcePath, dest)
	}
	return nil
}

// moveSingleSeries ports the private C# `MoveSingleSeries` (`:140-190`).
func (a *seriesMoveAdapter) moveSingleSeries(ctx context.Context, ser *series.Series, source, dest string) {
	if source == "" {
		a.log.Warn("move series: empty source path; skipping", "series", ser.Title)
		return
	}
	if !a.disk.FolderExists(source) {
		a.log.Debug("move series: source folder does not exist; skipping", "source", source)
		return
	}
	if filepath.Clean(source) == filepath.Clean(dest) {
		a.log.Debug("move series: already at destination", "dest", dest)
		// Ensure the DB reflects the destination even on the no-move path.
		a.persistPath(ctx, ser, dest)
		return
	}
	// Ensure the destination's parent exists (C# `:170-172`).
	if err := a.disk.CreateFolder(filepath.Dir(dest)); err != nil {
		a.log.Warn("move series: create destination parent failed", "dest", dest, "err", err)
	}
	if err := a.disk.MoveFolder(source, dest); err != nil {
		a.log.Error("move series: move folder failed; reverting path", "source", source, "dest", dest, "err", err)
		a.persistPath(ctx, ser, source) // RevertPath (C# `:185`)
		return
	}
	a.log.Info("move series: moved", "series", ser.Title, "dest", dest)
	a.persistPath(ctx, ser, dest)
}

// persistPath updates the series row's Path when it differs.
func (a *seriesMoveAdapter) persistPath(ctx context.Context, ser *series.Series, path string) {
	if ser.Path == path {
		return
	}
	ser.Path = path
	if err := a.series.Update(ctx, ser); err != nil {
		a.log.Error("move series: persist path failed", "seriesId", ser.ID, "path", path, "err", err)
	}
}

// folderOptions builds the organizer FolderOptions from the naming config
// (same mapping as seriesPathBuilderAdapter.BuildPath).
func (a *seriesMoveAdapter) folderOptions(ctx context.Context) (organizer.FolderOptions, error) {
	cfg, err := a.naming.GetConfig(ctx)
	if err != nil {
		return organizer.FolderOptions{}, fmt.Errorf("move series: namingconfig.GetConfig: %w", err)
	}
	return organizer.FolderOptions{
		SeriesFolderFormat:   defaultIfEmpty(cfg.SeriesFolderFormat, "{Series Title}"),
		SeasonFolderFormat:   cfg.SeasonFolderFormat,
		SpecialsFolderFormat: cfg.SpecialsFolderFormat,
	}, nil
}

// Compile-time guard.
var _ handlers.SeriesMover = (*seriesMoveAdapter)(nil)
