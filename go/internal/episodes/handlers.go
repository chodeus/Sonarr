package episodes

import (
	"context"
	"errors"
	"fmt"
)

// FileDeleteReason mirrors `episodefiles.DeleteMediaFileReason` at the
// shape level — the episodes package can't import episodefiles
// (episodefiles already imports episodes for its EpisodeProvider
// interface) so we redeclare it here. Numbering matches by convention;
// the host's bus adapter maps episodefiles.DeleteMediaFileReason →
// episodes.FileDeleteReason at the listener boundary.
//
// Values are pinned by tests on both sides; if either side reorders,
// the test bites and forces a host-adapter audit.
type FileDeleteReason int

const (
	// FileDeleteMissingFromDisk corresponds to
	// `episodefiles.ReasonMissingFromDisk`. Triggers the
	// missing-from-disk cache write when AutoUnmonitor is set.
	FileDeleteMissingFromDisk FileDeleteReason = iota

	// FileDeleteManual corresponds to `episodefiles.ReasonManual`.
	// User-initiated; participates in the auto-unmonitor cascade
	// when the AutoUnmonitor flag is set.
	FileDeleteManual

	// FileDeleteUpgrade corresponds to `episodefiles.ReasonUpgrade`.
	// Excluded from auto-unmonitor — the upgrade-import inserts a
	// replacement file immediately, so dropping Monitored would
	// race with the new file's add.
	FileDeleteUpgrade

	// FileDeleteNoLinkedEpisodes corresponds to
	// `episodefiles.ReasonNoLinkedEpisodes`. Participates in the
	// auto-unmonitor cascade when AutoUnmonitor is set.
	FileDeleteNoLinkedEpisodes

	// FileDeleteManualOverride corresponds to
	// `episodefiles.ReasonManualOverride`. Excluded from
	// auto-unmonitor — user explicitly overrode an automated
	// decision, monitor state should persist.
	FileDeleteManualOverride
)

// String returns the canonical name for log messages.
func (r FileDeleteReason) String() string {
	switch r {
	case FileDeleteMissingFromDisk:
		return "MissingFromDisk"
	case FileDeleteManual:
		return "Manual"
	case FileDeleteUpgrade:
		return "Upgrade"
	case FileDeleteNoLinkedEpisodes:
		return "NoLinkedEpisodes"
	case FileDeleteManualOverride:
		return "ManualOverride"
	default:
		return "Unknown"
	}
}

// HandleEpisodeFileAdded ports
// `EpisodeService.Handle(EpisodeFileAddedEvent)` at
// `EpisodeService.cs:288-306`. For each linked episode:
//
//  1. Set its EpisodeFileId to the new file ID.
//  2. Forget the episode from the missing-from-disk cache (matches
//     `:294-302`'s cache.Find + Remove).
//
// The host's bus adapter populates `linkedEpisodes` from the
// EpisodeFileAddedEvent.LinkedEpisodes payload (which is itself
// populated by episodefiles.Service via the EpisodeProvider
// interface).
//
// First-error wins (like the C# version, which doesn't try/catch
// per-episode). Returns nil for empty input.
func (s *Service) HandleEpisodeFileAdded(ctx context.Context, fileID int, linkedEpisodes []Episode) error {
	for i := range linkedEpisodes {
		ep := linkedEpisodes[i]
		if err := s.repo.SetFileID(ctx, &ep, fileID); err != nil {
			return fmt.Errorf("episodes: HandleEpisodeFileAdded(file=%d, ep=%d): %w", fileID, ep.ID, err)
		}
		s.forgetMissingFromDisk(ep.SeriesID, ep.ID)
	}
	return nil
}

// HandleEpisodeFileDeleted ports
// `EpisodeService.Handle(EpisodeFileDeletedEvent)` at `:261-286`.
// Decision matrix mirrors C# `:267-271`:
//
//	autoUnmonitor   = AutoUnmonitorPreviouslyDownloadedEpisodes config
//	unmonitorForReason = reason ∉ {Upgrade, ManualOverride, MissingFromDisk}
//
//	if reason == MissingFromDisk && autoUnmonitor:
//	    remember(seriesID, episodeID) — defer until next scan
//	repo.ClearFileID(episode, unmonitor=unmonitorForReason && autoUnmonitor)
//
// The "defer until next scan" path is implemented via the
// missing-from-disk cache (RememberMissingFromDisk); a future
// SeriesScannedEvent listener (Phase 3.x) drains it.
//
// First-error wins. Returns nil for empty input. The
// AutoUnmonitorMissingProvider lookup is fresh on each call (matches
// C# `_configService.AutoUnmonitorPreviouslyDownloadedEpisodes`
// being a property read).
func (s *Service) HandleEpisodeFileDeleted(ctx context.Context, fileID int, linkedEpisodes []Episode, reason FileDeleteReason) error {
	autoUnmonitor := false
	if s.autoUnmonitorMissing != nil {
		autoUnmonitor = s.autoUnmonitorMissing.AutoUnmonitorPreviouslyDownloadedEpisodes()
	}
	unmonitorForReason := reason != FileDeleteUpgrade &&
		reason != FileDeleteManualOverride &&
		reason != FileDeleteMissingFromDisk

	for i := range linkedEpisodes {
		ep := linkedEpisodes[i]
		if reason == FileDeleteMissingFromDisk && autoUnmonitor {
			s.RememberMissingFromDisk(ep.SeriesID, ep.ID)
		}
		if err := s.repo.ClearFileID(ctx, &ep, unmonitorForReason && autoUnmonitor); err != nil {
			if errors.Is(err, ErrNotFound) {
				// The episode row may already be gone (e.g. after
				// SeriesDeletedEvent fan-out). Tolerate.
				continue
			}
			return fmt.Errorf("episodes: HandleEpisodeFileDeleted(file=%d, ep=%d): %w", fileID, ep.ID, err)
		}
	}
	return nil
}

// HandleSeriesScanned ports
// `EpisodeService.HandleAsync(SeriesScannedEvent)` at `:314-327`.
// Drains the missing-from-disk cache for the given series and
// unmonitors any episode IDs that didn't pick up a file in the
// interim. Empty drain is a no-op.
//
// The host's bus adapter calls this when a SeriesScannedEvent
// fires — that event lives in the (Phase 3) mediafiles package.
func (s *Service) HandleSeriesScanned(ctx context.Context, seriesID int) error {
	ids := s.DrainMissingFromDisk(seriesID)
	if len(ids) == 0 {
		return nil
	}
	if _, err := s.repo.SetMonitoredByIDs(ctx, ids, false); err != nil {
		return fmt.Errorf("episodes: HandleSeriesScanned(series=%d): %w", seriesID, err)
	}
	return nil
}
