// MediaFile table cleanup. Ports
// `NzbDrone.Core.MediaFiles.MediaFileTableCleanupService` at
// `src/NzbDrone.Core/MediaFiles/MediaFileTableCleanupService.cs:1-86`.
//
// The C# service is the disk-scan workflow's reconciliation step: given
// a Series and the set of file paths the scanner observed on disk,
// drop any persisted EpisodeFile rows whose path is no longer on disk
// or whose linked Episode set is empty, then clear EpisodeFileId on
// any Episode that pointed at a now-deleted file.
//
// Port shape:
//
//   - CleanupService is a thin wrapper over the existing Service +
//     episodes.Service. It does not own state; it composes the two
//     services that already encapsulate the persistence + event
//     publishing.
//   - Path comparison uses `filepath.Clean(filepath.Join(...))`,
//     matching the existing `FilterExistingFiles` pattern at
//     `service.go:228-255` (Unix-style raw equality after normalising
//     separators; Windows case-folding is a future tweak when the
//     port has cross-platform CI).
//
// Deferred:
//
//   - The commented-out C# `ParsingService.GetLocalEpisode` re-parse
//     branch (`MediaFileTableCleanupService.cs:59-66`) is dead code
//     in the C# tree too — left as a comment in the source. The Go
//     port omits it entirely; if the parser-based reconciliation ever
//     re-activates, it ports as a separate slice.
//   - Per-row error-swallowing matches C# (catch + log, continue) so
//     a single bad row can't stop the cleanup pass.

package episodefiles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// CleanupService is the MediaFileTableCleanupService port. One per
// process; constructed at boot wired against the live Service +
// episodes.Service.
type CleanupService struct {
	files    *Service
	episodes *episodes.Service
	log      *slog.Logger
}

// NewCleanupService builds a CleanupService. files and eps must be
// non-nil; log defaults to slog.Default when nil.
func NewCleanupService(files *Service, eps *episodes.Service, log *slog.Logger) (*CleanupService, error) {
	if files == nil {
		return nil, errors.New("episodefiles: NewCleanupService: nil files service")
	}
	if eps == nil {
		return nil, errors.New("episodefiles: NewCleanupService: nil episodes service")
	}
	if log == nil {
		log = slog.Default()
	}
	return &CleanupService{files: files, episodes: eps, log: log}, nil
}

// Clean reconciles the EpisodeFiles + Episodes tables for `parent`
// against the supplied filesOnDisk list.
//
// Algorithm (`MediaFileTableCleanupService.cs:31-83`):
//
//  1. For each EpisodeFile in series:
//     a. If its expected on-disk path is not in filesOnDisk:
//     Delete with ReasonMissingFromDisk.
//     b. Else if no Episode rows reference its Id:
//     Delete with ReasonNoLinkedEpisodes.
//
//  2. For each Episode whose EpisodeFileID points at a now-removed
//     row (or any non-existent row): clear EpisodeFileID via
//     UpdateEpisode.
//
// Per-row failures are logged and the loop continues — matches the
// C# try/catch at `:43-71` so a single corrupt row can't halt the
// scan. Returns the number of files deleted + episode rows updated.
func (c *CleanupService) Clean(ctx context.Context, parent *series.Series, filesOnDisk []string) (deletedFiles int, clearedEpisodes int, err error) {
	if parent == nil {
		return 0, 0, errors.New("episodefiles: CleanupService.Clean: nil series")
	}

	seriesFiles, err := c.files.GetFilesBySeries(ctx, parent.ID)
	if err != nil {
		return 0, 0, fmt.Errorf("episodefiles: CleanupService.Clean: GetFilesBySeries(%d): %w", parent.ID, err)
	}
	eps, err := c.episodes.GetEpisodesBySeries(ctx, parent.ID)
	if err != nil {
		return 0, 0, fmt.Errorf("episodefiles: CleanupService.Clean: GetEpisodesBySeries(%d): %w", parent.ID, err)
	}

	// Snapshot of paths the scanner saw, normalised for direct lookup.
	onDisk := make(map[string]struct{}, len(filesOnDisk))
	for _, p := range filesOnDisk {
		onDisk[filepath.Clean(p)] = struct{}{}
	}

	// Snapshot of Episode → EpisodeFileID so we know which file IDs
	// have at least one linked episode.
	linked := make(map[int]bool, len(eps))
	for _, e := range eps {
		if e.EpisodeFileID != nil && *e.EpisodeFileID > 0 {
			linked[*e.EpisodeFileID] = true
		}
	}

	// Track which file IDs were deleted in this pass — used in the
	// second phase below to decide which Episode rows need their
	// EpisodeFileID cleared.
	deleted := make(map[int]bool, len(seriesFiles))

	for i := range seriesFiles {
		f := seriesFiles[i]
		expected := filepath.Clean(filepath.Join(parent.Path, f.RelativePath))

		if _, present := onDisk[expected]; !present {
			c.log.LogAttrs(ctx, slog.LevelDebug,
				"episodefiles: file no longer on disk, removing from db",
				slog.String("path", expected),
				slog.Int("id", f.ID))
			if err := c.files.Delete(ctx, &f, ReasonMissingFromDisk); err != nil {
				c.log.LogAttrs(ctx, slog.LevelError,
					"episodefiles: cleanup Delete(missing-from-disk) failed",
					slog.Int("id", f.ID),
					slog.Any("err", err))
				continue
			}
			deleted[f.ID] = true
			deletedFiles++
			continue
		}

		if !linked[f.ID] {
			c.log.LogAttrs(ctx, slog.LevelDebug,
				"episodefiles: no linked episodes, removing from db",
				slog.String("path", expected),
				slog.Int("id", f.ID))
			if err := c.files.Delete(ctx, &f, ReasonNoLinkedEpisodes); err != nil {
				c.log.LogAttrs(ctx, slog.LevelError,
					"episodefiles: cleanup Delete(no-linked) failed",
					slog.Int("id", f.ID),
					slog.Any("err", err))
				continue
			}
			deleted[f.ID] = true
			deletedFiles++
		}
	}

	// Build the survivor set so the second phase knows which file IDs
	// still exist — Episode rows pointing at any other ID need their
	// link cleared. The C# version compares against `seriesFiles`
	// post-mutation; the Go port computes survivors explicitly to
	// keep the test surface deterministic.
	survivors := make(map[int]bool, len(seriesFiles))
	for _, f := range seriesFiles {
		if !deleted[f.ID] {
			survivors[f.ID] = true
		}
	}

	for i := range eps {
		e := eps[i]
		if e.EpisodeFileID == nil || *e.EpisodeFileID == 0 {
			continue
		}
		if survivors[*e.EpisodeFileID] {
			continue
		}
		// Either points at a row we just deleted OR at a row that
		// never existed under this series (orphaned link).
		e.EpisodeFileID = nil
		if err := c.episodes.UpdateEpisode(ctx, &e); err != nil {
			c.log.LogAttrs(ctx, slog.LevelError,
				"episodefiles: cleanup UpdateEpisode(clear-file-id) failed",
				slog.Int("episodeId", e.ID),
				slog.Any("err", err))
			continue
		}
		clearedEpisodes++
	}

	return deletedFiles, clearedEpisodes, nil
}
