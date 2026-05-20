package signalr_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/signalr"
)

func TestAction_String(t *testing.T) {
	t.Parallel()
	cases := map[signalr.Action]string{
		signalr.ActionCreated: "created",
		signalr.ActionUpdated: "updated",
		signalr.ActionDeleted: "deleted",
		signalr.ActionSync:    "sync",
		signalr.Action(99):    "unknown",
	}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", a, got, want)
		}
	}
}

func TestAction_MarshalJSON(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(signalr.ActionUpdated)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"updated"` {
		t.Errorf("ActionUpdated wire form: got %s, want \"updated\"", b)
	}
}

func TestResourceChangeMessage_CreatedWire(t *testing.T) {
	t.Parallel()
	type series struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	msg := signalr.NewResourceChange(series{ID: 1, Title: "Demo"}, signalr.ActionCreated)
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"action":"created"`) {
		t.Errorf("action: %s", got)
	}
	if !strings.Contains(got, `"resource":{"id":1,"title":"Demo"}`) {
		t.Errorf("resource: %s", got)
	}
}

func TestResourceChangeMessage_SyncWire(t *testing.T) {
	t.Parallel()
	type series struct{ ID int }
	msg := signalr.NewResourceSync[series]()
	b, _ := json.Marshal(msg)
	got := string(b)
	// Sync bodies must omit the `resource` key entirely. C#'s
	// SignalR pipeline serializes ResourceChangeMessage with STJson
	// `WhenWritingNull` (Startup.cs:219-223 + STJson.cs:27), which
	// drops the null Resource property. Go matches via `omitempty`
	// on `ResourceChangeMessage.Resource`.
	if strings.Contains(got, `"resource"`) {
		t.Errorf("sync body must omit resource key: %s", got)
	}
	if !strings.Contains(got, `"action":"sync"`) {
		t.Errorf("sync action: %s", got)
	}
}

func TestResourceChangeMessage_DeleteWire(t *testing.T) {
	t.Parallel()
	type idOnly struct {
		ID int `json:"id"`
	}
	msg := signalr.NewResourceDelete(idOnly{ID: 42})
	b, _ := json.Marshal(msg)
	got := string(b)
	if !strings.Contains(got, `"resource":{"id":42}`) {
		t.Errorf("delete body should carry id-only resource: %s", got)
	}
	if !strings.Contains(got, `"action":"deleted"`) {
		t.Errorf("delete action: %s", got)
	}
}

func TestSignalRMessage_EnvelopeShape(t *testing.T) {
	t.Parallel()
	// Mirror the C# wire shape verbatim:
	//   { "name": "series", "body": {...}, "version": 3 }
	type seriesResource struct {
		ID int `json:"id"`
	}
	body := signalr.NewResourceChange(seriesResource{ID: 7}, signalr.ActionUpdated)
	env := signalr.SignalRMessage{Name: "series", Body: body, Version: signalr.V3Version}
	b, err := signalr.MarshalEnvelope(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"name":"series"`) {
		t.Errorf("name field: %s", got)
	}
	if !strings.Contains(got, `"version":3`) {
		t.Errorf("version 3: %s", got)
	}
	if !strings.Contains(got, `"action":"updated"`) {
		t.Errorf("nested action: %s", got)
	}
}

func TestSignalRMessage_VersionHandshake(t *testing.T) {
	t.Parallel()
	// The hub fires this body on every new client connect. The
	// envelope's `version` is null (no resource version applies);
	// the body's `version` is the running BuildInfo.Version string.
	env := signalr.SignalRMessage{
		Name:    "version",
		Body:    signalr.VersionMessage{Version: "5.0.0.123"},
		Version: nil,
	}
	b, _ := signalr.MarshalEnvelope(env)
	got := string(b)
	if !strings.Contains(got, `"version":null`) {
		t.Errorf("envelope version must be null: %s", got)
	}
	if !strings.Contains(got, `"body":{"version":"5.0.0.123"}`) {
		t.Errorf("body version: %s", got)
	}
}

func TestNoopBroadcaster_NotConnected(t *testing.T) {
	t.Parallel()
	var b signalr.Broadcaster = signalr.NoopBroadcaster{}
	if b.IsConnected() {
		t.Errorf("noop broadcaster must report not-connected")
	}
	if err := b.Broadcast(context.Background(), signalr.SignalRMessage{Name: "x"}); err != nil {
		t.Errorf("noop broadcast should return nil: %v", err)
	}
}
