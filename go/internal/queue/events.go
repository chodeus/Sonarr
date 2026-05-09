package queue

// QueueUpdatedEvent ports the C# `Queue/QueueUpdatedEvent.cs:1-8`
// marker event. Published by [Service] after every successful
// SetQueue replace (the C# tree publishes it from
// `QueueService.Handle(TrackedDownloadRefreshedEvent)` at
// `:104` after the cache rebuild).
//
// Marker shape: zero fields. Subscribers consume it as a "queue
// state changed; recompute UI / push SignalR" signal. The Phase-3
// SignalR push consumer ports later (Phase 5); the v3 API handler
// uses GetQueue's snapshot semantics rather than the event for
// freshness, so the event is "lit but no listeners" until Phase 5.
//
// Wire-stable: zero fields means no JSON payload to drift; future
// fields added APPEND-only.
type QueueUpdatedEvent struct{}
