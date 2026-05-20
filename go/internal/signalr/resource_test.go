package signalr_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/signalr"
)

type tagResource struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// recordingBroadcaster captures every Broadcast call for assertion.
type recordingBroadcaster struct {
	connected bool
	mu        sync.Mutex
	messages  []signalr.SignalRMessage
	err       error
}

func (r *recordingBroadcaster) IsConnected() bool { return r.connected }
func (r *recordingBroadcaster) Broadcast(_ context.Context, msg signalr.SignalRMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
	return r.err
}
func (r *recordingBroadcaster) last() signalr.SignalRMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.messages) == 0 {
		return signalr.SignalRMessage{}
	}
	return r.messages[len(r.messages)-1]
}
func (r *recordingBroadcaster) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

func TestResourceBroadcaster_NotConnected_NoEmit(t *testing.T) {
	t.Parallel()
	rec := &recordingBroadcaster{connected: false}
	rb := signalr.ResourceBroadcaster[tagResource]{
		Broadcaster: rec, Name: "tag", Version: signalr.V3Version,
	}
	if err := rb.Created(context.Background(), tagResource{ID: 1, Label: "hd"}); err != nil {
		t.Fatalf("Created: %v", err)
	}
	if rec.count() != 0 {
		t.Errorf("disconnected broadcaster should skip emit; got %d messages", rec.count())
	}
}

func TestResourceBroadcaster_Created_WireShape(t *testing.T) {
	t.Parallel()
	rec := &recordingBroadcaster{connected: true}
	rb := signalr.ResourceBroadcaster[tagResource]{
		Broadcaster: rec, Name: "tag", Version: signalr.V3Version,
	}
	if err := rb.Created(context.Background(), tagResource{ID: 1, Label: "hd"}); err != nil {
		t.Fatalf("Created: %v", err)
	}
	msg := rec.last()
	if msg.Name != "tag" {
		t.Errorf("name: %q", msg.Name)
	}
	if msg.Version == nil || *msg.Version != 3 {
		t.Errorf("version: %v", msg.Version)
	}
	b, _ := json.Marshal(msg)
	got := string(b)
	if !strings.Contains(got, `"action":"created"`) {
		t.Errorf("action: %s", got)
	}
	if !strings.Contains(got, `"resource":{"id":1,"label":"hd"}`) {
		t.Errorf("resource: %s", got)
	}
}

func TestResourceBroadcaster_AllActions(t *testing.T) {
	t.Parallel()
	rec := &recordingBroadcaster{connected: true}
	rb := signalr.ResourceBroadcaster[tagResource]{
		Broadcaster: rec, Name: "tag", Version: signalr.V3Version,
	}
	ctx := context.Background()
	_ = rb.Created(ctx, tagResource{ID: 1, Label: "a"})
	_ = rb.Updated(ctx, tagResource{ID: 1, Label: "b"})
	_ = rb.Deleted(ctx, tagResource{ID: 1})
	_ = rb.Sync(ctx)

	if rec.count() != 4 {
		t.Fatalf("expected 4 messages; got %d", rec.count())
	}
	for i, want := range []signalr.Action{signalr.ActionCreated, signalr.ActionUpdated, signalr.ActionDeleted, signalr.ActionSync} {
		body, ok := rec.messages[i].Body.(signalr.ResourceChangeMessage[tagResource])
		if !ok {
			t.Fatalf("message %d body type: %T", i, rec.messages[i].Body)
		}
		if body.Action != want {
			t.Errorf("message %d action: %v, want %v", i, body.Action, want)
		}
	}
}

func TestResourceBroadcaster_Sync_NoResource(t *testing.T) {
	t.Parallel()
	rec := &recordingBroadcaster{connected: true}
	rb := signalr.ResourceBroadcaster[tagResource]{
		Broadcaster: rec, Name: "tag", Version: signalr.V3Version,
	}
	if err := rb.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	b, _ := json.Marshal(rec.last())
	got := string(b)
	// C# parity: STJson `WhenWritingNull` drops the null Resource
	// property over SignalR (Startup.cs:219-223 + STJson.cs:27), so
	// Sync bodies emit just `{"action":"sync"}`. Go matches via
	// `omitempty` on `ResourceChangeMessage.Resource`.
	if strings.Contains(got, `"resource"`) {
		t.Errorf("sync should omit resource key; body=%s", got)
	}
	if !strings.Contains(got, `"action":"sync"`) {
		t.Errorf("sync action: %s", got)
	}
}

func TestResourceBroadcaster_BroadcastErrorPropagates(t *testing.T) {
	t.Parallel()
	rec := &recordingBroadcaster{connected: true, err: errors.New("boom")}
	rb := signalr.ResourceBroadcaster[tagResource]{
		Broadcaster: rec, Name: "tag", Version: signalr.V3Version,
	}
	err := rb.Created(context.Background(), tagResource{ID: 1})
	if err == nil || err.Error() != "boom" {
		t.Errorf("expected boom error; got %v", err)
	}
}

func TestResourceBroadcaster_NilBroadcaster_Safe(t *testing.T) {
	t.Parallel()
	// A controller wired before the Hub exists (or with a nil
	// dependency from a misconfigured test) must not panic.
	rb := signalr.ResourceBroadcaster[tagResource]{
		Broadcaster: nil, Name: "tag", Version: signalr.V3Version,
	}
	if err := rb.Created(context.Background(), tagResource{ID: 1}); err != nil {
		t.Errorf("nil-broadcaster Created should be no-op; got %v", err)
	}
}

func TestResourceBroadcaster_EndToEndViaHub(t *testing.T) {
	t.Parallel()
	// Spin up a Hub, attach a HubBroadcaster, push two messages
	// via ResourceBroadcaster and confirm two clients each see
	// them in order. Exercises the full Phase-5 P2a+P2b stack —
	// envelope+body types, Hub registry, fan-out.
	h := signalr.NewHub(signalr.WithHostVersion("test-build"))
	c1, _ := h.OnConnect(context.Background(), "client-1")
	c2, _ := h.OnConnect(context.Background(), "client-2")
	// Drain handshakes: client-1 receives its own + client-2's
	// handshake (every OnConnect broadcasts to all); client-2
	// receives just its own.
	for range 2 {
		<-c1.Messages
	}
	<-c2.Messages

	rb := signalr.ResourceBroadcaster[tagResource]{
		Broadcaster: signalr.HubBroadcaster{Hub: h},
		Name:        "tag", Version: signalr.V3Version,
	}
	ctx := context.Background()
	if err := rb.Created(ctx, tagResource{ID: 1, Label: "a"}); err != nil {
		t.Fatalf("Created: %v", err)
	}
	if err := rb.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, c := range []signalr.Conn{c1, c2} {
		// Created message.
		select {
		case msg := <-c.Messages:
			if msg.Name != "tag" || *msg.Version != 3 {
				t.Errorf("created: %+v", msg)
			}
			body, ok := msg.Body.(signalr.ResourceChangeMessage[tagResource])
			if !ok || body.Action != signalr.ActionCreated || body.Resource == nil || body.Resource.Label != "a" {
				t.Errorf("created body: %+v ok=%v", msg.Body, ok)
			}
		case <-time.After(time.Second):
			t.Fatalf("created message not delivered within 1s")
		}
		// Sync message.
		select {
		case msg := <-c.Messages:
			body, ok := msg.Body.(signalr.ResourceChangeMessage[tagResource])
			if !ok || body.Action != signalr.ActionSync || body.Resource != nil {
				t.Errorf("sync body: %+v ok=%v", msg.Body, ok)
			}
		case <-time.After(time.Second):
			t.Fatalf("sync message not delivered within 1s")
		}
	}
}
