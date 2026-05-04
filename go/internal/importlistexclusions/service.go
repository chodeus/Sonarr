package importlistexclusions

import (
	"context"
	"errors"
	"fmt"
)

// Service ports `ImportLists/Exclusions/ImportListExclusionService.cs:1-98`
// excluding the SeriesDeletedEvent handler (deferred until Series ships)
// and the Paged() shim (deferred until PagingSpec lands across the
// codebase).
//
// Surface:
//
//   - All / Get / Add / Update / Delete — straight passthroughs.
//   - DeleteMany(ids) — sequential repo.Delete loop. The C# version
//     uses Marr.Data DeleteMany; the Go port iterates because the
//     generic Repository[T] helper doesn't expose a bulk delete yet.
//   - FindByTvdbID(tvdbID) — passthrough to repo.GetByTvdbID. The
//     C# `FindByTvdbId` returns null on missing; the Go port returns
//     ErrNotFound for callers that want explicit handling, plus a
//     thin nil-on-missing helper for the SeriesDeletedEvent shape
//     once that lands.
//
// Deferred:
//
//   - HandleAsync(SeriesDeletedEvent) — the auto-add-exclusion-on-
//     delete behaviour. Lands when Series ships and SeriesDeletedEvent
//     exists.
//   - Paged(PagingSpec) — lands with PagingSpec.
type Service struct {
	repo *Repo
}

// NewService binds a Repo into an ImportListExclusion Service.
func NewService(r *Repo) *Service {
	return &Service{repo: r}
}

// All ports `ImportListExclusionService.All()` at `:60-63`.
func (s *Service) All(ctx context.Context) ([]Exclusion, error) {
	return s.repo.All(ctx)
}

// Get ports `ImportListExclusionService.Get(id)` at `:50-53`.
func (s *Service) Get(ctx context.Context, id int) (*Exclusion, error) {
	return s.repo.Get(ctx, id)
}

// Add ports `ImportListExclusionService.Add(...)` at `:30-33`.
func (s *Service) Add(ctx context.Context, exclusion *Exclusion) (*Exclusion, error) {
	if exclusion == nil {
		return nil, errors.New("importlistexclusions: cannot add nil exclusion")
	}
	if _, err := s.repo.Insert(ctx, exclusion); err != nil {
		return nil, fmt.Errorf("importlistexclusions: add: %w", err)
	}
	return exclusion, nil
}

// Update ports `ImportListExclusionService.Update(...)` at `:35-38`.
func (s *Service) Update(ctx context.Context, exclusion *Exclusion) error {
	if exclusion == nil {
		return errors.New("importlistexclusions: cannot update nil exclusion")
	}
	if err := s.repo.Update(ctx, exclusion); err != nil {
		return fmt.Errorf("importlistexclusions: update: %w", err)
	}
	return nil
}

// Delete ports `ImportListExclusionService.Delete(int)` at `:40-43`.
func (s *Service) Delete(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("importlistexclusions: delete(%d): %w", id, err)
	}
	return nil
}

// DeleteMany ports `ImportListExclusionService.Delete(List<int>)` at
// `:45-48`. Iterates Repo.Delete; ErrNotFound on any individual ID
// is tolerated (the C# Marr.Data DeleteMany silently skips missing
// rows, so the Go port matches that semantic).
func (s *Service) DeleteMany(ctx context.Context, ids []int) error {
	for _, id := range ids {
		if err := s.repo.Delete(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("importlistexclusions: deleteMany(%d): %w", id, err)
		}
	}
	return nil
}

// FindByTvdbID ports `ImportListExclusionService.FindByTvdbId(tvdbId)`
// at `:55-58`. Returns ErrNotFound when no row matches that TvdbId.
func (s *Service) FindByTvdbID(ctx context.Context, tvdbID int) (*Exclusion, error) {
	return s.repo.GetByTvdbID(ctx, tvdbID)
}
