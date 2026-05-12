package metadataservice

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

// Order is the C# `IManageExtraFiles.Order` priority — 0 for
// MetadataService. Mirrors C# `:58`. Higher-priority managers run
// first when ExtraService fans an event out (subtitles are
// historically priority 1+; metadata is the baseline).
//
// Exposed as a method on Service so the future ExtraService Go port
// can sort the registered managers consistently.
func (s *Service) Order() int { return 0 }

// CreateAfterMediaCoverUpdate fans the cover-cache-updated event
// across every enabled writer and emits per-series images. Mirrors
// C# `:60-83`.
//
// Algorithm:
//  1. Fetch every MetadataFile row for the series via [MetadataFileStore.FilesBySeries];
//  2. Run the [CleanMetadataService] orphan-pruner (nil-tolerant);
//  3. Skip when series.Path doesn't exist on disk;
//  4. For each enabled writer: filter the existing files by Consumer
//     + call [Service.ProcessSeriesImages] + accumulate rows;
//  5. Upsert the accumulated rows.
//
// Returns the rows that were persisted (matches the C# return).
// Empty when the folder doesn't exist or every writer skipped.
func (s *Service) CreateAfterMediaCoverUpdate(ctx context.Context, sr *series.Series) ([]metadatafiles.MetadataFile, error) {
	if s == nil || sr == nil {
		return nil, nil
	}
	existing, err := s.files.FilesBySeries(ctx, sr.ID)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterMediaCoverUpdate: FilesBySeries(%d): %w", sr.ID, err)
	}
	if cleanErr := s.runCleaner(ctx, sr); cleanErr != nil {
		s.log.Warn("metadataservice: cleaner failed (continuing)", "series", sr.ID, "err", cleanErr)
	}
	if !s.disk.FolderExists(sr.Path) {
		s.log.Info("metadataservice: series folder missing, skipping metadata image creation", "series", sr.ID, "path", sr.Path)
		return nil, nil
	}
	enabled, err := s.writers.Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterMediaCoverUpdate: WriterFactory.Enabled: %w", err)
	}
	var files []metadatafiles.MetadataFile
	for _, consumer := range enabled {
		consumerFiles := s.GetMetadataFilesForConsumer(consumer, existing)
		rows, processErr := s.ProcessSeriesImages(ctx, consumer, sr, consumerFiles)
		if processErr != nil {
			return nil, processErr
		}
		files = append(files, rows...)
	}
	if upsertErr := s.upsertIfAny(ctx, files); upsertErr != nil {
		return nil, upsertErr
	}
	return files, nil
}

// CreateAfterSeriesScan runs the full-series emission pipeline.
// Mirrors C# `:85-116`.
//
// For every enabled writer:
//   - ProcessSeriesMetadata (Reason.Scan);
//   - ProcessSeriesImages;
//   - ProcessSeasonImages;
//   - For each episodeFile: ProcessEpisodeMetadata + ProcessEpisodeImages.
//
// Skips when series.Path doesn't exist on disk.
func (s *Service) CreateAfterSeriesScan(ctx context.Context, sr *series.Series, episodeFilesList []episodefiles.File) ([]metadatafiles.MetadataFile, error) {
	if s == nil || sr == nil {
		return nil, nil
	}
	existing, err := s.files.FilesBySeries(ctx, sr.ID)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterSeriesScan: FilesBySeries(%d): %w", sr.ID, err)
	}
	if cleanErr := s.runCleaner(ctx, sr); cleanErr != nil {
		s.log.Warn("metadataservice: cleaner failed (continuing)", "series", sr.ID, "err", cleanErr)
	}
	if !s.disk.FolderExists(sr.Path) {
		s.log.Info("metadataservice: series folder missing, skipping metadata creation", "series", sr.ID, "path", sr.Path)
		return nil, nil
	}
	enabled, err := s.writers.Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterSeriesScan: WriterFactory.Enabled: %w", err)
	}
	var files []metadatafiles.MetadataFile
	for _, consumer := range enabled {
		consumerFiles := s.GetMetadataFilesForConsumer(consumer, existing)

		if row, e := s.ProcessSeriesMetadata(ctx, consumer, sr, consumerFiles, metadata.SeriesMetadataReasonScan); e != nil {
			return nil, e
		} else if row != nil {
			files = append(files, *row)
		}
		seriesImages, e := s.ProcessSeriesImages(ctx, consumer, sr, consumerFiles)
		if e != nil {
			return nil, e
		}
		files = append(files, seriesImages...)
		seasonImages, e := s.ProcessSeasonImages(ctx, consumer, sr, consumerFiles)
		if e != nil {
			return nil, e
		}
		files = append(files, seasonImages...)

		for i := range episodeFilesList {
			ef := &episodeFilesList[i]
			if row, e := s.ProcessEpisodeMetadata(ctx, consumer, sr, ef, consumerFiles); e != nil {
				return nil, e
			} else if row != nil {
				files = append(files, *row)
			}
			episodeImages, e := s.ProcessEpisodeImages(ctx, consumer, sr, ef, consumerFiles)
			if e != nil {
				return nil, e
			}
			files = append(files, episodeImages...)
		}
	}
	if upsertErr := s.upsertIfAny(ctx, files); upsertErr != nil {
		return nil, upsertErr
	}
	return files, nil
}

// CreateAfterEpisodesImported runs the post-import series-metadata
// refresh. Mirrors C# `:118-141`.
//
// Only ProcessSeriesMetadata fires (with Reason.EpisodesImported) —
// images + per-episode emission don't run on this event. The Reason
// flag also short-circuits XbmcMetadata.SeriesMetadata at the writer
// level (slice 4.BD).
//
// Skips when series.Path doesn't exist on disk.
func (s *Service) CreateAfterEpisodesImported(ctx context.Context, sr *series.Series) ([]metadatafiles.MetadataFile, error) {
	if s == nil || sr == nil {
		return nil, nil
	}
	existing, err := s.files.FilesBySeries(ctx, sr.ID)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterEpisodesImported: FilesBySeries(%d): %w", sr.ID, err)
	}
	if cleanErr := s.runCleaner(ctx, sr); cleanErr != nil {
		s.log.Warn("metadataservice: cleaner failed (continuing)", "series", sr.ID, "err", cleanErr)
	}
	if !s.disk.FolderExists(sr.Path) {
		s.log.Info("metadataservice: series folder missing, skipping metadata creation", "series", sr.ID, "path", sr.Path)
		return nil, nil
	}
	enabled, err := s.writers.Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterEpisodesImported: WriterFactory.Enabled: %w", err)
	}
	var files []metadatafiles.MetadataFile
	for _, consumer := range enabled {
		consumerFiles := s.GetMetadataFilesForConsumer(consumer, existing)
		row, e := s.ProcessSeriesMetadata(ctx, consumer, sr, consumerFiles, metadata.SeriesMetadataReasonEpisodesImported)
		if e != nil {
			return nil, e
		}
		if row != nil {
			files = append(files, *row)
		}
	}
	if upsertErr := s.upsertIfAny(ctx, files); upsertErr != nil {
		return nil, upsertErr
	}
	return files, nil
}

// CreateAfterEpisodeImport runs the per-episode-file metadata
// emission after a single episode is imported. Mirrors C# `:143-156`.
//
// IMPORTANT: the C# tree at `:149-150` passes a NEW empty
// `List<MetadataFile>` (NOT the per-series existing list) to
// ProcessEpisodeMetadata + ProcessEpisodeImages. Documented C#
// behaviour — likely a perf optimization since post-import only
// emits one episode's worth of files. The Go port mirrors that;
// existing-row dup-cleanup happens on the next scan-event.
func (s *Service) CreateAfterEpisodeImport(ctx context.Context, sr *series.Series, episodeFile *episodefiles.File) ([]metadatafiles.MetadataFile, error) {
	if s == nil || sr == nil || episodeFile == nil {
		return nil, nil
	}
	enabled, err := s.writers.Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterEpisodeImport: WriterFactory.Enabled: %w", err)
	}
	var files []metadatafiles.MetadataFile
	for _, consumer := range enabled {
		// Per C# `:149`, intentionally empty existing-files list.
		if row, e := s.ProcessEpisodeMetadata(ctx, consumer, sr, episodeFile, nil); e != nil {
			return nil, e
		} else if row != nil {
			files = append(files, *row)
		}
		rows, e := s.ProcessEpisodeImages(ctx, consumer, sr, episodeFile, nil)
		if e != nil {
			return nil, e
		}
		files = append(files, rows...)
	}
	if upsertErr := s.upsertIfAny(ctx, files); upsertErr != nil {
		return nil, upsertErr
	}
	return files, nil
}

// CreateAfterEpisodeFolder runs the per-folder emission triggered
// when a new series or season folder is created. Mirrors C# `:158-188`.
//
// The 2 folder arguments gate which subset emits:
//   - seriesFolder non-empty → ProcessSeriesMetadata (Reason.EpisodeFolderCreated)
//   - ProcessSeriesImages.
//   - seasonFolder non-empty → ProcessSeasonImages.
//   - Both empty → early-return empty result.
//
// NOTE: the C# tree at `:160` does NOT call the cleaner or the
// folder-exists check for this event. The Go port matches.
func (s *Service) CreateAfterEpisodeFolder(ctx context.Context, sr *series.Series, seriesFolder, seasonFolder string) ([]metadatafiles.MetadataFile, error) {
	if s == nil || sr == nil {
		return nil, nil
	}
	if strings.TrimSpace(seriesFolder) == "" && strings.TrimSpace(seasonFolder) == "" {
		return nil, nil
	}
	existing, err := s.files.FilesBySeries(ctx, sr.ID)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterEpisodeFolder: FilesBySeries(%d): %w", sr.ID, err)
	}
	enabled, err := s.writers.Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: CreateAfterEpisodeFolder: WriterFactory.Enabled: %w", err)
	}
	var files []metadatafiles.MetadataFile
	for _, consumer := range enabled {
		consumerFiles := s.GetMetadataFilesForConsumer(consumer, existing)
		if strings.TrimSpace(seriesFolder) != "" {
			if row, e := s.ProcessSeriesMetadata(ctx, consumer, sr, consumerFiles, metadata.SeriesMetadataReasonEpisodeFolderCreated); e != nil {
				return nil, e
			} else if row != nil {
				files = append(files, *row)
			}
			rows, e := s.ProcessSeriesImages(ctx, consumer, sr, consumerFiles)
			if e != nil {
				return nil, e
			}
			files = append(files, rows...)
		}
		if strings.TrimSpace(seasonFolder) != "" {
			rows, e := s.ProcessSeasonImages(ctx, consumer, sr, consumerFiles)
			if e != nil {
				return nil, e
			}
			files = append(files, rows...)
		}
	}
	if upsertErr := s.upsertIfAny(ctx, files); upsertErr != nil {
		return nil, upsertErr
	}
	return files, nil
}

// MoveFilesAfterRename relocates every per-series MetadataFile whose
// stored RelativePath no longer matches the writer's computed
// post-move filename. Mirrors C# `:190-229`.
//
// Iterates EVERY registered writer (enabled + disabled) — disabled
// writers may still have on-disk sidecars from when they were
// enabled, and renames should still relocate those.
//
// Per writer × per episodeFile × per matching MetadataFile:
//   - Compute the new filename via [metadata.Provider.GetFilenameAfterMove];
//   - If different from the existing path → MoveFile + update
//     RelativePath + queue for upsert.
//   - MoveFile errors are logged + swallowed (matches C# `:217-220`).
//
// Returns the moved rows for Upsert.
func (s *Service) MoveFilesAfterRename(ctx context.Context, sr *series.Series, episodeFilesList []episodefiles.File) ([]metadatafiles.MetadataFile, error) {
	if s == nil || sr == nil {
		return nil, nil
	}
	existing, err := s.files.FilesBySeries(ctx, sr.ID)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: MoveFilesAfterRename: FilesBySeries(%d): %w", sr.ID, err)
	}
	all, err := s.writers.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: MoveFilesAfterRename: WriterFactory.All: %w", err)
	}
	var moved []metadatafiles.MetadataFile
	for _, consumer := range all {
		consumerFiles := s.GetMetadataFilesForConsumer(consumer, existing)
		for i := range episodeFilesList {
			ef := &episodeFilesList[i]
			for j := range consumerFiles {
				mf := &consumerFiles[j]
				if mf.EpisodeFileID == nil || *mf.EpisodeFileID != ef.ID {
					continue
				}
				newFileName := consumer.GetFilenameAfterMove(sr, ef, mf)
				if newFileName == "" {
					continue
				}
				existingFileName := filepath.Join(sr.Path, mf.RelativePath)
				if pathEquals(newFileName, existingFileName) {
					continue
				}
				if moveErr := s.disk.MoveFile(existingFileName, newFileName); moveErr != nil {
					s.log.Warn("metadataservice: MoveFilesAfterRename move failed (continuing)", "src", existingFileName, "dst", newFileName, "err", moveErr)
					continue
				}
				rel, relErr := newFileNameRelative(sr.Path, newFileName)
				if relErr != nil {
					s.log.Warn("metadataservice: MoveFilesAfterRename relative-path failed (continuing)", "path", newFileName, "err", relErr)
					continue
				}
				mf.RelativePath = rel
				moved = append(moved, *mf)
			}
		}
	}
	if upsertErr := s.upsertIfAny(ctx, moved); upsertErr != nil {
		return nil, upsertErr
	}
	return moved, nil
}

// CanImportFile reports whether the manager handles importing a
// candidate file from a downloaded source. Mirrors C# `:231-234` —
// the metadata manager always returns false (it does not import any
// downloaded sidecar; per-writer .nfo/.xml output is emitted from
// SkyHook data, not adopted from disk).
//
// The Go port signature takes opaque `any` for LocalEpisode +
// EpisodeFile + path + extension + readOnly to avoid pulling in
// the episodeimport package as a build dep when the impl is a
// no-op return false.
func (s *Service) CanImportFile(_ any, _ *episodefiles.File, _ string, _ string, _ bool) bool {
	return false
}

// ImportFiles is the no-op companion to CanImportFile. Mirrors C#
// `:236-239` — always returns an empty list. The metadata manager
// does not adopt external sidecar files; per-writer emission is
// authoritative.
//
// The opaque-`any` LocalEpisode arg matches CanImportFile's shape;
// see that method's doc for rationale.
func (s *Service) ImportFiles(_ context.Context, _ any, _ *episodefiles.File, _ []string, _ bool) ([]metadatafiles.MetadataFile, error) {
	return nil, nil
}

// runCleaner runs the orphan-row pruner if configured. nil-tolerant;
// errors are returned to the caller (which logs + continues).
func (s *Service) runCleaner(ctx context.Context, sr *series.Series) error {
	if s.cleaner == nil {
		return nil
	}
	return s.cleaner.Clean(ctx, sr)
}

// upsertIfAny invokes [MetadataFileStore.Upsert] only when there's
// something to write. Avoids per-empty-slice round-trips that some
// store implementations may optimise away — but defensive against
// stores that don't.
func (s *Service) upsertIfAny(ctx context.Context, files []metadatafiles.MetadataFile) error {
	if len(files) == 0 {
		return nil
	}
	return s.files.Upsert(ctx, files)
}

// newFileNameRelative ports the C# `series.Path.GetRelativePath(newFileName)`
// at `:214`. Returns the suffix with leading slashes trimmed.
func newFileNameRelative(seriesPath, fullPath string) (string, error) {
	rel, err := filepath.Rel(seriesPath, fullPath)
	if err != nil {
		return "", err
	}
	return strings.TrimLeft(rel, `/\`), nil
}
