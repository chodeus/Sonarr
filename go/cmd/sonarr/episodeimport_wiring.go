// Boot-time adapters for episodeimport-package narrow interfaces
// that need a thin signature bridge to a real production service.
// Same posture as decisionengine_wiring.go / mediafiles_wiring.go.

package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/history"
)

// latestGrabFinderAdapter wraps `*downloadhistory.Service.GetLatestGrab`
// to satisfy [episodeimport.LatestGrabFinder]. Two shape mismatches:
//
//  1. ctx — the narrow interface drops it (the augmenter walks
//     in-memory state and shouldn't fail on cancellation). The
//     adapter passes [context.Background] and swallows
//     [context.Canceled] equivalents at the C# parity level.
//
//  2. error — the narrow interface returns just `*Entry`. The real
//     service returns `(*Entry, error)`. The adapter logs at Warn
//     and returns nil on error (matches the C# augmenter's
//     try/catch swallow at
//     `AugmentQualityFromReleaseName.cs:34-38` which catches and
//     returns null without a rejection).
type latestGrabFinderAdapter struct {
	svc *downloadhistory.Service
	log *slog.Logger
}

func (a latestGrabFinderAdapter) GetLatestGrab(downloadID string) *downloadhistory.Entry {
	if a.svc == nil {
		return nil
	}
	entry, err := a.svc.GetLatestGrab(context.Background(), downloadID)
	if err != nil {
		a.log.Warn("downloadhistory: GetLatestGrab failed; treating as no grab",
			"downloadID", downloadID, "err", err)
		return nil
	}
	return entry
}

// historyGrabsFinderAdapter wraps `*history.Service.Find` (with the
// typed [history.EventTypeGrabbed] filter) to satisfy
// [episodeimport.HistoryGrabsFinder]. The C# call chain at
// `Aggregators/AggregateReleaseInfo.cs:23-34` walks
// `_historyService.Find(downloadId, EpisodeHistoryEventType.Grabbed)`
// which returns the full Grabbed-only list (the per-import variant
// in slice 3.A1's historyGrabFinderForImportAdapter returns just
// the most-recent entry — different shape).
//
// Distinct from latestGrabFinderAdapter in this same file (which
// wraps *downloadhistory.Service for the AugmentQualityFromReleaseName
// augmenter) — different table (EpisodeHistory vs DownloadHistory),
// different semantic.
type historyGrabsFinderAdapter struct {
	svc *history.Service
}

func (a historyGrabsFinderAdapter) FindGrabsByDownloadID(ctx context.Context, downloadID string) ([]history.Entry, error) {
	if a.svc == nil {
		return nil, nil
	}
	return a.svc.Find(ctx, downloadID, history.EventTypeGrabbed)
}

// Compile-time guards.
var (
	_ episodeimport.LatestGrabFinder   = latestGrabFinderAdapter{}
	_ episodeimport.HistoryGrabsFinder = historyGrabsFinderAdapter{}
)
