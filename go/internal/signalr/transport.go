package signalr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// PingInterval is the keep-alive cadence the server sends Ping
// frames at when the connection is otherwise idle. Default 15s
// matches ASP.NET Core SignalR's `KeepAliveInterval` default.
const PingInterval = 15 * time.Second

// HandshakeTimeout is the upper bound for the client to send its
// HubProtocol handshake after the WebSocket upgrade completes. The
// SignalR spec recommends 15s; the C# server uses
// `HandshakeTimeout = TimeSpan.FromSeconds(15)` by default.
const HandshakeTimeout = 15 * time.Second

// WebSocketHandler returns the http.Handler for GET
// /signalr/messages. The handler upgrades the HTTP connection to
// WebSocket, runs the SignalR JSON-protocol handshake, registers
// the connection with the Hub, then pumps the Hub's outbound
// message channel onto the wire as Invocation frames until the
// client disconnects.
//
// Authentication: this handler MUST be wrapped in
// `host.requireAPIKey`. The SignalR JS client passes the API key
// via `?access_token=` (parseAPIKey learned to accept that fallback
// in slice 4.ES).
//
// connID generation: the negotiate endpoint already produced a
// connection id and the client passes it back as `?id=<connId>`.
// This handler trusts that id verbatim — there's no per-id state
// stored between negotiate and the WebSocket connect, so the same
// id can be reused. If `?id=` is missing the handler generates a
// fresh id; the client doesn't observe the difference.
//
// Inbound traffic: Sonarr's hub has no client-callable methods.
// The inbound goroutine reads frames purely to honour Ping/Close
// messages from the client — non-Ping/Close frames are silently
// discarded with a debug log.
func WebSocketHandler(hub *Hub, logger *slog.Logger) http.Handler {
	if hub == nil {
		panic("signalr.WebSocketHandler: nil hub")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept the WebSocket upgrade. coder/websocket validates
		// the upgrade headers + writes the 101 response.
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// InsecureSkipVerify keeps the same-origin check in
			// place. The SignalR JS client typically connects
			// same-origin so the default is fine. Cross-origin
			// support can be added when a use case surfaces.
			OriginPatterns: nil,
		})
		if err != nil {
			logger.Warn("signalr: ws upgrade failed", "err", err)
			return
		}
		defer func() { _ = ws.CloseNow() }()
		ws.SetReadLimit(64 * 1024) // SignalR frames are small; reject 64K+ payloads.

		connID := ConnID(r.URL.Query().Get("id"))
		if connID == "" {
			id, err := DefaultConnectionIDGenerator()
			if err != nil {
				logger.Warn("signalr: generate fallback conn id failed", "err", err)
				_ = ws.Close(websocket.StatusInternalError, "id generation failed")
				return
			}
			connID = ConnID(id)
		}

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// HubProtocol handshake: read first frame from the client
		// within HandshakeTimeout. Reject + close on failure.
		handshakeCtx, handshakeCancel := context.WithTimeout(ctx, HandshakeTimeout)
		if err := readClientHandshake(handshakeCtx, ws); err != nil {
			handshakeCancel()
			logger.Warn("signalr: handshake failed", "connID", connID, "err", err)
			respErr := err.Error()
			// Best-effort response — even if the client times out
			// the close frame, the server tried.
			_ = writeFrameWS(ctx, ws, HandshakeResponse{Error: respErr})
			closeCode := websocket.StatusPolicyViolation
			if errors.Is(err, ErrHandshakeUnsupported) {
				closeCode = websocket.StatusProtocolError
			}
			_ = ws.Close(closeCode, "handshake")
			return
		}
		handshakeCancel()
		if err := writeFrameWS(ctx, ws, HandshakeResponse{}); err != nil {
			logger.Warn("signalr: handshake reply failed", "connID", connID, "err", err)
			return
		}

		// Hub registration. OnConnect fires the version-handshake
		// event into Clients.All which this connection's outbound
		// loop will read + ship as the first Invocation frame.
		conn, err := hub.OnConnect(ctx, connID)
		if err != nil {
			logger.Warn("signalr: hub register failed", "connID", connID, "err", err)
			_ = ws.Close(websocket.StatusInternalError, "hub register failed")
			return
		}
		defer hub.OnDisconnect(connID)

		// Inbound reader: discard everything except Close.
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				_, _, err := ws.Read(ctx)
				if err != nil {
					return
				}
				// Frames are ignored — Sonarr's hub has no
				// client-callable methods. The mere act of reading
				// keeps the WS receive-pump alive so close frames
				// surface promptly.
			}
		}()

		// Outbound pump + ping timer.
		ticker := time.NewTicker(PingInterval)
		defer ticker.Stop()
		pingBytes, err := EncodePing()
		if err != nil {
			logger.Warn("signalr: encode ping failed", "connID", connID, "err", err)
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-conn.Done:
				return
			case <-readDone:
				return
			case msg := <-conn.Messages:
				payload, err := EncodeInvocation(msg)
				if err != nil {
					logger.Warn("signalr: encode invocation failed", "connID", connID, "err", err)
					return
				}
				if err := ws.Write(ctx, websocket.MessageText, payload); err != nil {
					logger.Debug("signalr: ws write failed", "connID", connID, "err", err)
					return
				}
			case <-ticker.C:
				if err := ws.Write(ctx, websocket.MessageText, pingBytes); err != nil {
					logger.Debug("signalr: ws ping failed", "connID", connID, "err", err)
					return
				}
			}
		}
	})
}

// readClientHandshake reads the first frame from the WebSocket,
// expects a ClientHandshake, validates protocol=json + version=1.
// Mirrors `ReadHandshake` but reads through the websocket reader
// API instead of an io.Reader. ctx-deadline enforces
// HandshakeTimeout.
func readClientHandshake(ctx context.Context, ws *websocket.Conn) error {
	_, data, err := ws.Read(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeMalformed, err)
	}
	frames := SplitFrames(data)
	if len(frames) == 0 {
		return fmt.Errorf("%w: empty frame", ErrHandshakeMalformed)
	}
	// Only the first frame is the handshake; any additional bytes
	// in the same WS message are out-of-spec — Sonarr's hub
	// rejects them via the same error.
	h, err := parseHandshakeBytes(frames[0])
	if err != nil {
		return err
	}
	if h.Protocol != "json" || h.Version != 1 {
		return fmt.Errorf("%w: protocol=%q version=%d", ErrHandshakeUnsupported, h.Protocol, h.Version)
	}
	return nil
}

// writeFrameWS marshals v as a SignalR JSON-protocol frame and
// sends it as a single WS Text message.
func writeFrameWS(ctx context.Context, ws *websocket.Conn, v any) error {
	frame, err := MarshalFrame(v)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, frame)
}
