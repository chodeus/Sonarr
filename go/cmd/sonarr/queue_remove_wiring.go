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

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

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
			a.tracked.StopTracking(td.DownloadID)
		}
	}

	a.Service.Remove(id)
	return true, nil
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
