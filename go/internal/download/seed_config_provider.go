package download

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/parser"
)

// SeedConfigProvider ports the readonly `IDownloadSeedConfigProvider`
// interface at `Download/DownloadSeedConfigProvider.cs:12-15` —
// "given an infoHash, what seed-policy override (Ratio / SeedTime)
// should be stamped onto the torrent we handed the client at grab
// time?".
//
// Implementations memoise per-infohash (the production impl is a
// rolling 1-hour cache mirroring the C# `GetRollingCache` at
// `:37`), so callers may invoke this on every torrent-status poll
// without re-walking the DownloadHistory event stream.
//
// Returns (nil, nil) for any infohash without a recorded grab event
// or whose grab event lacks a parseable title — the cleanup-gate
// callers in RQBit / Flood / Tribler treat nil as "no override,
// fall through to the client's global default" matching C#
// `:42-58`.
type SeedConfigProvider interface {
	GetSeedConfiguration(ctx context.Context, infoHash string) (*downloadclients.TorrentSeedConfiguration, error)
}

// LatestGrabReader is the narrow slice of [downloadhistory.Service]
// the SeedConfigProvider reaches for. Mirrors C#
// `IDownloadHistoryService.GetLatestGrab` consumed at
// `DownloadSeedConfigProvider.cs:63`.
//
// Pulled out as an interface so tests can inject a stub returning a
// canned [downloadhistory.Entry] without needing a live datastore,
// and so the wiring slice can compose any future replacement of the
// history service (e.g. an event-bus snapshot) without rewriting
// the seed-config provider.
//
// `*downloadhistory.Service` satisfies this surface today via its
// own `GetLatestGrab(ctx, downloadID) (*Entry, error)` at
// `downloadhistory/service.go:188`.
type LatestGrabReader interface {
	GetLatestGrab(ctx context.Context, downloadID string) (*downloadhistory.Entry, error)
}

// IndexerSeedConfig ports the (int indexerId, bool fullSeason)
// overload of `ISeedConfigProvider` at
// `Indexers/SeedConfigProvider.cs:10`. Given an indexer ID and the
// "is this a full-season pack?" flag, returns the per-indexer
// seed-policy override (or nil when the indexer's SeedCriteria are
// unset).
//
// The indexer-side ISeedConfigProvider isn't ported yet (cleanup-
// gate predicates in RQBit/Flood/Tribler still default to false
// pending its arrival). When that port lands, its production type
// will satisfy this interface and the wiring slice will inject it
// here. Tests inject a stub returning a fixed
// [downloadclients.TorrentSeedConfiguration].
type IndexerSeedConfig interface {
	GetSeedConfiguration(ctx context.Context, indexerID int, fullSeason bool) (*downloadclients.TorrentSeedConfiguration, error)
}

// DefaultSeedConfigCacheTTL mirrors the rolling-cache window the C#
// implementation uses at `DownloadSeedConfigProvider.cs:37` —
// `TimeSpan.FromHours(1)`. Per-infohash CachedSeedConfiguration
// entries are evicted when their first-insert timestamp falls
// outside this window; subsequent gets re-query DownloadHistory.
const DefaultSeedConfigCacheTTL = 1 * time.Hour

// cachedSeedConfig mirrors the private C# CachedSeedConfiguration
// nested class at `DownloadSeedConfigProvider.cs:23-27`. Two-field
// tuple keyed by infoHash; cheap to construct, cheap to keep, and
// shielded from the public surface (the public method materialises
// the TorrentSeedConfiguration on each call by composing this with
// the indexer provider).
type cachedSeedConfig struct {
	indexerID  int
	fullSeason bool
}

type seedCacheEntry struct {
	val       *cachedSeedConfig
	expiresAt time.Time
}

// downloadSeedConfigProvider ports `DownloadSeedConfigProvider` at
// `Download/DownloadSeedConfigProvider.cs:17-89`.
//
// Composition:
//   - [LatestGrabReader] supplies the most-recent grab event for the
//     supplied infohash; matches C# `:35,63` (`_downloadHistoryService`).
//   - [IndexerSeedConfig] resolves indexer-level SeedCriteria into a
//     TorrentSeedConfiguration; matches C# `:34,56` (`_indexerSeedConfigProvider`).
//   - The rolling map+mutex below implements the C# `ICached<>`
//     rolling-cache at `:29,37,49` — keys expire after `cacheTTL`,
//     misses re-fetch via `fetchIndexer`.
//
// `now` and `cacheTTL` are exposed via [NewSeedConfigProvider] /
// [seedConfigOption] so tests can pin clock + window without time-
// sleeping.
type downloadSeedConfigProvider struct {
	history  LatestGrabReader
	indexer  IndexerSeedConfig
	logger   *slog.Logger
	cacheTTL time.Duration
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]seedCacheEntry
}

// SeedConfigOption tunes a [NewSeedConfigProvider] construction.
// Defaults match C# production: 1h rolling-cache, wall-clock now.
type SeedConfigOption func(*downloadSeedConfigProvider)

// WithSeedConfigCacheTTL overrides the rolling-cache window. Pass 0
// to disable caching (every get re-fetches).
func WithSeedConfigCacheTTL(d time.Duration) SeedConfigOption {
	return func(p *downloadSeedConfigProvider) { p.cacheTTL = d }
}

// WithSeedConfigClock pins the clock used for cache-expiry checks
// — tests use this to step over the TTL boundary without a
// `time.Sleep`.
func WithSeedConfigClock(now func() time.Time) SeedConfigOption {
	return func(p *downloadSeedConfigProvider) {
		if now != nil {
			p.now = now
		}
	}
}

// NewSeedConfigProvider returns a production [SeedConfigProvider]
// composing the given history + indexer slices. Mirrors the C#
// constructor at `DownloadSeedConfigProvider.cs:31-38` — both
// dependencies are required (nil → constructor panic, matching the
// C# DI-container behaviour of refusing to compose a class with a
// null-resolved ISeedConfigProvider/IDownloadHistoryService).
//
// Logger nil → slog.Default so callers can omit it during boot
// before the structured logger is wired.
func NewSeedConfigProvider(history LatestGrabReader, indexer IndexerSeedConfig, logger *slog.Logger, opts ...SeedConfigOption) SeedConfigProvider {
	if history == nil {
		panic("download: NewSeedConfigProvider: nil LatestGrabReader")
	}
	if indexer == nil {
		panic("download: NewSeedConfigProvider: nil IndexerSeedConfig")
	}
	if logger == nil {
		logger = slog.Default()
	}
	p := &downloadSeedConfigProvider{
		history:  history,
		indexer:  indexer,
		logger:   logger,
		cacheTTL: DefaultSeedConfigCacheTTL,
		now:      time.Now,
		cache:    make(map[string]seedCacheEntry),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// GetSeedConfiguration ports `DownloadSeedConfigProvider.GetSeedConfiguration`
// at `:40-59` — the public surface.
//
// Algorithm:
//  1. Whitespace-empty infohash → (nil, nil). Matches C# `:42-45`
//     `IsNullOrWhiteSpace`. The C# returns null; Go translates to
//     nil-no-error so callers can `if cfg == nil` without examining
//     err.
//  2. Uppercase the infohash. Mirrors C# `:47`. Torrent clients
//     surface infohashes in mixed case; the cache key is
//     normalised so two callers with `abc...` and `ABC...` share a
//     row.
//  3. Cache lookup. On hit (entry within TTL) return the indexer-
//     composed TorrentSeedConfiguration without re-walking history.
//     Miss → fetchIndexer (which walks history + parses title +
//     stores the per-infohash CachedSeedConfiguration), then return
//     the indexer-composed result. Matches C# `:49` Get-with-factory.
//  4. cachedConfig==nil → (nil, nil). C# `:51-54`. This is the "no
//     grab history" / "unparseable title" path; the indexer provider
//     would receive zero/false and return nil anyway, but matching
//     C# short-circuit keeps the audit-trail tidy.
//  5. Delegate to indexer.GetSeedConfiguration. C# `:56`. The
//     indexer provider owns the SeedCriteria → TorrentSeedConfiguration
//     translation (full-season → SeasonPackSeedRatio vs. SeedRatio
//     branch).
//
// Repo/indexer errors propagate. The C# tree doesn't surface either
// (the cache factory swallows everything to "no config"); the Go
// port surfaces err because callers want the distinction between
// "no override" (nil) and "couldn't determine" (err) — and the
// transient-error path can be retried on the next poll without
// poisoning the cache (cache writes happen only on success).
func (p *downloadSeedConfigProvider) GetSeedConfiguration(ctx context.Context, infoHash string) (*downloadclients.TorrentSeedConfiguration, error) {
	if strings.TrimSpace(infoHash) == "" {
		return nil, nil
	}

	key := strings.ToUpper(infoHash)

	cached, err := p.getOrFetch(ctx, key)
	if err != nil {
		return nil, err
	}
	if cached == nil {
		return nil, nil
	}

	return p.indexer.GetSeedConfiguration(ctx, cached.indexerID, cached.fullSeason)
}

// getOrFetch implements the rolling-cache half of the C# `ICached<>`
// pattern at `:49`. Cache hit (entry within TTL) returns the stored
// value without invoking the factory; miss invokes [fetchIndexer]
// then memoises the result (positive or negative) until the TTL
// elapses.
//
// Negative caching: a fetch returning (nil, nil) — "no grab history
// for this infohash" — is still stored. This matches C# `:49`
// where `Get(key, factory)` invokes the factory once and caches
// whatever it returns, including null. Callers who poll on every
// status-tick benefit from not re-walking history when the answer
// is stably "no record".
//
// Errors are NOT cached. A transient DB read failure shouldn't
// poison the cache for an hour; the next call retries.
func (p *downloadSeedConfigProvider) getOrFetch(ctx context.Context, key string) (*cachedSeedConfig, error) {
	if p.cacheTTL > 0 {
		p.mu.Lock()
		entry, ok := p.cache[key]
		p.mu.Unlock()
		if ok && entry.expiresAt.After(p.now()) {
			return entry.val, nil
		}
	}

	val, err := p.fetchIndexer(ctx, key)
	if err != nil {
		return nil, err
	}

	if p.cacheTTL > 0 {
		p.mu.Lock()
		p.cache[key] = seedCacheEntry{val: val, expiresAt: p.now().Add(p.cacheTTL)}
		p.mu.Unlock()
	}

	return val, nil
}

// fetchIndexer ports the private factory `FetchIndexer` at
// `DownloadSeedConfigProvider.cs:61-88`.
//
//   - GetLatestGrab returns the most-recent DownloadGrabbed event for
//     the infohash; nil means "never grabbed" (or never recorded).
//     C# `:63-69`.
//   - Release.Title is parsed via [parser.ParseTitle] to recover
//     FullSeason. C# `:71-75` calls `Parser.Parser.ParseTitle`. nil-
//     Release / unparseable Title → nil cache entry. C# `:77-81`.
//   - On success return a CachedSeedConfiguration carrying IndexerID
//     (so the indexer provider knows which SeedCriteria to consult)
//     and FullSeason (so it picks SeasonPackSeedRatio vs SeedRatio).
//     C# `:83-87`.
//
// The Go port reads Release as a JSON string (the downloadhistory
// row stores ReleaseInfo as serialised JSON — see
// `downloadhistory/downloadhistory.go:13-17`); a decode failure is
// treated the same as nil-Release ("no parseable title").
func (p *downloadSeedConfigProvider) fetchIndexer(ctx context.Context, infoHash string) (*cachedSeedConfig, error) {
	historyItem, err := p.history.GetLatestGrab(ctx, infoHash)
	if err != nil {
		return nil, fmt.Errorf("download: SeedConfigProvider.GetLatestGrab: %w", err)
	}
	if historyItem == nil {
		p.logger.Debug("download: no download history item for infohash, unable to provide seed configuration", slog.String("infoHash", infoHash))
		return nil, nil
	}

	parsed := parseHistoryReleaseTitle(historyItem.Release)
	if parsed == nil {
		p.logger.Debug("download: no parsed title in download history item for infohash, unable to provide seed configuration", slog.String("infoHash", infoHash))
		return nil, nil
	}

	return &cachedSeedConfig{
		indexerID:  historyItem.IndexerID,
		fullSeason: parsed.FullSeason,
	}, nil
}

// parseHistoryReleaseTitle decodes the persisted ReleaseInfo JSON
// blob stored on a [downloadhistory.Entry] and parses its Title via
// [parser.ParseTitle]. Returns nil on any failure path — empty
// Release column, malformed JSON, or unparseable title. Matches the
// C# null-tolerance at `DownloadSeedConfigProvider.cs:72-81`.
func parseHistoryReleaseTitle(releaseJSON string) *parser.ParsedEpisodeInfo {
	if strings.TrimSpace(releaseJSON) == "" {
		return nil
	}
	var rel parser.ReleaseInfo
	if err := json.Unmarshal([]byte(releaseJSON), &rel); err != nil {
		return nil
	}
	if rel.Title == "" {
		return nil
	}
	return parser.ParseTitle(rel.Title)
}

// Compile-time interface guard. If [downloadhistory.Service] ever
// drops its GetLatestGrab method or changes the signature, the
// wiring breakage shows up at build time rather than at the wiring
// callsite. Mirrors the pattern used by [downloadhistory.Repo] at
// `downloadhistory/service.go:208`.
var _ LatestGrabReader = (*downloadhistory.Service)(nil)
