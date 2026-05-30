package downloadprocessing

import (
	"context"
	"log/slog"
	"slices"

	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// IndexerSettingsLookup is the narrow indexer-settings surface
// RejectedImportService reads. *indexers.CachedSettingsProvider
// satisfies it via GetSettings.
type IndexerSettingsLookup interface {
	GetSettings(ctx context.Context, indexerID int) (*indexers.CachedIndexerSettings, error)
}

// RejectedImportService ports C# `RejectedImportService`
// (`src/NzbDrone.Core/Download/RejectedImportService.cs`). When a
// completed download's single import result was REJECTED, it decides
// whether the download should be hard-failed (when the rejection is a
// dangerous/executable/user-rejected file AND the source indexer is
// configured to fail such downloads) or merely warned (so the user can
// intervene). Returns true when it handled the rejection (the caller
// then routes the tracked download to ImportBlocked or FailedPending).
type RejectedImportService struct {
	settings IndexerSettingsLookup
	log      *slog.Logger
}

// NewRejectedImportService binds the indexer-settings lookup. settings
// may be nil — then every rejection degrades to a Warn (no indexer
// config to consult), which is the same conservative branch C# takes
// when GetSettings returns null.
func NewRejectedImportService(settings IndexerSettingsLookup, log *slog.Logger) *RejectedImportService {
	if log == nil {
		log = slog.Default()
	}
	return &RejectedImportService{settings: settings, log: log}
}

// Process ports `RejectedImportService.Process` (`:25-65`). Returns
// false when the result is not a rejection or the tracked download has
// no release (not this service's concern); otherwise it applies the
// fail/warn decision and returns true.
func (s *RejectedImportService) Process(ctx context.Context, td *trackeddownloads.TrackedDownload, result *episodeimport.ImportResult) bool {
	if td == nil || result == nil {
		return false
	}
	if result.ResultType() != episodeimport.ImportResultRejected || !td.HasRelease() {
		return false
	}

	var settings *indexers.CachedIndexerSettings
	if s.settings != nil {
		if got, err := s.settings.GetSettings(ctx, td.ReleaseIndexerID()); err != nil {
			s.log.Debug("rejectedimport: indexer settings lookup failed; warning instead of failing", slog.Any("err", err))
		} else {
			settings = got
		}
	}

	reason := firstRejectionReason(result)

	// No indexer settings (unknown indexer / lookup failure) → warn so the
	// user can intervene (C#:36-40).
	if settings == nil {
		td.Warn(warnMessages(result)...)
		return true
	}

	switch {
	case reason == episodeimport.ImportRejectionDangerousFile &&
		slices.Contains(settings.FailDownloads, indexers.FailDownloadsPotentiallyDangerous):
		s.log.Debug("rejectedimport: potentially dangerous file, marking failed", slog.String("downloadId", td.DownloadID))
		td.Fail()
	case reason == episodeimport.ImportRejectionExecutableFile &&
		slices.Contains(settings.FailDownloads, indexers.FailDownloadsExecutables):
		s.log.Debug("rejectedimport: executable file, marking failed", slog.String("downloadId", td.DownloadID))
		td.Fail()
	case reason == episodeimport.ImportRejectionUserRejectedExtension &&
		slices.Contains(settings.FailDownloads, indexers.FailDownloadsUserDefinedExtensions):
		s.log.Debug("rejectedimport: user-rejected extension, marking failed", slog.String("downloadId", td.DownloadID))
		td.Fail()
	default:
		td.Warn(warnMessages(result)...)
	}
	return true
}

// firstRejectionReason returns the first rejection's reason code, or
// ImportRejectionUnknown when there is none. Mirrors C#
// `importResult.ImportDecision.Rejections.FirstOrDefault()?.Reason`.
func firstRejectionReason(result *episodeimport.ImportResult) episodeimport.ImportRejectionReason {
	if result.Decision != nil && len(result.Decision.Rejections) > 0 {
		return result.Decision.Rejections[0].Reason
	}
	return episodeimport.ImportRejectionUnknown
}

// warnMessages renders the import result's error lines for the tracked
// download's status warning. Mirrors the C#
// `new TrackedDownloadStatusMessage(DownloadItem.Title, importResult.Errors)`
// — the Go port's []string status-message simplification carries the
// error lines (the per-message Title grouping is a queue/UI concern).
func warnMessages(result *episodeimport.ImportResult) []string {
	if len(result.Errors) == 0 {
		return []string{"Import was rejected"}
	}
	return slices.Clone(result.Errors)
}
