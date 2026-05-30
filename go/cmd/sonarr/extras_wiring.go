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
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/parsingservice"
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

func (a extraFileStoreAdapter) AllBySeriesID(ctx context.Context, seriesID int) ([]extrafiles.ExtraFile, error) {
	return a.repo.AllBySeriesID(ctx, seriesID)
}

func (a extraFileStoreAdapter) Update(ctx context.Context, f *extrafiles.ExtraFile) error {
	return a.repo.Update(ctx, f)
}

// subtitleFileStoreAdapter persists subtitle-file rows (Insert, as above).
type subtitleFileStoreAdapter struct{ repo *subtitlefiles.Repo }

func (a subtitleFileStoreAdapter) Upsert(ctx context.Context, f *subtitlefiles.SubtitleFile) error {
	_, err := a.repo.Insert(ctx, f)
	return err
}

func (a subtitleFileStoreAdapter) AllBySeriesID(ctx context.Context, seriesID int) ([]subtitlefiles.SubtitleFile, error) {
	return a.repo.AllBySeriesID(ctx, seriesID)
}

func (a subtitleFileStoreAdapter) Update(ctx context.Context, f *subtitlefiles.SubtitleFile) error {
	return a.repo.Update(ctx, f)
}

// extrasSampleAdapter bridges [extras.SampleChecker] to the existing
// *episodeimport.SampleDetector for the subtitle single-video fallback.
type extrasSampleAdapter struct{ d *episodeimport.SampleDetector }

func (a extrasSampleAdapter) IsSampleVideo(ser *series.Series, path string) bool {
	return a.d.IsSampleForSeries(context.Background(), ser, path, false) == episodeimport.DetectSampleSample
}

// extrasEpisodeResolver bridges [extras.EpisodeFileResolver] to the
// parsing service: parse the companion path, map it within the series, and
// return the single EpisodeFile its episodes share (ambiguous / unmatched
// → ok=false). Mirrors the C# ExistingExtraImporter's
// AggregationService.Augment → single EpisodeFileId resolution.
type extrasEpisodeResolver struct {
	parsing *parsingservice.Service
	log     *slog.Logger
}

func (a extrasEpisodeResolver) ResolveEpisodeFile(ctx context.Context, ser *series.Series, path string) (int, int, bool) {
	parsed := parser.ParsePath(path)
	if parsed == nil || a.parsing == nil {
		return 0, 0, false
	}
	tvdb := 0
	if ser != nil {
		tvdb = ser.TvdbID
	}
	subj, err := a.parsing.Map(ctx, parsed, tvdb, 0, "", nil)
	if err != nil || subj == nil || len(subj.Episodes) == 0 {
		return 0, 0, false
	}
	fileID, season := 0, 0
	for i := range subj.Episodes {
		ef := subj.Episodes[i].EpisodeFileID
		if ef == nil || *ef == 0 {
			return 0, 0, false
		}
		if i == 0 {
			fileID, season = *ef, subj.Episodes[i].SeasonNumber
		} else if *ef != fileID {
			// Companion maps to episodes spanning >1 file — ambiguous.
			return 0, 0, false
		}
	}
	return fileID, season, true
}

// extrasImporterAdapter implements [episodeimport.ExtrasImporter] over the
// [extras.ExtraService] orchestrator (import + rename) plus the
// [extras.ExistingExtraService] (script-import existing-file arm).
type extrasImporterAdapter struct {
	svc      *extras.ExtraService
	existing *extras.ExistingExtraService
	log      *slog.Logger
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

// ImportExtraFiles is the script-import arm (J482): track the companion
// files a custom import script left on disk (PossibleExtraFiles), matched
// to their EpisodeFile, without moving them. Ports
// IExistingExtraFiles.ImportExtraFiles.
func (a extrasImporterAdapter) ImportExtraFiles(le *episodeimport.LocalEpisode, possibleExtraFiles []string, _ string) error {
	if le == nil || le.Series == nil || a.existing == nil || len(possibleExtraFiles) == 0 {
		return nil
	}
	_, err := a.existing.ImportExisting(context.Background(), le.Series, possibleExtraFiles)
	return err
}

// MoveFilesAfterRename relocates the imported companions when the import
// script renamed the file (J481). Delegates to the orchestrator's
// per-manager MoveFilesAfterRename for this single episode file.
func (a extrasImporterAdapter) MoveFilesAfterRename(le *episodeimport.LocalEpisode, file *episodefiles.File) error {
	if le == nil || le.Series == nil || file == nil {
		return nil
	}
	_, err := a.svc.MoveFilesAfterRename(context.Background(), le.Series, []*episodefiles.File{file})
	return err
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
	parsing *parsingservice.Service,
	cfg extrasConfig,
	log *slog.Logger,
) (episodeimport.ExtrasImporter, *extras.ExtraService, error) {
	tr := extrasTransferAdapter{fs: fs}
	extraStore := extraFileStoreAdapter{repo: extraRepo}
	subStore := subtitleFileStoreAdapter{repo: subRepo}
	// diskProv satisfies extras.FileMover (MoveFile) for the post-rename move.
	other := extras.NewOtherExtraManager(extraStore, tr, diskProv, cfg, log)
	sub := extras.NewSubtitleManager(subStore, tr, diskProv, cfg, diskProv, extrasSampleAdapter{d: sampleDetector}, log)
	svc, err := extras.NewExtraService([]extras.Manager{sub, other}, diskProv, cfg, log)
	if err != nil {
		return nil, nil, err
	}
	existing, err := extras.NewExistingExtraService(subStore, extraStore, extrasEpisodeResolver{parsing: parsing, log: log}, log)
	if err != nil {
		return nil, nil, err
	}
	return extrasImporterAdapter{svc: svc, existing: existing, log: log}, svc, nil
}

// Compile-time interface guards.
var (
	_ extras.FileTransferrer       = extrasTransferAdapter{}
	_ extras.ExtraFileStore        = extraFileStoreAdapter{}
	_ extras.SubtitleStore         = subtitleFileStoreAdapter{}
	_ extras.SampleChecker         = extrasSampleAdapter{}
	_ episodeimport.ExtrasImporter = extrasImporterAdapter{}
)
