package releaseprofiles

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service ports `ReleaseProfileService.cs:1-79`. CRUD passthroughs +
// four tag/indexer query helpers. No events, no caching — the C#
// service is uncached too (each query re-reads from the repo).
//
// What ships:
//
//   - All / Get / Add / Update / Delete — passthroughs.
//   - AllForTag(tagID) — filters to profiles whose Tags contains
//     tagID.
//   - AllExcludedForTag(tagID) — filters to profiles whose
//     ExcludedTags contains tagID.
//   - AllForTags(tagIDs) — composite filter (preserves a C#
//     operator-precedence quirk; see helper docs).
//   - EnabledForTags(tagIDs, indexerID) — AllForTags filtered to
//     Enabled profiles whose IndexerIDs match (or empty = all).
//
// What's deferred:
//
//   - DecisionEngine integration — the orchestrator that uses these
//     query helpers to evaluate releases. Lands when the
//     DecisionEngine arrives.
//   - The TermMatcher cache (shipped in 2.21 as `term_matcher.go`'s
//     TermMatcher type) is a separate concern; consumers that need
//     compiled-regex caching wire the matcher alongside this service.
//
// SignalR posture: C# emits no IEvent on CUD — its sync rides the
// inherited ModelEvent<ReleaseProfile>. The Go port publishes a
// Go-port additive [ReleaseProfileUpdatedEvent] from Add/Update/Delete
// so the SignalR `releaseprofile` channel can subscribe.
type Service struct {
	repo *Repo
	bus  *messaging.Bus
}

// NewService binds a Repo and an optional *messaging.Bus into a
// ReleaseProfile Service. nil bus is supported — the Service skips
// event publishing (matches the established Phase 2/3 skip-publish
// convention across other services).
func NewService(r *Repo, bus *messaging.Bus) *Service {
	return &Service{repo: r, bus: bus}
}

// All ports `ReleaseProfileService.All()` at `:30-35`.
func (s *Service) All(ctx context.Context) ([]Profile, error) {
	return s.repo.All(ctx)
}

// Get ports `ReleaseProfileService.Get(id)` at `:62-65`.
func (s *Service) Get(ctx context.Context, id int) (*Profile, error) {
	return s.repo.Get(ctx, id)
}

// Add ports `ReleaseProfileService.Add(restriction)` at `:72-75`.
func (s *Service) Add(ctx context.Context, profile *Profile) (*Profile, error) {
	if profile == nil {
		return nil, errors.New("releaseprofiles: cannot add nil profile")
	}
	if _, err := s.repo.Insert(ctx, profile); err != nil {
		return nil, fmt.Errorf("releaseprofiles: add: %w", err)
	}
	s.publishUpdated(ctx)
	return profile, nil
}

// Update ports `ReleaseProfileService.Update(restriction)` at `:77-79`.
func (s *Service) Update(ctx context.Context, profile *Profile) error {
	if profile == nil {
		return errors.New("releaseprofiles: cannot update nil profile")
	}
	if err := s.repo.Update(ctx, profile); err != nil {
		return fmt.Errorf("releaseprofiles: update: %w", err)
	}
	s.publishUpdated(ctx)
	return nil
}

// Delete ports `ReleaseProfileService.Delete(id)` at `:67-70`.
func (s *Service) Delete(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("releaseprofiles: delete: %w", err)
	}
	s.publishUpdated(ctx)
	return nil
}

func (s *Service) publishUpdated(ctx context.Context) {
	if s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, ReleaseProfileUpdatedEvent{})
}

// AllForTag ports `ReleaseProfileService.AllForTag(tagId)` at `:42-45`.
// Filters to profiles whose Tags contains tagID.
func (s *Service) AllForTag(ctx context.Context, tagID int) ([]Profile, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("releaseprofiles: allForTag: %w", err)
	}
	return filterByTag(all, tagID, includeIfTagPresent), nil
}

// AllExcludedForTag ports `ReleaseProfileService.AllExcludedForTag(tagId)`
// at `:37-40`. Filters to profiles whose ExcludedTags contains tagID.
func (s *Service) AllExcludedForTag(ctx context.Context, tagID int) ([]Profile, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("releaseprofiles: allExcludedForTag: %w", err)
	}
	return filterByTag(all, tagID, includeIfExcludedPresent), nil
}

// AllForTags ports `ReleaseProfileService.AllForTags(tagIds)` at
// `:47-50`. The C# expression has no explicit parentheses:
//
//	r.Tags.Intersect(tagIds).Any() || r.Tags.Empty() && !r.ExcludedTags.Intersect(tagIds).Any()
//
// C# operator precedence: `&&` binds tighter than `||`, so this parses
// as:
//
//	r.Tags.Intersect(tagIds).Any()
//	  || (r.Tags.Empty() && !r.ExcludedTags.Intersect(tagIds).Any())
//
// i.e., a profile whose Tags INTERSECT the input is included even if
// its ExcludedTags also intersect — the exclusion check is only
// applied to the empty-tags catch-all branch. The Go port preserves
// this verbatim (likely a latent C# bug, but porting it 1:1 keeps
// behaviour identical).
func (s *Service) AllForTags(ctx context.Context, tagIDs []int) ([]Profile, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("releaseprofiles: allForTags: %w", err)
	}
	tagSet := intSet(tagIDs)
	var out []Profile
	for _, p := range all {
		if intersectsAny(p.Tags, tagSet) {
			out = append(out, p)
			continue
		}
		if len(p.Tags) == 0 && !intersectsAny(p.ExcludedTags, tagSet) {
			out = append(out, p)
		}
	}
	return out, nil
}

// EnabledForTags ports `ReleaseProfileService.EnabledForTags(tagIds, indexerId)`
// at `:52-58`. Composes AllForTags with two further filters: Enabled
// must be true; IndexerIDs must contain indexerID OR be empty (empty
// = applies-to-all-indexers catch-all).
func (s *Service) EnabledForTags(ctx context.Context, tagIDs []int, indexerID int) ([]Profile, error) {
	tagged, err := s.AllForTags(ctx, tagIDs)
	if err != nil {
		return nil, fmt.Errorf("releaseprofiles: enabledForTags: %w", err)
	}
	var out []Profile
	for _, p := range tagged {
		if !p.Enabled {
			continue
		}
		if len(p.IndexerIDs) == 0 || containsInt(p.IndexerIDs, indexerID) {
			out = append(out, p)
		}
	}
	return out, nil
}

// includeIfTagPresent is the predicate for AllForTag.
func includeIfTagPresent(p Profile, tagID int) bool {
	return containsInt(p.Tags, tagID)
}

// includeIfExcludedPresent is the predicate for AllExcludedForTag.
func includeIfExcludedPresent(p Profile, tagID int) bool {
	return containsInt(p.ExcludedTags, tagID)
}

func filterByTag(all []Profile, tagID int, predicate func(Profile, int) bool) []Profile {
	var out []Profile
	for _, p := range all {
		if predicate(p, tagID) {
			out = append(out, p)
		}
	}
	return out
}

func containsInt(xs []int, target int) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

func intersectsAny(xs []int, set map[int]bool) bool {
	for _, x := range xs {
		if set[x] {
			return true
		}
	}
	return false
}

func intSet(xs []int) map[int]bool {
	out := make(map[int]bool, len(xs))
	for _, x := range xs {
		out[x] = true
	}
	return out
}
