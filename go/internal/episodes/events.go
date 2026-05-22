package episodes

// EpisodeMonitoredChangedEvent is a Go-port additive event fired by
// [Service.SetEpisodeMonitored], [Service.SetMonitored], and
// [Service.SetEpisodeMonitoredBySeason] after a successful repo
// flip.
//
// C# Sonarr emits the equivalent signal via Dapper's implicit
// `ModelEvent<Episode>` (the persistence layer fires it on every
// Repository.Update call — see
// `src/NzbDrone.Core/Datastore/Events/ModelEvent.cs` +
// `BasicRepository<T>.UpdateFields`). EpisodeController's
// `Handle(ModelEvent<Episode>)` (inherited from
// RestControllerWithSignalR) then broadcasts a per-row Updated
// resource-change on the `episode` channel
// (`src/Sonarr.Http/REST/RestControllerWithSignalR.cs:37-50`). The
// Go port doesn't have a generic ModelEvent analogue, so each
// Service that needs the "row mutated" signal publishes its own
// typed event.
//
// Consumers: the SignalR `episode` channel's Sync broadcaster
// (cmd/sonarr/signalr_wiring.go subscriber) — the React UI listens
// on Sync to refresh the episode list after a monitor-toggle. The
// audit-fix B.2 row specifies Sync (matches the existing
// qualityprofile / customformat pattern); per-row Updated with the
// projected EpisodeResource is a tightening for a later slice once
// the Episode→Resource mapper is reachable from the wiring file
// without forming a cycle.
//
// `IDs` carries the episode IDs whose monitored flag changed. Empty
// when the producer can't easily enumerate (e.g.
// SetEpisodeMonitoredBySeason which uses a bulk WHERE without
// returning affected IDs); consumers must not assume non-nil.
//
// `Monitored` is the new value applied to all carried IDs (same
// value for every ID in a single event — the C# bulk API has the
// same semantic).
type EpisodeMonitoredChangedEvent struct {
	IDs       []int
	Monitored bool
}

// EpisodeInfoRefreshedEvent ports C# `Tv/Events/EpisodeInfoRefreshedEvent.cs`.
// Published by the refreshepisodes service (J200) at the tail of a
// per-series episode reconcile pass. Carries the three slices the
// reconcile loop produced: inserted episodes (newly-seeded rows),
// updated episodes (existing rows whose metadata was refreshed), and
// removed episodes (rows the upstream no longer returns — pending
// DeleteMany).
//
// C# subscribers: ImportListSyncService (invalidates the import-list
// cache when episodes change), EpisodeUpdated SignalR broadcaster.
// Go-port subscribers will grow as those services land in
// Phase 5/6 — the event is published unconditionally so the wire shape
// is stable when consumers attach.
type EpisodeInfoRefreshedEvent struct {
	SeriesID int
	Added    []Episode
	Updated  []Episode
	Removed  []Episode
}
