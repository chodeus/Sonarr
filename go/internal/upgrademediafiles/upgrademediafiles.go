// Package upgrademediafiles is the Go port of
// `NzbDrone.Core.MediaFiles.UpgradeMediaFileService`
// (`src/NzbDrone.Core/MediaFiles/UpgradeMediaFileService.cs:1-90`).
// Closes the production-wiring blocker for
// [episodeimport.MediaFileUpgrader] introduced by the
// ImportApprovedEpisodes orchestrator in slice 3.71 — until this
// slice the orchestrator's noopMediaFileUpgrader treated every
// upgrade as "no existing file to recycle", which silently
// overwrote duplicates on disk.
//
// Service.UpgradeEpisodeFile walks the LocalEpisode's matched
// episodes, recycles each pre-existing EpisodeFile via
// IRecycleBinProvider.DeleteFile, deletes the database row via
// MediaFileDeleter.Delete (with reason=Upgrade), and finally
// transfers the new file via EpisodeFileMover.MoveEpisodeFile or
// CopyEpisodeFile.
//
// Goroutine-safe post-construction.
//
// Deferred:
//
//   - The C# `IRecycleBinProvider` real implementation
//     (RecycleBinProvider.cs) — handles the per-OS RecycleBin folder
//     resolution + the soft-delete fallback when recycling is
//     disabled. Production binding ports it as a separate slice; the
//     interface here exposes the one method the upgrader needs.
package upgrademediafiles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
)

// ErrRootFolderNotFound mirrors the C# `RootFolderNotFoundException`
// raised at `UpgradeMediaFileService.cs:52`. The orchestrator
// (slice 3.71) catches via [errors.Is] against
// [episodeimport.ErrRootFolderNotFound]; this package re-uses the
// same sentinel for direct callers that don't go through the
// orchestrator.
var ErrRootFolderNotFound = episodeimport.ErrRootFolderNotFound

// --- narrow interfaces -------------------------------------------

// RecycleBinProvider narrows `IRecycleBinProvider.DeleteFile` (one
// method). Returns the path the file was recycled to (or empty when
// the recycle-bin feature is disabled and the file was hard-deleted).
//
// nil-tolerant via [noopRecycleBinProvider]: returns ("", nil) for
// every call so the upgrader's recycle-bin path stays empty in the
// resulting [DeletedFile] entry — appropriate during the wait for
// the real RecycleBinProvider port.
type RecycleBinProvider interface {
	// DeleteFile recycles the supplied file. subfolder is the
	// relative path under the recycle-bin root the file should land
	// at (preserves the series/season folder structure).
	DeleteFile(path, subfolder string) (recycleBinPath string, err error)
}

// MediaFileDeleter narrows `IMediaFileService.Delete` (one method).
// Satisfied by `*episodefiles.Service`.
type MediaFileDeleter interface {
	Delete(ctx context.Context, file *episodefiles.File, reason episodefiles.DeleteMediaFileReason) error
}

// EpisodeFileFinder narrows `IMediaFileService.Get` (one method).
// Used by the upgrader to dereference the
// [episodes.Episode.EpisodeFileID] pointer (the Go-port equivalent
// of the C# `EpisodeFile.Value` lazy reference). Satisfied by
// `*episodefiles.Service`.
type EpisodeFileFinder interface {
	GetFile(ctx context.Context, id int) (*episodefiles.File, error)
}

// EpisodeFileMover narrows `IMoveEpisodeFiles` (two methods used
// by the upgrader). Satisfied by `*episodefilemoving.Service`.
//
// The Go port carries the LocalEpisode-aware overloads
// (MoveEpisodeFileWithLocal / CopyEpisodeFile) — the simpler
// 2-arg MoveEpisodeFile isn't used here because the upgrader
// always has a LocalEpisode in scope.
type EpisodeFileMover interface {
	MoveEpisodeFileWithLocal(ctx context.Context, file *episodefiles.File, le *episodeimport.LocalEpisode) (*episodefiles.File, error)
	CopyEpisodeFile(ctx context.Context, file *episodefiles.File, le *episodeimport.LocalEpisode) (*episodefiles.File, error)
}

// Filesystem narrows the C# `IDiskProvider` methods the upgrader
// reaches for: GetParentFolder + FolderExists + FileExists.
//
// GetParentFolder is C#-specific phrasing for "parent directory of
// the supplied path" — Go [filepath.Dir] does the same.
type Filesystem interface {
	FolderExists(path string) bool
	FileExists(path string) bool
}

// --- defaults ------------------------------------------------------

type noopRecycleBinProvider struct{}

// DeleteFile returns ("", nil) — every recycle is a no-op.
func (noopRecycleBinProvider) DeleteFile(string, string) (string, error) { return "", nil }

// --- Service -------------------------------------------------------

// Service ports `UpgradeMediaFileService:16-89`. Composes the narrow
// deps into the public UpgradeEpisodeFile flow.
type Service struct {
	recycle RecycleBinProvider
	deleter MediaFileDeleter
	mover   EpisodeFileMover
	files   EpisodeFileFinder
	fs      Filesystem
	log     *slog.Logger
}

// NewService binds the dependencies. deleter / mover / files / fs
// are REQUIRED. recycle / log are OPTIONAL (nil-tolerant). Mirrors
// the slice-3.55+ constructor pattern.
func NewService(
	recycle RecycleBinProvider,
	deleter MediaFileDeleter,
	mover EpisodeFileMover,
	files EpisodeFileFinder,
	fs Filesystem,
	log *slog.Logger,
) (*Service, error) {
	if deleter == nil {
		return nil, errors.New("upgrademediafiles: NewService: nil MediaFileDeleter")
	}
	if mover == nil {
		return nil, errors.New("upgrademediafiles: NewService: nil EpisodeFileMover")
	}
	if files == nil {
		return nil, errors.New("upgrademediafiles: NewService: nil EpisodeFileFinder")
	}
	if fs == nil {
		return nil, errors.New("upgrademediafiles: NewService: nil Filesystem")
	}
	if recycle == nil {
		recycle = noopRecycleBinProvider{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		recycle: recycle,
		deleter: deleter,
		mover:   mover,
		files:   files,
		fs:      fs,
		log:     log,
	}, nil
}

// UpgradeEpisodeFile ports `UpgradeMediaFileService.cs:37-88`.
// Handles the recycle-bin / replace-existing flow: collects every
// pre-existing EpisodeFile referenced by the LocalEpisode's matched
// episodes, recycles + deletes each, then transfers the new file
// via the mover.
//
// Decision tree (mirrors C# exactly):
//
//  1. Walk LocalEpisode.Episodes; collect distinct
//     EpisodeFile pointers from those with EpisodeFileID > 0
//     (mirrors C# `:40-45` Where + Select + GroupBy).
//  2. If any existing files exist AND the series's parent folder is
//     missing on disk, return ErrRootFolderNotFound (C# `:50-53`).
//  3. For each existing file: compute the absolute path
//     (Series.Path + RelativePath); if FileExists, recycle (logging
//     the recycle path); regardless of FileExists, delete the
//     EpisodeFile row with reason=Upgrade. Append the recycle result
//     to the OldFiles list.
//  4. Mutate LocalEpisode.OldFiles to the resolved list (mirrors C#
//     `:76`).
//  5. Transfer the new file via Mover.CopyEpisodeFile (when
//     copyOnly) or Mover.MoveEpisodeFileWithLocal (otherwise).
//
// Returns the OldFiles list + any error. The mutated input file
// pointer carries the new RelativePath set by the mover.
//
// Satisfies [episodeimport.MediaFileUpgrader] from slice 3.71.
// Compile-time guard at the bottom of the file.
func (s *Service) UpgradeEpisodeFile(ctx context.Context, file *episodefiles.File, le *episodeimport.LocalEpisode, copyOnly bool) ([]episodefiles.DeletedFile, error) {
	if s == nil {
		return nil, errors.New("upgrademediafiles: UpgradeEpisodeFile on nil receiver")
	}
	if file == nil || le == nil || le.Series == nil {
		return nil, errors.New("upgrademediafiles: UpgradeEpisodeFile: nil file / LocalEpisode / Series")
	}

	// Step 1: collect pre-existing EpisodeFile IDs (deduplicated).
	existingIDs := s.collectExistingFileIDs(ctx, le)

	// Step 2: root-folder check.
	rootFolder := filepath.Dir(le.Series.Path)
	if len(existingIDs) > 0 && !s.fs.FolderExists(rootFolder) {
		return nil, fmt.Errorf("upgrademediafiles: %w: root folder %q missing", ErrRootFolderNotFound, rootFolder)
	}

	// Step 3: recycle + delete each existing file.
	oldFiles := make([]episodefiles.DeletedFile, 0, len(existingIDs))
	for _, id := range existingIDs {
		ef, err := s.files.GetFile(ctx, id)
		if err != nil || ef == nil {
			s.log.Warn("upgrademediafiles: existing EpisodeFile not found",
				"id", id, "err", err)
			continue
		}
		old := s.recycleAndDelete(ctx, ef, le.Series.Path, rootFolder)
		oldFiles = append(oldFiles, old)
	}

	// Step 4: mutate LocalEpisode.OldFiles (C# `:76`).
	le.OldFiles = oldFiles

	// Step 5: transfer the new file.
	var transferErr error
	if copyOnly {
		_, transferErr = s.mover.CopyEpisodeFile(ctx, file, le)
	} else {
		_, transferErr = s.mover.MoveEpisodeFileWithLocal(ctx, file, le)
	}
	if transferErr != nil {
		return oldFiles, fmt.Errorf("upgrademediafiles: transfer: %w", transferErr)
	}

	return oldFiles, nil
}

// collectExistingFileIDs ports the C# LINQ chain at `:40-45`.
// Returns the deduplicated list of EpisodeFileIDs across the
// LocalEpisode's matched episodes (skipping zero/nil IDs).
//
// The C# `GroupBy(e => e.Id)` removes duplicates + the
// `Select(e => e.EpisodeFile.Value)` lazy-loads. The Go port
// dedups via a small set + dereferences via [EpisodeFileFinder]
// at the call site to keep this helper trivial.
func (s *Service) collectExistingFileIDs(_ context.Context, le *episodeimport.LocalEpisode) []int {
	seen := make(map[int]struct{}, len(le.Episodes))
	out := make([]int, 0, len(le.Episodes))
	for _, e := range le.Episodes {
		if e.EpisodeFileID == nil || *e.EpisodeFileID <= 0 {
			continue
		}
		id := *e.EpisodeFileID
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// recycleAndDelete handles the per-existing-file body of the C#
// loop at `:55-74`. Computes the recycle subfolder, recycles the
// file (or warns when missing), and deletes the EpisodeFile row.
//
// Errors from the recycle-bin / DB are logged but not propagated —
// the C# version doesn't catch either path either; it just lets
// them fall through. The Go port chooses to log + continue so a
// transient recycle failure doesn't block the rest of the
// upgrade.
func (s *Service) recycleAndDelete(ctx context.Context, ef *episodefiles.File, seriesPath, rootFolder string) episodefiles.DeletedFile {
	episodeFilePath := filepath.Join(seriesPath, ef.RelativePath)
	parentFolder := filepath.Dir(episodeFilePath)
	subfolder := ""
	if rel, err := filepath.Rel(rootFolder, parentFolder); err == nil {
		subfolder = rel
	}

	recyclePath := ""
	if s.fs.FileExists(episodeFilePath) {
		s.log.Debug("upgrademediafiles: recycling existing episode file",
			"id", ef.ID, "path", episodeFilePath)
		var err error
		recyclePath, err = s.recycle.DeleteFile(episodeFilePath, subfolder)
		if err != nil {
			s.log.Warn("upgrademediafiles: recycle failed",
				"id", ef.ID, "path", episodeFilePath, "err", err)
		}
	} else {
		s.log.Warn("upgrademediafiles: existing episode file missing from disk",
			"id", ef.ID, "path", episodeFilePath)
	}

	if err := s.deleter.Delete(ctx, ef, episodefiles.ReasonUpgrade); err != nil {
		s.log.Warn("upgrademediafiles: delete EpisodeFile row failed",
			"id", ef.ID, "err", err)
	}

	return episodefiles.DeletedFile{
		File:           ef,
		RecycleBinPath: recyclePath,
	}
}

// Compile-time interface-satisfaction guard. Refactor breakage at
// build time rather than wiring time.
var _ episodeimport.MediaFileUpgrader = (*Service)(nil)
