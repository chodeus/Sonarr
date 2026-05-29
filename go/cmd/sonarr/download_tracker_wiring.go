// Boot-time adapter implementing downloadmonitoring.DownloadItemTracker
// (J394 — the poll→track population keystone). Ports the primary path of
// C# `TrackedDownloadService.TrackDownload`
// (`src/NzbDrone.Core/Download/TrackedDownloads/TrackedDownloadService.cs:91-200`):
// parse the polled item's title, map it to a RemoteEpisode via the
// production parsing service, and Track the resulting TrackedDownload so
// the queue cache populates from the live download clients.
//
// Deferred vs C# (documented): the grab-history fallback title resolution
// (`:140-165`), the AggregationService.Augment + custom-format enrichment
// (`:181-194`), and the per-item state machine (Downloading/Completed/
// Failed) — those belong to the ProcessMonitoredDownloads /
// DownloadProcessingService layer that's intentionally out of this arc.
// Newly-tracked items default to StateDownloading; existing cache entries
// preserve their state across polls (C# `:101-112`).

package main

import (
	"context"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type downloadItemTrackerAdapter struct {
	parsing *parsingservice.Service
	tracked *trackeddownloads.Service
	log     *slog.Logger
}

// TrackDownload builds + Tracks a TrackedDownload for one polled item.
func (a downloadItemTrackerAdapter) TrackDownload(ctx context.Context, client downloadclients.Provider, item *downloadclients.Item) *trackeddownloads.TrackedDownload {
	if item == nil || strings.TrimSpace(item.DownloadID) == "" {
		return nil
	}

	// Existing-item arm (C# `:101-112`): keep the prior state/RemoteEpisode,
	// just refresh the live DownloadItem + re-mark trackable.
	if existing := a.tracked.Find(item.DownloadID); existing != nil {
		existing.IsTrackable = true
		existing.DownloadItem = item
		return existing
	}

	var remote *decisionengine.Subject
	if parsed := parser.ParseTitle(item.Title); parsed != nil && a.parsing != nil {
		if r, err := a.parsing.Map(ctx, parsed, 0, 0, "", nil); err == nil {
			remote = r
		} else {
			a.log.Debug("download tracker: Map failed", "title", item.Title, "err", err)
		}
	}

	clientID := 0
	if def := client.Definition(); def != nil {
		clientID = def.ID
	}

	td := &trackeddownloads.TrackedDownload{
		DownloadID:     item.DownloadID,
		DownloadClient: clientID,
		DownloadItem:   item,
		ImportItem:     item,
		Protocol:       int(client.Protocol()),
		RemoteEpisode:  remote,
		IsTrackable:    true,
		State:          trackeddownloads.StateDownloading,
	}
	if err := a.tracked.Track(td); err != nil {
		a.log.Warn("download tracker: Track failed", "downloadId", item.DownloadID, "err", err)
		return nil
	}
	return td
}
