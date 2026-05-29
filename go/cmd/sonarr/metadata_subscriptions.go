package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

// metadataWriterDriver is the narrow slice of *metadataservice.Service
// the event subscriptions need — the three CreateAfter* entry points
// that map to C# ExtraService's ported IHandle events.
type metadataWriterDriver interface {
	CreateAfterEpisodeImport(ctx context.Context, sr *series.Series, episodeFile *episodefiles.File) ([]metadatafiles.MetadataFile, error)
	CreateAfterMediaCoverUpdate(ctx context.Context, sr *series.Series) ([]metadatafiles.MetadataFile, error)
	CreateAfterSeriesScan(ctx context.Context, sr *series.Series, episodeFilesList []episodefiles.File) ([]metadatafiles.MetadataFile, error)
	CreateAfterEpisodeFolder(ctx context.Context, sr *series.Series, seriesFolder, seasonFolder string) ([]metadatafiles.MetadataFile, error)
	MoveFilesAfterRename(ctx context.Context, sr *series.Series, episodeFilesList []episodefiles.File) ([]metadatafiles.MetadataFile, error)
}

// metadataScanFileLister loads a series' episode files for the
// SeriesScanned handler (C# `_mediaFileService.GetFilesBySeries`).
type metadataScanFileLister interface {
	GetFilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
}

// subscribeMetadataWriters wires the metadata writer (Q1, J435) to the
// events C# ExtraService subscribes to
// (`src/NzbDrone.Core/Extras/ExtraService.cs:26-29`). Of C#'s six
// IHandle events, three are ported + published in the Go tree today:
//
//   - EpisodeImportedEvent     → CreateAfterEpisodeImport
//   - CoversUpdatedEvent        → CreateAfterMediaCoverUpdate
//   - SeriesScannedEvent        → CreateAfterSeriesScan
//   - EpisodeFolderCreatedEvent → CreateAfterEpisodeFolder (J452 — the
//     event is published by episodefilemoving when a series/season/episode
//     folder is freshly created during a file move)
//   - SeriesRenamedEvent        → MoveFilesAfterRename (J453 — published
//     by the rename handler once per series after files are renamed)
//
// The last one (DownloadsProcessedEvent → CreateAfterEpisodesImported) is
// not yet defined/published in the Go port; its subscription follows when
// the publisher lands (tracked in GAP_ANALYSIS Q1).
//
// Metadata is only actually written when a metadata consumer (e.g.
// Kodi nfo) is enabled + registered; with none configured these
// handlers are no-ops, matching C#. Each handler logs+swallows its
// error (returns nil) so a metadata write failure never aborts the
// import/scan flow or sibling subscribers — mirrors the C#
// per-consumer try/catch posture.
func subscribeMetadataWriters(bus *messaging.Bus, svc metadataWriterDriver, files metadataScanFileLister, log *slog.Logger) {
	messaging.Subscribe(bus, "metadata.CreateAfterEpisodeImport",
		func(ctx context.Context, ev episodeimport.EpisodeImportedEvent) error {
			if ev.LocalEpisode == nil || ev.LocalEpisode.Series == nil || ev.EpisodeFile == nil {
				return nil
			}
			if _, err := svc.CreateAfterEpisodeImport(ctx, ev.LocalEpisode.Series, ev.EpisodeFile); err != nil {
				log.Warn("metadata: CreateAfterEpisodeImport failed", "series", ev.LocalEpisode.Series.ID, "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "metadata.CreateAfterMediaCoverUpdate",
		func(ctx context.Context, ev mediacover.CoversUpdatedEvent) error {
			if ev.Series == nil {
				return nil
			}
			if _, err := svc.CreateAfterMediaCoverUpdate(ctx, ev.Series); err != nil {
				log.Warn("metadata: CreateAfterMediaCoverUpdate failed", "series", ev.Series.ID, "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "metadata.CreateAfterSeriesScan",
		func(ctx context.Context, ev mediafiles.SeriesScannedEvent) error {
			if ev.Series == nil {
				return nil
			}
			fs, err := files.GetFilesBySeries(ctx, ev.Series.ID)
			if err != nil {
				log.Warn("metadata: CreateAfterSeriesScan file load failed", "series", ev.Series.ID, "err", err)
				return nil
			}
			if _, err := svc.CreateAfterSeriesScan(ctx, ev.Series, fs); err != nil {
				log.Warn("metadata: CreateAfterSeriesScan failed", "series", ev.Series.ID, "err", err)
			}
			return nil
		})
	// EpisodeFolderCreatedEvent → CreateAfterEpisodeFolder (C# ExtraService
	// Handle(EpisodeFolderCreatedEvent), `ExtraService.cs:142-150`). The
	// event carries whichever of series/season/episode folders were just
	// created; CreateAfterEpisodeFolder writes series/season-level metadata
	// + images for the freshly-created folders (no-op when both folder
	// args are blank).
	messaging.Subscribe(bus, "metadata.CreateAfterEpisodeFolder",
		func(ctx context.Context, ev episodefilemoving.EpisodeFolderCreatedEvent) error {
			if ev.Series == nil {
				return nil
			}
			if _, err := svc.CreateAfterEpisodeFolder(ctx, ev.Series, ev.SeriesFolder, ev.SeasonFolder); err != nil {
				log.Warn("metadata: CreateAfterEpisodeFolder failed", "series", ev.Series.ID, "err", err)
			}
			return nil
		})
	// SeriesRenamedEvent → MoveFilesAfterRename (C# ExtraService
	// Handle(SeriesRenamedEvent), `ExtraService.cs:162-171`). The C#
	// handler ignores the event's RenamedFiles and reloads ALL of the
	// series' files, relocating each writer's sidecars to match the
	// renamed media.
	messaging.Subscribe(bus, "metadata.MoveFilesAfterRename",
		func(ctx context.Context, ev episodefiles.SeriesRenamedEvent) error {
			if ev.Series == nil {
				return nil
			}
			fs, err := files.GetFilesBySeries(ctx, ev.Series.ID)
			if err != nil {
				log.Warn("metadata: MoveFilesAfterRename file load failed", "series", ev.Series.ID, "err", err)
				return nil
			}
			if _, err := svc.MoveFilesAfterRename(ctx, ev.Series, fs); err != nil {
				log.Warn("metadata: MoveFilesAfterRename failed", "series", ev.Series.ID, "err", err)
			}
			return nil
		})
}
