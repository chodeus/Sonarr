package scenemappings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
)

// MappingsWriter is the slice of the [Repo] surface required by
// [Service.UpdateMappings]. Separated from [MappingsReader] so the
// service constructor stays nil-tolerant: the existing read-side
// callers (parser, search) need only the Reader; the
// UpdateSceneMappingCommand handler (sub-arc M6b-ii) wires both.
//
// Production binds *Repo against this interface via Go structural
// typing.
type MappingsWriter interface {
	// GetAllByType ports C# SceneMappingRepository.GetAllByType at
	// `SceneMappingRepository.cs:25-28` — returns every row whose
	// Type column equals the supplied provider type-name.
	GetAllByType(ctx context.Context, typ string) ([]Mapping, error)
	// InsertMany inserts every supplied row.
	InsertMany(ctx context.Context, mappings []Mapping) error
	// UpdateMany updates every supplied row by its Id.
	UpdateMany(ctx context.Context, mappings []Mapping) error
	// DeleteByIDs removes every row whose Id appears in the list.
	DeleteByIDs(ctx context.Context, ids []int) error
}

// MappingsReader is the slice of [Repo] the [Service] reaches for at
// cache-refresh time. The full Repo.CRUD surface is wider than the
// service needs; the narrow interface is one-method
// (`All(ctx)→([]Mapping,error)`) so tests can stub the populate path
// without the migration / row-mapper machinery.
type MappingsReader interface {
	// All returns every persisted SceneMapping. Used by the service's
	// lazy cache populate at first call (and by RefreshCache).
	All(ctx context.Context) ([]Mapping, error)
}

// Service ports the readonly half of `ISceneMappingService` at
// `DataAugmentation/Scene/SceneMappingService.cs:15-340`. Five
// methods today, with two persistent caches (parseTerm-keyed +
// tvdbId-keyed) populated lazily on first read. Subsequent reads
// hit the cache; [Service.RefreshCache] re-reads from the repo
// (production wiring calls this on the SeriesRefreshStartingEvent /
// SeriesAddedEvent / SeriesImportedEvent / UpdateSceneMappingCommand
// hooks the C# subscribers handle — those subscribers are deferred
// to a later slice).
//
// Goroutine-safe: every read takes the RWMutex's read lock; every
// populate takes the write lock. Concurrent readers don't block each
// other.
//
// Deferred:
//
//   - The C# `UpdateMappings` provider-driven refresh path — pulls
//     `ISceneMappingProvider` instances and writes through the
//     repo. Providers haven't ported yet; the writer surface waits
//     for them.
//   - The Levenshtein closest-match arm in `FindMappings` at
//     `:240-253`. With the no-FilterRegex caveat below the only
//     time the Go port produces a different verdict from C# is
//     when MULTIPLE candidates match an exact title prefix; the
//     Go port returns nil rather than picking the closest. Mirrors
//     C# `InvalidSceneMappingException` behaviour (which the C#
//     code throws as an exception) by treating ambiguity as
//     "no mapping" with a warn log.
//   - The FilterRegex sub-arm of `FilterSceneMappings(candidates,
//     releaseTitle)` at `:266-283`. Exporting `parser.SimplifyTitle`
//     for the regex-target form is a separate slice; until then the
//     Go port treats every mapping as a "normalCandidate" (those
//     without a FilterRegex) and skips the regex sub-arm. Behavior
//     differs only for mappings that explicitly carry a FilterRegex
//     — a small subset.
//   - The four `IHandle<...>` event-bus subscribers + `IExecute<...>`
//     command handler — defer with the unported event/command shapes.
type Service struct {
	repo MappingsReader
	log  *slog.Logger

	// writer carries the bulk-mutation surface UpdateMappings needs.
	// Optional — wired only when the host has a real repo. nil writer
	// makes UpdateMappings a no-op + warn (test path).
	writer MappingsWriter
	// providers are the per-source mapping fetchers UpdateMappings
	// iterates over. Sub-arc M6a shipped the Provider interface +
	// SceneMappingProxy concrete impl; main.go wiring (sub-arc M6b-ii)
	// passes a non-nil slice including the proxy. Empty / nil slice
	// makes UpdateMappings a no-op + warn.
	providers []Provider
	// bus is the messaging surface UpdateMappings publishes the
	// UpdatedEvent on after a successful refresh. nil bus silently
	// skips the publish — matches the unit-test path where no
	// downstream subscribers care.
	bus *messaging.Bus

	mu sync.RWMutex
	// populated tracks whether the cache has been filled at least
	// once. First read populates; later reads check this flag and
	// skip the populate when true.
	populated bool
	// byParseTerm groups mappings by `parser.CleanSeriesTitle(Title)`
	// for the FindSceneMapping lookup. Mirrors C#
	// `_getTvdbIdCache.Update(mappings.GroupBy(v => v.ParseTerm))`
	// at `:260`.
	byParseTerm map[string][]Mapping
	// byTvdbID groups mappings by TvdbID for FindByTvdbID. Mirrors
	// C# `_findByTvdbIdCache.Update(... GroupBy(v => v.TvdbId))`
	// at `:261`.
	byTvdbID map[int][]Mapping
}

// WithWriter binds the bulk-mutation surface UpdateMappings needs.
// Returns the same Service for chaining at the constructor call site.
// Idempotent — successive calls replace the writer.
//
// Production main.go calls `NewService(repo, log).WithWriter(repo)`
// since the same *Repo satisfies both [MappingsReader] and
// [MappingsWriter] via Go structural typing.
func (s *Service) WithWriter(w MappingsWriter) *Service {
	if s == nil {
		return nil
	}
	s.writer = w
	return s
}

// WithProviders binds the per-source mapping fetchers UpdateMappings
// iterates over. Returns the same Service for chaining.
// Idempotent. nil / empty slice clears the providers list.
//
// Production main.go (sub-arc M6b-ii) passes the SceneMappingProxy.
func (s *Service) WithProviders(providers ...Provider) *Service {
	if s == nil {
		return nil
	}
	s.providers = providers
	return s
}

// WithBus binds the messaging bus UpdateMappings publishes the
// UpdatedEvent on. Returns the same Service for chaining.
// Idempotent. nil bus disables the publish (test path).
func (s *Service) WithBus(bus *messaging.Bus) *Service {
	if s == nil {
		return nil
	}
	s.bus = bus
	return s
}

// NewService binds a [MappingsReader] (REQUIRED) + log (OPTIONAL).
// nil log defaults to [slog.Default]. Mirrors the slice-3.55+
// constructor pattern.
//
// Returns nil on nil reader so caller wiring can skip optional
// services without an explicit guard.
func NewService(repo MappingsReader, log *slog.Logger) *Service {
	if repo == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, log: log}
}

// RefreshCache re-reads every Mapping from the repo and rebuilds
// both internal caches. Mirrors the C# `RefreshCache` private at
// `:256-262`, but exposed publicly so the production wiring's
// SceneMappingsUpdated event handler can call it.
//
// Errors propagate; the caller decides whether to log and continue
// or fail the bus dispatch. On error, the existing cache is left
// untouched (the lookup methods continue to return the previous
// cache contents).
func (s *Service) RefreshCache(ctx context.Context) error {
	if s == nil {
		return errors.New("scenemappings: RefreshCache on nil receiver")
	}
	mappings, err := s.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("scenemappings: RefreshCache: %w", err)
	}
	byParse := make(map[string][]Mapping, len(mappings))
	byID := make(map[int][]Mapping, len(mappings))
	for _, m := range mappings {
		key := m.ParseTerm
		byParse[key] = append(byParse[key], m)
		byID[m.TvdbID] = append(byID[m.TvdbID], m)
	}
	s.mu.Lock()
	s.byParseTerm = byParse
	s.byTvdbID = byID
	s.populated = true
	s.mu.Unlock()
	return nil
}

// ensurePopulated lazy-populates the cache if RefreshCache has never
// been called. Mirrors C# `if (_getTvdbIdCache.Count == 0) RefreshCache();`
// at `:79,221`.
func (s *Service) ensurePopulated(ctx context.Context) error {
	s.mu.RLock()
	populated := s.populated
	s.mu.RUnlock()
	if populated {
		return nil
	}
	return s.RefreshCache(ctx)
}

// FindByTvdbID ports `FindByTvdbId` at C# `:77-92`. Returns every
// mapping registered for the supplied TVDB id (empty slice for
// unknown ids), in repo-insertion order.
//
// Errors propagate from the lazy cache populate.
func (s *Service) FindByTvdbID(ctx context.Context, tvdbID int) ([]Mapping, error) {
	if s == nil {
		return nil, errors.New("scenemappings: FindByTvdbID on nil receiver")
	}
	if err := s.ensurePopulated(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.byTvdbID[tvdbID]
	if len(out) == 0 {
		return []Mapping{}, nil
	}
	// Defensive copy so caller mutations don't leak into the cache.
	cp := make([]Mapping, len(out))
	copy(cp, out)
	return cp, nil
}

// FindSceneMapping ports `FindSceneMapping(seriesTitle, releaseTitle, sceneSeasonNumber)`
// at C# `:94-125`. Returns the single matching mapping for a
// (cleaned) series title + optional scene-season filter, or nil
// when no unique match resolves.
//
// Empty seriesTitle returns nil (mirrors C# `:96-99`).
//
// Behavior diverges from C# in two well-documented ways:
//
//   - The Levenshtein closest-match arm at C# `:240-253` is omitted.
//     Multi-candidate ambiguity returns nil + warn log instead of
//     picking the closest by edit distance. The C# `InvalidSceneMappingException`
//     (`:124`) is treated as "no mapping" + log.
//   - The FilterRegex sub-arm of `FilterSceneMappings(candidates,
//     releaseTitle)` at `:266-283` is omitted — every candidate is
//     treated as a "normalCandidate" (those without FilterRegex).
//     Mappings that DO carry a FilterRegex won't be filtered down by
//     the regex; the parsingservice consumer tolerates a nil result
//     gracefully.
//
// Errors propagate from the lazy cache populate.
func (s *Service) FindSceneMapping(ctx context.Context, seriesTitle, releaseTitle string, sceneSeasonNumber int) (*Mapping, error) {
	if s == nil {
		return nil, errors.New("scenemappings: FindSceneMapping on nil receiver")
	}
	if strings.TrimSpace(seriesTitle) == "" {
		return nil, nil
	}
	if err := s.ensurePopulated(ctx); err != nil {
		return nil, err
	}

	candidates := s.findMappings(seriesTitle, releaseTitle)
	if len(candidates) == 0 {
		return nil, nil
	}

	candidates = filterByScene(candidates, sceneSeasonNumber)
	distinct := distinctByTvdbID(candidates)
	switch len(distinct) {
	case 0:
		return nil, nil
	case 1:
		s.log.Debug("scenemappings: found scene mapping",
			"seriesTitle", seriesTitle, "tvdbId", distinct[0].TvdbID)
		out := distinct[0]
		return &out, nil
	default:
		// C# `:124` throws InvalidSceneMappingException. The Go port
		// treats ambiguity as "no mapping" + warn log; the
		// parsingservice consumer's nil-arm tolerates the result.
		s.log.Warn("scenemappings: ambiguous scene mapping; returning nil",
			"seriesTitle", seriesTitle, "releaseTitle", releaseTitle,
			"distinctCount", len(distinct))
		return nil, nil
	}
}

// GetSceneSeasonNumber ports `GetSceneSeasonNumber` at C# `:127-130`.
// Delegates to FindSceneMapping with sceneSeasonNumber=-1 and returns
// the matched SceneSeasonNumber (or nil if no match / no override).
//
// Errors propagate.
func (s *Service) GetSceneSeasonNumber(ctx context.Context, seriesTitle, releaseTitle string) (*int, error) {
	mapping, err := s.FindSceneMapping(ctx, seriesTitle, releaseTitle, -1)
	if err != nil {
		return nil, err
	}
	if mapping == nil {
		return nil, nil
	}
	return mapping.SceneSeasonNumber, nil
}

// FindTvdbID ports `FindTvdbId` at C# `:72-75`. Delegates to
// FindSceneMapping and returns the matched TvdbID as a *int (nil
// when no match).
func (s *Service) FindTvdbID(ctx context.Context, seriesTitle, releaseTitle string, sceneSeasonNumber int) (*int, error) {
	mapping, err := s.FindSceneMapping(ctx, seriesTitle, releaseTitle, sceneSeasonNumber)
	if err != nil {
		return nil, err
	}
	if mapping == nil {
		return nil, nil
	}
	v := mapping.TvdbID
	return &v, nil
}

// GetSceneNames ports `GetSceneNames(tvdbId, seasonNumbers, sceneSeasonNumbers)`
// at C# `:53-70`. Returns a deduplicated case-insensitive list of
// scene-named SearchTerm values for the supplied TVDB id whose
// season filters match.
//
// A mapping passes the filter if ANY of:
//
//   - mapping.SeasonNumber is in `seasonNumbers` (with nil treated as -1).
//   - mapping.SceneSeasonNumber is in `sceneSeasonNumbers` (nil → -1).
//   - mapping has neither SeasonNumber nor SceneSeasonNumber AND its
//     SceneOrigin is NOT "tvdb" (the catch-all "applies to every
//     season" entry, but only when not tvdb-numbered).
//
// Empty result for an unknown TVDB id; nil-safe inputs.
//
// Errors propagate.
func (s *Service) GetSceneNames(ctx context.Context, tvdbID int, seasonNumbers, sceneSeasonNumbers []int) ([]string, error) {
	mappings, err := s.FindByTvdbID(ctx, tvdbID)
	if err != nil {
		return nil, err
	}
	seasons := newIntSet(seasonNumbers)
	sceneSeasons := newIntSet(sceneSeasonNumbers)
	seen := make(map[string]string) // lowercased → original (preserves first-encountered casing)
	out := []string{}
	for _, m := range mappings {
		var ms, mss = -1, -1
		if m.SeasonNumber != nil {
			ms = *m.SeasonNumber
		}
		if m.SceneSeasonNumber != nil {
			mss = *m.SceneSeasonNumber
		}
		// C# `:62-64`:
		//   v.SeasonNumber ∈ seasons OR v.SceneSeasonNumber ∈ sceneSeasons
		//   OR (both are -1 AND SceneOrigin != "tvdb")
		ok := seasons.contains(ms) || sceneSeasons.contains(mss) ||
			(ms == -1 && mss == -1 && m.SceneOrigin != "tvdb")
		if !ok {
			continue
		}
		key := strings.ToLower(m.SearchTerm)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = m.SearchTerm
		out = append(out, m.SearchTerm)
	}
	// C# `Distinct(StringComparer.InvariantCultureIgnoreCase)` doesn't
	// stably sort — the order is preserved insertion order from the
	// LINQ pipeline. The Go port preserves insertion order via the
	// `out` append loop above; no sort needed.
	return out, nil
}

// findMappings ports the private `FindMappings` at C# `:219-254`,
// minus the FilterRegex sub-arm + Levenshtein closest-match. Returns
// candidates whose ParseTerm matches CleanSeriesTitle(seriesTitle),
// optionally narrowed to those whose Title equals seriesTitle exactly
// (when multiple distinct titles compete).
func (s *Service) findMappings(seriesTitle, releaseTitle string) []Mapping {
	cleaned := parser.CleanSeriesTitle(seriesTitle)
	s.mu.RLock()
	candidates := s.byParseTerm[cleaned]
	s.mu.RUnlock()
	if len(candidates) == 0 {
		return nil
	}
	// FilterRegex sub-arm deferred — see Service doc. Caller's
	// releaseTitle is unused at this slice; signature kept for parity
	// with the C# private method so a future slice can wire it without
	// changing the call sites.
	_ = releaseTitle

	if len(candidates) <= 1 {
		// Defensive copy.
		cp := make([]Mapping, len(candidates))
		copy(cp, candidates)
		return cp
	}

	// Multi-candidate: prefer exact-title matches, ordered by
	// SeasonNumber descending (mirror C# `:240-242`).
	var exact []Mapping
	for _, c := range candidates {
		if c.Title == seriesTitle {
			exact = append(exact, c)
		}
	}
	if len(exact) > 0 {
		sort.SliceStable(exact, func(i, j int) bool {
			ai := -1
			aj := -1
			if exact[i].SeasonNumber != nil {
				ai = *exact[i].SeasonNumber
			}
			if exact[j].SeasonNumber != nil {
				aj = *exact[j].SeasonNumber
			}
			return ai > aj
		})
		return exact
	}
	// Levenshtein closest-match deferred — see Service doc. Return
	// the full candidate list; the FindSceneMapping caller will
	// resolve via DistinctBy(TvdbId) and treat >1 as ambiguous.
	cp := make([]Mapping, len(candidates))
	copy(cp, candidates)
	return cp
}

// filterByScene ports the second `FilterSceneMappings(candidates,
// sceneSeasonNumber)` overload at C# `:286-309`.
//
// Splits candidates into "filtered" (those with both SceneSeasonNumber
// AND SeasonNumber non-null AND non-negative) and "normal". When
// sceneSeasonNumber == -1 (the special "no filter" sentinel) returns
// only normalCandidates. Otherwise returns the filteredCandidates
// (narrowed to SceneSeasonNumber <= sceneSeasonNumber, deduped per
// Title via SceneSeasonNumber-then-SeasonNumber DESC ordering) when
// any match, else falls back to normalCandidates.
func filterByScene(candidates []Mapping, sceneSeasonNumber int) []Mapping {
	var filtered, normal []Mapping
	for _, c := range candidates {
		mss := -1
		ms := -1
		if c.SceneSeasonNumber != nil {
			mss = *c.SceneSeasonNumber
		}
		if c.SeasonNumber != nil {
			ms = *c.SeasonNumber
		}
		if mss != -1 && ms != -1 {
			filtered = append(filtered, c)
		} else {
			normal = append(normal, c)
		}
	}
	if sceneSeasonNumber == -1 {
		return normal
	}
	if len(filtered) == 0 {
		return normal
	}
	// Narrow filtered to SceneSeasonNumber <= sceneSeasonNumber.
	matching := filtered[:0]
	for _, c := range filtered {
		if c.SceneSeasonNumber != nil && *c.SceneSeasonNumber <= sceneSeasonNumber {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		return normal
	}
	// Dedup by Title, keeping the entry with highest SceneSeasonNumber
	// then highest SeasonNumber. Mirrors C# `:299-303`
	// `GroupBy(v => v.Title).Select(g => g.OrderByDescending(...).First())`.
	byTitle := map[string]Mapping{}
	for _, c := range matching {
		existing, ok := byTitle[c.Title]
		if !ok || isHigherScenePrec(c, existing) {
			byTitle[c.Title] = c
		}
	}
	out := make([]Mapping, 0, len(byTitle))
	for _, m := range byTitle {
		out = append(out, m)
	}
	// Map iteration is randomised; sort by Title for stable test
	// output (the C# LINQ pipeline preserves whatever the underlying
	// dictionary's enumeration order is — varies by runtime version).
	sort.SliceStable(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// isHigherScenePrec returns true if `a` should beat `b` under the
// "DESCENDING SceneSeasonNumber, then DESCENDING SeasonNumber" tie
// breaker.
func isHigherScenePrec(a, b Mapping) bool {
	ass, bss := -1, -1
	if a.SceneSeasonNumber != nil {
		ass = *a.SceneSeasonNumber
	}
	if b.SceneSeasonNumber != nil {
		bss = *b.SceneSeasonNumber
	}
	if ass != bss {
		return ass > bss
	}
	asn, bsn := -1, -1
	if a.SeasonNumber != nil {
		asn = *a.SeasonNumber
	}
	if b.SeasonNumber != nil {
		bsn = *b.SeasonNumber
	}
	return asn > bsn
}

// distinctByTvdbID ports the `mappings.DistinctBy(v => v.TvdbId)`
// step at C# `:110`. Preserves first-encountered insertion order.
func distinctByTvdbID(in []Mapping) []Mapping {
	seen := make(map[int]struct{}, len(in))
	out := make([]Mapping, 0, len(in))
	for _, m := range in {
		if _, dup := seen[m.TvdbID]; dup {
			continue
		}
		seen[m.TvdbID] = struct{}{}
		out = append(out, m)
	}
	return out
}

// intSet is a small set helper used by GetSceneNames. Pulled out so
// the per-mapping `seasons.contains(ms)` reads cleanly in the loop.
type intSet map[int]struct{}

func newIntSet(in []int) intSet {
	s := make(intSet, len(in))
	for _, v := range in {
		s[v] = struct{}{}
	}
	return s
}

func (s intSet) contains(v int) bool {
	_, ok := s[v]
	return ok
}

// UpdateMappings ports `SceneMappingService.UpdateMappings()` at C#
// `SceneMappingService.cs:132-217`. Iterates over every wired
// [Provider], fetches its mapping list, filters invalid rows
// (empty Title OR SearchTerm), stamps ParseTerm + Type, diff-merges
// against the persisted set by MappingId, and persists the net
// changes (insert / update / delete). Finishes with a cache refresh
// + UpdatedEvent publish.
//
// Behaviour matches the C# slot-by-slot:
//
//  1. Per-provider try/catch wrapper (C# `:140-211`) — an error from
//     ONE provider doesn't fail the whole pass. Logged at Error and
//     the loop continues to the next provider.
//  2. Empty list from a provider → log Warn + skip (C# `:204-206`).
//     No deletes — the empty fetch could be transient.
//  3. Invalid-row filter (empty Title or empty SearchTerm) → log Warn
//     + skip the row (C# `:148-158`).
//  4. ParseTerm = CleanSeriesTitle(Title); Type = providerType — done
//     in-place on every surviving row.
//  5. Diff by MappingId. Empty MappingId rows are skipped + warned
//     (C# `:179-183`).
//  6. Persist: DeleteByIDs(missing) + UpdateMany(matched) + InsertMany(new).
//  7. Refresh cache. Publish UpdatedEvent regardless of which
//     providers succeeded (matches C# `:216`).
//
// Returns nil on success. Returns an error ONLY when:
//   - The Service itself is nil.
//   - The writer is not wired (host called UpdateMappings without
//     binding [Service.WithWriter]).
//   - The cache refresh fails after persistence (rare — would also
//     surface to consumers via stale-but-old cache).
//
// Per-provider HTTP/decode errors do NOT bubble — they're logged and
// the loop continues.
//
// ctx is propagated to every Provider call + every Repo call so a
// shutdown signal stops the refresh in-flight.
func (s *Service) UpdateMappings(ctx context.Context) error {
	if s == nil {
		return errors.New("scenemappings: UpdateMappings on nil receiver")
	}
	if s.writer == nil {
		s.log.Warn("scenemappings: UpdateMappings called without writer; skipping")
		return errors.New("scenemappings: UpdateMappings: writer not wired (call WithWriter)")
	}
	if len(s.providers) == 0 {
		s.log.Warn("scenemappings: UpdateMappings called without providers; skipping")
		// Not an error — production wiring may legitimately skip
		// providers during early-boot or in degraded modes.
		return nil
	}

	s.log.Info("scenemappings: updating scene mappings", "providers", len(s.providers))

	for _, prov := range s.providers {
		providerType := providerTypeName(prov)
		if err := s.refreshOneProvider(ctx, prov, providerType); err != nil {
			// Per C# `:208-211`: log at Error, continue to next provider.
			s.log.Error("scenemappings: provider refresh failed",
				"providerType", providerType, "err", err)
		}
	}

	if err := s.RefreshCache(ctx); err != nil {
		// C# does the same RefreshCache call at `:214` but doesn't
		// guard against errors there (it'd throw). The Go port surfaces
		// the error so the caller can log; the UpdatedEvent fires
		// regardless because a stale-but-loaded cache + the event tells
		// downstream consumers "something happened, re-check".
		s.log.Error("scenemappings: cache refresh after UpdateMappings failed", "err", err)
		if s.bus != nil {
			_ = messaging.Publish(ctx, s.bus, UpdatedEvent{})
		}
		return fmt.Errorf("scenemappings: UpdateMappings: cache refresh: %w", err)
	}

	if s.bus != nil {
		if err := messaging.Publish(ctx, s.bus, UpdatedEvent{}); err != nil {
			// Publish errors aren't fatal — they indicate a subscriber
			// failed, not that the refresh itself broke. Log + return nil.
			s.log.Warn("scenemappings: UpdatedEvent publish error", "err", err)
		}
	}
	return nil
}

// refreshOneProvider ports the per-provider loop body at C#
// `:140-202`. Pulled out so the per-provider try/catch shape
// (Go: error-return-and-continue) is readable in the outer loop.
//
// Mutates state via s.writer; reads existing rows via writer too.
// Does NOT update s.byParseTerm / s.byTvdbID directly — UpdateMappings
// calls RefreshCache after all providers complete (matches C#).
func (s *Service) refreshOneProvider(ctx context.Context, prov Provider, providerType string) error {
	mappings, err := prov.GetSceneMappings(ctx)
	if err != nil {
		return fmt.Errorf("provider %q: fetch: %w", providerType, err)
	}
	if len(mappings) == 0 {
		// C# `:204-206`: warn and skip. Do NOT delete existing rows for
		// this provider — the empty fetch may be transient.
		s.log.Warn("scenemappings: provider returned empty mappings; will not update",
			"providerType", providerType)
		return nil
	}

	// Filter + stamp pass. Mirrors C# `:148-164` — RemoveAll(invalid)
	// then per-row ParseTerm + Type assignment.
	valid := make([]Mapping, 0, len(mappings))
	for _, m := range mappings {
		if strings.TrimSpace(m.Title) == "" || strings.TrimSpace(m.SearchTerm) == "" {
			s.log.Warn("scenemappings: invalid scene mapping; skipping",
				"tvdbId", m.TvdbID, "providerType", providerType)
			continue
		}
		m.ParseTerm = parser.CleanSeriesTitle(m.Title)
		m.Type = providerType
		valid = append(valid, m)
	}

	// Read existing rows for this provider type. Index by MappingId.
	existing, err := s.writer.GetAllByType(ctx, providerType)
	if err != nil {
		return fmt.Errorf("provider %q: getAllByType: %w", providerType, err)
	}
	existingByMappingID := make(map[string]Mapping, len(existing))
	for _, e := range existing {
		key := e.MappingID
		if key == "" {
			// C# `:171`: `e.MappingId ?? $"{e.Id}"` — fall back to Id
			// when MappingId is empty so the row is still de-dupable.
			key = fmt.Sprintf("%d", e.ID)
		}
		existingByMappingID[key] = e
	}

	// Partition the fetched-valid rows into insert / update.
	toInsert := make([]Mapping, 0)
	toUpdate := make([]Mapping, 0)
	for _, m := range valid {
		if strings.TrimSpace(m.MappingID) == "" {
			// C# `:179-183`: rows without MappingId are skipped + warned.
			s.log.Warn("scenemappings: scene mapping with missing MappingId; skipping",
				"tvdbId", m.TvdbID, "title", m.Title)
			continue
		}
		if existingRow, found := existingByMappingID[m.MappingID]; found {
			m.ID = existingRow.ID
			toUpdate = append(toUpdate, m)
			delete(existingByMappingID, m.MappingID)
		} else {
			toInsert = append(toInsert, m)
		}
	}

	// Anything still in existingByMappingID after the loop wasn't in
	// the fresh fetch → delete.
	toDeleteIDs := make([]int, 0, len(existingByMappingID))
	for _, e := range existingByMappingID {
		toDeleteIDs = append(toDeleteIDs, e.ID)
	}

	// Persist in C# order: delete → update → insert. Matches `:199-201`.
	if err := s.writer.DeleteByIDs(ctx, toDeleteIDs); err != nil {
		return fmt.Errorf("provider %q: deleteByIDs: %w", providerType, err)
	}
	if err := s.writer.UpdateMany(ctx, toUpdate); err != nil {
		return fmt.Errorf("provider %q: updateMany: %w", providerType, err)
	}
	if err := s.writer.InsertMany(ctx, toInsert); err != nil {
		return fmt.Errorf("provider %q: insertMany: %w", providerType, err)
	}

	s.log.Info("scenemappings: provider refresh complete",
		"providerType", providerType,
		"inserted", len(toInsert),
		"updated", len(toUpdate),
		"deleted", len(toDeleteIDs))
	return nil
}

// providerTypeName mirrors the C# `sceneMappingProvider.GetType().Name`
// at `SceneMappingService.cs:146`. Returns the concrete Go type name
// without the package prefix. e.g. `*scenemappings.Proxy` → "Proxy".
func providerTypeName(p Provider) string {
	if p == nil {
		return ""
	}
	t := fmt.Sprintf("%T", p)
	// Strip leading '*' and any package prefix so the result matches
	// the C# `Type.Name` semantics. `*scenemappings.Proxy` → "Proxy".
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		return t[i+1:]
	}
	if strings.HasPrefix(t, "*") {
		return t[1:]
	}
	return t
}

// Compile-time interface-satisfaction guards. A refactor that breaks
// either contract fails at build time rather than at the wiring callsite.
var (
	_ MappingsReader = (*Repo)(nil)
	_ MappingsWriter = (*Repo)(nil)
)
