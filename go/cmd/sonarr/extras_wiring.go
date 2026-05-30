// Boot-time wiring for the extra-file import subsystem (`internal/extras`
// — the C# `Extras/ExtraService`). Builds the per-type managers
// (Subtitle, Other) + the orchestrator and adapts them to the
// `episodeimport.ExtrasImporter` seam the import pipeline calls after a
// successful episode-file import (replacing the prior noopExtrasImporter).
package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/extrafiles"
	"sonarr.tv/sonarr/internal/extras"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/subtitlefiles"
)

// extrasConfig is the union of the config surfaces the extras subsystem
// reads — ImportExtraFiles + ExtraFileExtensions (orchestrator) and
// CopyUsingHardlinks (the read-only transfer-mode choice). *config.Provider
// satisfies all three.
type extrasConfig interface {
	extras.ExtraConfig
	extras.HardlinkConfig
}

// extrasTransferAdapter bridges [extras.FileTransferrer] to the existing
// *episodefilemoving.DiskFilesystem (the production file mover), mapping
// the package-local TransferMode.
type extrasTransferAdapter struct {
	fs *episodefilemoving.DiskFilesystem
}

func (a extrasTransferAdapter) TransferFile(src, dst string, mode extras.TransferMode) error {
	return a.fs.TransferFile(src, dst, mapExtrasTransferMode(mode))
}

func mapExtrasTransferMode(m extras.TransferMode) episodefilemoving.TransferMode {
	switch m {
	case extras.TransferModeCopy:
		return episodefilemoving.TransferModeCopy
	case extras.TransferModeHardLinkOrCopy:
		return episodefilemoving.TransferModeHardLinkOrCopy
	default:
		return episodefilemoving.TransferModeMove
	}
}

// extraFileStoreAdapter persists "other" extra-file rows. The C# Upsert
// inserts when Id==0 (the import-produced row always is), so a plain
// Insert is faithful for this path.
type extraFileStoreAdapter struct{ repo *extrafiles.Repo }

func (a extraFileStoreAdapter) Upsert(ctx context.Context, f *extrafiles.ExtraFile) error {
	_, err := a.repo.Insert(ctx, f)
	return err
}

// subtitleFileStoreAdapter persists subtitle-file rows (Insert, as above).
type subtitleFileStoreAdapter struct{ repo *subtitlefiles.Repo }

func (a subtitleFileStoreAdapter) Upsert(ctx context.Context, f *subtitlefiles.SubtitleFile) error {
	_, err := a.repo.Insert(ctx, f)
	return err
}

// extrasSampleAdapter bridges [extras.SampleChecker] to the existing
// *episodeimport.SampleDetector for the subtitle single-video fallback.
type extrasSampleAdapter struct{ d *episodeimport.SampleDetector }

func (a extrasSampleAdapter) IsSampleVideo(ser *series.Series, path string) bool {
	return a.d.IsSampleForSeries(context.Background(), ser, path, false) == episodeimport.DetectSampleSample
}

// extrasImporterAdapter implements [episodeimport.ExtrasImporter] over the
// [extras.ExtraService] orchestrator. Only [ImportEpisode] (the main
// auto-import path) is live; the script-import + post-rename arms remain
// no-ops (a follow-up — they need the managers' MoveFilesAfterRename).
type extrasImporterAdapter struct {
	svc *extras.ExtraService
	log *slog.Logger
}

func (a extrasImporterAdapter) ImportEpisode(le *episodeimport.LocalEpisode, file *episodefiles.File, copyOnly bool) error {
	if le == nil || le.Series == nil || file == nil {
		return nil
	}
	in := extras.ImportInput{
		Series:      le.Series,
		EpisodeFile: file,
		SourcePath:  le.Path,
		FileInfo:    le.FileEpisodeInfo,
		ReadOnly:    copyOnly,
		Recursive:   le.FolderEpisodeInfo != nil,
	}
	n, err := a.svc.ImportEpisode(context.Background(), in)
	if err != nil {
		return err
	}
	if n > 0 {
		a.log.Debug("extras: imported companion files", "count", n, "episodeFile", file.RelativePath)
	}
	return nil
}

// ImportExtraFiles is the script-import arm — deferred (the C# variant
// re-runs the extras detection on the script result). No-op for now.
func (extrasImporterAdapter) ImportExtraFiles(*episodeimport.LocalEpisode, []string, string) error {
	return nil
}

// MoveFilesAfterRename is the post-rename companion-move arm — deferred
// (needs the managers' MoveFilesAfterRename). No-op for now.
func (extrasImporterAdapter) MoveFilesAfterRename(*episodeimport.LocalEpisode, *episodefiles.File) error {
	return nil
}

// buildExtrasImporter wires the extras subsystem into an
// [episodeimport.ExtrasImporter]. diskProv supplies both the
// source-folder enumeration (orchestrator) and the subtitle single-video
// fallback's video listing.
func buildExtrasImporter(
	fs *episodefilemoving.DiskFilesystem,
	extraRepo *extrafiles.Repo,
	subRepo *subtitlefiles.Repo,
	diskProv *disk.Provider,
	sampleDetector *episodeimport.SampleDetector,
	cfg extrasConfig,
	log *slog.Logger,
) (episodeimport.ExtrasImporter, error) {
	tr := extrasTransferAdapter{fs: fs}
	other := extras.NewOtherExtraManager(extraFileStoreAdapter{repo: extraRepo}, tr, cfg, log)
	sub := extras.NewSubtitleManager(subtitleFileStoreAdapter{repo: subRepo}, tr, cfg, diskProv, extrasSampleAdapter{d: sampleDetector}, log)
	svc, err := extras.NewExtraService([]extras.Manager{sub, other}, diskProv, cfg, log)
	if err != nil {
		return nil, err
	}
	return extrasImporterAdapter{svc: svc, log: log}, nil
}

// Compile-time interface guards.
var (
	_ extras.FileTransferrer       = extrasTransferAdapter{}
	_ extras.ExtraFileStore        = extraFileStoreAdapter{}
	_ extras.SubtitleStore         = subtitleFileStoreAdapter{}
	_ extras.SampleChecker         = extrasSampleAdapter{}
	_ episodeimport.ExtrasImporter = extrasImporterAdapter{}
)
