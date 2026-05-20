package indexers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// IndexerFinder is the narrow slice of [Repo] the
// [CachedSettingsProvider] reaches for — "given an indexer ID,
// return the persisted Indexer row".
//
// Mirrors the C# `IIndexerFactory.Find(int indexerId)` consumed at
// `CachedIndexerSettingsProvider.cs:42`. `*Repo` already satisfies
// this surface via its embedded Repository's `Get`; tests inject a
// stub returning canned rows without needing a live datastore.
//
// Returns (nil, nil) for "no such indexer" (i.e. the production
// adapter maps [ErrNotFound] → nil + nil-error so the cached
// provider's null-handling stays close to the C# tree's `?.`
// chaining at `:44`).
type IndexerFinder interface {
	Find(ctx context.Context, indexerID int) (*Indexer, error)
}

// SettingsDecoder ports the C# polymorphism at
// `CachedIndexerSettingsProvider.cs:44-59` — "if the resolved
// IIndexer's Settings implements IIndexerSettings, extract
// FailDownloads; if it ALSO implements ITorrentIndexerSettings,
// extract SeedCriteria".
//
// In C# the discrimination happens via runtime pattern-match against
// the typed Settings property the IndexerFactory has already
// deserialised. In Go the Indexer row carries Settings as a JSON
// string (nullable), so the discrimination collapses to "decode the
// JSON and pluck the optional fields". The decoder takes the
// implementation name + raw JSON and returns the populated
// CachedIndexerSettings; nil-return means "indexer has no
// IIndexerSettings shape, treat as no-settings" (matches C# `:46-48`
// null-on-cast-failure).
//
// The default implementation [DefaultSettingsDecoder] handles the
// shipped indexer types (newznab/torznab/torrentrss) which all
// surface `failDownloads` + optional `seedCriteria` JSON keys; the
// SettingsDecoder hook stays callable so future indexer
// implementations with non-default settings layouts can plug in
// without modifying this package.
type SettingsDecoder func(implementation, settingsJSON string) (*CachedIndexerSettings, error)

// DefaultRollingCacheTTL mirrors the cache window the C# wires at
// `CachedIndexerSettingsProvider.cs:27` —
// `cacheManager.GetRollingCache(..., TimeSpan.FromHours(1))`. Each
// per-indexer entry expires after this window; on miss the provider
// re-walks the IndexerFinder + SettingsDecoder.
const DefaultRollingCacheTTL = 1 * time.Hour

// cachedSettingsEntry pairs the cached value with its rolling-cache
// expiry. Negative entries (nil-value) are also stored so a "no
// settings" outcome doesn't re-walk the factory on every status
// poll — same posture as J91's [download.SeedConfigProvider]
// negative-caching note.
type cachedSettingsEntry struct {
	val       *CachedIndexerSettings
	expiresAt time.Time
}

// CachedSettingsProvider ports the C# class
// `CachedIndexerSettingsProvider` at
// `Indexers/CachedIndexerSettingsProvider.cs:17-73`.
//
// Composes:
//   - [IndexerFinder] (production: *Repo; tests: stub) — looks up
//     the persisted Indexer row by ID. Matches C# `_indexerFactory`
//     at `:21,26`.
//   - [SettingsDecoder] (production: [DefaultSettingsDecoder]; tests:
//     pluggable) — converts the row's Settings JSON into the typed
//     CachedIndexerSettings shape. Replaces the C# runtime pattern-
//     match at `:44-59`.
//
// Internal state is a TTL map + mutex. The C# uses
// `ICacheManager.GetRollingCache` which is shared infrastructure;
// the Go port inlines a small TTL cache because (a) the
// rolling-cache abstraction hasn't ported yet, and (b) the surface
// area is small enough that the inline impl is faster to audit
// than a generic indirection.
//
// Satisfies [IndexerSettingsLookup] — the wiring slice can pass
// `*CachedSettingsProvider` directly to
// `download.NewIndexerSeedConfigProvider`.
type CachedSettingsProvider struct {
	finder  IndexerFinder
	decoder SettingsDecoder
	logger  *slog.Logger
	ttl     time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[int]cachedSettingsEntry
}

// CachedSettingsOption tunes a [NewCachedSettingsProvider]
// construction. Defaults match C# production: 1h rolling-cache,
// wall-clock now, [DefaultSettingsDecoder].
type CachedSettingsOption func(*CachedSettingsProvider)

// WithCachedSettingsTTL overrides the rolling-cache window. 0
// disables caching (every get re-walks).
func WithCachedSettingsTTL(d time.Duration) CachedSettingsOption {
	return func(p *CachedSettingsProvider) { p.ttl = d }
}

// WithCachedSettingsClock pins the clock used for cache-expiry
// checks. Tests use this to step over the TTL window without
// `time.Sleep`.
func WithCachedSettingsClock(now func() time.Time) CachedSettingsOption {
	return func(p *CachedSettingsProvider) {
		if now != nil {
			p.now = now
		}
	}
}

// WithSettingsDecoder overrides the JSON decoder hook. Future
// indexer implementations whose settings JSON diverges from the
// shipped `failDownloads`/`seedCriteria` shape can plug in a custom
// decoder via this option.
func WithSettingsDecoder(d SettingsDecoder) CachedSettingsOption {
	return func(p *CachedSettingsProvider) {
		if d != nil {
			p.decoder = d
		}
	}
}

// NewCachedSettingsProvider constructs the C# `CachedIndexerSettingsProvider`
// at `:24-28`. Logger nil → slog.Default for boot-time wiring before
// the structured logger is composed.
//
// nil finder panics — matches the C# DI posture (the IoC container
// refuses to compose with a null-resolved IIndexerFactory).
func NewCachedSettingsProvider(finder IndexerFinder, logger *slog.Logger, opts ...CachedSettingsOption) *CachedSettingsProvider {
	if finder == nil {
		panic("indexers: NewCachedSettingsProvider: nil IndexerFinder")
	}
	if logger == nil {
		logger = slog.Default()
	}
	p := &CachedSettingsProvider{
		finder:  finder,
		decoder: DefaultSettingsDecoder,
		logger:  logger,
		ttl:     DefaultRollingCacheTTL,
		now:     time.Now,
		cache:   make(map[int]cachedSettingsEntry),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// GetSettings ports `CachedIndexerSettingsProvider.GetSettings` at
// `:30-38`.
//
//   - indexerID == 0 → (nil, nil). C# `:32-35`. Matches the
//     0-sentinel "no indexer" used elsewhere in the seed-config
//     chain (see [download.IndexerSeedConfigProvider.GetSeedConfiguration]
//     at the J92 slice).
//   - Otherwise: rolling-cache get with factory. Cache hit (entry
//     within TTL) skips the IndexerFinder + decoder; miss invokes
//     fetchSettings then memoises the result. Errors NOT cached so
//     a transient DB read doesn't poison the lookup for an hour.
func (p *CachedSettingsProvider) GetSettings(ctx context.Context, indexerID int) (*CachedIndexerSettings, error) {
	if indexerID == 0 {
		return nil, nil
	}

	if p.ttl > 0 {
		p.mu.Lock()
		entry, ok := p.cache[indexerID]
		p.mu.Unlock()
		if ok && entry.expiresAt.After(p.now()) {
			return entry.val, nil
		}
	}

	val, err := p.fetchSettings(ctx, indexerID)
	if err != nil {
		return nil, err
	}

	if p.ttl > 0 {
		p.mu.Lock()
		p.cache[indexerID] = cachedSettingsEntry{val: val, expiresAt: p.now().Add(p.ttl)}
		p.mu.Unlock()
	}

	return val, nil
}

// fetchSettings ports the private factory at `:40-62`.
//
//   - Find the indexer row. nil row (C#: `indexer?` == null) →
//     log trace + return nil. C# `:42,46-48`.
//   - Decode the settings JSON via the configured [SettingsDecoder].
//     Decoder returning (nil, nil) → log trace + nil cache entry,
//     matching the C# `is not IIndexerSettings` short-circuit.
//   - Decoder error propagates — distinguishes "indexer record
//     exists but JSON is malformed" from "indexer doesn't exist".
//
// The C# tree never errors here (the runtime pattern-match returns
// null on failure). The Go port surfaces decoder errors because (a)
// JSON decode failure is a configuration bug we want surfaced, not
// silently swallowed into a stable "no settings" cache row; (b)
// transient errors aren't cached, so the next call retries cleanly.
func (p *CachedSettingsProvider) fetchSettings(ctx context.Context, indexerID int) (*CachedIndexerSettings, error) {
	row, err := p.finder.Find(ctx, indexerID)
	if err != nil {
		return nil, fmt.Errorf("indexers: CachedSettingsProvider.Find(%d): %w", indexerID, err)
	}
	if row == nil {
		p.logger.Log(ctx, slog.LevelDebug, "indexers: could not load row for indexer ID", slog.Int("indexerID", indexerID))
		return nil, nil
	}

	settingsJSON := ""
	if row.Settings != nil {
		settingsJSON = *row.Settings
	}

	settings, err := p.decoder(row.Implementation, settingsJSON)
	if err != nil {
		return nil, fmt.Errorf("indexers: CachedSettingsProvider.Decode(%d, impl=%q): %w", indexerID, row.Implementation, err)
	}
	if settings == nil {
		p.logger.Log(ctx, slog.LevelDebug, "indexers: could not load settings for indexer ID", slog.Int("indexerID", indexerID))
		return nil, nil
	}
	return settings, nil
}

// Clear ports the C# `_cache.Clear()` invocations at `:64-72` —
// the event-handler half of `CachedIndexerSettingsProvider` that
// reacts to `ProviderUpdatedEvent<IIndexer>` /
// `ProviderDeletedEvent<IIndexer>` by purging every cached entry.
//
// The Go event-bus port isn't yet hooked into the providerstatus
// subsystem; once the bus subscriber wiring lands, the message-bus
// handler will call this method on each Indexer-updated/-deleted
// event. Exposed as a public method so the wiring slice can attach
// it without reaching into the impl's private map.
func (p *CachedSettingsProvider) Clear() {
	p.mu.Lock()
	p.cache = make(map[int]cachedSettingsEntry)
	p.mu.Unlock()
}

// settingsExtract is the JSON shape DefaultSettingsDecoder unmarshals
// into. Both fields are nullable in the wire shape; the decoder
// distinguishes "absent" (usenet indexer with no seedCriteria key)
// from "present-but-zero" via the pointer for SeedCriteria.
//
// FailDownloads on usenet indexers (newznab) is always present (the
// constructor at newznab.NewSettings populates an empty slice), so
// the absent-key case maps to nil-slice naturally and the
// CachedIndexerSettings consumer treats a nil-or-empty slice
// identically.
type settingsExtract struct {
	FailDownloads []int                 `json:"failDownloads"`
	SeedCriteria  *SeedCriteriaSettings `json:"seedCriteria"`
}

// DefaultSettingsDecoder is the JSON-driven SettingsDecoder used by
// production. Implements the C# polymorphism at `:44-59`:
//
//   - Empty/whitespace JSON → (nil, nil). Matches C# `:46-48` —
//     indexer has no IIndexerSettings, treat as no-settings.
//   - Decode succeeds → build CachedIndexerSettings. FailDownloads
//     ints are typed via the [FailDownloads] constant set.
//     SeedCriteriaSettings is plumbed through verbatim (already the
//     canonical type from J92).
//   - Decode fails → return wrapped error so the cached provider
//     can surface it.
//
// The implementation name is currently unused — the JSON shape is
// uniform across the shipped indexer types. The parameter stays in
// the signature so future indexer implementations with bespoke
// settings JSON can be discriminated on without breaking the
// decoder contract.
func DefaultSettingsDecoder(implementation, settingsJSON string) (*CachedIndexerSettings, error) {
	_ = implementation
	if settingsJSON == "" {
		return nil, nil
	}
	var ex settingsExtract
	if err := json.Unmarshal([]byte(settingsJSON), &ex); err != nil {
		return nil, fmt.Errorf("decode indexer settings: %w", err)
	}
	out := &CachedIndexerSettings{
		SeedCriteriaSettings: ex.SeedCriteria,
	}
	if len(ex.FailDownloads) > 0 {
		out.FailDownloads = make([]FailDownloads, 0, len(ex.FailDownloads))
		for _, f := range ex.FailDownloads {
			out.FailDownloads = append(out.FailDownloads, FailDownloads(f))
		}
	}
	return out, nil
}

// RepoIndexerFinder adapts a [*Repo] (or anything with a
// `Get(ctx, id) (*Indexer, error)`) into the narrower
// [IndexerFinder] surface, mapping the repo's ErrNotFound sentinel
// to a nil-result so the cached provider's null-handling matches
// the C# tree's `?.` chaining.
//
// Pulled out as a small adapter rather than baking ErrNotFound-
// awareness into [CachedSettingsProvider] itself — that keeps the
// provider testable against arbitrary IndexerFinder stubs without
// dragging the sentinel sentinel into every test fixture.
type RepoIndexerFinder struct {
	Repo interface {
		Get(ctx context.Context, id int) (*Indexer, error)
	}
}

// Find satisfies [IndexerFinder]. Returns (nil, nil) on ErrNotFound;
// other errors propagate.
func (r RepoIndexerFinder) Find(ctx context.Context, indexerID int) (*Indexer, error) {
	if r.Repo == nil {
		return nil, errors.New("indexers: RepoIndexerFinder: nil Repo")
	}
	row, err := r.Repo.Get(ctx, indexerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return row, nil
}

// Compile-time guard: CachedSettingsProvider satisfies the
// IndexerSettingsLookup interface from J92's seed_criteria.go —
// the wiring slice can pass it straight to
// download.NewIndexerSeedConfigProvider.
var _ IndexerSettingsLookup = (*CachedSettingsProvider)(nil)
