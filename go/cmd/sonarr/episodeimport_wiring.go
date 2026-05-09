// Boot-time adapters for episodeimport-package narrow interfaces
// that need a thin signature bridge to a real production service.
// Same posture as decisionengine_wiring.go / mediafiles_wiring.go.

package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/episodeimport"
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

// Compile-time guard.
var _ episodeimport.LatestGrabFinder = latestGrabFinderAdapter{}
