package namingconfig

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Service ports `NamingConfigService.cs:1-44`. Wraps the singleton
// NamingConfig row with seed-on-empty Get + Save (Upsert).
//
// What ships:
//
//   - GetConfig(ctx) — returns the single row, inserting Default()
//     if the table is empty. Mirrors C# `:18-37` including the
//     double-checked-locking pattern around the seed insert.
//   - Save(ctx, config) — Insert if ID == 0, else Update. Mirrors
//     C# `_repository.Upsert(namingConfig)`.
//
// What's deferred: nothing — the C# class has only these two methods
// and no event publishing.
type Service struct {
	repo *Repo

	// seedMu protects the seed-on-empty path so concurrent first
	// callers don't race two default inserts. After the first row
	// exists, the GetConfig fast path bypasses the lock entirely.
	seedMu sync.Mutex
}

// NewService binds a Repo into a NamingConfig Service.
func NewService(r *Repo) *Service {
	return &Service{repo: r}
}

// GetConfig returns the singleton NamingConfig row, inserting the
// canonical Default() if the table is empty. Mirrors C# GetConfig at
// `:18-37` including the double-check-locked seed path.
//
// On a fresh DB the post-migration NamingConfig table is empty until
// the first GetConfig call. Sonarr's C# wiring calls this at app
// startup; the Go port preserves the same trigger semantics by
// having the host startup invoke Service.GetConfig once.
func (s *Service) GetConfig(ctx context.Context) (*Config, error) {
	if cfg, err := s.firstRow(ctx); err != nil {
		return nil, err
	} else if cfg != nil {
		return cfg, nil
	}

	// Empty: take the seed lock, re-check, then insert the default.
	s.seedMu.Lock()
	defer s.seedMu.Unlock()

	if cfg, err := s.firstRow(ctx); err != nil {
		return nil, err
	} else if cfg != nil {
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
	return cfg, nil
}

// Save persists `config` via Insert if its ID == 0, else Update.
// Mirrors the C# `_repository.Upsert(namingConfig)` semantic.
func (s *Service) Save(ctx context.Context, config *Config) error {
	if config == nil {
		return errors.New("namingconfig: cannot save nil config")
	}
	if config.ID == 0 {
		if _, err := s.repo.Insert(ctx, config); err != nil {
			return fmt.Errorf("namingconfig: save: insert: %w", err)
		}
		return nil
	}
	if err := s.repo.Update(ctx, config); err != nil {
		return fmt.Errorf("namingconfig: save: update: %w", err)
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
