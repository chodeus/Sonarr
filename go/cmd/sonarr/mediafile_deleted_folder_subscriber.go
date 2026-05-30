package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/messaging"
)

// deletedFolderCleanupConfig is the narrow `*config.Provider` slice the
// empty-folder-cleanup subscriber needs — the single boolean that gates
// the C# `MediaFileDeletionService.Handle(EpisodeFileDeletedEvent)` arm
// at `src/NzbDrone.Core/MediaFiles/MediaFileDeletionService.cs:218`.
//
// `*config.Provider` satisfies this via its DeleteEmptyFolders accessor.
type deletedFolderCleanupConfig interface {
	DeleteEmptyFolders() bool
}

// deletedFolderCleanupDisk is the narrow `*disk.Provider` slice the
// subscriber needs to prune now-empty folders after a file delete.
type deletedFolderCleanupDisk interface {
	FolderExists(path string) bool
	FolderEmpty(path string) bool
	RemoveEmptySubfolders(path string) error
	DeleteFolder(path string, recursive bool) error
}

// subscribeMediaFileDeletedFolderCleanup wires an
// [episodefiles.EpisodeFileDeletedEvent] subscriber onto bus that prunes
// the now-empty season/series subfolders after an episode file is
// deleted. Ports C# `MediaFileDeletionService.Handle(EpisodeFileDeletedEvent)`
// (`MediaFileDeletionService.cs:215-243`), which is annotated
// `[EventHandleOrder(EventHandleOrder.Last)]` — so the Go port subscribes
// with [messaging.OrderLast] to run after the episode-cache / history /
// SignalR handlers (none of which touch the folder, but the ordering is
// preserved for faithfulness).
//
// The C# gate: skip entirely when DeleteEmptyFolders is off OR the delete
// reason is MissingFromDisk (the file already vanished — the folder may
// hold other still-present files; pruning would be wrong). When the gate
// passes, walk UP from the deleted file's parent folder to the series
// root, calling RemoveEmptySubfolders on each existing level, then sweep
// the series root itself and delete it outright if it ended up empty.
func subscribeMediaFileDeletedFolderCleanup(
	bus *messaging.Bus,
	cfg deletedFolderCleanupConfig,
	disk deletedFolderCleanupDisk,
	log *slog.Logger,
) {
	messaging.SubscribeOrdered(bus, "mediafile.deleted.folder.cleanup", messaging.OrderLast,
		func(ctx context.Context, ev episodefiles.EpisodeFileDeletedEvent) error {
			handleMediaFileDeletedForFolderCleanup(ctx, ev, cfg, disk, log)
			return nil
		})
}

// handleMediaFileDeletedForFolderCleanup factors out the logic so tests
// can drive it without a real bus or a subscribe-then-publish round-trip.
// Fire-and-forget — errors are logged, never propagated.
func handleMediaFileDeletedForFolderCleanup(
	ctx context.Context,
	ev episodefiles.EpisodeFileDeletedEvent,
	cfg deletedFolderCleanupConfig,
	disk deletedFolderCleanupDisk,
	log *slog.Logger,
) {
	// C# `:218` — DeleteEmptyFolders off OR reason==MissingFromDisk → skip.
	if !cfg.DeleteEmptyFolders() || ev.Reason == episodefiles.ReasonMissingFromDisk {
		return
	}
	// Need the parent series (for its Path) + the deleted file (for its
	// relative path). C# reads `message.EpisodeFile.Series.Value` +
	// `message.EpisodeFile.Path`; the Go event carries Series + the file
	// row whose Path = Join(Series.Path, RelativePath).
	if ev.Series == nil || ev.EpisodeFile == nil {
		return
	}
	seriesPath := ev.Series.Path
	if seriesPath == "" {
		return
	}
	fileFullPath := filepath.Join(seriesPath, ev.EpisodeFile.RelativePath)

	// C# `:227-235` — walk up from the file's parent folder to (but not
	// including) the series root, pruning empty subfolders at each level.
	folder := filepath.Dir(fileFullPath)
	for isParentPath(seriesPath, folder) {
		if disk.FolderExists(folder) {
			if err := disk.RemoveEmptySubfolders(folder); err != nil {
				log.LogAttrs(ctx, slog.LevelWarn,
					"mediafile delete: RemoveEmptySubfolders failed (continuing)",
					slog.String("folder", folder), slog.Any("err", err))
			}
		}
		folder = filepath.Dir(folder)
	}

	// C# `:237` — sweep the series root itself.
	if err := disk.RemoveEmptySubfolders(seriesPath); err != nil {
		log.LogAttrs(ctx, slog.LevelWarn,
			"mediafile delete: RemoveEmptySubfolders(seriesPath) failed (continuing)",
			slog.String("seriesPath", seriesPath), slog.Any("err", err))
	}

	// C# `:239-242` — if the series folder is now completely empty, delete it.
	if disk.FolderEmpty(seriesPath) {
		log.LogAttrs(ctx, slog.LevelInfo,
			"mediafile delete: series folder is now empty, removing it",
			slog.String("seriesPath", seriesPath))
		if err := disk.DeleteFolder(seriesPath, true); err != nil {
			log.LogAttrs(ctx, slog.LevelWarn,
				"mediafile delete: DeleteFolder(seriesPath) failed",
				slog.String("seriesPath", seriesPath), slog.Any("err", err))
		}
	}
}
