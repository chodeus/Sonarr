package customfilters

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service ports `CustomFilters/CustomFilterService.cs:1-49`. Thin
// CRUD wrapper with no caching, no validation. C# emits no IEvent
// — its SignalR sync rides the inherited ModelEvent<CustomFilter>;
// the Go port publishes a Go-port additive
// [CustomFilterUpdatedEvent] from each CUD path so the SignalR
// `customfilter` channel can subscribe.
//
// Why ship the wrapper at all when it adds little over the Repo:
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
	bus  *messaging.Bus
}

// NewService binds a Repo and an optional *messaging.Bus into a
// CustomFilter Service. nil bus is supported — the Service skips
// event publishing (matches the established Phase 2/3 skip-publish
// convention across other services).
func NewService(r *Repo, bus *messaging.Bus) *Service {
	return &Service{repo: r, bus: bus}
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
// Publishes [CustomFilterUpdatedEvent] on success.
func (s *Service) Add(ctx context.Context, filter *Filter) (*Filter, error) {
	if filter == nil {
		return nil, errors.New("customfilters: cannot add nil filter")
	}
	if _, err := s.repo.Insert(ctx, filter); err != nil {
		return nil, fmt.Errorf("customfilters: add: %w", err)
	}
	s.publishUpdated(ctx)
	return filter, nil
}

// Update ports `CustomFilterService.Update(customFilter)` at `:29-32`.
// Publishes [CustomFilterUpdatedEvent] on success.
func (s *Service) Update(ctx context.Context, filter *Filter) error {
	if filter == nil {
		return errors.New("customfilters: cannot update nil filter")
	}
	if err := s.repo.Update(ctx, filter); err != nil {
		return fmt.Errorf("customfilters: update: %w", err)
	}
	s.publishUpdated(ctx)
	return nil
}

// Delete ports `CustomFilterService.Delete(id)` at `:34-37`.
// Publishes [CustomFilterUpdatedEvent] on success.
func (s *Service) Delete(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("customfilters: delete: %w", err)
	}
	s.publishUpdated(ctx)
	return nil
}

func (s *Service) publishUpdated(ctx context.Context) {
	if s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, CustomFilterUpdatedEvent{})
}
