package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/episodefiles"
)

// DeletedEpisodeFileSpecificationName is the canonical Spec.Name() identifier.
const DeletedEpisodeFileSpecificationName = "DeletedEpisodeFile"

// AutoUnmonitorReader narrows [config.Provider].AutoUnmonitorPreviouslyDownloadedEpisodes
// (one method) — the master switch that decides whether a download whose
// file was deleted from disk should be left for the disk-scan to
// unmonitor rather than re-grabbed.
type AutoUnmonitorReader interface {
	AutoUnmonitorPreviouslyDownloadedEpisodes() bool
}

// FileExistenceChecker narrows [disk.Provider].FileExists (one method).
// Production binding: [*disk.Provider] satisfies it structurally.
type FileExistenceChecker interface {
	FileExists(path string) bool
}

// DeletedEpisodeFileSpecification holds (temporarily rejects) a release
// for an episode whose file row exists in the DB but is missing from
// disk, when AutoUnmonitorPreviouslyDownloadedEpisodes is enabled. The
// next disk scan will unmonitor that episode, so re-grabbing it now would
// fight the user's "I deleted this on purpose" intent — the release is
// skipped until the scan reconciles.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RssSync/DeletedEpisodeFileSpecification.cs:1-72`.
//
// # Algorithm (C# `:29-62`)
//
//  1. AutoUnmonitorPreviouslyDownloadedEpisodes == false → Accept.
//  2. SearchCriteria present → Accept (skip during search).
//  3. Gather the distinct existing episode files; if ANY is missing from
//     disk → Reject [ReasonEpisodeNotMonitored] (Temporary — the scan
//     will reconcile, then the release can be reconsidered).
//
// Temporary rejection + Disk priority — mirrors the C# `RejectionType.Temporary`
// + `SpecificationPriority.Disk` (the file-existence probe is I/O, so it
// runs last).
type DeletedEpisodeFileSpecification struct {
	cfg   AutoUnmonitorReader
	disk  FileExistenceChecker
	files EpisodeFileResolver
	log   *slog.Logger
}

// NewDeletedEpisodeFileSpecification builds the spec. cfg / disk / files
// MUST be non-nil; log defaults to [slog.Default].
func NewDeletedEpisodeFileSpecification(cfg AutoUnmonitorReader, disk FileExistenceChecker, files EpisodeFileResolver, log *slog.Logger) (*DeletedEpisodeFileSpecification, error) {
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: DeletedEpisodeFileSpecification requires a non-nil AutoUnmonitorReader")
	}
	if disk == nil {
		return nil, fmt.Errorf("decisionengine: DeletedEpisodeFileSpecification requires a non-nil FileExistenceChecker")
	}
	if files == nil {
		return nil, fmt.Errorf("decisionengine: DeletedEpisodeFileSpecification requires a non-nil EpisodeFileResolver")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DeletedEpisodeFileSpecification{cfg: cfg, disk: disk, files: files, log: log}, nil
}

// Name returns the canonical spec name.
func (*DeletedEpisodeFileSpecification) Name() string { return DeletedEpisodeFileSpecificationName }

// Kind reports Temporary — the disk scan will reconcile the missing file,
// after which the release can be reconsidered.
func (*DeletedEpisodeFileSpecification) Kind() RejectionKind { return RejectionTemporary }

// Priority returns Disk — the per-file FileExists probe is I/O.
func (*DeletedEpisodeFileSpecification) Priority() SpecPriority { return SpecPriorityDisk }

// IsSatisfiedBy mirrors the C# method at `:29-62`.
func (s *DeletedEpisodeFileSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil {
		return Accept()
	}
	if !s.cfg.AutoUnmonitorPreviouslyDownloadedEpisodes() {
		return Accept()
	}
	if criteria, ok := info.SearchCriteria.(*SearchCriteria); ok && criteria != nil {
		s.log.Debug("deletedEpisodeFile: skipping check during search")
		return Accept()
	}

	seen := make(map[int]struct{})
	for _, ep := range subject.Episodes {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			continue
		}
		file, err := s.files.GetFile(ctx, *ep.EpisodeFileID)
		if err != nil {
			if errors.Is(err, episodefiles.ErrNotFound) {
				continue
			}
			s.log.Warn("deletedEpisodeFile: episode-file lookup failed; skipping",
				"episodeFileId", *ep.EpisodeFileID, "err", err)
			continue
		}
		if file == nil {
			continue
		}
		if _, dup := seen[file.ID]; dup {
			continue
		}
		seen[file.ID] = struct{}{}

		fullPath := filepath.Join(subject.Series.Path, file.RelativePath)
		if !s.disk.FileExists(fullPath) {
			s.log.Debug("deletedEpisodeFile: file in DB but missing on disk; rejecting until disk scan reconciles",
				"episodeFileId", file.ID, "path", fullPath)
			return Reject(ReasonEpisodeNotMonitored, "Episode is not monitored")
		}
	}

	return Accept()
}

// Compile-time interface satisfaction.
var _ Specification = (*DeletedEpisodeFileSpecification)(nil)
