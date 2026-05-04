package qualitydefinitions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/quality"
)

// Decorated is one persisted Definition row decorated with its
// runtime-only fields from `quality.DefaultDefinitions`. The
// persistence shape is just (Id, Quality, Title); Weight and
// GroupName are looked up from the default-definitions table at
// read time. Mirrors the C# `QualityDefinition` post-decoration
// shape that `WithWeight` produces.
type Decorated struct {
	ID        int
	Quality   quality.Quality
	Title     string
	Weight    int
	GroupName string
}

// Service ports `QualityDefinitionService.cs:1-133`. Wraps the Repo
// with a TTL cache + decoration + EnsureDefaults seeding.
//
// Surface (mirrors C# verbatim):
//
//   - All(ctx) — cached typed-Decorated list, ordered by Weight ASC.
//     5-second TTL matches the C# `_cache.Get(... TimeSpan.FromSeconds(5))`.
//   - GetByID(ctx, id) — single Decorated by row Id.
//   - Get(ctx, q) — single Decorated by Quality.
//   - Update(ctx, row) — persists Title, clears cache.
//
// Application-startup seeding:
//
//   - EnsureDefaults(ctx) — ports the C# `Handle(ApplicationStartedEvent)
//     → InsertMissingDefinitions` flow. Inserts any Quality from the
//     default table that's missing a row, deletes any persisted row
//     whose Quality is no longer in the default table (matches C#
//     `_repo.DeleteMany(existingDefinitions)` where remaining-after-
//     pair-up == orphans). Idempotent; safe to invoke on every host
//     start.
//
// Reset (port of `IExecute<ResetQualityDefinitionsCommand>`):
//
//   - ResetTitles(ctx, resetTitles) — when resetTitles==true, restores
//     every persisted row's Title to the default. When false, leaves
//     Title alone (C# preserves user-edited Titles in this branch
//     even though it still issues the UpdateMany — a no-op write).
//     The Go port elides the no-op write when resetTitles==false.
//
// Concurrency: one mutex protects the cache. Update / EnsureDefaults
// / ResetTitles all clear the cache atomically before returning.
type Service struct {
	repo *Repo

	mu        sync.RWMutex
	cached    []Decorated // nil → cold; populated → live until expiresAt
	expiresAt time.Time

	// ttl is the cache duration; mirrors the C# 5-second TimeSpan.
	// Configurable via NewServiceWithTTL for tests that want to skip
	// the wait.
	ttl time.Duration
}

// NewService binds a Repo and uses the C# 5-second cache TTL.
func NewService(r *Repo) *Service {
	return &Service{repo: r, ttl: 5 * time.Second}
}

// NewServiceWithTTL binds a Repo with a custom cache TTL — tests pass
// 0 to disable caching entirely.
func NewServiceWithTTL(r *Repo, ttl time.Duration) *Service {
	return &Service{repo: r, ttl: ttl}
}

// All returns every Decorated definition, ordered by Weight ASC.
// Mirrors `QualityDefinitionService.All()` at `:53-56` (which sorts
// by Weight). Cached for the configured TTL; mutations clear the
// cache.
func (s *Service) All(ctx context.Context) ([]Decorated, error) {
	if cached := s.fastPathCached(); cached != nil {
		out := make([]Decorated, len(cached))
		copy(out, cached)
		return out, nil
	}
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("qualitydefinitions: all: %w", err)
	}
	dec := make([]Decorated, 0, len(rows))
	for _, row := range rows {
		dec = append(dec, decorate(row))
	}
	sort.SliceStable(dec, func(i, j int) bool { return dec[i].Weight < dec[j].Weight })

	s.mu.Lock()
	s.cached = dec
	s.expiresAt = time.Now().Add(s.ttl)
	s.mu.Unlock()

	out := make([]Decorated, len(dec))
	copy(out, dec)
	return out, nil
}

// GetByID ports `QualityDefinitionService.GetById(id)` at `:58-61`.
// The C# version uses `.Single` which throws on missing; the Go port
// returns ErrNotFound for clean handling.
func (s *Service) GetByID(ctx context.Context, id int) (*Decorated, error) {
	all, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			d := all[i]
			return &d, nil
		}
	}
	return nil, ErrNotFound
}

// Get ports `QualityDefinitionService.Get(quality)` at `:63-66`.
// Returns ErrNotFound when no row exists for the given Quality.
func (s *Service) Get(ctx context.Context, q quality.Quality) (*Decorated, error) {
	all, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Quality.ID == q.ID {
			d := all[i]
			return &d, nil
		}
	}
	return nil, ErrNotFound
}

// Update ports `QualityDefinitionService.Update(qualityDefinition)`
// at `:40-45`. Persists the underlying row (only Title is
// configurable) and invalidates the cache.
func (s *Service) Update(ctx context.Context, def *Definition) error {
	if def == nil {
		return errors.New("qualitydefinitions: cannot update nil definition")
	}
	if err := s.repo.Update(ctx, def); err != nil {
		return fmt.Errorf("qualitydefinitions: update: %w", err)
	}
	s.invalidate()
	return nil
}

// EnsureDefaults ports the seed half of
// `QualityDefinitionService.InsertMissingDefinitions()` at `:68-96`
// plus the orphan-cleanup half. Inserts any Quality from
// `quality.DefaultDefinitions` that's missing a persisted row;
// deletes any persisted row whose Quality is no longer in the
// default table. Idempotent.
//
// The C# call site is `Handle(ApplicationStartedEvent)`; the Go host
// invokes EnsureDefaults at startup to mirror that semantic.
func (s *Service) EnsureDefaults(ctx context.Context) error {
	existing, err := s.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("qualitydefinitions: ensureDefaults: load: %w", err)
	}
	byQuality := make(map[int]Definition, len(existing))
	for _, row := range existing {
		byQuality[row.Quality] = row
	}

	defaults := quality.DefaultDefinitions()
	for _, d := range defaults {
		if _, ok := byQuality[d.Quality.ID]; ok {
			delete(byQuality, d.Quality.ID) // mark as paired
			continue
		}
		row := &Definition{Quality: d.Quality.ID, Title: d.Title}
		if _, err := s.repo.Insert(ctx, row); err != nil {
			return fmt.Errorf("qualitydefinitions: ensureDefaults: insert quality=%d: %w", d.Quality.ID, err)
		}
	}

	// Anything left in byQuality is an orphan: a persisted row whose
	// Quality is no longer in the default table.
	for _, orphan := range byQuality {
		if err := s.repo.Delete(ctx, orphan.ID); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("qualitydefinitions: ensureDefaults: delete orphan id=%d: %w", orphan.ID, err)
		}
	}

	s.invalidate()
	return nil
}

// ResetTitles ports `Execute(ResetQualityDefinitionsCommand)` at
// `:112-131`. When resetTitles==true, restores every persisted row's
// Title to its default-definition value. When false, the C# version
// still issues UpdateMany with the existing Titles (effectively a
// no-op write); the Go port elides that case.
func (s *Service) ResetTitles(ctx context.Context, resetTitles bool) error {
	if !resetTitles {
		return nil
	}
	existing, err := s.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("qualitydefinitions: resetTitles: load: %w", err)
	}
	for _, row := range existing {
		def, ok := quality.DefaultDefinitionFor(quality.Quality{ID: row.Quality})
		if !ok {
			continue
		}
		if row.Title == def.Title {
			continue
		}
		row.Title = def.Title
		updated := row
		if err := s.repo.Update(ctx, &updated); err != nil {
			return fmt.Errorf("qualitydefinitions: resetTitles: update id=%d: %w", row.ID, err)
		}
	}
	s.invalidate()
	return nil
}

// fastPathCached returns the live cached slice if present and not
// expired, else nil. Caller must copy before returning the slice to
// callers — the cache is shared.
func (s *Service) fastPathCached() []Decorated {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cached == nil || time.Now().After(s.expiresAt) {
		return nil
	}
	return s.cached
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.expiresAt = time.Time{}
	s.mu.Unlock()
}

// decorate looks up the persisted row's Quality in the default table
// to attach Weight + GroupName. Falls back to (0, "") when the
// persisted Quality isn't in the default table — this can only happen
// for rows whose default was retired (e.g. id 11 HDTV480p, retired
// pre-port). EnsureDefaults deletes those, but a stale row that
// hasn't been seen by EnsureDefaults yet still surfaces here.
func decorate(row Definition) Decorated {
	out := Decorated{
		ID:    row.ID,
		Title: row.Title,
	}
	q, ok := quality.FindByID(row.Quality)
	if ok {
		out.Quality = q
	} else {
		out.Quality = quality.Quality{ID: row.Quality, Name: "Unknown"}
	}
	if def, ok := quality.DefaultDefinitionFor(out.Quality); ok {
		out.Weight = def.Weight
		out.GroupName = def.GroupName
	}
	return out
}
