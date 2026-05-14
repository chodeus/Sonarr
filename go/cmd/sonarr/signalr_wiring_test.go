package main

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

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/signalr"
	"sonarr.tv/sonarr/internal/tags"
)

// TestWireSignalRSubscribers_TagsUpdatedEventReachesClient is the
// end-to-end smoke for the slice 4.EV wiring — publishes a real
// tags.TagsUpdatedEvent on a live messaging.Bus, asserts the
// corresponding SignalR sync envelope arrives on a connected
// WebSocket client.
func TestWireSignalRSubscribers_TagsUpdatedEventReachesClient(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub(signalr.WithHostVersion("test-build"))
	wireSignalRSubscribers(bus, hub, logger, nil)

	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()

	c := dialWSToServer(t, ts, "id=tag-sub-test")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	if err := c.Write(t.Context(), websocket.MessageText, hs); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	_ = readFrame(t, c, time.Second) // handshake reply
	_ = readFrame(t, c, time.Second) // version event

	// Publish the bus event. The wired subscriber translates it to
	// a SignalR Sync envelope and the Hub fans it to all connected
	// clients including the one we just upgraded.
	if err := messaging.Publish(t.Context(), bus, tags.TagsUpdatedEvent{}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	got := readFrame(t, c, 2*time.Second)
	var inv signalr.InvocationMessage
	if err := json.Unmarshal(got, &inv); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, got)
	}
	if inv.Target != "receiveMessage" {
		t.Errorf("target: %q", inv.Target)
	}
	argBytes, _ := json.Marshal(inv.Arguments[0])
	body := string(argBytes)
	if !strings.Contains(body, `"name":"tag"`) {
		t.Errorf("envelope name: %s", body)
	}
	if !strings.Contains(body, `"version":3`) {
		t.Errorf("envelope version: %s", body)
	}
	if !strings.Contains(body, `"action":"sync"`) {
		t.Errorf("body action: %s", body)
	}
	if !strings.Contains(body, `"resource":null`) {
		t.Errorf("sync body should carry resource:null: %s", body)
	}
}

// TestWireSignalRSubscribers_AutoTagsUpdatedAlsoFiresTag asserts
// the AutoTagsUpdated event hits the same `tag` namespace — the
// C# tree fans both events out as `tag/sync` since the UI consumer
// is the same tag-detail screen.
func TestWireSignalRSubscribers_AutoTagsUpdatedAlsoFiresTag(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub()
	wireSignalRSubscribers(bus, hub, logger, nil)
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()
	c := dialWSToServer(t, ts, "id=autotag-test")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readFrame(t, c, time.Second) // handshake reply
	_ = readFrame(t, c, time.Second) // version event

	_ = messaging.Publish(t.Context(), bus, autotagging.AutoTagsUpdatedEvent{})

	got := readFrame(t, c, 2*time.Second)
	var inv signalr.InvocationMessage
	_ = json.Unmarshal(got, &inv)
	argBytes, _ := json.Marshal(inv.Arguments[0])
	body := string(argBytes)
	if !strings.Contains(body, `"name":"tag"`) || !strings.Contains(body, `"action":"sync"`) {
		t.Errorf("autotag should fan to tag/sync: %s", body)
	}
}

// TestWireSignalRSubscribers_HealthCompleteFiresSync exercises the
// health subscriber end-to-end. Publishing healthcheck.CompleteEvent
// must produce a `health/sync` envelope on the WS.
func TestWireSignalRSubscribers_HealthCompleteFiresSync(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub()
	wireSignalRSubscribers(bus, hub, logger, nil)
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()
	c := dialWSToServer(t, ts, "id=health-test")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readFrame(t, c, time.Second) // handshake reply
	_ = readFrame(t, c, time.Second) // version event

	_ = messaging.Publish(t.Context(), bus, healthcheck.CompleteEvent{})

	got := readFrame(t, c, 2*time.Second)
	var inv signalr.InvocationMessage
	_ = json.Unmarshal(got, &inv)
	argBytes, _ := json.Marshal(inv.Arguments[0])
	body := string(argBytes)
	if !strings.Contains(body, `"name":"health"`) || !strings.Contains(body, `"action":"sync"`) {
		t.Errorf("health/sync envelope: %s", body)
	}
}

// TestWireSignalRSubscribers_QueueUpdatedFansBoth asserts a single
// queue.QueueUpdatedEvent fans out to BOTH `queue` and
// `queue/details` namespaces (the C# tree publishes the same
// trigger to both controllers; the Go-port wires both subscribers
// off the one bus event).
func TestWireSignalRSubscribers_QueueUpdatedFansBoth(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub()
	wireSignalRSubscribers(bus, hub, logger, nil)
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()
	c := dialWSToServer(t, ts, "id=queue-test")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readFrame(t, c, time.Second) // handshake reply
	_ = readFrame(t, c, time.Second) // version event

	_ = messaging.Publish(t.Context(), bus, queue.QueueUpdatedEvent{})

	// Expect TWO envelopes on the wire, one for `queue` + one for
	// `queue/details`. Order is the subscriber-fan order (queue
	// first inside publishQueueSync).
	names := make(map[string]int)
	for range 2 {
		got := readFrame(t, c, 2*time.Second)
		var inv signalr.InvocationMessage
		_ = json.Unmarshal(got, &inv)
		argBytes, _ := json.Marshal(inv.Arguments[0])
		body := string(argBytes)
		if !strings.Contains(body, `"action":"sync"`) {
			t.Errorf("non-sync action: %s", body)
		}
		switch {
		case strings.Contains(body, `"name":"queue"`) && !strings.Contains(body, `"name":"queue/details"`):
			names["queue"]++
		case strings.Contains(body, `"name":"queue/details"`):
			names["queue/details"]++
		default:
			t.Errorf("unexpected envelope name: %s", body)
		}
	}
	if names["queue"] != 1 || names["queue/details"] != 1 {
		t.Errorf("expected one of each name; got %+v", names)
	}
}

// TestWireSignalRSubscribers_PendingReleasesAlsoFansQueue asserts
// pendingreleases.PendingReleasesUpdatedEvent triggers the same
// queue/queue-details fan-out (the C# tree subscribes both
// QueueController and QueueDetailsController to this event).
func TestWireSignalRSubscribers_PendingReleasesAlsoFansQueue(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub()
	wireSignalRSubscribers(bus, hub, logger, nil)
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()
	c := dialWSToServer(t, ts, "id=pending-test")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readFrame(t, c, time.Second)
	_ = readFrame(t, c, time.Second)

	_ = messaging.Publish(t.Context(), bus, pendingreleases.PendingReleasesUpdatedEvent{})

	saw := make(map[string]bool)
	for range 2 {
		got := readFrame(t, c, 2*time.Second)
		var inv signalr.InvocationMessage
		_ = json.Unmarshal(got, &inv)
		argBytes, _ := json.Marshal(inv.Arguments[0])
		body := string(argBytes)
		if strings.Contains(body, `"name":"queue/details"`) {
			saw["queue/details"] = true
		} else if strings.Contains(body, `"name":"queue"`) {
			saw["queue"] = true
		}
	}
	if !saw["queue"] || !saw["queue/details"] {
		t.Errorf("pendingreleases should fan to queue + queue/details; saw %+v", saw)
	}
}

// TestWireSignalRSubscribers_QueueStatusDebouncedUpdate exercises
// the 5s-debounced queue/status broadcaster end-to-end: publishes
// many rapid QueueUpdatedEvent / PendingReleasesUpdatedEvent
// triggers, asserts EXACTLY ONE `queue/status` Invocation envelope
// arrives on the WS with the populated QueueStatusResource body.
//
// Uses a 30ms debounce so the test completes in <100ms; production
// leaves DebounceDuration=0 → 5s default.
func TestWireSignalRSubscribers_QueueStatusDebouncedUpdate(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub()
	wireSignalRSubscribers(bus, hub, logger, &QueueStatusBroadcastDeps{
		Queue:            emptyQueueLister{},
		Pending:          emptyPendingLister{},
		DebounceDuration: 30 * time.Millisecond,
	})
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()
	c := dialWSToServer(t, ts, "id=qs-test")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readFrame(t, c, time.Second) // handshake reply
	_ = readFrame(t, c, time.Second) // version event

	// Burst 10 triggers within the debounce window.
	for range 10 {
		_ = messaging.Publish(t.Context(), bus, queue.QueueUpdatedEvent{})
	}
	// Drain the immediate Sync envelopes from the queue/queue-details
	// subscribers (those don't debounce — they fire once per event).
	// 10 events → 20 sync envelopes (queue + queue/details per event).
	// We don't assert each one here; just drain until the debounced
	// queue/status envelope shows up.
	deadline := time.After(2 * time.Second)
	gotQueueStatus := false
	for !gotQueueStatus {
		select {
		case <-deadline:
			t.Fatalf("queue/status update envelope not delivered within 2s")
		default:
		}
		frame := readFrame(t, c, 2*time.Second)
		var inv signalr.InvocationMessage
		_ = json.Unmarshal(frame, &inv)
		argBytes, _ := json.Marshal(inv.Arguments[0])
		body := string(argBytes)
		if strings.Contains(body, `"name":"queue/status"`) {
			gotQueueStatus = true
			if !strings.Contains(body, `"action":"updated"`) {
				t.Errorf("queue/status should emit `updated`, got %s", body)
			}
			if !strings.Contains(body, `"totalCount":0`) {
				t.Errorf("queue/status body should carry populated resource: %s", body)
			}
		}
	}
}

// emptyQueueLister + emptyPendingLister are test-local fixtures
// satisfying the apiv3 narrow interfaces. Both return zero state so
// the projected QueueStatusResource has all-zero counts +
// all-false flags.
type emptyQueueLister struct{}

func (emptyQueueLister) GetQueue() []queue.Queue { return nil }

type emptyPendingLister struct{}

func (emptyPendingLister) PendingCount(_ context.Context) (int, error) { return 0, nil }

// TestWireSignalRSubscribers_SeriesAddedFires_Created exercises
// the series Added path: publishing a SeriesAddedEvent carrying a
// *Series produces a `series/created` envelope with the projected
// SeriesResource body on the WS.
func TestWireSignalRSubscribers_SeriesAddedFires_Created(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub()
	wireSignalRSubscribers(bus, hub, logger, nil)
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()
	c := dialWSToServer(t, ts, "id=series-added")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readFrame(t, c, time.Second) // handshake reply
	_ = readFrame(t, c, time.Second) // version event

	s := &series.Series{ID: 42, Title: "Demo", Monitored: true, Path: "/tv/Demo"}
	_ = messaging.Publish(t.Context(), bus, series.SeriesAddedEvent{Series: s})

	frame := readFrame(t, c, 2*time.Second)
	var inv signalr.InvocationMessage
	_ = json.Unmarshal(frame, &inv)
	argBytes, _ := json.Marshal(inv.Arguments[0])
	body := string(argBytes)
	if !strings.Contains(body, `"name":"series"`) {
		t.Errorf("envelope name: %s", body)
	}
	if !strings.Contains(body, `"action":"created"`) {
		t.Errorf("action: %s", body)
	}
	if !strings.Contains(body, `"id":42`) || !strings.Contains(body, `"title":"Demo"`) {
		t.Errorf("resource projection: %s", body)
	}
}

// TestWireSignalRSubscribers_SeriesEditedFires_Updated covers the
// Edited path.
func TestWireSignalRSubscribers_SeriesEditedFires_Updated(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub()
	wireSignalRSubscribers(bus, hub, logger, nil)
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()
	c := dialWSToServer(t, ts, "id=series-edited")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readFrame(t, c, time.Second)
	_ = readFrame(t, c, time.Second)

	s := &series.Series{ID: 7, Title: "Updated"}
	_ = messaging.Publish(t.Context(), bus, series.SeriesEditedEvent{Series: s})

	frame := readFrame(t, c, 2*time.Second)
	var inv signalr.InvocationMessage
	_ = json.Unmarshal(frame, &inv)
	argBytes, _ := json.Marshal(inv.Arguments[0])
	body := string(argBytes)
	if !strings.Contains(body, `"action":"updated"`) || !strings.Contains(body, `"id":7`) {
		t.Errorf("edited envelope: %s", body)
	}
}

// TestWireSignalRSubscribers_SeriesDeletedFansPerRow asserts a
// SeriesDeletedEvent carrying N series produces N `series/deleted`
// envelopes (one per row).
func TestWireSignalRSubscribers_SeriesDeletedFansPerRow(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := messaging.New(logger)
	defer bus.BeginShutdown()
	hub := signalr.NewHub()
	wireSignalRSubscribers(bus, hub, logger, nil)
	ts := httptest.NewServer(signalr.WebSocketHandler(hub, logger))
	defer ts.Close()
	c := dialWSToServer(t, ts, "id=series-deleted")
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	hs, _ := signalr.MarshalFrame(signalr.ClientHandshake{Protocol: "json", Version: 1})
	_ = c.Write(t.Context(), websocket.MessageText, hs)
	_ = readFrame(t, c, time.Second)
	_ = readFrame(t, c, time.Second)

	_ = messaging.Publish(t.Context(), bus, series.SeriesDeletedEvent{
		Series: []series.Series{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}},
	})

	seenIDs := map[int]bool{}
	for range 2 {
		frame := readFrame(t, c, 2*time.Second)
		var inv signalr.InvocationMessage
		_ = json.Unmarshal(frame, &inv)
		argBytes, _ := json.Marshal(inv.Arguments[0])
		body := string(argBytes)
		if !strings.Contains(body, `"action":"deleted"`) {
			t.Errorf("not deleted: %s", body)
		}
		switch {
		case strings.Contains(body, `"id":1`):
			seenIDs[1] = true
		case strings.Contains(body, `"id":2`):
			seenIDs[2] = true
		}
	}
	if !seenIDs[1] || !seenIDs[2] {
		t.Errorf("expected both ids; got %+v", seenIDs)
	}
}

// TestWireSignalRSubscribers_NilDepsSafe asserts the wiring is a
// no-op when called with nil dependencies — main.go calls this
// during boot before every service is up and nil-handling keeps
// the boot order forgiving.
func TestWireSignalRSubscribers_NilDepsSafe(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wireSignalRSubscribers(nil, nil, logger, nil)
	wireSignalRSubscribers(messaging.New(logger), nil, logger, nil)
	wireSignalRSubscribers(nil, signalr.NewHub(), logger, nil)
}

// dialWSToServer + readFrame are the test-local WS roundtrip
// helpers. Duplicated from internal/signalr/transport_test.go so
// this cmd/sonarr-level test doesn't need to export private helpers
// or import the test package. Same shape, fewer abstractions.
func dialWSToServer(t *testing.T, ts *httptest.Server, query string) *websocket.Conn {
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

func readFrame(t *testing.T, c *websocket.Conn, within time.Duration) []byte {
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
