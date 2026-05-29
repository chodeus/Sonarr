// Package downloadprocessing ports the auto-import arm of the C#
// download lifecycle: `DownloadProcessingService.Execute(
// ProcessMonitoredDownloadsCommand)` →
// `CompletedDownloadService.Import(trackedDownload)`
// (`src/NzbDrone.Core/Download/DownloadProcessingService.cs` +
// `CompletedDownloadService.cs`).
//
// After downloadmonitoring.Refresh populates the tracked-download cache
// from the live clients (slice J394), this service walks that cache and
// auto-imports every download the client reports Completed, by handing
// its output path to the existing import pipeline
// (downloadedimport.Service.ProcessPath — the same primitive ManualImport
// + DownloadedEpisodesScan use).
//
// J399 scope — the happy path: completed + matched (RemoteEpisode.Series
// non-nil) downloads import via ProcessPath; when every result imported,
// the tracked download is marked Imported. DEFERRED to follow-ups (each
// a documented gap, NOT silently skipped):
//   - the rejection / partial-import / RejectedImportService handling
//     (C# CompletedDownloadService.cs:51-120) — left at Downloading for
//     re-attempt on the next cycle;
//   - script-import (SetImportItem) + ValidatePath + remove-completed-
//     download cleanup + DownloadCompletedEvent publication;
//   - the import-blocked state for unparsed (RemoteEpisode==nil) downloads
//     (logged + skipped here).
package downloadprocessing

import (
	"context"
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// TrackedDownloadStore reads the in-memory tracked-download cache.
// *trackeddownloads.Service satisfies it via GetTrackedDownloads.
type TrackedDownloadStore interface {
	GetTrackedDownloads() []*trackeddownloads.TrackedDownload
}

// Importer runs the path-based import pipeline.
// *downloadedimport.Service satisfies it via ProcessPath.
type Importer interface {
	ProcessPath(path string, mode episodeimport.ImportMode, ser *series.Series, dci *downloadclients.Item) []*episodeimport.ImportResult
}

// Service auto-imports completed tracked downloads.
type Service struct {
	tracked  TrackedDownloadStore
	importer Importer
	log      *slog.Logger
}

// NewService binds the tracked-download store + importer. Both required.
func NewService(tracked TrackedDownloadStore, importer Importer, log *slog.Logger) (*Service, error) {
	if tracked == nil || importer == nil {
		return nil, errors.New("downloadprocessing: nil tracked store or importer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{tracked: tracked, importer: importer, log: log}, nil
}

// ProcessTrackedDownloads ports `DownloadProcessingService.Execute` +
// the happy path of `CompletedDownloadService.Import`. Returns the
// number of tracked downloads that fully imported.
func (s *Service) ProcessTrackedDownloads(ctx context.Context) int {
	if s == nil {
		return 0
	}
	imported := 0
	for _, td := range s.tracked.GetTrackedDownloads() {
		if !s.shouldImport(td) {
			continue
		}
		if s.importOne(ctx, td) {
			imported++
		}
	}
	if imported > 0 {
		s.log.LogAttrs(ctx, slog.LevelInfo, "downloadprocessing: auto-imported completed downloads",
			slog.Int("count", imported))
	}
	return imported
}

// shouldImport reports whether a tracked download is a candidate for
// auto-import: still in the Downloading state, the client reports it
// Completed, and it has a matched series (C# guards at
// CompletedDownloadService.cs:28-44 + DownloadProcessingService.cs:40-63).
func (s *Service) shouldImport(td *trackeddownloads.TrackedDownload) bool {
	if td == nil || td.DownloadItem == nil {
		return false
	}
	if td.State != trackeddownloads.StateDownloading {
		return false // already importing/imported/failed — skip
	}
	if td.DownloadItem.Status != downloadclients.DownloadItemStatusCompleted {
		return false
	}
	if td.RemoteEpisode == nil || td.RemoteEpisode.Series == nil {
		// Unparsed/unmatched — C# sets ImportBlocked + warns; the Go
		// port logs + skips (the queue still surfaces the row).
		s.log.Debug("downloadprocessing: completed download not matched to a series; skipping auto-import",
			"downloadId", td.DownloadID)
		return false
	}
	return true
}

// importOne runs the import for one completed download and marks it
// Imported when every result imported. Returns true on full import.
func (s *Service) importOne(ctx context.Context, td *trackeddownloads.TrackedDownload) bool {
	outputPath := td.DownloadItem.OutputPath.FullPath()
	if outputPath == "" {
		s.log.Debug("downloadprocessing: completed download has no output path; skipping", "downloadId", td.DownloadID)
		return false
	}
	results := s.importer.ProcessPath(outputPath, episodeimport.ImportModeAuto, td.RemoteEpisode.Series, td.DownloadItem)
	if len(results) == 0 {
		s.log.Debug("downloadprocessing: no importable files found", "path", outputPath, "downloadId", td.DownloadID)
		return false
	}
	if !allImported(results) {
		// Partial / rejected — leave Downloading for the next cycle (the
		// rejection-handling state machine is a documented follow-up).
		s.log.Debug("downloadprocessing: not all files imported; leaving for retry", "downloadId", td.DownloadID)
		return false
	}
	td.State = trackeddownloads.StateImported
	s.log.LogAttrs(ctx, slog.LevelInfo, "downloadprocessing: imported",
		slog.String("downloadId", td.DownloadID), slog.String("path", outputPath))
	return true
}

// allImported reports whether every result imported (and there's at
// least one). Mirrors the C# all-imported check.
func allImported(results []*episodeimport.ImportResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r == nil || r.ResultType() != episodeimport.ImportResultImported {
			return false
		}
	}
	return true
}
