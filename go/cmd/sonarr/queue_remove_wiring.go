// Boot-time adapter porting the side-effect arms of C#
// `QueueController.Remove(TrackedDownload, removeFromClient, blocklist,
// skipRedownload, changeCategory)` (`src/Sonarr.Api.V3/Queue/QueueController.cs:333-372`).
//
// Embeds *queue.Service (→ satisfies apiv3.QueueWriter via the promoted
// Find/Remove) and adds apiv3.QueueItemRemover, which the queue-delete
// handlers discover by type-assertion. Resolves the queue row's tracked
// download, applies the download-client side-effect (RemoveItem when
// removeFromClient, else MarkItemAsImported when changeCategory),
// stops tracking, and removes the cache row.
//
// J395 scope: removeFromClient + changeCategory + StopTracking + cache
// remove. The blocklist arm (opts.Blocklist) is wired in J396.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// releaseInfoOf extracts the base *parser.ReleaseInfo from a Subject's
// `Release` (which is a *parser.ReleaseInfo or *parser.TorrentInfo).
// Returns nil for any other/absent value.
func releaseInfoOf(release any) *parser.ReleaseInfo {
	switch r := release.(type) {
	case *parser.ReleaseInfo:
		return r
	case *parser.TorrentInfo:
		return &r.ReleaseInfo
	}
	return nil
}

// queueClientResolver builds the live download-client provider for a
// config-row id (mirrors C# `_downloadClientProvider.Get(id)`). The
// production impl scans the rows + runs the registry constructor; tests
// fake it to avoid constructing real network clients.
type queueClientResolver interface {
	ResolveClient(ctx context.Context, clientID int) (downloadclients.Provider, error)
}

type queueRemoveAdapter struct {
	*queue.Service // Find/Remove → apiv3.QueueWriter

	tracked *trackeddownloads.Service
	clients queueClientResolver
	bus     *messaging.Bus
	log     *slog.Logger
}

// downloadClientResolver is the production queueClientResolver: scan the
// configured rows for the id, then build the provider via the registry.
type downloadClientResolver struct {
	rows     downloadClientRowLister
	registry *downloadclients.Registry
	deps     downloadclients.Dependencies
}

func (r downloadClientResolver) ResolveClient(ctx context.Context, clientID int) (downloadclients.Provider, error) {
	rows, err := r.rows.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list download clients: %w", err)
	}
	for i := range rows {
		if rows[i].ID == clientID {
			return r.registry.Construct(&rows[i], r.deps)
		}
	}
	return nil, fmt.Errorf("no download client with id %d", clientID)
}

// RemoveQueueItem applies the removal side-effects then drops the cache row.
func (a *queueRemoveAdapter) RemoveQueueItem(ctx context.Context, id int, opts apiv3.QueueRemoveOptions) (bool, error) {
	q := a.Service.Find(id)
	if q == nil {
		return false, nil
	}

	if q.DownloadID != "" && a.tracked != nil {
		if td := a.tracked.Find(q.DownloadID); td != nil {
			if err := a.applyClientSideEffects(ctx, td, opts); err != nil {
				return false, err
			}
			if opts.Blocklist {
				a.blocklist(ctx, td, opts.SkipRedownload)
			}
			a.tracked.StopTracking(td.DownloadID)
		}
	}

	a.Service.Remove(id)
	return true, nil
}

// blocklist publishes a DownloadFailedEvent for the tracked download.
// Mirrors the C# `_failedDownloadService.MarkAsFailed(trackedDownload,
// skipRedownload)` arm (`QueueController.cs:360`): the already-subscribed
// blocklist + history handlers consume the event and add the blocklist
// row. SkipRedownload is carried verbatim for the redownload-on-failure
// consumer (when one is wired). No-op when no bus is available.
//
// J439 enriches the event Data with the release indexer/size from the
// Subject's Release so the blocklist row carries them. Still deferred:
// the redownload-search consumer for the !skipRedownload arm — a
// follow-up that doesn't affect the core "don't re-grab this release".
func (a *queueRemoveAdapter) blocklist(ctx context.Context, td *trackeddownloads.TrackedDownload, skipRedownload bool) {
	if a.bus == nil {
		return
	}
	ev := trackeddownloads.DownloadFailedEvent{
		DownloadID:      td.DownloadID,
		TrackedDownload: td,
		Message:         "Manually marked as failed",
		Source:          "ManualOverride",
		SkipRedownload:  skipRedownload,
		ReleaseSource:   decisionengine.ReleaseSourceUnknown,
		Data:            map[string]string{"protocol": strconv.Itoa(td.Protocol)},
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
		// J439: carry the release Indexer + Size so the
		// blocklisthandlers consumer populates the blocklist row's
		// Indexer/Size columns (it reads ev.Data["indexer"]/["size"]).
		// Mirrors C# where the blocklist IHandle<DownloadFailedEvent>
		// reads TrackedDownload.RemoteEpisode.Release.{Indexer,Size}.
		if rel := releaseInfoOf(r.Release); rel != nil {
			if rel.Indexer != "" {
				ev.Data["indexer"] = rel.Indexer
			}
			ev.Data["size"] = strconv.FormatInt(rel.Size, 10)
		}
	}
	if ev.SourceTitle == "" && td.DownloadItem != nil {
		ev.SourceTitle = td.DownloadItem.Title
	}
	if err := messaging.Publish(ctx, a.bus, ev); err != nil {
		a.log.Warn("queue remove: blocklist publish failed", "downloadId", td.DownloadID, "err", err)
	}
}

// applyClientSideEffects mirrors C# `Remove(trackedDownload, ...)`
// (`:333-360`): removeFromClient → RemoveItem; else changeCategory →
// MarkItemAsImported. A client that can't be resolved/constructed maps
// to apiv3.ErrQueueDownloadClientUnavailable (→ 400).
func (a *queueRemoveAdapter) applyClientSideEffects(ctx context.Context, td *trackeddownloads.TrackedDownload, opts apiv3.QueueRemoveOptions) error {
	if !opts.RemoveFromClient && !opts.ChangeCategory {
		return nil
	}
	client, err := a.clients.ResolveClient(ctx, td.DownloadClient)
	if err != nil {
		return fmt.Errorf("%w: client %d: %v", apiv3.ErrQueueDownloadClientUnavailable, td.DownloadClient, err)
	}
	switch {
	case opts.RemoveFromClient:
		if err := client.RemoveItem(ctx, td.DownloadItem, true); err != nil {
			return fmt.Errorf("queue remove: RemoveItem: %w", err)
		}
	case opts.ChangeCategory:
		if err := client.MarkItemAsImported(ctx, td.DownloadItem); err != nil {
			return fmt.Errorf("queue remove: MarkItemAsImported: %w", err)
		}
	}
	return nil
}

// Compile-time guards.
var (
	_ apiv3.QueueWriter      = (*queueRemoveAdapter)(nil)
	_ apiv3.QueueItemRemover = (*queueRemoveAdapter)(nil)
)
