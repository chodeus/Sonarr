package httpclient

import (
	"context"
	"sync"
	"time"
)

// RateLimiter is the per-host (with optional sub-key) rate limiter that
// indexer/import-list calls block on before dispatching. C# cite:
// src/NzbDrone.Common/TPL/RateLimitService.cs:9-94.
//
// The C# IRateLimitService exposes both sync and async variants of
// WaitAndPulse; the Go interface is async-only and ctx-aware.
type RateLimiter interface {
	// WaitAndPulse blocks until enough time has passed since the previous
	// invocation for `key` (and optionally `subKey`), then pulses the timer
	// forward. Cancellation honoured via ctx.
	//
	// `key` is typically the request host (e.g. "api.indexer.tld").
	// `subKey` is an optional bucket within the host (e.g. an API key).
	// `interval` is the minimum spacing between pulses for that bucket.
	WaitAndPulse(ctx context.Context, key, subKey string, interval time.Duration) error
}

// rateLimiter is the default in-memory implementation. State is a
// `key→waitUntil` map under a single mutex (we expect contention to be
// low because the entire wait computation is O(2) map ops).
type rateLimiter struct {
	mu        sync.Mutex
	waitUntil map[string]time.Time
	now       func() time.Time // injectable clock for tests
	sleep     func(ctx context.Context, d time.Duration) error
}

// NewRateLimiter returns a fresh in-memory rate limiter.
func NewRateLimiter() RateLimiter {
	return &rateLimiter{
		waitUntil: map[string]time.Time{},
		now:       time.Now,
		sleep:     ctxSleep,
	}
}

// ctxSleep blocks for d or until ctx cancels. Returns ctx.Err on cancel.
func ctxSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitAndPulse implements RateLimiter. Mirrors RateLimitService.GetDelay
// at C# RateLimitService.cs:60-92.
//
// Algorithm (faithful port):
//  1. Compute `waitUntil = now + interval`.
//  2. If subKey non-empty:
//     a. Update the BASE key with `max(waitUntil, current)` — extending the
//     base timer up to (but never beyond) `now+interval`. C# uses
//     `i.Add(interval)` only when `subKey` is empty; with a subKey the
//     base only advances to `max(waitUntil, current)` (no add). This
//     lets multiple subKeys on the same host share a backstop without
//     runaway extension.
//     b. Update the COMBINED `key-subKey` with
//     `max(waitUntil, current+interval)` — i.e. always queue the next
//     pulse at least `interval` past the previous combined entry.
//  3. Else (no subKey):
//     - Update the BASE key with `max(waitUntil, current+interval)`.
//  4. Subtract `interval` from waitUntil — the C# code does this so that
//     the *delay* returned represents "how long until I can fire", whereas
//     the stored value represents "the next slot's start". Finally,
//     `delay = waitUntil - now`.
//  5. Sleep for `delay` if positive; the stored entries persist past the
//     sleep so subsequent calls see the updated horizon.
func (r *rateLimiter) WaitAndPulse(ctx context.Context, key, subKey string, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}

	r.mu.Lock()

	now := r.now()
	waitUntil := now.Add(interval)

	if subKey != "" {
		// Base key: max(waitUntil, current) — no Add.
		if cur, ok := r.waitUntil[key]; ok && cur.After(waitUntil) {
			waitUntil = cur
		}
		r.waitUntil[key] = waitUntil

		// Combined key: max(waitUntil, current+interval).
		combined := key + "-" + subKey
		if cur, ok := r.waitUntil[combined]; ok {
			next := cur.Add(interval)
			if next.After(waitUntil) {
				waitUntil = next
			}
		}
		r.waitUntil[combined] = waitUntil
	} else {
		// No subKey: max(waitUntil, current+interval).
		if cur, ok := r.waitUntil[key]; ok {
			next := cur.Add(interval)
			if next.After(waitUntil) {
				waitUntil = next
			}
		}
		r.waitUntil[key] = waitUntil
	}

	// The C# code subtracts `interval` from waitUntil before computing the
	// delay (RateLimitService.cs:88-90). Preserve that exactly.
	delay := waitUntil.Add(-interval).Sub(now)

	r.mu.Unlock()

	return r.sleep(ctx, delay)
}
