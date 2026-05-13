package signalr

import "context"

// ResourceBroadcaster is the per-resource dispatch helper every
// controller-side bus subscriber uses to translate domain events
// into SignalR fan-out. Mirrors the role of C# `RestControllerWith
// SignalR<TResource, TModel>.BroadcastResourceChange(...)` at
// `src/Sonarr.Http/REST/RestControllerWithSignalR.cs:59-115` — the
// engine each resource controller inherits from.
//
// Wiring: a resource controller (Tag, Series, Queue, Health, …)
// subscribes to its domain event via messaging.Bus, then calls one
// of the three Broadcast* methods. The generic T parameter pins the
// resource shape — the body marshals as `ResourceChangeMessage[T]`
// so the wire JSON carries the same field set the corresponding
// REST list endpoint would.
//
// The IsConnected short-circuit fires BEFORE the resource is
// projected so a typical "nobody listening" hot path skips both the
// projection work and the broadcast allocation. Mirrors C#
// `if (!_broadcaster.IsConnected) return;` guard every controller
// applies (see `RestControllerWithSignalR.cs:69`).
//
// Version is the per-API namespace integer (3 for V3, 5 for V5)
// pinned at construction. The C# tree runs V3 and V5 controllers
// side-by-side and BOTH broadcast for the same underlying event —
// the Go port preserves that by wiring two ResourceBroadcasters per
// resource family (one with V3Version, one with V5Version) once the
// V5 API surface starts landing.
type ResourceBroadcaster[T any] struct {
	// Broadcaster is the fan-out sink. In production this is a
	// HubBroadcaster wrapping the running Hub; in tests a
	// NoopBroadcaster or a custom recording stub.
	Broadcaster Broadcaster
	// Name is the resource identifier the frontend listens for
	// ("tag", "series", "queue", "health", ...). Mirrors C#
	// `[VersionedApiController]` Resource attribute extracted by
	// RestControllerWithSignalR's constructor at :22-23.
	Name string
	// Version is the per-API version integer pointer. Pass
	// V3Version or V5Version. Nil emits as JSON null (only the
	// connection-handshake "version" event uses null).
	Version *int
}

// Created emits a `created` resource-change event with the
// populated resource. Mirrors C# `BroadcastResourceChange(
// ModelAction.Created, resource)` path.
func (rb *ResourceBroadcaster[T]) Created(ctx context.Context, resource T) error {
	return rb.broadcast(ctx, NewResourceChange(resource, ActionCreated))
}

// Updated emits an `updated` resource-change event with the
// populated resource. Mirrors C# `BroadcastResourceChange(
// ModelAction.Updated, resource)`.
func (rb *ResourceBroadcaster[T]) Updated(ctx context.Context, resource T) error {
	return rb.broadcast(ctx, NewResourceChange(resource, ActionUpdated))
}

// Deleted emits a `deleted` resource-change event. C# typically
// passes a minimal resource carrying only the Id (per
// `BroadcastResourceChange(action, id)` at :59-67) though some
// controllers pass the full pre-delete resource (V3
// SeriesDeletedEvent). The caller picks the shape; the broadcaster
// just wraps it.
func (rb *ResourceBroadcaster[T]) Deleted(ctx context.Context, resource T) error {
	return rb.broadcast(ctx, NewResourceDelete(resource))
}

// Sync emits a `sync` resource-change event with no body resource.
// The frontend uses this as "your snapshot is stale; refetch via
// REST" — typically fired after bulk operations (TagsUpdatedEvent,
// AutoTagsUpdatedEvent, QueueUpdatedEvent, etc.).
func (rb *ResourceBroadcaster[T]) Sync(ctx context.Context) error {
	return rb.broadcast(ctx, NewResourceSync[T]())
}

func (rb *ResourceBroadcaster[T]) broadcast(ctx context.Context, body ResourceChangeMessage[T]) error {
	if rb.Broadcaster == nil || !rb.Broadcaster.IsConnected() {
		return nil
	}
	return rb.Broadcaster.Broadcast(ctx, SignalRMessage{
		Name:    rb.Name,
		Body:    body,
		Version: rb.Version,
	})
}
