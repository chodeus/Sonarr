package signalr_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/signalr"
)

func TestHub_NotConnectedWhenEmpty(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub()
	if h.IsConnected() {
		t.Errorf("empty hub should not report connected")
	}
	if h.ConnectionCount() != 0 {
		t.Errorf("ConnectionCount: %d, want 0", h.ConnectionCount())
	}
}

func TestHub_OnConnect_FiresHandshake(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub(signalr.WithHostVersion("5.0.0.123"))
	conn, err := h.OnConnect(context.Background(), "conn-a")
	if err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	if !h.IsConnected() {
		t.Errorf("hub should be connected after OnConnect")
	}
	select {
	case msg := <-conn.Messages:
		if msg.Name != "version" {
			t.Errorf("handshake name: %q, want version", msg.Name)
		}
		body, ok := msg.Body.(signalr.VersionMessage)
		if !ok {
			t.Fatalf("handshake body type: %T, want VersionMessage", msg.Body)
		}
		if body.Version != "5.0.0.123" {
			t.Errorf("handshake version: %q, want 5.0.0.123", body.Version)
		}
		if msg.Version != nil {
			t.Errorf("handshake envelope Version must be nil; got %d", *msg.Version)
		}
	case <-time.After(time.Second):
		t.Fatalf("handshake not delivered within 1s")
	}
}

func TestHub_OnDisconnect_FiresDoneSignal(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub()
	conn, _ := h.OnConnect(context.Background(), "conn-a")
	<-conn.Messages // drain handshake
	h.OnDisconnect("conn-a")
	if h.IsConnected() {
		t.Errorf("hub should not be connected after OnDisconnect")
	}
	select {
	case <-conn.Done:
	case <-time.After(time.Second):
		t.Fatalf("Done signal not closed within 1s after OnDisconnect")
	}
}

func TestHub_OnConnect_DoubleRegistrationRejected(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub()
	if _, err := h.OnConnect(context.Background(), "conn-a"); err != nil {
		t.Fatalf("first OnConnect: %v", err)
	}
	if _, err := h.OnConnect(context.Background(), "conn-a"); !errors.Is(err, signalr.ErrConnAlreadyRegistered) {
		t.Errorf("double OnConnect: got %v, want ErrConnAlreadyRegistered", err)
	}
}

func TestHub_Broadcast_FansToEveryConnection(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub()
	c1, _ := h.OnConnect(context.Background(), "conn-1")
	c2, _ := h.OnConnect(context.Background(), "conn-2")
	for range 2 {
		<-c1.Messages
	}
	<-c2.Messages

	if err := h.Broadcast(context.Background(), signalr.SignalRMessage{Name: "series"}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	for _, c := range []signalr.Conn{c1, c2} {
		select {
		case msg := <-c.Messages:
			if msg.Name != "series" {
				t.Errorf("got name=%q, want series", msg.Name)
			}
		case <-time.After(time.Second):
			t.Errorf("broadcast didn't reach connection within 1s")
		}
	}
}

func TestHub_Broadcast_SlowConsumerDropped(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub(signalr.WithChannelBuffer(1))
	_, _ = h.OnConnect(context.Background(), "slow")
	done := make(chan struct{})
	go func() {
		for range 5 {
			_ = h.Broadcast(context.Background(), signalr.SignalRMessage{Name: "series"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Broadcast blocked on slow consumer")
	}
}

func TestHub_Broadcast_RespectsContextCancel(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub()
	_, _ = h.OnConnect(context.Background(), "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := h.Broadcast(ctx, signalr.SignalRMessage{Name: "series"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Broadcast on cancelled ctx: got %v, want context.Canceled", err)
	}
}

func TestHub_Broadcast_DroppedAfterDisconnect(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub(signalr.WithChannelBuffer(64))
	_, _ = h.OnConnect(context.Background(), "a")
	h.OnDisconnect("a")
	if err := h.Broadcast(context.Background(), signalr.SignalRMessage{Name: "x"}); err != nil {
		t.Errorf("post-disconnect broadcast: %v", err)
	}
}

func TestHubBroadcaster_NilHub_NoCrash(t *testing.T) {
	t.Parallel()
	var b signalr.Broadcaster = signalr.HubBroadcaster{Hub: nil}
	if b.IsConnected() {
		t.Errorf("nil-Hub broadcaster reports connected")
	}
	if err := b.Broadcast(context.Background(), signalr.SignalRMessage{}); err != nil {
		t.Errorf("nil-Hub broadcast: %v", err)
	}
}

func TestHubBroadcaster_BindsToHub(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub()
	b := signalr.HubBroadcaster{Hub: h}
	if b.IsConnected() {
		t.Errorf("empty hub via broadcaster should not be connected")
	}
	_, _ = h.OnConnect(context.Background(), "x")
	if !b.IsConnected() {
		t.Errorf("broadcaster should report connected after OnConnect")
	}
}

func TestHub_ConcurrentLifecycle(t *testing.T) {
	t.Parallel()
	h := signalr.NewHub(signalr.WithChannelBuffer(64))
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cid := signalr.ConnID("c" + string(rune('a'+id%26)) + string(rune('0'+id/10)))
			conn, err := h.OnConnect(context.Background(), cid)
			if errors.Is(err, signalr.ErrConnAlreadyRegistered) {
				return
			}
			if err != nil {
				t.Errorf("OnConnect: %v", err)
				return
			}
			for range 3 {
				select {
				case <-conn.Messages:
				case <-conn.Done:
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
			h.OnDisconnect(cid)
		}(i)
	}
	stop := make(chan struct{})
	prodDone := make(chan struct{})
	go func() {
		defer close(prodDone)
		for {
			select {
			case <-stop:
				return
			default:
				_ = h.Broadcast(context.Background(), signalr.SignalRMessage{Name: "x"})
				time.Sleep(time.Millisecond)
			}
		}
	}()
	consumersDone := make(chan struct{})
	go func() { wg.Wait(); close(consumersDone) }()
	select {
	case <-consumersDone:
	case <-time.After(3 * time.Second):
		close(stop)
		<-prodDone
		t.Fatalf("consumers exceeded timeout")
	}
	close(stop)
	<-prodDone
	if h.ConnectionCount() != 0 {
		t.Errorf("ConnectionCount after teardown: %d, want 0", h.ConnectionCount())
	}
}
