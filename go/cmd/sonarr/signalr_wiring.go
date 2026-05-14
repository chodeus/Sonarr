// SignalR bus subscribers — the boot-time wiring that translates
// in-process domain events into SignalR fan-out. Mirrors the
// per-controller IHandle<TEvent> handlers in C# (every
// RestControllerWithSignalR subclass + the explicit Handle methods
// on QueueController/HealthController/CommandController/...).
//
// Each subscriber:
//
//  1. Subscribes to a messaging.Bus event type.
//  2. Builds a SignalRMessage envelope (resource name + version + body).
//  3. Calls broadcaster.Broadcast — IsConnected short-circuit
//     happens inside ResourceBroadcaster, so a busy hot path with
//     no listeners is just a sync.RWMutex read.
//
// The slice 4.EV first wave covers `tag` (TagsUpdatedEvent +
// AutoTagsUpdatedEvent → Sync). Subsequent slices add the rest of
// the resource families per go/analysis/signalr/inventory.md.

package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/signalr"
	"sonarr.tv/sonarr/internal/tags"
)

// wireSignalRSubscribers attaches every per-resource bus subscriber
// the SignalR sub-arc needs. Called once at boot from main.go's
// serve() after the Hub + bus are constructed.
//
// The function returns nothing — subscribers live for the lifetime
// of the bus. Each subscriber logs its broadcast errors at Warn
// level; a single broadcast failure must not propagate back to the
// upstream event handler (the underlying domain event has
// downstream consumers that don't care about the SignalR fan-out
// outcome).
func wireSignalRSubscribers(bus *messaging.Bus, hub *signalr.Hub, logger *slog.Logger) {
	if hub == nil || bus == nil {
		return
	}

	// `tag` family: both TagsUpdatedEvent (from internal/tags) and
	// AutoTagsUpdatedEvent (from internal/autotagging) emit Sync —
	// the C# wire-shape carries no resource on Sync, just the
	// "snapshot is stale; refetch" signal.
	tagBroadcaster := &signalr.ResourceBroadcaster[tagSyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "tag",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.tag.TagsUpdated",
		func(ctx context.Context, _ tags.TagsUpdatedEvent) error {
			if err := tagBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: tag sync broadcast failed", "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "signalr.tag.AutoTagsUpdated",
		func(ctx context.Context, _ autotagging.AutoTagsUpdatedEvent) error {
			if err := tagBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: autotag sync broadcast failed", "err", err)
			}
			return nil
		})
}

// tagSyncPayload is the placeholder TResource for the `tag` Sync
// path — Sync bodies carry no resource on the wire so the type
// parameter only needs to satisfy the generic constraint. A future
// Per-tag Created/Updated/Deleted slice will introduce a richer
// type wired to the v3 TagResource projection; for now an empty
// struct keeps the ResourceBroadcaster contract minimal.
type tagSyncPayload struct{}
