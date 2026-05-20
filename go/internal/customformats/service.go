package customformats

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"sonarr.tv/sonarr/internal/messaging"
)

// CustomFormatAddedEvent ports
// `CustomFormats/Events/CustomFormatAddedEvent.cs:1-12`. Carries
// the inserted Format so listeners (QualityProfile, etc.) can
// fan-in the new format to their score tables.
type CustomFormatAddedEvent struct {
	Format *Format
}

// CustomFormatDeletedEvent ports
// `CustomFormats/Events/CustomFormatDeletedEvent.cs:1-12`. Carries
// the deleted Format so listeners can prune stale references
// before the row disappears from the DB.
type CustomFormatDeletedEvent struct {
	Format *Format
}

// CustomFormatUpdatedEvent is a Go-port additive event fired by
// [Service.Update] after a successful repo update. C# Sonarr's
// `CustomFormatService.Update` at `:51-55` does NOT publish an
// equivalent event — the React UI's CustomFormat list is kept in
// sync via the inherited `RestControllerWithSignalR.Handle(
// ModelEvent<CustomFormat>)` path which fires implicitly from the
// generic Dapper Repository.Update.
//
// The Go port doesn't have a ModelEvent analogue, so the Service
// publishes its own typed event. Subscriber: the existing
// `customformat` channel Sync broadcaster in
// cmd/sonarr/signalr_wiring.go (third subscriber alongside the
// Added/Deleted pair already wired in slice 5.J20).
type CustomFormatUpdatedEvent struct {
	Format *Format
}

// Service ports `CustomFormatService.cs:1-95`. Wraps the Repo with
// a typed-Format cache, CRUD passthroughs, and lifecycle event
// publishing (Add / Delete — the C# Update path doesn't publish).
//
// Symmetric with `autotagging.Service`; the typed cache + invalidate
// pattern is identical.
type Service struct {
	repo *Repo
	bus  *messaging.Bus

	mu     sync.RWMutex
	cached map[int]*Format
}

// NewService binds a Repo + Bus into a CustomFormat Service. Pass
// nil for `bus` to skip event publishing.
func NewService(r *Repo, bus *messaging.Bus) *Service {
	return &Service{repo: r, bus: bus}
}

// All returns every typed CustomFormat. Cached.
func (s *Service) All(ctx context.Context) ([]*Format, error) {
	tags, err := s.allDictionary(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Format, 0, len(tags))
	for _, f := range tags {
		out = append(out, f)
	}
	return out, nil
}

// GetByID ports `CustomFormatService.GetById(int)` at `:46-49`.
// Returns ErrNotFound on missing.
func (s *Service) GetByID(ctx context.Context, id int) (*Format, error) {
	tags, err := s.allDictionary(ctx)
	if err != nil {
		return nil, err
	}
	f, ok := tags[id]
	if !ok {
		return nil, ErrNotFound
	}
	return f, nil
}

// Insert ports `CustomFormatService.Insert(CustomFormat)` at
// `:64-72`. Persists the row, decodes the typed Format, invalidates
// the cache, publishes CustomFormatAddedEvent.
func (s *Service) Insert(ctx context.Context, row *CustomFormat) (*Format, error) {
	if row == nil {
		return nil, errors.New("customformats: cannot insert nil row")
	}
	inserted, err := s.repo.Insert(ctx, row)
	if err != nil {
		return nil, fmt.Errorf("customformats: insert: %w", err)
	}
	f, err := LoadFromRow(inserted)
	if err != nil {
		return nil, err
	}
	s.invalidate()
	s.publishAdded(ctx, f)
	return f, nil
}

// Update ports `CustomFormatService.Update(CustomFormat)` at
// `:51-55`. Persists, invalidates cache, publishes
// [CustomFormatUpdatedEvent]. Go-port additive — C# doesn't fire a
// dedicated lifecycle event on Update; its SignalR `customformat`
// channel `updated` action comes from the implicit
// `ModelEvent<CustomFormat>` (Dapper repository). See
// CustomFormatUpdatedEvent doc-comment for the divergence note.
func (s *Service) Update(ctx context.Context, row *CustomFormat) error {
	if row == nil {
		return errors.New("customformats: cannot update nil row")
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return fmt.Errorf("customformats: update: %w", err)
	}
	s.invalidate()
	updated, err := LoadFromRow(row)
	if err != nil {
		// Update succeeded at the DB layer but the typed-Format
		// decode failed; surface the event with a nil Format so
		// subscribers can still trigger their Sync re-fetch.
		s.publishUpdated(ctx, nil)
		return fmt.Errorf("customformats: update decode: %w", err)
	}
	s.publishUpdated(ctx, updated)
	return nil
}

// Delete ports `CustomFormatService.Delete(int)` at `:74-82`.
// Loads the typed Format BEFORE removing the row (so the
// CustomFormatDeletedEvent payload is non-nil for listeners),
// publishes the event, then deletes.
//
// The C# version publishes the event BEFORE the repo delete so
// listeners can prune stale references while the row is still
// queryable. The Go port matches that ordering.
func (s *Service) Delete(ctx context.Context, id int) error {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("customformats: delete lookup %d: %w", id, err)
	}
	f, err := LoadFromRow(row)
	if err != nil {
		return fmt.Errorf("customformats: delete decode %d: %w", id, err)
	}
	s.publishDeleted(ctx, f)
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("customformats: delete: %w", err)
	}
	s.invalidate()
	return nil
}

func (s *Service) allDictionary(ctx context.Context) (map[int]*Format, error) {
	s.mu.RLock()
	if s.cached != nil {
		out := s.cached
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()

	formats, err := LoadAll(ctx, s.repo)
	if err != nil {
		return nil, err
	}
	dict := make(map[int]*Format, len(formats))
	for _, f := range formats {
		dict[f.ID] = f
	}

	s.mu.Lock()
	if s.cached == nil {
		s.cached = dict
	}
	out := s.cached
	s.mu.Unlock()
	return out, nil
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

func (s *Service) publishAdded(ctx context.Context, f *Format) {
	if s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, CustomFormatAddedEvent{Format: f})
}

func (s *Service) publishDeleted(ctx context.Context, f *Format) {
	if s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, CustomFormatDeletedEvent{Format: f})
}

func (s *Service) publishUpdated(ctx context.Context, f *Format) {
	if s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, CustomFormatUpdatedEvent{Format: f})
}
