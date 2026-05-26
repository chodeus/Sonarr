// Package releasecache provides a TTL-keyed in-memory cache for
// release decisions. It mirrors the C# `ICached<RemoteEpisode>` cache
// that `ReleaseController` holds at 30-minute TTL (via the global
// `ICacheManager`).
//
// The cache is keyed by `<indexerID>_<guid>` — the same composite key
// C# uses at `ReleaseController.GetCacheKey`. GET /release / search
// handlers seed the cache as a side-effect of decision-engine fanout;
// POST /release (manual download) reads from it to recover the full
// `decisionengine.Decision` for dispatch.
//
// Cache miss is the dominant path — it happens whenever the entry's
// TTL expired, Sonarr restarted, or the user is hitting Sonarr from
// a fresh session. The POST handler treats miss as 404
// `"Couldn't find requested release in cache, try searching again"`
// matching C# behaviour at `ReleaseController.cs:75-80`.
//
// Audit row C.1.i-follow-5a (J237).
package releasecache

import (
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
)

// DefaultTTL matches the C# `TimeSpan.FromMinutes(30)` value at
// `ReleaseController.cs:240`.
const DefaultTTL = 30 * time.Minute

// Cache is a thread-safe TTL-keyed in-memory store of release
// decisions. Goroutine-safe; the zero value is unusable — construct
// via [New].
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration
	now     func() time.Time // injectable for tests
}

type entry struct {
	decision  decisionengine.Decision
	expiresAt time.Time
}

// New constructs a Cache with the configured TTL. ttl=0 falls back to
// [DefaultTTL]. The clock is injectable via SetClock for deterministic
// tests; by default uses [time.Now].
func New(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Cache{
		entries: make(map[string]entry),
		ttl:     ttl,
		now:     time.Now,
	}
}

// SetClock overrides the time source. Test-only — production code
// uses the default time.Now binding from [New].
func (c *Cache) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// Key formats the composite cache key per C# `GetCacheKey` at
// `ReleaseController.cs:245-248`: `<indexerID>_<guid>`.
func Key(indexerID int, guid string) string {
	return formatInt(indexerID) + "_" + guid
}

// Put writes a decision to the cache under the supplied key. Resets
// the TTL on re-write (idempotent — same release recorded twice in a
// search batch extends the lifespan, matching C#'s LazyDictionary
// re-Set semantic).
func (c *Cache) Put(key string, decision decisionengine.Decision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{
		decision:  decision,
		expiresAt: c.now().Add(c.ttl),
	}
}

// PutMany is the bulk-Put convenience. Each key in the supplied map
// gets its own fresh TTL stamp.
func (c *Cache) PutMany(keyed map[string]decisionengine.Decision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := c.now().Add(c.ttl)
	for k, d := range keyed {
		c.entries[k] = entry{decision: d, expiresAt: stamp}
	}
}

// Get returns the cached decision under key, or (zero, false) on
// miss / expired-entry. Expired entries are removed on read (lazy
// eviction; the harness has no goroutine sweeper since cache size
// stays bounded by the search-fanout cadence + TTL).
func (c *Cache) Get(key string) (decisionengine.Decision, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return decisionengine.Decision{}, false
	}
	if c.now().After(e.expiresAt) {
		// Lazy eviction. Swap to write lock for the delete.
		c.mu.Lock()
		// Re-check expired state under write lock in case a Put
		// raced in between.
		if cur, still := c.entries[key]; still && c.now().After(cur.expiresAt) {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return decisionengine.Decision{}, false
	}
	return e.decision, true
}

// Len returns the current entry count (including not-yet-evicted
// expired ones). Test-helper; production callers don't need it.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Reset clears every entry. Used by tests + boot-time housekeeping
// (e.g. after the user changes the indexer config which would have
// invalidated every cached release anyway).
func (c *Cache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]entry)
}

// formatInt is a small int→string conversion that avoids importing
// strconv just for one call site. Inlined to keep the package's
// import surface tight.
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
