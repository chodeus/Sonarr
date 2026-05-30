package downloadprocessing

import (
	"context"
	"path/filepath"

	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// This file ports the incomplete-import arm of C#
// `CompletedDownloadService.Import` (`:130-300`) — the state machine that
// routes a completed-but-not-fully-imported download to ImportPending /
// ImportBlocked, classifies single-file rejections (via
// RejectedImportService), and fires ManualInteractionRequiredEvent when
// a download first becomes blocked. The happy "all imported" arm + the
// DownloadCompletedEvent publish remain in service.go (importOne /
// publishCompleted, J399/J449).

// HistoryByDownloadReader reads the history rows for a download's grab/
// import lifecycle. *history.Repo satisfies it via AllByDownloadID.
type HistoryByDownloadReader interface {
	AllByDownloadID(ctx context.Context, downloadID string) ([]history.Entry, error)
}

// ManualInteractionPublisher publishes a
// [trackeddownloads.ManualInteractionRequiredEvent]. A boot-time adapter
// over *messaging.Bus satisfies it; tests fake it.
type ManualInteractionPublisher interface {
	PublishManualInteractionRequired(ctx context.Context, ev trackeddownloads.ManualInteractionRequiredEvent) error
}

// importBlockedActive reports whether the faithful blocking state machine
// is wired (the publisher is the activation signal; the wiring slice sets
// all three seams together).
func (s *Service) importBlockedActive() bool {
	return s.manualInteractionPub != nil
}

// verifyImport ports `CompletedDownloadService.VerifyImport` (`:212-283`).
// Returns true (and transitions to Imported + publishes
// DownloadCompletedEvent) when every result imported, OR — via the
// history double-check — when every episode's most-recent history event
// is DownloadFolderImported (the re-process-of-already-imported case).
func (s *Service) verifyImport(ctx context.Context, td *trackeddownloads.TrackedDownload, results []*episodeimport.ImportResult) bool {
	if allImported(results) {
		td.State = trackeddownloads.StateImported
		s.publishCompleted(ctx, td, results)
		return true
	}

	if s.historyReader != nil {
		entries, err := s.historyReader.AllByDownloadID(ctx, td.DownloadID)
		if err != nil {
			s.log.Debug("downloadprocessing: history lookup failed during import verify", "downloadId", td.DownloadID, "err", err)
		} else if allEpisodesImportedInHistory(td, entries) {
			s.log.Debug("downloadprocessing: all episodes already imported in history", "downloadId", td.DownloadID)
			td.State = trackeddownloads.StateImported
			s.publishCompleted(ctx, td, results)
			return true
		}
	}

	return false
}

// handleIncompleteImport ports the post-VerifyImport arm of
// `CompletedDownloadService.Import` (`:158-209`). The download is set
// ImportPending, then — depending on the results — either left pending
// (no eligible files), or blocked for manual interaction.
func (s *Service) handleIncompleteImport(ctx context.Context, td *trackeddownloads.TrackedDownload, outputPath string, results []*episodeimport.ImportResult) {
	td.State = trackeddownloads.StateImportPending

	if len(results) == 0 {
		// No eligible files — stays ImportPending (NOT blocked; C#:160-165).
		td.Warnf("No files found are eligible for import in %s", outputPath)
		return
	}

	// Single-result rejection handling (C#:167-187).
	if len(results) == 1 {
		first := results[0]
		if s.rejectedImport != nil && s.rejectedImport.Process(ctx, td, first) {
			// RejectedImportService may have hard-Failed the download
			// (FailedPending); only block when it merely warned.
			if td.State != trackeddownloads.StateFailedPending {
				s.setStateToImportBlocked(ctx, td)
			}
			return
		}
		if firstRejectionReason(first) == episodeimport.ImportRejectionMultiSeason {
			td.Warn(warnMessages(first)...)
			s.setStateToImportBlocked(ctx, td)
			return
		}
	}

	// Multi-result / partial (C#:189-209): a generic message plus one per
	// non-imported file, then block.
	msgs := []string{"One or more episodes expected in this release were not imported or missing from the release"}
	for _, r := range results {
		if r == nil || r.ResultType() == episodeimport.ImportResultImported {
			continue
		}
		if r.Decision == nil || r.Decision.LocalEpisode == nil {
			continue
		}
		name := filepath.Base(r.Decision.LocalEpisode.Path)
		if len(r.Errors) == 0 {
			msgs = append(msgs, name)
			continue
		}
		for _, e := range r.Errors {
			msgs = append(msgs, name+": "+e)
		}
	}
	td.Warn(msgs...)
	s.setStateToImportBlocked(ctx, td)
}

// setStateToImportBlocked ports `CompletedDownloadService.SetStateToImportBlocked`
// (`:285-300`): transitions to ImportBlocked and, the FIRST time, fires
// ManualInteractionRequiredEvent so the user is notified once.
//
// The C# event carries a GrabbedReleaseInfo recovered from grabbed
// history; the Go port passes nil today (the notification message is
// built from the tracked download's RemoteEpisode — recovering the
// release info is a follow-up that needs a grabbed-history → release
// projection).
func (s *Service) setStateToImportBlocked(ctx context.Context, td *trackeddownloads.TrackedDownload) {
	td.State = trackeddownloads.StateImportBlocked

	if td.HasNotifiedManualInteractionRequired {
		return
	}
	td.HasNotifiedManualInteractionRequired = true

	if s.manualInteractionPub != nil {
		ev := trackeddownloads.ManualInteractionRequiredEvent{TrackedDownload: td}
		if err := s.manualInteractionPub.PublishManualInteractionRequired(ctx, ev); err != nil {
			s.log.Warn("downloadprocessing: publish ManualInteractionRequiredEvent failed", "downloadId", td.DownloadID, "err", err)
		}
	}
}

// allEpisodesImportedInHistory ports
// `TrackedDownloadAlreadyImported.IsImported`
// (`TrackedDownloadAlreadyImported.cs:24-58`): true when, for EVERY
// episode in the tracked download, the most-recent history event is
// DownloadFolderImported. entries are the download's history rows
// (AllByDownloadID returns them Date-DESC, so the first match per
// episode is the most recent). Empty history → false.
func allEpisodesImportedInHistory(td *trackeddownloads.TrackedDownload, entries []history.Entry) bool {
	if len(entries) == 0 || td == nil || td.RemoteEpisode == nil {
		return false
	}
	eps := td.RemoteEpisode.Episodes
	if len(eps) == 0 {
		return false
	}
	for _, ep := range eps {
		var found *history.Entry
		for i := range entries {
			if entries[i].EpisodeID == ep.ID {
				found = &entries[i] // first match = most recent (Date-DESC)
				break
			}
		}
		if found == nil || found.EventType == nil ||
			*found.EventType != int(history.EventTypeDownloadFolderImported) {
			return false
		}
	}
	return true
}
