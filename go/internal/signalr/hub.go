package signalr

import (
	"context"
	"errors"
	"sync"
)

// Hub is the Go port of NzbDrone.SignalR.MessageHub at
// `src/NzbDrone.SignalR/MessageHub.cs:26-73` — the single broadcast
// pipeline every server-pushed event fans out through.
//
// Scope of this slice (Phase 5 P2b):
//
//   - In-process connection registry keyed by ConnID with a buffered
//     send channel per connection.
//   - OnConnect / OnDisconnect lifecycle hooks the future transport
//     layer (WebSocket handler) will call as raw connections open
//     and close.
//   - Broadcast fan-out that pushes one message into every live
//     channel. Slow consumers don't block the producer: when a
//     channel's buffer is full the message is dropped for that
//     connection (the client must reconcile via REST on reconnect,
//     matching the C# "no server-side missed-event buffer" posture).
//   - Version-handshake event fired into Clients.All on every new
//     connection (mirrors C# MessageHub.OnConnectedAsync at :41-58).
//
// Deferred to subsequent slices:
//
//   - WebSocket transport that wraps each connection's outbound
//     channel into the SignalR protocol framing.
//   - Negotiation endpoint at POST /signalr/messages/negotiate.
//   - Long-polling fallback transport.
type Hub struct {
	// channelBuffer is the per-connection send buffer depth. Picked
	// small enough that backpressure surfaces fast (slow client →
	// drop, not memory-pin), large enough that bursty emitters
	// (CommandUpdatedEvent debouncer fires every 100ms; queue
	// updates fan in from several producers) don't lose messages
	// to a single slow tick.
	channelBuffer int

	// hostVersion is the value sent inside the connection-handshake
	// VersionMessage body. Wired at hub construction from
	// BuildInfo.Version (the C# `BuildInfo.Version.ToString()` at
	// MessageHub.cs:50). Empty when boot hasn't populated it yet.
	hostVersion string

	mu          sync.RWMutex
	connections map[ConnID]*hubConn
}

// hubConn is the per-connection state the Hub tracks. The send
// channel carries outbound messages. The done signal is closed by
// OnDisconnect; transport readers should observe done alongside the
// send channel and exit when done is closed. This avoids the
// "close the channel concurrently with a send" race that the naïve
// approach (closing send-channel on disconnect) creates — the Go
// race detector treats close-vs-send as a hard race even when the
// `default` branch of a select prevents the actual panic.
type hubConn struct {
	ch   chan SignalRMessage
	done chan struct{}
}

// ConnID is the transport-level connection identifier. The
// WebSocket handler picks a fresh UUID per accepted connection and
// hands it to OnConnect; the same identifier is passed to
// OnDisconnect when the socket closes.
type ConnID string

// HubOption configures a Hub at construction.
type HubOption func(*Hub)

// WithChannelBuffer sets the per-connection send buffer depth.
// Default 32. Tune up for high-throughput emitters; tune down to
// surface client slowness sooner.
func WithChannelBuffer(n int) HubOption {
	return func(h *Hub) { h.channelBuffer = n }
}

// WithHostVersion pins the value the version-handshake event
// carries (BuildInfo.Version equivalent). Empty defaults to "dev"
// so a missing wire-up surfaces visibly rather than silently emit
// blanks.
func WithHostVersion(v string) HubOption {
	return func(h *Hub) { h.hostVersion = v }
}

// NewHub constructs an empty Hub. Connection registration happens
// via OnConnect; Broadcast fans out to all current connections.
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		channelBuffer: 32,
		hostVersion:   "dev",
		connections:   make(map[ConnID]*hubConn),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Conn is the read-side view of a connection's outbound queue + its
// disconnect signal. The transport handler reads Messages until
// Done fires, then exits.
type Conn struct {
	Messages <-chan SignalRMessage
	Done     <-chan struct{}
}

// ErrConnAlreadyRegistered surfaces when OnConnect is called twice
// with the same ConnID without an intervening OnDisconnect. Indicates
// a transport-layer bug — a healthy SignalR handshake assigns a
// fresh ConnID per accepted socket.
var ErrConnAlreadyRegistered = errors.New("signalr: conn already registered")

// OnConnect registers a new connection, allocates its send channel,
// and fires the version-handshake event into Clients.All.
//
// Returns a Conn carrying the outbound message channel + the
// disconnect signal. The transport handler reads Messages until
// Done fires, then returns.
//
// Mirrors C# `MessageHub.OnConnectedAsync` at
// `src/NzbDrone.SignalR/MessageHub.cs:41-58`. The C# tree adds the
// id to `_connections` THEN sends the handshake to `Clients.All`;
// the Go port preserves that order so the new connection sees its
// own handshake event (the C# `Clients.All.SendAsync` fans to every
// member of `_connections`, including the just-added one).
func (h *Hub) OnConnect(ctx context.Context, id ConnID) (Conn, error) {
	h.mu.Lock()
	if _, exists := h.connections[id]; exists {
		h.mu.Unlock()
		return Conn{}, ErrConnAlreadyRegistered
	}
	conn := &hubConn{
		ch:   make(chan SignalRMessage, h.channelBuffer),
		done: make(chan struct{}),
	}
	h.connections[id] = conn
	h.mu.Unlock()

	// Fire the version-handshake to every connection including the
	// just-registered one. Use the public Broadcast path so a
	// future hook on Broadcast (logging, metrics) sees the
	// handshake too. The send happens AFTER releasing the
	// registration lock so a slow channel doesn't pin the lock.
	_ = h.Broadcast(ctx, SignalRMessage{
		Name: "version",
		Body: VersionMessage{Version: h.hostVersion},
		// Version pointer is nil — the handshake event carries no
		// per-API version (matches C# `Version: null` default on
		// the anonymous-typed SignalRMessage at MessageHub.cs:47-53).
	})

	return Conn{Messages: conn.ch, Done: conn.done}, nil
}

// OnDisconnect removes the connection from the registry and fires
// its `done` signal so the transport reader can exit. Idempotent:
// calling with an unknown ConnID is a no-op (the C#
// `_connections.Remove` skips the missing case silently).
//
// The outbound message channel is intentionally NOT closed — Go
// forbids closing a channel that may be concurrently sent to, and
// a Broadcast snapshot taken just before OnDisconnect can still
// reach the slow-consumer `default` branch. The done channel is
// strictly read-only from the Broadcast side, sidestepping the
// race.
//
// Mirrors C# `MessageHub.OnDisconnectedAsync` at `:60-67`.
func (h *Hub) OnDisconnect(id ConnID) {
	h.mu.Lock()
	conn, ok := h.connections[id]
	if ok {
		delete(h.connections, id)
	}
	h.mu.Unlock()
	if ok {
		close(conn.done)
	}
}

// IsConnected reports whether ≥1 client is currently registered.
// Mirrors C# `MessageHub.IsConnected` static accessor at `:28-34`.
// Used by every producer to short-circuit resource-projection work
// when nobody is listening.
func (h *Hub) IsConnected() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections) > 0
}

// Broadcast fans the message into every live connection's send
// channel. Slow consumers (full buffer) drop the message for that
// connection — the producer is never blocked.
//
// Mirrors C# `SignalRMessageBroadcaster.BroadcastMessage` at
// `src/NzbDrone.SignalR/MessageHub.cs:18-22` which does
// `_hubContext.Clients.All.SendAsync("receiveMessage", message)` —
// every client gets the same message unconditionally.
//
// The ctx is observed during the non-blocking sends: if ctx is
// cancelled mid-fanout (host shutdown), remaining connections are
// skipped. Returns nil unless ctx was already cancelled before the
// first send.
func (h *Hub) Broadcast(ctx context.Context, msg SignalRMessage) error {
	// Snapshot the connection list under a read lock so the
	// producer doesn't hold the lock across slow sends. Connections
	// added after the snapshot won't receive this message; for
	// connections removed after the snapshot the per-conn `done`
	// channel is closed by OnDisconnect and the select below
	// observes it as "consumer is gone, drop this message".
	h.mu.RLock()
	snapshot := make([]*hubConn, 0, len(h.connections))
	for _, c := range h.connections {
		snapshot = append(snapshot, c)
	}
	h.mu.RUnlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	for _, c := range snapshot {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			// Consumer disconnected between snapshot + send;
			// drop without touching the (still-open) send channel.
		case c.ch <- msg:
		default:
			// Full buffer → drop for this connection. The client
			// must reconcile via REST on next reconnect; same
			// posture as C# `Clients.All.SendAsync` which fires
			// and forgets per-client.
		}
	}
	return nil
}

// ConnectionCount returns the live connection count. Useful for
// observability + tests; not surfaced on the production API
// (Sonarr has no equivalent of an admin endpoint that reports
// SignalR connection counts).
func (h *Hub) ConnectionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}

// HubBroadcaster adapts *Hub to the Broadcaster interface. Use this
// when wiring per-resource controllers — they get the Broadcaster
// shape and the production binary swaps NoopBroadcaster for
// HubBroadcaster{Hub: h} once the transport layer lands.
type HubBroadcaster struct {
	Hub *Hub
}

// IsConnected delegates to the wrapped Hub.
func (b HubBroadcaster) IsConnected() bool {
	if b.Hub == nil {
		return false
	}
	return b.Hub.IsConnected()
}

// Broadcast delegates to the wrapped Hub. Returns nil when Hub is
// nil so a half-wired binary doesn't panic during boot.
func (b HubBroadcaster) Broadcast(ctx context.Context, msg SignalRMessage) error {
	if b.Hub == nil {
		return nil
	}
	return b.Hub.Broadcast(ctx, msg)
}

// Compile-time guard.
var _ Broadcaster = HubBroadcaster{}
