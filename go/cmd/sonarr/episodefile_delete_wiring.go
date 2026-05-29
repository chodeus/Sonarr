// Boot-time adapter that ports C# `MediaFileDeletionService.DeleteEpisodeFile`
// (`src/NzbDrone.Core/MediaFiles/MediaFileDeletionService.cs:33-60`) for the
// API episode-file delete endpoints. Embeds *episodefiles.Service so it
// satisfies apiv3.EpisodeFileWriter for the delete handlers, and adds the
// recycle-bin + disk arm via apiv3.EpisodeFileDiskDeleter (discovered by the
// handler through an optional-extension type-assertion — same idiom as the
// manual-import lister/rejector).
//
// Flow (matching C#): resolve the series → validate the best-matching root
// folder exists and is non-empty (else 409 via ErrEpisodeFileRootFolderInvalid)
// → recycle/delete the physical file when it's present under the series path
// → delete the DB row (+ fire EpisodeFileDeletedEvent) via the embedded
// service. The disk step precedes the DB delete so a recycle failure aborts
// before the row is removed, exactly like C#.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/series"
)

// Consumer-side interfaces for the recycle-bin delete deps (per the
// port plan's Go-idiom rule). Production binds *series.Service /
// *rootfolders.Service / *recyclebin.Service / *disk.Provider; tests
// pass fakes.
type (
	efDeleteSeriesGetter interface {
		GetSeries(ctx context.Context, id int) (*series.Series, error)
	}
	efDeleteRootFolder interface {
		GetBestRootFolderPath(ctx context.Context, path string) (string, error)
	}
	efDeleteRecycleBin interface {
		DeleteFile(path, subfolder string) (string, error)
	}
	efDeleteDisk interface {
		FolderExists(path string) bool
		FileExists(path string) bool
		GetDirectories(path string) ([]string, error)
	}
)

// episodeFileDeleteAdapter wraps the episode-file service with the
// cross-service deps the recycle-bin delete needs.
type episodeFileDeleteAdapter struct {
	*episodefiles.Service // embedded → promotes GetFile/GetFiles/Delete/Update/UpdateMany (apiv3.EpisodeFileWriter)

	series      efDeleteSeriesGetter
	rootFolders efDeleteRootFolder
	recycleBin  efDeleteRecycleBin
	disk        efDeleteDisk
	log         *slog.Logger
}

// DeleteEpisodeFileWithDisk ports C# MediaFileDeletionService.DeleteEpisodeFile.
func (a *episodeFileDeleteAdapter) DeleteEpisodeFileWithDisk(ctx context.Context, f *episodefiles.File, reason episodefiles.DeleteMediaFileReason) error {
	ser, err := a.series.GetSeries(ctx, f.SeriesID)
	if err != nil || ser == nil {
		return fmt.Errorf("episodefile delete: resolve series %d: %w", f.SeriesID, err)
	}

	fullPath := filepath.Join(ser.Path, f.RelativePath)

	// Validate the root folder (C# `:35-46`): missing or empty → 409.
	rootFolder, err := a.rootFolders.GetBestRootFolderPath(ctx, ser.Path)
	if err != nil {
		return fmt.Errorf("episodefile delete: best root folder: %w", err)
	}
	if !a.disk.FolderExists(rootFolder) {
		a.log.Warn("episodefile delete: series root folder doesn't exist", "rootFolder", rootFolder)
		return apiv3.ErrEpisodeFileRootFolderInvalid
	}
	if dirs, err := a.disk.GetDirectories(rootFolder); err != nil || len(dirs) == 0 {
		a.log.Warn("episodefile delete: series root folder is empty", "rootFolder", rootFolder, "err", err)
		return apiv3.ErrEpisodeFileRootFolderInvalid
	}

	// Recycle/delete the physical file when present (C# `:48-58`).
	if a.disk.FolderExists(ser.Path) && a.disk.FileExists(fullPath) {
		// subfolder = parent(series.Path) relative to parent(fullPath) —
		// preserves the series/season structure inside the recycle bin
		// (C# `_diskProvider.GetParentFolder(...).GetRelativePath(...)`).
		subfolder, relErr := ospath.Relative(filepath.Dir(ser.Path), filepath.Dir(fullPath))
		if relErr != nil {
			subfolder = ""
		}
		a.log.Info("episodefile delete: recycling file", "path", fullPath)
		if _, err := a.recycleBin.DeleteFile(fullPath, subfolder); err != nil {
			return fmt.Errorf("episodefile delete: recycle %q: %w", fullPath, err)
		}
	}

	// Delete the DB row even if the file was already gone (C# `:60`).
	return a.Service.Delete(ctx, f, reason)
}

// Compile-time guards.
var (
	_ apiv3.EpisodeFileWriter      = (*episodeFileDeleteAdapter)(nil)
	_ apiv3.EpisodeFileDiskDeleter = (*episodeFileDeleteAdapter)(nil)
)
