package delayprofiles

import "sort"

// Pure-logic helpers ported from
// `src/NzbDrone.Core/Profiles/Delay/DelayProfileService.cs`. The
// service-layer wrapper itself (with its repo + cache + event
// publishing) lands when DecisionEngine arrives — the helpers here
// are the parts that operate on a `[]Profile` slice with no
// dependencies, which are also the parts the caller-side tests and
// future service-layer code need to share.

// AllForTag mirrors `DelayProfileService.AllForTag(tagId)` at
// `:80-84`. Returns the subset of `profiles` that include `tagID`
// in their `Tags` slice. Order of input is preserved.
func AllForTag(profiles []Profile, tagID int) []Profile {
	var out []Profile
	for _, p := range profiles {
		for _, t := range p.Tags {
			if t == tagID {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// AllForTags mirrors `DelayProfileService.AllForTags(tagIds)` at
// `:86-89`. Returns the subset of `profiles` whose `Tags`
// intersects `tagIDs` OR whose `Tags` is empty (the empty-tag
// profile is the catch-all default that applies to series with
// no tag overlap).
func AllForTags(profiles []Profile, tagIDs []int) []Profile {
	tagSet := intSet(tagIDs)
	var out []Profile
	for _, p := range profiles {
		if len(p.Tags) == 0 {
			out = append(out, p)
			continue
		}
		for _, t := range p.Tags {
			if tagSet[t] {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// BestForTags mirrors
// `DelayProfileService.FetchBestForTags(tagIds)` at `:97-102`. The
// "best" profile is the lowest-Order one whose tags intersect
// `tagIDs` (or whose tags are empty — the default catch-all).
//
// Returns (Profile{}, false) when no candidate matches. The C#
// version uses `.First()` which throws on empty; the Go port
// returns `ok=false` instead so callers can decide whether to
// surface the empty-result condition.
//
// The C# `BestForTags` consumer wraps this in a 30-second
// `_bestForTagsCache`; the cache wrapper is deferred to the
// service-layer slice. Callers that want caching today can
// memoise on top of this pure function.
func BestForTags(profiles []Profile, tagIDs []int) (Profile, bool) {
	candidates := AllForTags(profiles, tagIDs)
	if len(candidates) == 0 {
		return Profile{}, false
	}
	best := candidates[0]
	for _, p := range candidates[1:] {
		if p.Order < best.Order {
			best = p
		}
	}
	return best, true
}

// ComputeReorder ports the in-memory half of
// `DelayProfileService.Reorder(id, afterId)` at `:104-151` — the
// part that updates `Profile.Order` values across the supplied
// slice. The persistence half (`_repo.UpdateMany(all)`) is the
// caller's responsibility.
//
// `id` is the profile being moved; `afterID` (nullable, pass
// `nil` to move to the front) is the profile after which `id`
// should be inserted. The C# `Id == 1` skip is preserved verbatim
// — the default profile (Id=1) is pinned at its original Order
// and never reshuffled.
//
// Returns the same slice with mutated `Order` fields, sorted by
// the new Order ascending. Returns the input unchanged when
// `id` doesn't appear in `profiles`.
func ComputeReorder(profiles []Profile, id int, afterID *int) []Profile {
	// Sort by current Order so the C# walk operates on a stable
	// shape regardless of input ordering.
	sort.SliceStable(profiles, func(i, j int) bool {
		return profiles[i].Order < profiles[j].Order
	})

	var moving *Profile
	for i := range profiles {
		if profiles[i].ID == id {
			moving = &profiles[i]
			break
		}
	}
	if moving == nil {
		return profiles
	}

	var after *Profile
	if afterID != nil {
		for i := range profiles {
			if profiles[i].ID == *afterID {
				after = &profiles[i]
				break
			}
		}
	}

	afterOrder := getAfterOrder(moving, after)
	afterCount := afterOrder + 2
	movingOrder := moving.Order

	for i := range profiles {
		p := &profiles[i]
		if p.ID == 1 {
			continue
		}
		switch {
		case p.ID == id:
			p.Order = afterOrder + 1
		case after != nil && p.ID == after.ID:
			p.Order = afterOrder
		case p.Order > afterOrder:
			p.Order = afterCount
			afterCount++
		case p.Order > movingOrder:
			p.Order--
		}
	}

	sort.SliceStable(profiles, func(i, j int) bool {
		return profiles[i].Order < profiles[j].Order
	})
	return profiles
}

// getAfterOrder ports `DelayProfileService.GetAfterOrder(moving, after)`
// at `:153-166`.
func getAfterOrder(moving, after *Profile) int {
	if after == nil {
		return 0
	}
	if moving.Order < after.Order {
		return after.Order - 1
	}
	return after.Order
}

// intSet builds a lookup map from a slice of ints. Used by
// AllForTags / BestForTags to avoid O(n*m) intersection checks.
func intSet(xs []int) map[int]bool {
	out := make(map[int]bool, len(xs))
	for _, x := range xs {
		out[x] = true
	}
	return out
}
