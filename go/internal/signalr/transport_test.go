package signalr_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"sonarr.tv/sonarr/internal/signalr"
)

// dialWS opens a WebSocket to the test server's /signalr/messages
// path and returns the connected client. The negotiate handshake
// is bypassed — the test directly upgrades to WS just like the JS
// client does after negotiate.
func dialWS(t *testing.T, ts *httptest.Server, query string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signalr/messages"
	if query != "" {
		url += "?" + query
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	return c
}

// readNext reads one WS message + splits frames. Returns the first
// frame; errors the test on a multi-frame batch or timeout.
func readNext(t *testing.T, c *websocket.Conn, within time.Duration) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	frames := signalr.SplitFrames(data)
	if len(frames) == 0 {
		t.Fatalf("empty WS message")
	}
	return frames[0]
}

func TestWebSocketHandler_HandshakeAndHandshakeReply(t *testing.T) {
	t.Parallel()
	hub := signalr.NewHub(signalr.WithHostVersion("test-build"))
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer ts.Close()

	c := dialWS(t, ts, "id=test-conn")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	// Client sends handshake.
	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	if err := c.Write(t.Context(), websocket.MessageText, hs); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	// Server replies with empty handshake response.
	first := readNext(t, c, time.Second)
	if string(first) != `{}` {
		t.Errorf("handshake reply: got %q, want `{}`", first)
	}
}

func TestWebSocketHandler_VersionEventArrivesAfterHandshake(t *testing.T) {
	t.Parallel()
	hub := signalr.NewHub(signalr.WithHostVersion("5.0.0.99"))
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer ts.Close()

	c := dialWS(t, ts, "id=v-conn")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)

	// Drain handshake reply.
	_ = readNext(t, c, time.Second)
	// Next frame is the version-handshake Invocation event.
	frame := readNext(t, c, time.Second)
	var inv signalr.InvocationMessage
	if err := json.Unmarshal(frame, &inv); err != nil {
		t.Fatalf("unmarshal invocation: %v body=%q", err, frame)
	}
	if inv.Type != signalr.MessageTypeInvocation || inv.Target != "receiveMessage" {
		t.Errorf("envelope: type=%d target=%q", inv.Type, inv.Target)
	}
	if len(inv.Arguments) != 1 {
		t.Fatalf("arguments len: %d", len(inv.Arguments))
	}
	argBytes, _ := json.Marshal(inv.Arguments[0])
	if !strings.Contains(string(argBytes), `"name":"version"`) {
		t.Errorf("first invocation should be the version handshake: got %s", argBytes)
	}
	if !strings.Contains(string(argBytes), `"version":"5.0.0.99"`) {
		t.Errorf("handshake body version: got %s", argBytes)
	}
}

func TestWebSocketHandler_RejectsUnsupportedHandshake(t *testing.T) {
	t.Parallel()
	hub := signalr.NewHub()
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer ts.Close()

	c := dialWS(t, ts, "")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	bad, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "messagepack", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, bad)

	// Server should respond with HandshakeResponse carrying an
	// error, then close.
	frame := readNext(t, c, time.Second)
	var resp signalr.HandshakeResponse
	if err := json.Unmarshal(frame, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == "" {
		t.Errorf("expected handshake error; got empty response")
	}
}

func TestWebSocketHandler_HubBroadcastReachesClient(t *testing.T) {
	t.Parallel()
	hub := signalr.NewHub()
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer ts.Close()

	c := dialWS(t, ts, "id=broadcast-conn")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readNext(t, c, time.Second) // handshake reply
	_ = readNext(t, c, time.Second) // version event

	// Producer-side broadcast.
	go func() {
		// Tiny sleep so the WS read can start before the broadcast.
		time.Sleep(50 * time.Millisecond)
		_ = hub.Broadcast(t.Context(), signalr.SignalRMessage{
			Name:    "series",
			Body:    signalr.NewResourceSync[struct{ ID int }](),
			Version: signalr.V3Version,
		})
	}()

	frame := readNext(t, c, 2*time.Second)
	var inv signalr.InvocationMessage
	_ = json.Unmarshal(frame, &inv)
	if inv.Target != "receiveMessage" {
		t.Errorf("target: %q", inv.Target)
	}
	argBytes, _ := json.Marshal(inv.Arguments[0])
	if !strings.Contains(string(argBytes), `"name":"series"`) {
		t.Errorf("broadcast: %s", argBytes)
	}
}
