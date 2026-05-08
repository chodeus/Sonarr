package namingconfig

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service ports `NamingConfigService.cs:1-44`. Wraps the singleton
// NamingConfig row with seed-on-empty Get + Save (Upsert) plus a
// process-local cache invalidated on every Save.
//
// What ships:
//
//   - GetConfig(ctx) — returns the single row, inserting Default()
//     if the table is empty. Mirrors C# `:18-37` including the
//     double-checked-locking pattern around the seed insert. Wraps
//     a process-local pointer cache populated on first read +
//     invalidated on Save / NamingConfigSavedEvent.
//   - Save(ctx, config) — Insert if ID == 0, else Update. Mirrors
//     C# `_repository.Upsert(namingConfig)`. Invalidates the cache
//     and publishes [NamingConfigSavedEvent] so subscribers (the
//     Service itself via Subscribe; future external consumers) drop
//     stale state.
//   - Subscribe(bus) — registers a sync handler that invalidates
//     the cache on every NamingConfigSavedEvent. Mirrors the
//     [config.Provider] posture — call once at boot AFTER NewService.
//
// Cache strategy: lazy populate, atomic invalidate. The cached row
// is stored as a copy and getters return a fresh copy so callers
// can't mutate the cache — matches the defensive pattern at
// `internal/config/provider.go:50-53` ("setters update the cache
// in-place... avoids a round-trip back to the DB on every Set"
// adapted to a single-row table where invalidate-and-lazy-load is
// cheaper than partial updates).
//
// # Slice 3.A2 deviation from C#
//
// The C# class has only GetConfig + Save and NO event publishing —
// the IConfigService cache it competes with at the IoC graph IS
// keyed by Setting key (not by row), and namingconfig.Save in C#
// just calls _repository.Upsert directly. The Go port adds the
// event for parity with the cache-invalidation pattern that
// config.Provider already established at slice 2.x; the event is
// independent of the C# tree and can be retired once the cache
// strategy stabilises.
type Service struct {
	repo *Repo
	bus  *messaging.Bus

	// seedMu protects the seed-on-empty path so concurrent first
	// callers don't race two default inserts. After the first row
	// exists, the GetConfig fast path bypasses the lock entirely.
	seedMu sync.Mutex

	// cacheMu protects the cached pointer. RWMutex because
	// GetConfig (read) is the hot path and runs at every
	// import-decision spec / writer-path invocation.
	cacheMu sync.RWMutex
	cached  *Config
}

// NewService binds a Repo + optional Bus into a Service. bus may be
// nil for tests that don't care about NamingConfigSavedEvent emission;
// production wiring at `cmd/sonarr/main.go` always passes the
// process-wide messaging.Bus so the cache-invalidation Subscribe
// path can fire.
//
// NewService does NOT subscribe — call [Service.Subscribe] after
// construction to wire the cache-invalidation handler. Splitting
// construction from subscription matches the [config.NewProvider]
// posture and lets the host wire-up code log the boot order.
func NewService(r *Repo, bus *messaging.Bus) *Service {
	return &Service{repo: r, bus: bus}
}

// Subscribe registers a sync handler that invalidates the cache on
// every NamingConfigSavedEvent. Pass the same Bus that was given to
// NewService — nil bus is a no-op so the wiring is forgiving.
//
// Safe to call multiple times (each call adds another subscriber);
// production wiring calls it exactly once at boot.
func (s *Service) Subscribe(bus *messaging.Bus) {
	if bus == nil {
		return
	}
	messaging.Subscribe(bus, "namingconfig.Service.invalidate",
		func(_ context.Context, _ NamingConfigSavedEvent) error {
			s.invalidate()
			return nil
		})
}

// invalidate drops the cached pointer. Next GetConfig call repopulates
// from the repo. Lock-protected so a concurrent reader doesn't see a
// torn nil-write.
func (s *Service) invalidate() {
	s.cacheMu.Lock()
	s.cached = nil
	s.cacheMu.Unlock()
}

// GetConfig returns the singleton NamingConfig row, inserting the
// canonical Default() if the table is empty. Mirrors C# GetConfig at
// `:18-37` including the double-check-locked seed path.
//
// On a fresh DB the post-migration NamingConfig table is empty until
// the first GetConfig call. Sonarr's C# wiring calls this at app
// startup; the Go port preserves the same trigger semantics by
// having the host startup invoke Service.GetConfig once.
//
// # Cache fast path
//
// First GetConfig populates the cache; subsequent calls return a
// copy of the cached row without touching the DB. Save invalidates
// the cache so the next read picks up the persisted change. The
// returned pointer is a fresh copy — callers can mutate it without
// affecting the cache or other concurrent readers.
func (s *Service) GetConfig(ctx context.Context) (*Config, error) {
	if cached := s.cachedRow(); cached != nil {
		return cached, nil
	}

	if cfg, err := s.firstRow(ctx); err != nil {
		return nil, err
	} else if cfg != nil {
		s.cacheRow(cfg)
		return cfg, nil
	}

	// Empty: take the seed lock, re-check, then insert the default.
	s.seedMu.Lock()
	defer s.seedMu.Unlock()

	if cfg, err := s.firstRow(ctx); err != nil {
		return nil, err
	} else if cfg != nil {
		s.cacheRow(cfg)
		return cfg, nil
	}

	def := Default()
	if _, err := s.repo.Insert(ctx, def); err != nil {
		return nil, fmt.Errorf("namingconfig: seed default: %w", err)
	}

	cfg, err := s.firstRow(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, errors.New("namingconfig: insert succeeded but no row visible")
	}
	s.cacheRow(cfg)
	return cfg, nil
}

// Save persists `config` via Insert if its ID == 0, else Update.
// Mirrors the C# `_repository.Upsert(namingConfig)` semantic.
//
// On success: invalidates the cache + publishes
// [NamingConfigSavedEvent] (when bus is non-nil). The
// invalidate-before-publish ordering matches [config.Provider.setString]
// at `internal/config/provider.go:178-187` — local invalidate ensures
// the publishing goroutine's next read returns fresh state even if
// the bus subscriber hasn't run yet.
func (s *Service) Save(ctx context.Context, config *Config) error {
	if config == nil {
		return errors.New("namingconfig: cannot save nil config")
	}
	if config.ID == 0 {
		if _, err := s.repo.Insert(ctx, config); err != nil {
			return fmt.Errorf("namingconfig: save: insert: %w", err)
		}
	} else {
		if err := s.repo.Update(ctx, config); err != nil {
			return fmt.Errorf("namingconfig: save: update: %w", err)
		}
	}
	s.invalidate()
	if s.bus != nil {
		if err := messaging.Publish(ctx, s.bus, NamingConfigSavedEvent{}); err != nil &&
			!errors.Is(err, messaging.ErrShutdown) {
			return fmt.Errorf("namingconfig: publish NamingConfigSavedEvent: %w", err)
		}
	}
	return nil
}

// firstRow returns the first row from the table, or (nil, nil) if
// empty. Mirrors C# `_repository.SingleOrDefault()` semantic — the
// table is a singleton so any row is THE row, but a misconfigured
// install with multiple rows still returns deterministic order.
func (s *Service) firstRow(ctx context.Context) (*Config, error) {
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("namingconfig: firstRow: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	first := rows[0]
	return &first, nil
}

// cachedRow returns a fresh copy of the cached row, or nil when the
// cache is empty. Defensive copy keeps the cache immutable from
// caller mutations.
func (s *Service) cachedRow() *Config {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.cached == nil {
		return nil
	}
	out := *s.cached
	return &out
}

// cacheRow stores a copy of cfg under the cache lock. Defensive copy
// so a caller mutating the returned pointer can't reach back into the
// cache.
func (s *Service) cacheRow(cfg *Config) {
	if cfg == nil {
		return
	}
	stored := *cfg
	s.cacheMu.Lock()
	s.cached = &stored
	s.cacheMu.Unlock()
}
