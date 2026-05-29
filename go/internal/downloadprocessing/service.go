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
// the tracked download is marked Imported.
//
// J400 scope — the failure arm: ports `FailedDownloadService.Check` +
// `ProcessFailed` (`src/NzbDrone.Core/Download/FailedDownloadService.cs:85-138`).
// A tracked download the client reports Failed (or that came back
// encrypted) and which matched a series is marked Failed and a
// [trackeddownloads.DownloadFailedEvent] is published — feeding the
// already-subscribed blocklist + history handlers (the same event the
// queue-delete blocklist arm publishes, J396). C#'s two-phase
// Downloading→FailedPending→Failed (Check during the poll, ProcessFailed
// in the next command) is collapsed into one pass here, consistent with
// how J399 collapsed the completed Check+Import — the end state (Failed +
// event) is identical, the event just fires one cycle earlier. The
// failure arm only runs when a publisher is wired (SetFailurePublisher);
// unwired it no-ops, preserving prior behavior.
//
// DEFERRED to follow-ups (each a documented gap, NOT silently skipped):
//   - the rejection / partial-import / RejectedImportService handling
//     (C# CompletedDownloadService.cs:51-120) — left at Downloading for
//     re-attempt on the next cycle;
//   - script-import (SetImportItem) + ValidatePath + remove-completed-
//     download cleanup + DownloadCompletedEvent publication;
//   - the import-blocked state for unparsed (RemoteEpisode==nil) downloads
//     (logged + skipped here);
//   - the redownload-on-failure search consumer for the published event
//     (the event already feeds blocklist + history; SkipRedownload=false
//     so a future RedownloadFailedDownloadService would re-search).
package downloadprocessing

import (
	"context"
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
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

// FailurePublisher publishes a [trackeddownloads.DownloadFailedEvent]
// for a tracked download the client reported failed (J400). A boot-time
// adapter over *messaging.Bus satisfies it; tests fake it to capture
// the event. Optional — when unset the failure arm no-ops.
type FailurePublisher interface {
	PublishDownloadFailed(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error
}

// CompletionPublisher publishes a [trackeddownloads.DownloadCompletedEvent]
// for a tracked download whose every episode just imported (J449). A
// boot-time adapter over *messaging.Bus satisfies it; tests fake it to
// capture the event. Optional — when unset the import path still marks
// the download Imported but publishes nothing (preserving prior J399
// behavior). Mirrors the optional [FailurePublisher] (J400).
type CompletionPublisher interface {
	PublishDownloadCompleted(ctx context.Context, ev trackeddownloads.DownloadCompletedEvent) error
}

// ProcessedPublisher publishes a [trackeddownloads.DownloadsProcessedEvent]
// at the end of each process pass (J454). A boot-time adapter over
// *messaging.Bus satisfies it; tests fake it. Optional — when unset the
// marker isn't published. Mirrors C# `DownloadProcessingService.cs:80`.
type ProcessedPublisher interface {
	PublishDownloadsProcessed(ctx context.Context, ev trackeddownloads.DownloadsProcessedEvent) error
}

// CanBeRemovedPublisher publishes a [trackeddownloads.DownloadCanBeRemovedEvent]
// for each Imported, now-removable tracked download at the end of a pass
// (J458, the RemoveCompletedDownloads sweep). Optional. Mirrors C#
// `DownloadProcessingService.RemoveCompletedDownloads` (`:35-45`).
type CanBeRemovedPublisher interface {
	PublishDownloadCanBeRemoved(ctx context.Context, ev trackeddownloads.DownloadCanBeRemovedEvent) error
}

// Service auto-imports completed tracked downloads and (J400) marks
// failed ones Failed + publishes a DownloadFailedEvent.
type Service struct {
	tracked      TrackedDownloadStore
	importer     Importer
	failPub      FailurePublisher
	completePub  CompletionPublisher
	processedPub ProcessedPublisher
	canRemovePub CanBeRemovedPublisher
	log          *slog.Logger
}

// SetFailurePublisher wires the optional [FailurePublisher] (J400). The
// failure-detection arm of ProcessTrackedDownloads only runs once a
// publisher is set; nil-safe. Mirrors the optional-extension wiring of
// downloadmonitoring.SetProcessor.
func (s *Service) SetFailurePublisher(p FailurePublisher) {
	if s != nil {
		s.failPub = p
	}
}

// SetCompletionPublisher wires the optional [CompletionPublisher] (J449).
// When set, a fully-imported tracked download publishes a
// DownloadCompletedEvent after its state transitions to Imported;
// unwired the import still happens, just silently (prior J399 behavior).
// nil-safe. Mirrors SetFailurePublisher.
func (s *Service) SetCompletionPublisher(p CompletionPublisher) {
	if s != nil {
		s.completePub = p
	}
}

// SetProcessedPublisher wires the optional [ProcessedPublisher] (J454).
// When set, a DownloadsProcessedEvent is published at the end of every
// ProcessTrackedDownloads pass (mirrors C# `DownloadProcessingService.cs:80`);
// unwired the marker isn't published. nil-safe.
func (s *Service) SetProcessedPublisher(p ProcessedPublisher) {
	if s != nil {
		s.processedPub = p
	}
}

// SetCanBeRemovedPublisher wires the optional [CanBeRemovedPublisher]
// (J458). When set, the end-of-pass RemoveCompletedDownloads sweep
// publishes a DownloadCanBeRemovedEvent per Imported + removable tracked
// download; unwired the sweep is skipped. nil-safe.
func (s *Service) SetCanBeRemovedPublisher(p CanBeRemovedPublisher) {
	if s != nil {
		s.canRemovePub = p
	}
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
		// C# processes completed items followed by failed within the
		// same walk (DownloadProcessingService.cs:55-72). The two are
		// mutually exclusive per item (DownloadItem.Status is one
		// value), so import first, otherwise check for failure.
		if s.shouldImport(td) {
			if s.importOne(ctx, td) {
				imported++
			}
			continue
		}
		if s.shouldFail(td) {
			s.failOne(ctx, td)
		}
	}
	if imported > 0 {
		s.log.LogAttrs(ctx, slog.LevelInfo, "downloadprocessing: auto-imported completed downloads",
			slog.Int("count", imported))
	}
	// RemoveCompletedDownloads sweep (C# `DownloadProcessingService.cs:35-45`,
	// run after the per-item loop): every Imported tracked download the
	// client now reports removable gets a DownloadCanBeRemovedEvent — the
	// deferred-removal path for items not removable at completion time
	// (e.g. a torrent that was still seeding). Items removed immediately
	// at completion (J455) are already StopTracked, so this re-fetched
	// snapshot won't re-publish them.
	s.sweepRemovableImports(ctx)
	// End-of-pass marker (C# `DownloadProcessingService.cs:80`) —
	// published unconditionally (even with zero imports) so the
	// metadata/extras writer's CreateAfterEpisodesImported batch fires
	// once per pass for every series that imported a file this cycle.
	if s.processedPub != nil {
		if err := s.processedPub.PublishDownloadsProcessed(ctx, trackeddownloads.DownloadsProcessedEvent{}); err != nil {
			s.log.Warn("downloadprocessing: publish DownloadsProcessedEvent failed", "err", err)
		}
	}
	return imported
}

// sweepRemovableImports publishes a DownloadCanBeRemovedEvent for every
// tracked download that is Imported and the client now reports removable.
// Ports C# `DownloadProcessingService.RemoveCompletedDownloads` (`:35-45`).
// No-op when no publisher is wired.
func (s *Service) sweepRemovableImports(ctx context.Context) {
	if s.canRemovePub == nil {
		return
	}
	for _, td := range s.tracked.GetTrackedDownloads() {
		if td == nil || td.DownloadItem == nil {
			continue
		}
		if td.DownloadItem.CanBeRemoved && td.State == trackeddownloads.StateImported {
			if err := s.canRemovePub.PublishDownloadCanBeRemoved(ctx, trackeddownloads.DownloadCanBeRemovedEvent{TrackedDownload: td}); err != nil {
				s.log.Warn("downloadprocessing: publish DownloadCanBeRemovedEvent failed", "downloadId", td.DownloadID, "err", err)
			}
		}
	}
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
	s.publishCompleted(ctx, td, results)
	return true
}

// publishCompleted publishes a DownloadCompletedEvent for a fully-
// imported tracked download. Ports the publication arm of C#
// `CompletedDownloadService.VerifyImport` (`:229-234`): after the state
// transition to Imported, fire the event carrying the tracked download,
// matched series id, and the freshly-written EpisodeFile rows (the
// imported results' files). The history + notification subscribers
// consume it (the DownloadImported audit row is written by the already-
// wired downloadhistory handler).
//
// Release (the originating GrabbedReleaseInfo) is left nil: C# recovers
// it from grabbed history (`:222`), a lookup this service doesn't have;
// the event field is documented-optional and the history consumer
// ignores it. Recovering it is a follow-up (would need a history reader).
//
// Best-effort: the import already moved files on disk, so a publish
// error never reverts the Imported state — it's logged and swallowed
// (the event is an audit/notification signal, not a transactional gate).
func (s *Service) publishCompleted(ctx context.Context, td *trackeddownloads.TrackedDownload, results []*episodeimport.ImportResult) {
	if s.completePub == nil {
		return
	}
	var files []episodefiles.File
	for _, r := range results {
		if r != nil && r.ResultType() == episodeimport.ImportResultImported && r.EpisodeFile != nil {
			files = append(files, *r.EpisodeFile)
		}
	}
	ev := trackeddownloads.DownloadCompletedEvent{
		TrackedDownload: td,
		SeriesID:        td.RemoteEpisode.Series.ID,
		EpisodeFiles:    files,
	}
	if err := s.completePub.PublishDownloadCompleted(ctx, ev); err != nil {
		s.log.Warn("downloadprocessing: publish DownloadCompletedEvent failed", "downloadId", td.DownloadID, "err", err)
	}
}

// shouldFail reports whether a tracked download is a candidate for the
// failure arm: a publisher is wired, the download is still Downloading
// or ImportBlocked, the client reports it Failed (or it came back
// encrypted), and it matched a series. Mirrors the guards in C#
// `FailedDownloadService.Check` (`FailedDownloadService.cs:85-105`): the
// state filter, the `IsEncrypted || Status==Failed` trigger, and the
// "wasn't grabbed by Sonarr → skip" warn (modelled here as the matched-
// series guard, since the Go port carries the parsed grab on the
// tracked download rather than re-reading grabbed history).
func (s *Service) shouldFail(td *trackeddownloads.TrackedDownload) bool {
	if s.failPub == nil {
		return false // failure arm not wired — no-op
	}
	if td == nil || td.DownloadItem == nil {
		return false
	}
	if td.State != trackeddownloads.StateDownloading && td.State != trackeddownloads.StateImportBlocked {
		return false
	}
	if !td.DownloadItem.IsEncrypted && td.DownloadItem.Status != downloadclients.DownloadItemStatusFailed {
		return false
	}
	if td.RemoteEpisode == nil || td.RemoteEpisode.Series == nil {
		s.log.Warn("downloadprocessing: failed/encrypted download wasn't grabbed by Sonarr; skipping automatic handling",
			"downloadId", td.DownloadID)
		return false
	}
	return true
}

// failOne marks a failed tracked download Failed and publishes a
// DownloadFailedEvent. Ports `FailedDownloadService.ProcessFailed`
// (`FailedDownloadService.cs:107-138`): compose the failure message,
// set State=Failed, publish the event for the blocklist + history
// handlers. SkipRedownload stays false so a future redownload consumer
// would re-search (the event today feeds blocklist + history only).
func (s *Service) failOne(ctx context.Context, td *trackeddownloads.TrackedDownload) {
	ev := s.buildFailedEvent(td)
	if err := s.failPub.PublishDownloadFailed(ctx, ev); err != nil {
		// Don't transition state if the event didn't fire — the next
		// cycle retries (the download is still Failed at the client).
		s.log.Warn("downloadprocessing: publish DownloadFailedEvent failed", "downloadId", td.DownloadID, "err", err)
		return
	}
	td.State = trackeddownloads.StateFailed
	s.log.LogAttrs(ctx, slog.LevelInfo, "downloadprocessing: marked download failed",
		slog.String("downloadId", td.DownloadID), slog.String("message", ev.Message))
}

// buildFailedEvent composes the DownloadFailedEvent from the tracked
// download. The failure-message precedence mirrors
// `FailedDownloadService.ProcessFailed` (`:118-128`): encrypted →
// "Encrypted download detected"; failed-with-message → the client's
// message; else "Failed download detected". Release-shape carry-over
// (series / episodes / quality / languages / source-title) mirrors the
// same field-build the queue-delete blocklist arm uses (J396).
func (s *Service) buildFailedEvent(td *trackeddownloads.TrackedDownload) trackeddownloads.DownloadFailedEvent {
	message := "Failed download detected"
	switch {
	case td.DownloadItem.IsEncrypted:
		message = "Encrypted download detected"
	case td.DownloadItem.Status == downloadclients.DownloadItemStatusFailed && td.DownloadItem.Message != "":
		message = td.DownloadItem.Message
	}
	ev := trackeddownloads.DownloadFailedEvent{
		DownloadID:      td.DownloadID,
		TrackedDownload: td,
		Message:         message,
		Source:          "Sonarr Failed Download Handling",
		ReleaseSource:   decisionengine.ReleaseSourceUnknown,
	}
	if r := td.RemoteEpisode; r != nil {
		if r.Series != nil {
			ev.SeriesID = r.Series.ID
		}
		for _, e := range r.Episodes {
			ev.EpisodeIDs = append(ev.EpisodeIDs, e.ID)
		}
		ev.Languages = r.Languages
		if r.ParsedEpisodeInfo != nil {
			ev.Quality = r.ParsedEpisodeInfo.Quality
			ev.SourceTitle = r.ParsedEpisodeInfo.ReleaseTitle
			if len(ev.Languages) == 0 {
				ev.Languages = r.ParsedEpisodeInfo.Languages
			}
		}
	}
	if ev.SourceTitle == "" {
		ev.SourceTitle = td.DownloadItem.Title
	}
	return ev
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
