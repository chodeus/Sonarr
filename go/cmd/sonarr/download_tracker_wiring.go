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
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// remoteEpisodeMapper is the narrow surface the tracker needs from
// *parsingservice.Service — parse-result → RemoteEpisode mapping. Defined
// as an interface so the series-refresh fan-out (J474) is unit-testable
// with a stub. *parsingservice.Service satisfies it structurally.
type remoteEpisodeMapper interface {
	Map(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, tvdbID, tvRageID int, imdbID string, searchCriteria *decisionengine.SearchCriteria) (*decisionengine.Subject, error)
}

type downloadItemTrackerAdapter struct {
	parsing remoteEpisodeMapper
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

	remote := a.resolveRemote(ctx, item.Title)

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

// resolveRemote parses a download title and maps it to a RemoteEpisode
// (nil when the title doesn't parse / map). Shared by the initial
// TrackDownload + the series-edit refresh (J474). Mirrors the C#
// `ParseTitle` + `_parsingService.Map(...)` pair used in both
// `TrackedDownloadService.TrackDownload` and `UpdateCachedItem`.
func (a downloadItemTrackerAdapter) resolveRemote(ctx context.Context, title string) *decisionengine.Subject {
	if a.parsing == nil {
		return nil
	}
	parsed := parser.ParseTitle(title)
	if parsed == nil {
		return nil
	}
	r, err := a.parsing.Map(ctx, parsed, 0, 0, "", nil)
	if err != nil {
		a.log.Debug("download tracker: Map failed", "title", title, "err", err)
		return nil
	}
	return r
}

// RefreshTrackedForSeries re-resolves the cached RemoteEpisode of every
// tracked download whose matched series satisfies `match` (by id/tvdbId),
// so the queue/activity view reflects renamed/edited/deleted series
// instead of the stale title captured when the download was first
// tracked. Returns true when at least one was refreshed (the caller then
// publishes [trackeddownloads.TrackedDownloadRefreshedEvent]).
//
// Ports the fan-out the C# `TrackedDownloadService` performs from its
// `IHandle<SeriesEditedEvent>` / `IHandle<SeriesDeletedEvent>` handlers
// via `UpdateCachedItem` (`TrackedDownloadService.cs:245-360`). The Go
// port had deferred these handlers (the trackeddownloads doc-comment's
// "unported event shapes" note is stale — series.SeriesEditedEvent /
// SeriesDeletedEvent are ported + published at series/mutations.go:355,490).
func (a downloadItemTrackerAdapter) RefreshTrackedForSeries(ctx context.Context, match func(seriesID, tvdbID int) bool) bool {
	return a.refreshTracked(ctx, func(td *trackeddownloads.TrackedDownload) bool {
		r := td.RemoteEpisode
		return r != nil && r.Series != nil && match(r.Series.ID, r.Series.TvdbID)
	})
}

// RefreshTrackedForAddedSeries re-resolves tracked downloads that either
// have no mapped series yet (one tracked before the series existed may
// now map) OR already match the added series' tvdbId. Ports C#
// `TrackedDownloadService.Handle(SeriesAddedEvent)` (`:294-307`).
func (a downloadItemTrackerAdapter) RefreshTrackedForAddedSeries(ctx context.Context, tvdbID int) bool {
	return a.refreshTracked(ctx, func(td *trackeddownloads.TrackedDownload) bool {
		r := td.RemoteEpisode
		if r == nil || r.Series == nil {
			return true
		}
		return tvdbID != 0 && r.Series.TvdbID == tvdbID
	})
}

// refreshTracked re-resolves the cached RemoteEpisode (via [resolveRemote]:
// ParseTitle + Map, C# UpdateCachedItem) of every tracked download for
// which `should` returns true, returning whether any changed. Shared by
// the series event handlers (J474/J475). A re-resolve that no longer maps
// (e.g. deleted series) sets RemoteEpisode to nil, matching C#.
func (a downloadItemTrackerAdapter) refreshTracked(ctx context.Context, should func(*trackeddownloads.TrackedDownload) bool) bool {
	updated := false
	for _, td := range a.tracked.GetTrackedDownloads() {
		if td == nil || td.DownloadItem == nil {
			continue
		}
		if !should(td) {
			continue
		}
		td.RemoteEpisode = a.resolveRemote(ctx, td.DownloadItem.Title)
		updated = true
	}
	return updated
}
