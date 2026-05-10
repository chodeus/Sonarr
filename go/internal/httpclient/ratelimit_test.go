package httpclient

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock + sleepRecorder make rate-limiter tests deterministic without
// actually sleeping. The limiter calls now() to read time and sleep(d) to
// pause; we drive both manually.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type sleepRecorder struct {
	mu     sync.Mutex
	delays []time.Duration
	// When advance is true, the recorded sleep also advances the clock —
	// useful for tests that exercise multiple back-to-back pulses. Stored-
	// value tests (the C# fixture transcriptions) leave it false so the
	// epoch frame of reference doesn't drift between assertion and pulse.
	clock   *fakeClock
	advance bool
}

func (s *sleepRecorder) Sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	s.mu.Unlock()
	if s.advance && s.clock != nil && d > 0 {
		s.clock.Advance(d)
	}
	return nil
}

// newTestRateLimiter returns a fake-clock-backed limiter. The default
// recorder does NOT advance the clock on sleep — that mode is opt-in via
// the second factory below.
func newTestRateLimiter() (*rateLimiter, *fakeClock, *sleepRecorder) {
	clk := &fakeClock{now: time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)}
	rec := &sleepRecorder{clock: clk}
	rl := &rateLimiter{
		waitUntil: map[string]time.Time{},
		now:       clk.Now,
		sleep:     rec.Sleep,
	}
	return rl, clk, rec
}

// newAdvancingRateLimiter returns a limiter whose sleeps advance the clock —
// the right shape for back-to-back-pulse delay-spacing tests.
func newAdvancingRateLimiter() (*rateLimiter, *fakeClock, *sleepRecorder) {
	rl, clk, rec := newTestRateLimiter()
	rec.advance = true
	return rl, clk, rec
}

func TestRateLimiter_NoSubKey_PulsesIntervalApart(t *testing.T) {
	rl, _, rec := newAdvancingRateLimiter()
	ctx := context.Background()
	interval := 100 * time.Millisecond

	// First call: no prior pulse → no sleep.
	if err := rl.WaitAndPulse(ctx, "host", "", interval); err != nil {
		t.Fatal(err)
	}
	// Second call immediately: must sleep ~interval.
	if err := rl.WaitAndPulse(ctx, "host", "", interval); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.delays) != 2 {
		t.Fatalf("expected 2 sleep calls, got %d", len(rec.delays))
	}
	if rec.delays[0] != 0 {
		t.Errorf("first delay = %v want 0", rec.delays[0])
	}
	// The second should be very close to interval (could be slightly less
	// if any clock jitter, but with the fake clock it should be exact).
	if rec.delays[1] != interval {
		t.Errorf("second delay = %v want %v", rec.delays[1], interval)
	}
}

func TestRateLimiter_DifferentKeysIndependent(t *testing.T) {
	rl, _, rec := newTestRateLimiter()
	ctx := context.Background()
	interval := 100 * time.Millisecond

	if err := rl.WaitAndPulse(ctx, "a", "", interval); err != nil {
		t.Fatal(err)
	}
	if err := rl.WaitAndPulse(ctx, "b", "", interval); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for i, d := range rec.delays {
		if d != 0 {
			t.Errorf("delay[%d] = %v, want 0 (different keys)", i, d)
		}
	}
}

func TestRateLimiter_ZeroIntervalNoOp(t *testing.T) {
	rl, _, rec := newTestRateLimiter()
	if err := rl.WaitAndPulse(context.Background(), "host", "", 0); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.delays) != 0 {
		t.Errorf("expected no sleep for zero interval, got %d", len(rec.delays))
	}
}

func TestRateLimiter_SubKeyExtendsCombined(t *testing.T) {
	// With a subKey, two subsequent calls on the same combined key should
	// space out by `interval` (the combined entry uses current+interval).
	rl, _, rec := newAdvancingRateLimiter()
	ctx := context.Background()
	interval := 100 * time.Millisecond

	if err := rl.WaitAndPulse(ctx, "host", "alice", interval); err != nil {
		t.Fatal(err)
	}
	if err := rl.WaitAndPulse(ctx, "host", "alice", interval); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.delays[0] != 0 {
		t.Errorf("first subkey delay = %v want 0", rec.delays[0])
	}
	if rec.delays[1] != interval {
		t.Errorf("second subkey delay = %v want %v", rec.delays[1], interval)
	}
}

// The next four tests transcribe the C# fixture at
// src/NzbDrone.Common.Test/TPLTests/RateLimitServiceFixture.cs:75-124.
// They verify the stored waitUntil values directly, which is the C# fixture's
// preferred assertion mechanism (avoids timing flakes).

func TestRateLimiter_ExtendDelay(t *testing.T) {
	// Mirrors `should_extend_delay`. With a pre-existing entry at +200ms
	// and a 100ms pulse, the stored value must be ≥ +300ms (extended).
	rl, clk, _ := newTestRateLimiter()
	rl.waitUntil["me"] = clk.Now().Add(200 * time.Millisecond)
	if err := rl.WaitAndPulse(context.Background(), "me", "", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	delta := rl.waitUntil["me"].Sub(clk.Now())
	if delta < 300*time.Millisecond {
		t.Errorf("stored = +%v want >=+300ms", delta)
	}
}

func TestRateLimiter_ExtendSubKeyDelay(t *testing.T) {
	// Mirrors `should_extend_subkey_delay`. base=+200ms, sub=+300ms,
	// pulse with 100ms → combined must be ≥ +400ms.
	rl, clk, _ := newTestRateLimiter()
	rl.waitUntil["me"] = clk.Now().Add(200 * time.Millisecond)
	rl.waitUntil["me-sub"] = clk.Now().Add(300 * time.Millisecond)
	if err := rl.WaitAndPulse(context.Background(), "me", "sub", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	delta := rl.waitUntil["me-sub"].Sub(clk.Now())
	if delta < 400*time.Millisecond {
		t.Errorf("combined stored = +%v want >=+400ms", delta)
	}
}

func TestRateLimiter_HonorBaseKeyDelay(t *testing.T) {
	// Mirrors `should_honor_basekey_delay`. base=+200ms, sub=+0ms,
	// pulse → combined must be ≥ +200ms (base wins).
	rl, clk, _ := newTestRateLimiter()
	rl.waitUntil["me"] = clk.Now().Add(200 * time.Millisecond)
	rl.waitUntil["me-sub"] = clk.Now()
	if err := rl.WaitAndPulse(context.Background(), "me", "sub", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	delta := rl.waitUntil["me-sub"].Sub(clk.Now())
	if delta < 200*time.Millisecond {
		t.Errorf("combined stored = +%v want >=+200ms", delta)
	}
}

func TestRateLimiter_NotExtendBaseKeyDelay(t *testing.T) {
	// Mirrors `should_not_extend_basekey_delay`. base=+200ms, sub=+100ms,
	// pulse → base must remain ~+200ms (NOT extended by sub-key activity).
	rl, clk, _ := newTestRateLimiter()
	rl.waitUntil["me"] = clk.Now().Add(200 * time.Millisecond)
	rl.waitUntil["me-sub"] = clk.Now().Add(100 * time.Millisecond)
	if err := rl.WaitAndPulse(context.Background(), "me", "sub", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	delta := rl.waitUntil["me"].Sub(clk.Now())
	// Tolerate a 20ms window (matches C# fixture's BeCloseTo).
	if delta < 180*time.Millisecond || delta > 220*time.Millisecond {
		t.Errorf("base stored = +%v want ~+200ms", delta)
	}
}

func TestRateLimiter_ContextCancelInterruptsSleep(t *testing.T) {
	rl, _, _ := newTestRateLimiter()
	// Override the sleep to use the real ctxSleep so we can verify cancel.
	rl.sleep = ctxSleep

	// Prime the limiter so the next call needs to sleep.
	if err := rl.WaitAndPulse(context.Background(), "host", "", 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := rl.WaitAndPulse(ctx, "host", "", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected context-cancel error, got nil")
	}
}
