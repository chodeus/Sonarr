package scenemappings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"sonarr.tv/sonarr/internal/parser"
)

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
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
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

// Compile-time interface-satisfaction guard. A refactor that breaks
// the contract (Repo loses All, signature changes) fails at build
// time rather than at the wiring callsite.
var _ MappingsReader = (*Repo)(nil)
