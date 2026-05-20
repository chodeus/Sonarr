package delayprofiles

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service ports `DelayProfileService.cs:22-167`. Wraps the Repo with
// the BestForTags memo (mirrors C# `_bestForTagsCache`), the auto-Order
// assignment on Add, the reorder-on-Delete renumber, and the explicit
// Reorder helper. The pure helpers from logic.go (AllForTag /
// AllForTags / BestForTags / ComputeReorder) do the in-memory
// computation; this Service threads them through the Repo.
//
// What ships here vs deferred:
//
//   - Add(ctx, profile) — assigns Order = Count, inserts, clears cache.
//   - Update(ctx, profile) — passthrough + cache clear.
//   - Delete(ctx, id) — repo delete, then renumber survivors via
//     UpdateMany, then cache clear. Mirrors C# `:50-68`.
//   - Get(ctx, id) — passthrough.
//   - All(ctx) — passthrough (the C# version is a thin
//     `_repo.All().ToList()` and is NOT cached; the only cache in the
//     C# service is `_bestForTagsCache`).
//   - AllForTag(ctx, tagID) — load + filter via logic.AllForTag.
//   - AllForTags(ctx, tagIDs) — load + filter via logic.AllForTags.
//   - BestForTags(ctx, tagIDs) — memoised via the in-process
//     `bestForTagsCache`, keyed by sorted tagIDs. Cleared on every
//     mutation.
//   - Reorder(ctx, id, afterID) — load all, run logic.ComputeReorder,
//     persist via UpdateMany, return refreshed list.
//
// What's deferred:
//
//   - C# `DelayProfileService` does NOT emit any IEvent on
//     Add/Update/Delete/Reorder (no `_eventAggregator` field, no
//     `PublishEvent` calls). Its SignalR sync rides the inherited
//     ModelEvent<DelayProfile>. The Go port publishes a Go-port
//     additive [DelayProfileUpdatedEvent] from each CUD path so the
//     SignalR `delayprofile` channel can subscribe — see the
//     event's doc-comment.
//   - The 30-second TTL on `_bestForTagsCache` is omitted. The C# cache
//     uses TimeSpan.FromSeconds(30) as a fallback bound; in practice
//     the cache is always cleared by the next mutation. The Go port
//     relies on mutation-driven invalidation alone — every Add/Update/
//     Delete/Reorder clears the entire memo.
type Service struct {
	repo *Repo
	bus  *messaging.Bus

	mu         sync.RWMutex
	bestByTags map[string]Profile // keyed by sortedTagsKey
}

// NewService binds a Repo and an optional *messaging.Bus into a
// DelayProfile Service. nil bus is supported — the Service skips
// event publishing (matches the established Phase 2/3 skip-publish
// convention across other services).
func NewService(r *Repo, bus *messaging.Bus) *Service {
	return &Service{repo: r, bus: bus, bestByTags: make(map[string]Profile)}
}

// Add ports `DelayProfileService.Add(profile)` at `:33-41`. Assigns
// `profile.Order = Count()` so new rows land at the tail of the
// existing-profile sequence (the migration-seeded default profile
// retains its Order = INT_MAX, so new profiles fall before it),
// inserts, and clears the BestForTags memo. Returns the inserted
// Profile pointer with its repo-assigned ID.
func (s *Service) Add(ctx context.Context, profile *Profile) (*Profile, error) {
	if profile == nil {
		return nil, errors.New("delayprofiles: cannot add nil profile")
	}
	n, err := s.repo.Count(ctx)
	if err != nil {
		return nil, err
	}
	profile.Order = n
	if _, err := s.repo.Insert(ctx, profile); err != nil {
		return nil, fmt.Errorf("delayprofiles: add: %w", err)
	}
	s.invalidateBest()
	s.publishUpdated(ctx)
	return profile, nil
}

// Update ports `DelayProfileService.Update(profile)` at `:43-48`.
func (s *Service) Update(ctx context.Context, profile *Profile) error {
	if profile == nil {
		return errors.New("delayprofiles: cannot update nil profile")
	}
	if err := s.repo.Update(ctx, profile); err != nil {
		return fmt.Errorf("delayprofiles: update: %w", err)
	}
	s.invalidateBest()
	s.publishUpdated(ctx)
	return nil
}

// Delete ports `DelayProfileService.Delete(id)` at `:50-68`. Removes the
// row, then walks the remaining profiles in Order and renumbers them
// 1..N (skipping the default Id=1, which keeps its INT_MAX Order),
// persists the renumbered set, and clears the BestForTags memo.
func (s *Service) Delete(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delayprofiles: delete: %w", err)
	}

	all, err := s.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("delayprofiles: delete: reload: %w", err)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Order < all[j].Order })

	for i := range all {
		if all[i].ID == 1 {
			continue
		}
		all[i].Order = i + 1
		row := all[i]
		if err := s.repo.Update(ctx, &row); err != nil {
			return fmt.Errorf("delayprofiles: delete: renumber id=%d: %w", row.ID, err)
		}
	}

	s.invalidateBest()
	s.publishUpdated(ctx)
	return nil
}

// Get ports `DelayProfileService.Get(id)` at `:75-78`.
func (s *Service) Get(ctx context.Context, id int) (*Profile, error) {
	return s.repo.Get(ctx, id)
}

// All ports `DelayProfileService.All()` at `:70-73`. Note: the C#
// version is uncached; only BestForTags is cached.
func (s *Service) All(ctx context.Context) ([]Profile, error) {
	return s.repo.All(ctx)
}

// AllForTag ports `DelayProfileService.AllForTag(tagId)` at `:80-84`.
// Delegates to the pure helper after loading the full set.
func (s *Service) AllForTag(ctx context.Context, tagID int) ([]Profile, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("delayprofiles: allForTag: %w", err)
	}
	return AllForTag(all, tagID), nil
}

// AllForTags ports `DelayProfileService.AllForTags(tagIds)` at `:86-89`.
func (s *Service) AllForTags(ctx context.Context, tagIDs []int) ([]Profile, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("delayprofiles: allForTags: %w", err)
	}
	return AllForTags(all, tagIDs), nil
}

// BestForTags ports `DelayProfileService.BestForTags(tagIds)` at
// `:91-95`. Memoised via the in-process bestByTags map, keyed by
// the sorted tagIDs as a comma-joined string (matches the C# `key`
// shape of "-1,2,3"). Returns ErrNotFound when no candidate
// matches — the C# version's `.First()` would throw; the Go port
// returns the configured sentinel for callers to handle cleanly.
func (s *Service) BestForTags(ctx context.Context, tagIDs []int) (*Profile, error) {
	key := sortedTagsKey(tagIDs)

	s.mu.RLock()
	if hit, ok := s.bestByTags[key]; ok {
		s.mu.RUnlock()
		out := hit
		return &out, nil
	}
	s.mu.RUnlock()

	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("delayprofiles: bestForTags: %w", err)
	}
	best, ok := BestForTags(all, tagIDs)
	if !ok {
		return nil, ErrNotFound
	}

	s.mu.Lock()
	s.bestByTags[key] = best
	s.mu.Unlock()

	out := best
	return &out, nil
}

// Reorder ports `DelayProfileService.Reorder(id, afterId)` at
// `:104-151`. Loads all profiles, runs the pure ComputeReorder
// helper to compute new Order values, persists the changes, and
// returns the freshly-reloaded set ordered by Order.
//
// Pass `afterID = nil` to move `id` to the front (matches the C#
// `int? afterId = null` overload semantics).
//
// The C# version silently returns the unchanged list when `id`
// doesn't appear; the Go port preserves that behaviour (the C#
// `// TODO: This should throw` comment is documented for posterity).
func (s *Service) Reorder(ctx context.Context, id int, afterID *int) ([]Profile, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("delayprofiles: reorder: %w", err)
	}

	// ComputeReorder mutates the slice in place — capture pre-state
	// per ID so we only push UPDATEs for rows whose Order changed.
	pre := make(map[int]int, len(all))
	for _, p := range all {
		pre[p.ID] = p.Order
	}

	reordered := ComputeReorder(all, id, afterID)
	for i := range reordered {
		row := reordered[i]
		if pre[row.ID] == row.Order {
			continue
		}
		if err := s.repo.Update(ctx, &row); err != nil {
			return nil, fmt.Errorf("delayprofiles: reorder: update id=%d: %w", row.ID, err)
		}
	}

	s.invalidateBest()
	s.publishUpdated(ctx)
	return s.repo.All(ctx)
}

// publishUpdated emits [DelayProfileUpdatedEvent] best-effort.
// No-op when bus is nil (matches the established skip-publish
// pattern across other services).
func (s *Service) publishUpdated(ctx context.Context) {
	if s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, DelayProfileUpdatedEvent{})
}

// invalidateBest clears the BestForTags memo. Called after every
// mutation so the next BestForTags call recomputes from the repo.
func (s *Service) invalidateBest() {
	s.mu.Lock()
	s.bestByTags = make(map[string]Profile)
	s.mu.Unlock()
}

// sortedTagsKey builds the BestForTags cache key. Matches the C#
// `"-" + tagIds.Select(v => v.ToString()).Join(",")` shape, except
// the IDs are sorted first so callers passing the same set in
// different orders share a cache slot. (The C# version also gets
// stable order from HashSet<int>'s insertion-ordered enumeration
// when the same site populates it the same way; sorting is a small
// safety belt.)
func sortedTagsKey(tagIDs []int) string {
	if len(tagIDs) == 0 {
		return "-"
	}
	dup := make([]int, len(tagIDs))
	copy(dup, tagIDs)
	sort.Ints(dup)

	var sb strings.Builder
	sb.WriteByte('-')
	for i, v := range dup {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(v))
	}
	return sb.String()
}
