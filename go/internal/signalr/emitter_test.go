package signalr

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type tagRow struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

func TestNewV3Emitter_PinsV3Version(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	em := NewV3Emitter[tagRow](hub, "tag")
	if em.Version != V3Version {
		t.Errorf("Version: got %v want V3Version (%v)", em.Version, V3Version)
	}
	if em.Name != "tag" {
		t.Errorf("Name: got %q", em.Name)
	}
	if _, ok := em.Broadcaster.(HubBroadcaster); !ok {
		t.Errorf("Broadcaster: got %T want HubBroadcaster", em.Broadcaster)
	}
}

func TestNewV5Emitter_PinsV5Version(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	em := NewV5Emitter[tagRow](hub, "tag")
	if em.Version != V5Version {
		t.Errorf("Version: got %v want V5Version (%v)", em.Version, V5Version)
	}
}

func TestNewV3Emitter_NilHubNoOps(t *testing.T) {
	t.Parallel()
	// Tests pass nil hub — broadcast must short-circuit via
	// HubBroadcaster.IsConnected, not panic.
	em := NewV3Emitter[tagRow](nil, "tag")
	if err := em.Created(context.Background(), tagRow{ID: 1, Label: "x"}); err != nil {
		t.Errorf("Created with nil hub: got %v want nil (no-op)", err)
	}
	if err := em.Updated(context.Background(), tagRow{ID: 1}); err != nil {
		t.Errorf("Updated with nil hub: got %v", err)
	}
	if err := em.Deleted(context.Background(), tagRow{ID: 1}); err != nil {
		t.Errorf("Deleted with nil hub: got %v", err)
	}
	if err := em.Sync(context.Background()); err != nil {
		t.Errorf("Sync with nil hub: got %v", err)
	}
}

func TestFireAndLog_NoLogOnNilError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	FireAndLog(logger, "tag", "created", func() error { return nil })
	if buf.Len() != 0 {
		t.Errorf("logger must be silent on nil error; got %q", buf.String())
	}
}

func TestFireAndLog_WarnsOnError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	FireAndLog(logger, "tag", "created", func() error { return errors.New("slow consumer") })
	body := buf.String()
	if !strings.Contains(body, "signalr broadcast failed") {
		t.Errorf("logger must capture failure: %q", body)
	}
	if !strings.Contains(body, "tag") {
		t.Errorf("logger must include resource: %q", body)
	}
	if !strings.Contains(body, "created") {
		t.Errorf("logger must include action: %q", body)
	}
	if !strings.Contains(body, "slow consumer") {
		t.Errorf("logger must include error: %q", body)
	}
}

func TestFireAndLog_NilLoggerSafe(t *testing.T) {
	t.Parallel()
	// nil logger must NOT panic — handlers in early-build state may
	// not yet have a logger threaded through.
	called := false
	FireAndLog(nil, "tag", "created", func() error {
		called = true
		return errors.New("x")
	})
	if !called {
		t.Errorf("broadcastFn must still run with nil logger")
	}
}

func TestNewV3Emitter_BroadcastsToConnectedSubscriber(t *testing.T) {
	t.Parallel()
	// End-to-end: emitter wired to a real hub broadcasts a message
	// that a subscriber receives. Confirms the factory's wiring is
	// equivalent to constructing the broadcaster literal manually.
	hub := NewHub()
	conn, err := hub.OnConnect(context.Background(), "test-conn")
	if err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	defer hub.OnDisconnect("test-conn")
	<-conn.Messages // drain handshake

	em := NewV3Emitter[tagRow](hub, "tag")

	if err := em.Created(context.Background(), tagRow{ID: 1, Label: "x"}); err != nil {
		t.Fatalf("Created: %v", err)
	}

	select {
	case msg := <-conn.Messages:
		if msg.Name != "tag" {
			t.Errorf("Name: got %q want tag", msg.Name)
		}
		if msg.Version != V3Version {
			t.Errorf("Version: got %v want V3Version", msg.Version)
		}
	default:
		t.Errorf("no message received")
	}
}

func TestFireAndLogContext_RunsFn(t *testing.T) {
	t.Parallel()
	// Sanity-check the unexported context-aware helper that 5.A4
	// will use to wire handlers — currently //nolint:unused so it
	// compiles, but the integration layer needs it to actually run
	// the underlying function with the context. Pin the contract.
	called := false
	var seenCtx context.Context
	ctx := context.WithValue(context.Background(), ctxKey{}, "pin")
	fireAndLogContext(ctx, nil, "tag", "created", func(c context.Context) error {
		called = true
		seenCtx = c
		return nil
	})
	if !called {
		t.Errorf("inner fn must run")
	}
	if seenCtx.Value(ctxKey{}) != "pin" {
		t.Errorf("ctx not threaded; got %v", seenCtx.Value(ctxKey{}))
	}
}

type ctxKey struct{}
