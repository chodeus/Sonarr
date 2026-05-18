package signalr

import (
	"context"
	"log/slog"
)

// NewV3Emitter constructs a per-resource ResourceBroadcaster wired
// to the running hub with the V3 API version preset. v3 handlers
// receive one of these per resource family (Tag, Series, Queue,
// etc.) and call Created/Updated/Deleted/Sync at write sites.
//
// Use NewV5Emitter when wiring the V5 controller surface — both
// versions broadcast for the same underlying domain event in C#
// Sonarr (see `src/Sonarr.Http/REST/RestControllerWithSignalR.cs:22`
// per-resource attribute extraction), and the Go port mirrors that
// fan-out with one emitter per (resource, version) pair.
//
// Closes the "SignalR broadcast plumbing exists but is never
// invoked" gap surfaced by the Phase 5 audit: until this factory
// landed, every write handler would have had to construct its own
// ResourceBroadcaster literal with HubBroadcaster{Hub: hub} + Name +
// Version triple. The factory keeps the wire-shape choice (V3 vs V5)
// at the host-wiring boundary instead of repeated in every handler
// signature.
//
// hub may be nil during tests — the resulting emitter's broadcast
// path short-circuits via the HubBroadcaster.IsConnected guard and
// no-ops, so callers don't need to special-case "no hub".
func NewV3Emitter[T any](hub *Hub, name string) *ResourceBroadcaster[T] {
	return &ResourceBroadcaster[T]{
		Broadcaster: HubBroadcaster{Hub: hub},
		Name:        name,
		Version:     V3Version,
	}
}

// NewV5Emitter mirrors [NewV3Emitter] for the V5 API namespace.
// Reserved for when V5 controllers start landing; included now so
// the V3/V5 symmetry the C# tree depends on doesn't get lost in
// later wiring.
func NewV5Emitter[T any](hub *Hub, name string) *ResourceBroadcaster[T] {
	return &ResourceBroadcaster[T]{
		Broadcaster: HubBroadcaster{Hub: hub},
		Name:        name,
		Version:     V5Version,
	}
}

// FireAndLog runs broadcastFn and, if it returns a non-nil error,
// logs it at slog.Warn with the resource name and action. Designed
// for write-site handlers that want fire-and-forget semantics but
// still want operator observability when a broadcast fails (slow
// consumer dropped, hub shut down mid-write, etc.).
//
// Typical use at a write site:
//
//	signalr.FireAndLog(logger, tagEmitter.Name, "created", func() error {
//	    return tagEmitter.Created(ctx, resource)
//	})
//
// The C# pipeline doesn't surface broadcast errors to operators —
// they're swallowed deep in SignalR core. The Go port can do better
// here because the ResourceBroadcaster API returns an error; the
// logging keeps that observability without forcing every handler to
// write the same err-check boilerplate.
//
// logger nil is safe — when nil, the function still runs broadcastFn
// (so the broadcast happens) but swallows the error silently.
func FireAndLog(logger *slog.Logger, name, action string, broadcastFn func() error) {
	if err := broadcastFn(); err != nil && logger != nil {
		logger.Warn("signalr broadcast failed",
			"resource", name,
			"action", action,
			"err", err,
		)
	}
}

// fireAndLogContext is an internal convenience used by some
// emitter helpers when a context is in scope at the call site.
// Exposed via the public FireAndLog signature for callers that
// already have one.
//
//nolint:unused // kept for the v3 handler integration layer landing in 5.A4.
func fireAndLogContext(ctx context.Context, logger *slog.Logger, name, action string, fn func(context.Context) error) {
	FireAndLog(logger, name, action, func() error { return fn(ctx) })
}
