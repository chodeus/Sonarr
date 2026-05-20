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
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/signalr"
	"sonarr.tv/sonarr/internal/tags"
)

// QueueStatusBroadcastDeps bundles the dependencies the queue/status
// debounced broadcaster needs to project the live QueueStatusResource
// snapshot on each fire. Decoupled into its own struct so the main
// wireSignalRSubscribers signature doesn't grow per-resource.
//
// DebounceDuration overrides the 5s C# default — production leaves
// it 0 (use the default); tests pass a short value (e.g. 30ms) so
// the debounced fire can be observed without slowing the suite.
type QueueStatusBroadcastDeps struct {
	Queue            apiv3.QueueLister
	Pending          apiv3.PendingLister
	DebounceDuration time.Duration
}

// SeriesPathLookup is the narrow series-lookup surface the
// EpisodeFile broadcasters use at fan-out time to join
// Series.Path onto the projected EpisodeFileResource.Path
// (otherwise the wire carries the bare RelativePath, diverging
// from the C# `Path.Combine(Series.Path, RelativePath)` projection
// at `src/Sonarr.Api.V3/EpisodeFiles/EpisodeFileResourceMapper.cs`).
//
// *series.Service satisfies this via its GetSeries(ctx, id)
// (*Series, error) method — no adapter needed. Pass nil to opt out
// (the projection falls back to bare RelativePath, matching the
// pre-slice-4.FL behaviour).
type SeriesPathLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// wireSignalRSubscribers attaches every per-resource bus subscriber
// the SignalR sub-arc needs. Called once at boot from main.go's
// serve() after the Hub + bus are constructed.
//
// Subscribers live for the lifetime of the bus. Each subscriber
// logs its broadcast errors at Warn level; a single broadcast
// failure must not propagate back to the upstream event handler
// (the underlying domain event has downstream consumers that don't
// care about the SignalR fan-out outcome).
//
// Returns the queue/status debouncer when queueStatus deps were
// supplied; nil otherwise. The GET /api/v3/queue/status handler
// wraps its projection in this debouncer's Pause / Resume so the
// read doesn't re-arm the broadcaster (matches the C#
// QueueStatusController shared-Debouncer pattern at
// `src/Sonarr.Api.V3/Queue/QueueStatusController.cs:49,65`).
func wireSignalRSubscribers(bus *messaging.Bus, hub *signalr.Hub, logger *slog.Logger, queueStatus *QueueStatusBroadcastDeps, seriesPathLookup SeriesPathLookup) *signalr.Debouncer {
	if hub == nil || bus == nil {
		return nil
	}

	// seriesPathFor is the per-event helper the EpisodeFile
	// broadcasters call to resolve Series.Path. Returns "" on any
	// failure path (nil lookup, lookup error, nil result) so the
	// broadcast falls back to the bare RelativePath rather than
	// dropping the event.
	seriesPathFor := func(ctx context.Context, seriesID int) string {
		if seriesPathLookup == nil || seriesID == 0 {
			return ""
		}
		s, err := seriesPathLookup.GetSeries(ctx, seriesID)
		if err != nil || s == nil {
			return ""
		}
		return s.Path
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

	// `health` family: HealthCheckCompleteEvent fires once per
	// scheduled-batch completion + once per event-driven re-check
	// batch. The C# wire-shape emits `sync` with no body — the
	// frontend re-fetches /api/v3/health to discover the new
	// failing-result set.
	healthBroadcaster := &signalr.ResourceBroadcaster[emptySyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "health",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.health.Complete",
		func(ctx context.Context, _ healthcheck.CompleteEvent) error {
			if err := healthBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: health sync broadcast failed", "err", err)
			}
			return nil
		})

	// `queue` + `queue/details` families: same triggers (every
	// QueueUpdatedEvent or PendingReleasesUpdatedEvent) but
	// different broadcaster names so the JS client can subscribe
	// to either or both. queue/status uses a 5s debouncer in the
	// C# tree (BroadcastDebounce.Execute) — DEFERRED until the Go-
	// port adds a per-broadcaster debouncer layer.
	queueBroadcaster := &signalr.ResourceBroadcaster[emptySyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "queue",
		Version:     signalr.V3Version,
	}
	queueDetailsBroadcaster := &signalr.ResourceBroadcaster[emptySyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "queue/details",
		Version:     signalr.V3Version,
	}
	publishQueueSync := func(ctx context.Context, source string) {
		if err := queueBroadcaster.Sync(ctx); err != nil {
			logger.Warn("signalr: queue sync broadcast failed", "source", source, "err", err)
		}
		if err := queueDetailsBroadcaster.Sync(ctx); err != nil {
			logger.Warn("signalr: queue/details sync broadcast failed", "source", source, "err", err)
		}
	}
	messaging.Subscribe(bus, "signalr.queue.QueueUpdated",
		func(ctx context.Context, _ queue.QueueUpdatedEvent) error {
			publishQueueSync(ctx, "queue.QueueUpdated")
			return nil
		})
	messaging.Subscribe(bus, "signalr.queue.PendingReleasesUpdated",
		func(ctx context.Context, _ pendingreleases.PendingReleasesUpdatedEvent) error {
			publishQueueSync(ctx, "pendingreleases.PendingReleasesUpdated")
			return nil
		})

	// `queue/status` family: same triggers as `queue` BUT the
	// emission is debounced 5s + carries a populated
	// QueueStatusResource body (not Sync). The C# wire-shape:
	// `{name:"queue/status", version:3, body:{resource:<status>,
	// action:"updated"}}`. The debouncer's fire callback rebuilds
	// the snapshot from the live queue + pending listers; bursty
	// triggers within the 5s window coalesce into one wire emit.
	//
	// queueStatus may be nil during early boot — without the
	// projection deps we skip wiring the debounced broadcaster
	// (Sync via the `queue` namespace still fires; only the
	// populated-body `updated` envelope is missed).
	var queueStatusDebouncer *signalr.Debouncer
	if queueStatus != nil && queueStatus.Queue != nil {
		queueStatusBroadcaster := &signalr.ResourceBroadcaster[apiv3.QueueStatusResource]{
			Broadcaster: signalr.HubBroadcaster{Hub: hub},
			Name:        "queue/status",
			Version:     signalr.V3Version,
		}
		// Capture the deps + broadcaster in the closure so the
		// debouncer's no-arg fire callback can project the
		// snapshot on demand. The 5s window matches the C#
		// QueueStatusController's `_broadcastDebounce =
		// new Debouncer(BroadcastUpdated, TimeSpan.FromSeconds(5))`
		// at `src/Sonarr.Api.V3/Queue/QueueStatusController.cs:31`.
		debounceDur := queueStatus.DebounceDuration
		if debounceDur <= 0 {
			debounceDur = 5 * time.Second
		}
		queueStatusDebouncer = signalr.NewDebouncer(func() {
			ctx := context.Background()
			body := apiv3.BuildQueueStatus(ctx, queueStatus.Queue, queueStatus.Pending)
			if err := queueStatusBroadcaster.Updated(ctx, body); err != nil {
				logger.Warn("signalr: queue/status updated broadcast failed", "err", err)
			}
		}, debounceDur)
		messaging.Subscribe(bus, "signalr.queueStatus.QueueUpdated",
			func(_ context.Context, _ queue.QueueUpdatedEvent) error {
				queueStatusDebouncer.Execute()
				return nil
			})
		messaging.Subscribe(bus, "signalr.queueStatus.PendingReleasesUpdated",
			func(_ context.Context, _ pendingreleases.PendingReleasesUpdatedEvent) error {
				queueStatusDebouncer.Execute()
				return nil
			})
		// The returned debouncer is wired into graceful shutdown
		// at cmd/sonarr/main.go (slice 4.FJ): after the worker
		// goroutines exit but before the bus drains, main.go
		// calls debouncer.Stop() to release the pending timer.
		// Stop is idempotent + a no-op on subsequent Execute()s
		// so any stray bus event mid-shutdown won't re-arm it.
	}

	// `series` family: populated-body emits with the projected
	// apiv3.SeriesResource. Three triggers:
	//   - SeriesAddedEvent → Created (Resource = the inserted series)
	//   - SeriesEditedEvent → Updated (Resource = the new series)
	//   - SeriesDeletedEvent → Deleted, fanned per row in the batch
	//     (the C# V3 path emits a full SeriesResource on delete,
	//     not just an Id — preserves UI state for transient
	//     deletions where the resource still has data)
	// Sync emits would come from SeriesBulkEditedEvent /
	// SeriesImportedEvent / SeriesRenamedEvent / MediaCoversUpdated
	// but those events aren't ported (or aren't easily projected
	// from the batch shape) — DEFERRED to follow-up slices.
	seriesBroadcaster := &signalr.ResourceBroadcaster[apiv3.SeriesResource]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "series",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.series.Added",
		func(ctx context.Context, ev series.SeriesAddedEvent) error {
			if ev.Series == nil {
				return nil
			}
			if err := seriesBroadcaster.Created(ctx, apiv3.ToSeriesResource(*ev.Series)); err != nil {
				logger.Warn("signalr: series created broadcast failed", "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "signalr.series.Edited",
		func(ctx context.Context, ev series.SeriesEditedEvent) error {
			if ev.Series == nil {
				return nil
			}
			if err := seriesBroadcaster.Updated(ctx, apiv3.ToSeriesResource(*ev.Series)); err != nil {
				logger.Warn("signalr: series updated broadcast failed", "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "signalr.series.Deleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			for _, s := range ev.Series {
				if err := seriesBroadcaster.Deleted(ctx, apiv3.ToSeriesResource(s)); err != nil {
					logger.Warn("signalr: series deleted broadcast failed", "id", s.ID, "err", err)
				}
			}
			return nil
		})

	// `episodefile` family: populated-body emits via the projected
	// apiv3.EpisodeFileResource. Two triggers wired today:
	//   - EpisodeFileAddedEvent → Created (resource projected from
	//     the event's EpisodeFile; the event itself doesn't carry
	//     Series, so slice 4.FL looks it up at broadcast time via
	//     SeriesPathLookup to produce the C# `Path.Combine(Series.Path,
	//     RelativePath)` wire shape. When the lookup is nil or fails
	//     the projection falls back to the bare RelativePath, matching
	//     the pre-4.FL behaviour and the Path.Combine("", path) C#
	//     edge case at EpisodeFileResourceMapper.cs.)
	//   - EpisodeFileDeletedEvent → Deleted (resource projected
	//     from the event's EpisodeFile + the carried Series.Path
	//     if Series is non-nil — preferred over the lookup since the
	//     event captures Series as of delete-time; falls through to
	//     the lookup when Series is nil so producers that don't
	//     populate it still get the joined path)
	// Slice 5.J24 closes the Updated path (audit B.2 second wave):
	// episodefiles.Service.Update / UpdateMany now publish
	// EpisodeFileUpdatedEvent (the Go-port stand-in for C#'s
	// ModelEvent<EpisodeFile>). Subscribed below as `Updated`.
	//
	// EpisodeFileRenamedEvent path remains DEFERRED — Renamed needs
	// an "updated path" envelope shape the C# wire-spec doesn't pin
	// precisely; the api/v3/rename path doesn't currently emit it
	// in either tree.
	episodeFileBroadcaster := &signalr.ResourceBroadcaster[apiv3.EpisodeFileResource]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "episodefile",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.episodefile.Added",
		func(ctx context.Context, ev episodefiles.EpisodeFileAddedEvent) error {
			if ev.EpisodeFile == nil {
				return nil
			}
			res := apiv3.ToEpisodeFileResource(*ev.EpisodeFile, seriesPathFor(ctx, ev.EpisodeFile.SeriesID))
			if err := episodeFileBroadcaster.Created(ctx, res); err != nil {
				logger.Warn("signalr: episodefile created broadcast failed", "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "signalr.episodefile.Deleted",
		func(ctx context.Context, ev episodefiles.EpisodeFileDeletedEvent) error {
			if ev.EpisodeFile == nil {
				return nil
			}
			seriesPath := ""
			if ev.Series != nil {
				seriesPath = ev.Series.Path
			} else {
				seriesPath = seriesPathFor(ctx, ev.EpisodeFile.SeriesID)
			}
			res := apiv3.ToEpisodeFileResource(*ev.EpisodeFile, seriesPath)
			if err := episodeFileBroadcaster.Deleted(ctx, res); err != nil {
				logger.Warn("signalr: episodefile deleted broadcast failed", "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "signalr.episodefile.Updated",
		func(ctx context.Context, ev episodefiles.EpisodeFileUpdatedEvent) error {
			if ev.EpisodeFile == nil {
				return nil
			}
			seriesPath := ""
			if ev.Series != nil {
				seriesPath = ev.Series.Path
			} else {
				seriesPath = seriesPathFor(ctx, ev.EpisodeFile.SeriesID)
			}
			res := apiv3.ToEpisodeFileResource(*ev.EpisodeFile, seriesPath)
			if err := episodeFileBroadcaster.Updated(ctx, res); err != nil {
				logger.Warn("signalr: episodefile updated broadcast failed", "err", err)
			}
			return nil
		})

	// `system/task` family: scheduled-task state changes. The C#
	// inventory lists two drivers — ModelEvent<ScheduledTask> +
	// CommandExecutedEvent — both emitting `sync`. The Go port has
	// commands.CommandExecutedEvent (fired after every command's
	// Complete/Fail call); ModelEvent<ScheduledTask> isn't wired
	// because the Go scheduledtasks package doesn't publish per-
	// row mutation events today. Single subscriber wires the
	// half we have.
	systemTaskBroadcaster := &signalr.ResourceBroadcaster[emptySyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "system/task",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.systemTask.CommandExecuted",
		func(ctx context.Context, _ commands.CommandExecutedEvent) error {
			if err := systemTaskBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: system/task sync broadcast failed", "err", err)
			}
			return nil
		})

	// Slice 5.J25 (audit B.2 second wave): `command` family —
	// distinct from system/task. CommandUpdatedEvent fires from
	// commands.Service on every lifecycle transition (Push /
	// PushMany / Start / Complete / Fail / Cancel). C# emits
	// `Updated` ResourceChange per command via a 100ms debouncer
	// (`CommandController.cs:98-128`); the Go port broadcasts
	// directly without debounce — see CommandUpdatedEvent
	// doc-comment for the divergence note. The
	// SendUpdatesToClient filter from C# `CommandController.cs:100`
	// is also deferred until the command Body schema lands typed
	// access; until then every command broadcasts.
	commandBroadcaster := &signalr.ResourceBroadcaster[apiv3.CommandResource]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "command",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.command.Updated",
		func(ctx context.Context, ev commands.CommandUpdatedEvent) error {
			if ev.Command == nil {
				return nil
			}
			res := apiv3.ToCommandResource(*ev.Command)
			if err := commandBroadcaster.Updated(ctx, res); err != nil {
				logger.Warn("signalr: command updated broadcast failed", "err", err)
			}
			return nil
		})

	// Slice 5.J26 (audit B.2 second wave): `episode` channel —
	// EpisodeMonitoredChangedEvent fires from episodes.Service on
	// SetEpisodeMonitored / SetMonitored / SetEpisodeMonitoredBySeason.
	// C# broadcasts per-row Updated via the generic ModelEvent<Episode>
	// inherited handler; the Go-port matches the audit spec by
	// emitting Sync on the `episode` channel (the React UI refetches
	// the relevant episode list on Sync, so the wire shape is
	// functionally equivalent for the monitor-toggle use case). A
	// later slice can tighten this to per-row Updated with the
	// projected EpisodeResource once the Episode→Resource mapper is
	// reachable from this file without forming an import cycle.
	episodeBroadcaster := &signalr.ResourceBroadcaster[emptySyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "episode",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.episode.MonitoredChanged",
		func(ctx context.Context, _ episodes.EpisodeMonitoredChangedEvent) error {
			if err := episodeBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: episode sync broadcast failed", "err", err)
			}
			return nil
		})

	// Audit fix B.2 (first wave). `qualityprofile` family —
	// QualityProfileUpdatedEvent fires from qualityprofiles.Service
	// on Add / Update / Delete (the service publishes a single
	// Updated event regardless of CUD kind). C# emits Sync without a
	// resource body for QualityProfile changes; the Go-port matches.
	qualityProfileBroadcaster := &signalr.ResourceBroadcaster[emptySyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "qualityprofile",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.qualityprofile.Updated",
		func(ctx context.Context, _ qualityprofiles.QualityProfileUpdatedEvent) error {
			if err := qualityProfileBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: qualityprofile sync broadcast failed", "err", err)
			}
			return nil
		})

	// Audit fix B.2. `customformat` family —
	// customformats.Service publishes CustomFormatAddedEvent on
	// Insert (5.J20), CustomFormatDeletedEvent on Delete (5.J20), and
	// CustomFormatUpdatedEvent on Update (5.J27 — Go-port additive,
	// C# `CustomFormatService.cs:51-55` doesn't emit; its UI sync on
	// Update flows through the inherited ModelEvent<CustomFormat>
	// pipeline which the Go port doesn't have).
	customFormatBroadcaster := &signalr.ResourceBroadcaster[emptySyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "customformat",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.customformat.Added",
		func(ctx context.Context, _ customformats.CustomFormatAddedEvent) error {
			if err := customFormatBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: customformat add sync broadcast failed", "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "signalr.customformat.Deleted",
		func(ctx context.Context, _ customformats.CustomFormatDeletedEvent) error {
			if err := customFormatBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: customformat delete sync broadcast failed", "err", err)
			}
			return nil
		})
	messaging.Subscribe(bus, "signalr.customformat.Updated",
		func(ctx context.Context, _ customformats.CustomFormatUpdatedEvent) error {
			if err := customFormatBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: customformat update sync broadcast failed", "err", err)
			}
			return nil
		})

	// Slice 5.J28 (audit B.2 second wave): `blocklist` channel.
	// BlocklistChangedEvent fires from blocklist.Service Delete /
	// DeleteByIDs / DeleteForSeries (Go-port additive — C#
	// `BlocklistService.cs:165-173` doesn't emit a typed event; its
	// SignalR sync flows through the inherited generic
	// ModelEvent<Blocklist>). The React UI's blocklist list refetches
	// on Sync; per-row Updated/Deleted projections are a tightening
	// for a later slice if needed.
	blocklistBroadcaster := &signalr.ResourceBroadcaster[emptySyncPayload]{
		Broadcaster: signalr.HubBroadcaster{Hub: hub},
		Name:        "blocklist",
		Version:     signalr.V3Version,
	}
	messaging.Subscribe(bus, "signalr.blocklist.Changed",
		func(ctx context.Context, _ blocklist.BlocklistChangedEvent) error {
			if err := blocklistBroadcaster.Sync(ctx); err != nil {
				logger.Warn("signalr: blocklist sync broadcast failed", "err", err)
			}
			return nil
		})

	return queueStatusDebouncer
}

// tagSyncPayload is the placeholder TResource for the `tag` Sync
// path. A future per-tag Created/Updated/Deleted slice will introduce
// a richer type wired to the v3 TagResource projection.
type tagSyncPayload struct{}

// emptySyncPayload is the shared placeholder TResource for resource
// families whose SignalR envelope is Sync-only — health, queue,
// queue/details. Sync bodies carry no resource on the wire so the
// generic type parameter only needs to satisfy the constraint.
type emptySyncPayload struct{}
