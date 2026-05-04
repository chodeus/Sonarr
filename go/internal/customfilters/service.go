package customfilters

import (
	"context"
	"errors"
	"fmt"
)

// Service ports `CustomFilters/CustomFilterService.cs:1-49`. Thin
// CRUD wrapper with no caching, no events, no validation. The C#
// version is identical: every method is a single passthrough.
//
// Why ship the wrapper at all when it adds nothing over the Repo:
//
//  1. Establishes the service-layer dependency point. Future callers
//     (HTTP handlers, command handlers) depend on `*Service`, not on
//     `*Repo`, so cross-cutting concerns (auditing, validation) can
//     be added later without touching every call site.
//
//  2. Matches the C# wiring topology — `ICustomFilterService` is
//     what gets DI-registered in the C# tree. Keeping the same
//     dependency graph makes Phase 4/5 wiring mechanical.
type Service struct {
	repo *Repo
}

// NewService binds a Repo into a CustomFilter Service.
func NewService(r *Repo) *Service {
	return &Service{repo: r}
}

// All ports `CustomFilterService.All()` at `:44-47`.
func (s *Service) All(ctx context.Context) ([]Filter, error) {
	return s.repo.All(ctx)
}

// Get ports `CustomFilterService.Get(id)` at `:39-42`.
func (s *Service) Get(ctx context.Context, id int) (*Filter, error) {
	return s.repo.Get(ctx, id)
}

// Add ports `CustomFilterService.Add(customFilter)` at `:24-27`.
func (s *Service) Add(ctx context.Context, filter *Filter) (*Filter, error) {
	if filter == nil {
		return nil, errors.New("customfilters: cannot add nil filter")
	}
	if _, err := s.repo.Insert(ctx, filter); err != nil {
		return nil, fmt.Errorf("customfilters: add: %w", err)
	}
	return filter, nil
}

// Update ports `CustomFilterService.Update(customFilter)` at `:29-32`.
func (s *Service) Update(ctx context.Context, filter *Filter) error {
	if filter == nil {
		return errors.New("customfilters: cannot update nil filter")
	}
	if err := s.repo.Update(ctx, filter); err != nil {
		return fmt.Errorf("customfilters: update: %w", err)
	}
	return nil
}

// Delete ports `CustomFilterService.Delete(id)` at `:34-37`.
func (s *Service) Delete(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("customfilters: delete: %w", err)
	}
	return nil
}
