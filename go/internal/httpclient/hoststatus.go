package httpclient

import (
	"sync"
	"time"
)

// HostStatusTracker is the per-host circuit-breaker the production Client
// consults before each dispatch. There is no direct C# equivalent — Sonarr's
// IHttpClient does not maintain per-host state; per-host failure tracking
// lives at the Provider layer (IndexerStatusService etc., queue [3] of
// Phase 4). The Go port adds this layer at the HttpClient level so a single
// host failing across multiple providers (e.g. a Cloudflare-fronted indexer
// and a sister site sharing infrastructure) can fall through one cooldown
// instead of N independent provider cooldowns.
//
// The tracker is OPTIONAL. When the Client is constructed without one
// (ClientOptions.HostStatusTracker == nil), every dispatch is allowed and
// no recording happens — preserving backwards-compatibility with the
// minimal-config path used in tests.
//
// Behavioural contract:
//   - Allow(host) returns (true, 0) when the host has had no recorded
//     failures, or its cooldown has expired.
//   - Allow(host) returns (false, retryAfter) when the host is in cooldown.
//   - Record(host, success=true) clears the failure level back to zero.
//   - Record(host, success=false) escalates the failure level by one and
//     applies the backoff ladder duration.
//
// We deliberately do NOT trip the tracker on application-layer status codes
// (4xx) — those typically indicate request-side issues that the per-host
// circuit shouldn't penalise. Only transport-level failures (dispatcher
// error) and 5xx server responses count as failures.
type HostStatusTracker interface {
	// Allow reports whether dispatch to the host is permitted right now.
	// When allowed=false, retryAfter is the remaining cooldown.
	Allow(host string) (allowed bool, retryAfter time.Duration)

	// Record updates the tracker after a dispatch. success=true clears the
	// failure level; success=false escalates it.
	Record(host string, success bool)
}

// HostBackoffPeriods is the per-host failure ladder. Index 0 is the
// no-failure baseline (allow); higher indices apply progressively longer
// cooldowns. Distinct from providerstatus.BackOffPeriods (which is a longer
// 0..24h ladder for provider-level cooldowns) because the per-host layer
// should be much more aggressive about recovering — a single transient
// failure shouldn't lock a host out for 60s+ across every provider that
// uses it.
var HostBackoffPeriods = []time.Duration{
	0,                // level 0: no cooldown
	5 * time.Second,  // level 1
	30 * time.Second, // level 2
	2 * time.Minute,  // level 3
	5 * time.Minute,  // level 4
	15 * time.Minute, // level 5: cap
}

// hostStatusTracker is the default in-memory HostStatusTracker. State is a
// `host → entry` map under sync.Mutex. now() is injectable for tests.
type hostStatusTracker struct {
	mu      sync.Mutex
	entries map[string]*hostEntry
	now     func() time.Time
	periods []time.Duration
}

type hostEntry struct {
	level    int       // 0 = healthy; 1+ = cooled down
	disabled time.Time // when the cooldown ends (zero when level==0)
}

// NewHostStatusTracker returns a fresh in-memory tracker using the default
// HostBackoffPeriods ladder.
func NewHostStatusTracker() HostStatusTracker {
	return &hostStatusTracker{
		entries: map[string]*hostEntry{},
		now:     time.Now,
		periods: HostBackoffPeriods,
	}
}

// Allow implements HostStatusTracker.
func (t *hostStatusTracker) Allow(host string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.entries[host]
	if !ok || e.level == 0 {
		return true, 0
	}
	now := t.now()
	if !now.Before(e.disabled) {
		// Cooldown elapsed but we keep the level until a Record(true)
		// resets it (or the next failure escalates further). Allow now.
		return true, 0
	}
	return false, e.disabled.Sub(now)
}

// Record implements HostStatusTracker.
func (t *hostStatusTracker) Record(host string, success bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if success {
		// Drop the entry entirely on success — the next failure starts
		// from level 0 again. Mirrors C# ProviderStatusServiceBase.OnSuccess
		// at ThingiProvider/Status/ProviderStatusServiceBase.cs (RecordSuccess
		// resets EscalationLevel to 0; we go further and remove the row).
		delete(t.entries, host)
		return
	}

	e, ok := t.entries[host]
	if !ok {
		e = &hostEntry{}
		t.entries[host] = e
	}
	if e.level+1 < len(t.periods) {
		e.level++
	}
	e.disabled = t.now().Add(t.periods[e.level])
}

// HostBlockedError is returned by Client.Execute when the host status
// tracker has the host in cooldown. RetryAfter carries the remaining
// cooldown so callers can decide whether to retry now or punt.
type HostBlockedError struct {
	Host       string
	RetryAfter time.Duration
}

// Error implements error.
func (e *HostBlockedError) Error() string {
	return "httpclient: host " + e.Host + " is in cooldown (retry after " + e.RetryAfter.String() + ")"
}
