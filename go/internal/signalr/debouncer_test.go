package signalr_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/signalr"
)

// Debouncer tests use short durations (5-30ms) to keep the suite
// fast. Real-world delays (100ms / 5s) work identically — the
// underlying time.AfterFunc has no special-cased short timers.

func TestDebouncer_FiresAfterDelay(t *testing.T) {
	t.Parallel()
	fired := make(chan struct{}, 1)
	d := signalr.NewDebouncer(func() { fired <- struct{}{} }, 20*time.Millisecond)
	d.Execute()
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatalf("fn never fired")
	}
}

func TestDebouncer_CoalescesBursty(t *testing.T) {
	t.Parallel()
	// Many Execute() calls within the debounce window must result
	// in exactly ONE fn invocation. The C# trailing-edge semantic
	// pinned here.
	var count int32
	d := signalr.NewDebouncer(func() { atomic.AddInt32(&count, 1) }, 30*time.Millisecond)
	for range 50 {
		d.Execute()
	}
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("burst coalesce: fn fired %d times, want 1", got)
	}
}

func TestDebouncer_SecondBurstAfterFire(t *testing.T) {
	t.Parallel()
	// After a first fire, a fresh Execute() must re-arm the timer
	// (NOT be absorbed). Two distinct fires for two separated bursts.
	var count int32
	d := signalr.NewDebouncer(func() { atomic.AddInt32(&count, 1) }, 20*time.Millisecond)
	d.Execute()
	time.Sleep(60 * time.Millisecond)
	d.Execute()
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 2 {
		t.Errorf("two bursts: fn fired %d times, want 2", got)
	}
}

func TestDebouncer_PauseSuppresses(t *testing.T) {
	t.Parallel()
	var count int32
	d := signalr.NewDebouncer(func() { atomic.AddInt32(&count, 1) }, 20*time.Millisecond)
	d.Pause()
	d.Execute()
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Errorf("pause should suppress: got %d fires", got)
	}
	// Resume re-arms the timer for the recorded trigger.
	d.Resume()
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("post-resume fire: got %d, want 1", got)
	}
}

func TestDebouncer_PauseResumeStacks(t *testing.T) {
	t.Parallel()
	var count int32
	d := signalr.NewDebouncer(func() { atomic.AddInt32(&count, 1) }, 20*time.Millisecond)
	d.Pause()
	d.Pause()
	d.Execute()
	d.Resume()
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Errorf("counter > 0 after partial resume should still suppress: got %d", got)
	}
	d.Resume() // counter back to 0
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("full resume fire: got %d, want 1", got)
	}
}

func TestDebouncer_StopHaltsPendingFire(t *testing.T) {
	t.Parallel()
	var count int32
	d := signalr.NewDebouncer(func() { atomic.AddInt32(&count, 1) }, 20*time.Millisecond)
	d.Execute()
	d.Stop()
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Errorf("Stop should suppress pending fire: got %d", got)
	}
}

func TestDebouncer_ExecuteAfterStopIsNoop(t *testing.T) {
	t.Parallel()
	var count int32
	d := signalr.NewDebouncer(func() { atomic.AddInt32(&count, 1) }, 20*time.Millisecond)
	d.Stop()
	d.Execute()
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Errorf("Execute after Stop should be no-op: got %d", got)
	}
}

func TestDebouncer_FireCanReExecute(t *testing.T) {
	t.Parallel()
	// The wrapped fn may call Execute back into the debouncer (a
	// real consumer might trigger a re-evaluation on completion).
	// The Go port must not deadlock — fn runs WITHOUT d.mu held.
	var d *signalr.Debouncer
	var count int32
	d = signalr.NewDebouncer(func() {
		// First call: re-trigger. Second call: stop.
		n := atomic.AddInt32(&count, 1)
		if n == 1 {
			d.Execute()
		}
	}, 20*time.Millisecond)
	d.Execute()
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 2 {
		t.Errorf("self-re-execute: got %d fires, want 2", got)
	}
}

func TestDebouncer_NilFnPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil fn")
		}
	}()
	signalr.NewDebouncer(nil, time.Second)
}

func TestDebouncer_ZeroDelayPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on zero delay")
		}
	}()
	signalr.NewDebouncer(func() {}, 0)
}

func TestDebouncer_ConcurrentExecute(t *testing.T) {
	t.Parallel()
	// 100 goroutines calling Execute concurrently must still
	// produce exactly ONE fire — pinning the mutex correctness.
	var count int32
	d := signalr.NewDebouncer(func() { atomic.AddInt32(&count, 1) }, 30*time.Millisecond)
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Execute()
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("concurrent burst coalesce: got %d fires, want 1", got)
	}
}
