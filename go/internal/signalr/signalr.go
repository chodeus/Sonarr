// Package signalr is the Go port of NzbDrone.SignalR — the single-hub
// broadcast pipeline the web frontend subscribes to for live updates.
//
// Scope of this initial slice:
//
//   - Envelope types (SignalRMessage + ResourceChangeMessage[T]) that
//     match the C# wire shape verbatim. Used by every Go-side producer
//     once the bus→broadcaster wiring lands.
//   - Action enum mirroring `NzbDrone.Core.Datastore.Events.ModelAction`
//     with the four values that drive the body shape (Created /
//     Updated / Deleted / Sync).
//   - Broadcaster interface — the single fan-out surface every
//     resource controller will call. NoopBroadcaster is the default
//     stand-in until the real WebSocket transport ports.
//
// Deferred to subsequent slices (see go/analysis/signalr/inventory.md
// for the full plan):
//
//   - SignalR protocol negotiation (HTTP `POST /signalr/messages
//     /negotiate` handshake + transport selection).
//   - WebSocket transport for `receiveMessage` fan-out.
//   - Long-polling fallback transport.
//   - Per-resource controller-side broadcasters that translate
//     messaging.Bus events into BroadcastMessage calls.
//   - Debouncing for high-rate emitters (CommandController 100ms,
//     QueueStatusController 5s — `Debouncer.Execute` semantics).
//   - Per-connection lifecycle hooks (OnConnect → version-handshake
//     event; OnDisconnect → connection-count maintenance).
//
// The C# server emits identical events on both V3 and V5 namespaces
// with different `version` integers so dual-version clients
// disambiguate. The Go port preserves that posture by carrying
// `Version *int` on every SignalRMessage emit.
package signalr

import (
	"context"
	"encoding/json"
	"fmt"
)

// Action mirrors `NzbDrone.Core.Datastore.Events.ModelAction` —
// the four enum values that drive the SignalR resource-change body
// shape. The wire format is the lower-case enum name (camelCase
// applied by JsonStringEnumConverter):
//
//	created / updated / deleted / sync
type Action int

// Action enum values. Order matches the C# enum so any future
// integer-coded persistence keeps wire-stable.
const (
	// ActionCreated indicates a resource was just inserted.
	ActionCreated Action = iota
	// ActionUpdated indicates an existing resource changed.
	ActionUpdated
	// ActionDeleted indicates a resource was removed. Body carries
	// only the deleted id (per `RestControllerWithSignalR
	// .BroadcastResourceChange(action, id)` at
	// `Sonarr.Http/REST/RestControllerWithSignalR.cs:59-67`).
	ActionDeleted
	// ActionSync is the "your snapshot is stale; refetch via REST"
	// marker. Body omits the `resource` key entirely (C# emits null,
	// then STJson's `WhenWritingNull` drops the property; Go matches
	// via `omitempty` on `ResourceChangeMessage.Resource`).
	ActionSync
)

// String returns the lower-case wire form. Mirrors the C#
// `JsonStringEnumConverter(JsonNamingPolicy.CamelCase)` emit.
func (a Action) String() string {
	switch a {
	case ActionCreated:
		return "created"
	case ActionUpdated:
		return "updated"
	case ActionDeleted:
		return "deleted"
	case ActionSync:
		return "sync"
	}
	return "unknown"
}

// MarshalJSON emits the camelCase string form on the wire so the
// embedded body's `action` field serialises like the C# enum.
func (a Action) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

// SignalRMessage is the outer envelope every server-pushed event
// rides in. Mirrors `NzbDrone.SignalR.SignalRMessage` at
// `src/NzbDrone.SignalR/SignalRMessage.cs:5-15`.
//
// Wire shape:
//
//	Resource event: { "name": "<resource>", "body": {...}, "version": 3 }
//	Handshake event: { "name": "version", "body": {"version": "..."} }
//
// The C# `Action` field is `[JsonIgnore]` on the envelope — the
// action communicates via the body's own `action` field for
// ResourceChangeMessage bodies. The Go port omits Action from the
// envelope entirely; callers stash it in the body.
//
// Version is `*int` so the no-version handshake event can drop the
// key entirely. C# `int? Version` is nullable and SignalR serializes
// via STJson's `WhenWritingNull` (`Startup.cs:219-223` +
// `STJson.cs:27`), which omits null properties — so C# emits
// `{"name":"version","body":{...}}` without the envelope `version`
// key on the handshake. Go matches via `omitempty` on the pointer.
// Resource events keep emitting `"version":3` (or `5`) because the
// `*int` is non-nil (`signalr.V3Version` / `signalr.V5Version`).
type SignalRMessage struct {
	Name    string `json:"name"`
	Body    any    `json:"body"`
	Version *int   `json:"version,omitempty"`
}

// V3Version is the int pointer reused for every V3 emit. Allocated
// once so callers can `Version: signalr.V3Version` without
// per-message heap traffic.
var V3Version = func() *int { v := 3; return &v }()

// V5Version mirrors V3Version for the V5 namespace.
var V5Version = func() *int { v := 5; return &v }()

// ResourceChangeMessage is the body shape every resource-change
// event carries. Generic on TResource so each per-controller emitter
// gets a typed body. Mirrors `Sonarr.Http.ResourceChangeMessage<T>`
// at `src/Sonarr.Http/ResourceChangeMessage.cs:7-28`.
//
// Wire shape:
//
//	Created/Updated/Deleted: { "resource": <TResource>, "action": "..." }
//	Sync:                    { "action": "sync" }
//
// SignalR uses STJson via `services.AddSignalR().AddJsonProtocol(...
// PayloadSerializerOptions = STJson.GetSerializerSettings())` at
// `src/NzbDrone.Host/Startup.cs:219-223`, which sets
// `DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull` at
// `STJson.cs:27`. For Sync bodies the C# `Resource` property is the
// default value (`null` since TResource : RestResource is a reference
// type) so the property is OMITTED from the emitted JSON. The Go port
// matches via `omitempty` on the pointer: nil Resource drops the key
// entirely rather than emitting `"resource":null`. For Deleted bodies
// Resource carries only the Id — callers project to a minimal struct
// rather than handing in a full row.
type ResourceChangeMessage[TResource any] struct {
	Resource *TResource `json:"resource,omitempty"`
	Action   Action     `json:"action"`
}

// NewResourceChange builds a Created/Updated message with a fully
// populated resource. Mirrors `new ResourceChangeMessage<T>(resource,
// action)` at `ResourceChangeMessage.cs:23-27`.
func NewResourceChange[T any](resource T, action Action) ResourceChangeMessage[T] {
	return ResourceChangeMessage[T]{Resource: &resource, Action: action}
}

// NewResourceSync builds a Sync message (no resource). Mirrors
// `new ResourceChangeMessage<T>(ModelAction.Sync)` at
// `ResourceChangeMessage.cs:13-21`. Sync + Deleted are the only
// actions valid without a resource (the C# constructor rejects
// other combinations — the Go port enforces via the constructor
// signature: there is no public way to build a no-resource message
// with action=Created/Updated).
func NewResourceSync[T any]() ResourceChangeMessage[T] {
	return ResourceChangeMessage[T]{Action: ActionSync}
}

// NewResourceDelete builds a Deleted message. Convention is to pass
// a minimal resource carrying only the Id, but the type accepts any
// TResource so callers can include richer payloads (the V3
// SeriesDeletedEvent emits the full SeriesResource, for example).
func NewResourceDelete[T any](resource T) ResourceChangeMessage[T] {
	return ResourceChangeMessage[T]{Resource: &resource, Action: ActionDeleted}
}

// VersionMessage is the body shape of the connection-handshake
// event the hub emits on every new client connection. Mirrors the
// C# `new { Version = BuildInfo.Version.ToString() }` anonymous-
// type body at `src/NzbDrone.SignalR/MessageHub.cs:48`.
type VersionMessage struct {
	Version string `json:"version"`
}

// Broadcaster is the single fan-out surface every producer calls.
// Mirrors `NzbDrone.SignalR.IBroadcastSignalRMessage` at
// `src/NzbDrone.SignalR/IBroadcastSignalRMessage.cs:5-9`. The hub
// implementation tracks live connections + sends to
// `Clients.All.SendAsync("receiveMessage", message)`.
//
// IsConnected is the optimization gate every emitter checks first —
// when no client is listening, the resource projection itself is
// skipped (avoids loading rows just to drop the broadcast).
type Broadcaster interface {
	// IsConnected reports whether ≥1 SignalR client is currently
	// subscribed. Producers should call this BEFORE building the
	// message body to avoid wasted projection work.
	IsConnected() bool
	// Broadcast fans the message to every connected client. The
	// ctx is for graceful-shutdown awareness; an in-flight send
	// may be cancelled when the hub is tearing down. Errors are
	// returned to the caller but typical producers log-and-
	// continue: a single failed broadcast is not worth dropping
	// the upstream event for.
	Broadcast(ctx context.Context, msg SignalRMessage) error
}

// NoopBroadcaster is the default Broadcaster used during boot and
// in tests. Reports IsConnected=false so producers short-circuit;
// Broadcast is a no-op that returns nil. Used by cmd/sonarr until
// the WebSocket transport lands — the resource controllers can
// already call BroadcastResourceChange, but nothing reaches the
// wire until the real broadcaster swaps in.
type NoopBroadcaster struct{}

// IsConnected reports false — the noop broadcaster never has a
// listener attached. Producers short-circuit before projecting
// their resource.
func (NoopBroadcaster) IsConnected() bool { return false }

// Broadcast is a no-op. The ctx is ignored; nil is returned so
// callers don't observe a fake error.
func (NoopBroadcaster) Broadcast(context.Context, SignalRMessage) error { return nil }

// MarshalEnvelope JSON-encodes the outer envelope. Useful for tests
// + the eventual WebSocket transport which wraps the envelope inside
// the SignalR framing layer. Returns the same bytes the wire
// pipeline will send so callers can pin the shape end-to-end.
func MarshalEnvelope(msg SignalRMessage) ([]byte, error) {
	out, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("signalr: marshal envelope: %w", err)
	}
	return out, nil
}
