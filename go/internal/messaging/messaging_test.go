package messaging_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/messaging"
)

type fooEvent struct{ Value int }
type barEvent struct{ Tag string }

// shutdownEvent is a Lifecycle event for the BeginShutdown gate test.
type shutdownEvent struct{}

func (shutdownEvent) Lifecycle() bool { return true }

func newBus(t *testing.T) *messaging.Bus {
	t.Helper()
	return messaging.New(slog.New(slog.DiscardHandler))
}

func TestPublish_SyncHandler_ReceivesEvent(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var got int
	messaging.Subscribe(b, "test.foo", func(_ context.Context, e fooEvent) error {
		got = e.Value
		return nil
	})
	if err := messaging.Publish(t.Context(), b, fooEvent{Value: 42}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestPublish_NoSubscribers_NoOp(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	if err := messaging.Publish(t.Context(), b, fooEvent{Value: 1}); err != nil {
		t.Errorf("Publish without subscribers: %v", err)
	}
}

func TestPublish_TypeIsolated(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var fooCalls, barCalls int
	messaging.Subscribe(b, "foo", func(_ context.Context, _ fooEvent) error {
		fooCalls++
		return nil
	})
	messaging.Subscribe(b, "bar", func(_ context.Context, _ barEvent) error {
		barCalls++
		return nil
	})
	_ = messaging.Publish(t.Context(), b, fooEvent{})
	_ = messaging.Publish(t.Context(), b, fooEvent{})
	_ = messaging.Publish(t.Context(), b, barEvent{})
	if fooCalls != 2 || barCalls != 1 {
		t.Errorf("foo=%d (want 2), bar=%d (want 1)", fooCalls, barCalls)
	}
}

func TestPublish_OrderingFirstAnyLast(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var seq []string
	messaging.SubscribeOrdered(b, "any-1", messaging.OrderAny, func(_ context.Context, _ fooEvent) error {
		seq = append(seq, "any-1")
		return nil
	})
	messaging.SubscribeOrdered(b, "last", messaging.OrderLast, func(_ context.Context, _ fooEvent) error {
		seq = append(seq, "last")
		return nil
	})
	messaging.SubscribeOrdered(b, "first", messaging.OrderFirst, func(_ context.Context, _ fooEvent) error {
		seq = append(seq, "first")
		return nil
	})
	messaging.SubscribeOrdered(b, "any-2", messaging.OrderAny, func(_ context.Context, _ fooEvent) error {
		seq = append(seq, "any-2")
		return nil
	})
	_ = messaging.Publish(t.Context(), b, fooEvent{})
	want := []string{"first", "any-1", "any-2", "last"}
	if fmt.Sprint(seq) != fmt.Sprint(want) {
		t.Errorf("order = %v, want %v", seq, want)
	}
}

func TestPublish_HandlerErrorDoesNotStopOthers(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var aCalls, cCalls int
	messaging.Subscribe(b, "a", func(_ context.Context, _ fooEvent) error {
		aCalls++
		return errors.New("handler A failure")
	})
	messaging.Subscribe(b, "b", func(_ context.Context, _ fooEvent) error {
		panic("handler B panicking on purpose")
	})
	messaging.Subscribe(b, "c", func(_ context.Context, _ fooEvent) error {
		cCalls++
		return nil
	})
	if err := messaging.Publish(t.Context(), b, fooEvent{}); err != nil {
		t.Fatalf("Publish (should swallow handler errors): %v", err)
	}
	if aCalls != 1 || cCalls != 1 {
		t.Errorf("a=%d c=%d, want 1 each (handler error/panic must not stop later handlers)", aCalls, cCalls)
	}
}

func TestPublish_AsyncHandler_RunsOffPublishGoroutine(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	done := make(chan struct{})
	messaging.SubscribeAsync(b, "async", func(_ context.Context, _ fooEvent) error {
		close(done)
		return nil
	})
	if err := messaging.Publish(t.Context(), b, fooEvent{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("async handler did not run within 2s")
	}
	b.Wait()
}

func TestPublish_GlobalHandlerSeesEveryEvent(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var n int64
	messaging.SubscribeAllAsync(b, "global", func(_ context.Context, _ any) error {
		atomic.AddInt64(&n, 1)
		return nil
	})
	_ = messaging.Publish(t.Context(), b, fooEvent{})
	_ = messaging.Publish(t.Context(), b, barEvent{})
	_ = messaging.Publish(t.Context(), b, fooEvent{})
	b.Wait()
	if got := atomic.LoadInt64(&n); got != 3 {
		t.Errorf("global handler fired %d times, want 3", got)
	}
}

func TestBeginShutdown_BlocksLifecycleEvents(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var fooCalls, shutdownCalls int
	messaging.Subscribe(b, "foo", func(_ context.Context, _ fooEvent) error {
		fooCalls++
		return nil
	})
	messaging.Subscribe(b, "shutdown", func(_ context.Context, _ shutdownEvent) error {
		shutdownCalls++
		return nil
	})
	b.BeginShutdown()
	// Lifecycle event blocked.
	if err := messaging.Publish(t.Context(), b, shutdownEvent{}); !errors.Is(err, messaging.ErrShutdown) {
		t.Errorf("Lifecycle Publish during shutdown: %v, want ErrShutdown", err)
	}
	// Non-lifecycle event still flows.
	if err := messaging.Publish(t.Context(), b, fooEvent{}); err != nil {
		t.Errorf("Non-lifecycle Publish during shutdown: %v", err)
	}
	if fooCalls != 1 || shutdownCalls != 0 {
		t.Errorf("fooCalls=%d shutdownCalls=%d (want 1, 0)", fooCalls, shutdownCalls)
	}
}

func TestPublish_RecursionLimit(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var calls int64
	messaging.Subscribe(b, "recursive", func(ctx context.Context, _ fooEvent) error {
		atomic.AddInt64(&calls, 1)
		// Recurse on every handler invocation. Should hit the
		// limit and stop without overflowing the stack.
		return messaging.Publish(ctx, b, fooEvent{})
	})
	err := messaging.Publish(t.Context(), b, fooEvent{})
	if err != nil {
		t.Fatalf("outer Publish: %v", err)
	}
	// Each call's recursive Publish returns ErrRecursionLimit (logged
	// but not propagated through Subscribe). We only assert that the
	// guard fires before exhausting the stack — call count should be
	// finite and reasonable.
	if got := atomic.LoadInt64(&calls); got <= 1 || got > 100 {
		t.Errorf("recursion guard not enforced: calls=%d (want >1, ≤100)", got)
	}
}

func TestWait_DrainsPendingAsyncHandlers(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var seen sync.Map
	messaging.SubscribeAsync(b, "slow", func(_ context.Context, e fooEvent) error {
		// Simulate a non-trivial async handler.
		time.Sleep(10 * time.Millisecond)
		seen.Store(e.Value, struct{}{})
		return nil
	})
	for i := 0; i < 50; i++ {
		_ = messaging.Publish(t.Context(), b, fooEvent{Value: i})
	}
	b.Wait()
	count := 0
	seen.Range(func(_, _ any) bool { count++; return true })
	if count != 50 {
		t.Errorf("Wait did not drain: seen=%d, want 50", count)
	}
}

func TestPublish_NilHandlerPanics(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Subscribe(nil) did not panic")
		}
	}()
	messaging.Subscribe[fooEvent](b, "nil", nil)
}

func TestPublish_ConcurrentPublishesSafe(t *testing.T) {
	t.Parallel()
	b := newBus(t)
	var n int64
	messaging.Subscribe(b, "counter", func(_ context.Context, _ fooEvent) error {
		atomic.AddInt64(&n, 1)
		return nil
	})
	messaging.SubscribeAsync(b, "counter-async", func(_ context.Context, _ fooEvent) error {
		atomic.AddInt64(&n, 1)
		return nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = messaging.Publish(t.Context(), b, fooEvent{})
		}()
	}
	wg.Wait()
	b.Wait()
	if got := atomic.LoadInt64(&n); got != 200 {
		t.Errorf("concurrent publishes: got %d, want 200 (100 sync + 100 async)", got)
	}
}
