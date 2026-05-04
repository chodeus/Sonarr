package autotagging

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service ports `AutoTaggingService.cs:1-138`. Wraps the Repo with
// a typed-Tag cache, CRUD passthroughs, AutoTagsUpdatedEvent
// publishing, and the AllForTag query helper.
//
// What ships:
//
//   - All(ctx) []*Tag (cached; clears on mutation).
//   - GetByID(ctx, id) — cached lookup.
//   - Insert(ctx, row) — persists, decodes, caches, publishes.
//   - Update(ctx, row) — persists the storage row, refreshes cache,
//     publishes.
//   - Delete(ctx, id) — repo delete, clears cache, publishes.
//   - AllForTag(ctx, tagID) — filters typed Tags whose `Tags`
//     contains tagID.
//
// What's deferred:
//
//   - GetTagChanges(series): prefer the pure `ChangesFor(series,
//     autoTags, rootFolders)` from changes.go (shipped in 2.25)
//     wired by callers that hold the typed Tag list. The Service
//     can be augmented with a `ChangesFor` shim once
//     RootFolderService lands; until then callers pass the
//     rootFolders slice they already have.
type Service struct {
	repo *Repo
	bus  *messaging.Bus

	mu     sync.RWMutex
	cached map[int]*Tag // nil → cache cold; non-nil → cache populated
}

// NewService binds a Repo + Bus into an AutoTagging Service. Pass
// nil for `bus` to skip event publishing.
func NewService(r *Repo, bus *messaging.Bus) *Service {
	return &Service{repo: r, bus: bus}
}

// All returns every typed AutoTag, in arbitrary order. Mirrors
// `AutoTaggingService.All()` at `:50-53`. Cached; cache is cleared
// on every mutation.
func (s *Service) All(ctx context.Context) ([]*Tag, error) {
	tags, err := s.allDictionary(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, t)
	}
	return out, nil
}

// GetByID ports `AutoTaggingService.GetById(int)` at `:55-58`. The
// C# version uses `AllDictionary()[id]` which throws on missing;
// the Go port returns ErrNotFound to surface the condition without
// a panic.
func (s *Service) GetByID(ctx context.Context, id int) (*Tag, error) {
	tags, err := s.allDictionary(ctx)
	if err != nil {
		return nil, err
	}
	t, ok := tags[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

// Insert ports `AutoTaggingService.Insert(AutoTag)` at `:69-77`.
// Persists the storage row + the typed cache via LoadFromRow,
// publishes AutoTagsUpdatedEvent. Returns the typed Tag (with the
// repo-assigned ID).
func (s *Service) Insert(ctx context.Context, row *AutoTag) (*Tag, error) {
	if row == nil {
		return nil, errors.New("autotagging: cannot insert nil row")
	}
	inserted, err := s.repo.Insert(ctx, row)
	if err != nil {
		return nil, fmt.Errorf("autotagging: insert: %w", err)
	}
	tag, err := LoadFromRow(inserted)
	if err != nil {
		return nil, err
	}
	s.invalidate()
	s.publishUpdated(ctx)
	return tag, nil
}

// Update ports `AutoTaggingService.Update(AutoTag)` at `:60-67`.
// Persists, invalidates the cache, publishes the event.
func (s *Service) Update(ctx context.Context, row *AutoTag) error {
	if row == nil {
		return errors.New("autotagging: cannot update nil row")
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return fmt.Errorf("autotagging: update: %w", err)
	}
	s.invalidate()
	s.publishUpdated(ctx)
	return nil
}

// Delete ports `AutoTaggingService.Delete(int)` at `:79-86`.
func (s *Service) Delete(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("autotagging: delete: %w", err)
	}
	s.invalidate()
	s.publishUpdated(ctx)
	return nil
}

// AllForTag ports `AutoTaggingService.AllForTag(int)` at `:88-92`.
// Returns the subset of cached AutoTags whose `Tags` slice contains
// `tagID`.
func (s *Service) AllForTag(ctx context.Context, tagID int) ([]*Tag, error) {
	tags, err := s.allDictionary(ctx)
	if err != nil {
		return nil, err
	}
	var out []*Tag
	for _, t := range tags {
		for _, tg := range t.Tags {
			if tg == tagID {
				out = append(out, t)
				break
			}
		}
	}
	return out, nil
}

// allDictionary mirrors the C# `AllDictionary()` cache helper at
// `:43-46`. Returns the cached `id → *Tag` map, populating it on
// first access. Concurrency-safe via RWMutex; concurrent first-load
// callers may briefly each compute the dictionary, with the
// loser's result discarded.
func (s *Service) allDictionary(ctx context.Context) (map[int]*Tag, error) {
	s.mu.RLock()
	if s.cached != nil {
		out := s.cached
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()

	tags, err := LoadAll(ctx, s.repo)
	if err != nil {
		return nil, err
	}
	dict := make(map[int]*Tag, len(tags))
	for _, t := range tags {
		dict[t.ID] = t
	}

	s.mu.Lock()
	if s.cached == nil {
		s.cached = dict
	}
	out := s.cached
	s.mu.Unlock()
	return out, nil
}

// invalidate clears the typed-Tag cache. Called after every
// mutation so the next read repopulates from the repo.
func (s *Service) invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

func (s *Service) publishUpdated(ctx context.Context) {
	if s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, AutoTagsUpdatedEvent{})
}
