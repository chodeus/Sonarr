package autotagging

import (
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/series"
)

// AutoTaggingChanges ports `AutoTaggingChanges.cs:1-15`. The
// suggested tag-set delta for a series, computed by walking every
// AutoTag's specifications and accumulating "tags to add when ALL
// specs match" and (when `RemoveTagsAutomatically=true`) "tags to
// remove when not all specs match".
//
// Callers — typically the SeriesService on series-CRUD events —
// apply the delta by adding `TagsToAdd` to `series.Tags` and
// removing `TagsToRemove`.
type AutoTaggingChanges struct {
	TagsToAdd    map[int]struct{}
	TagsToRemove map[int]struct{}
}

// AutoTagsUpdatedEvent ports `AutoTagsUpdatedEvent.cs:1-7`. Fires
// after Insert / Update / Delete operations on the AutoTag table.
type AutoTagsUpdatedEvent struct{}

// NewAutoTaggingChanges constructs an empty changes set.
func NewAutoTaggingChanges() AutoTaggingChanges {
	return AutoTaggingChanges{
		TagsToAdd:    map[int]struct{}{},
		TagsToRemove: map[int]struct{}{},
	}
}

// ChangesFor ports
// `AutoTaggingService.GetTagChanges(Series)` at
// `AutoTaggingService.cs:91-138`. Pure-function form: returns the
// suggested tag delta for `series` given the active `autoTags` and
// the configured `rootFolders` (used to derive
// `Series.RootFolderPath` per `RootFolderService.GetBestRootFolderPath`).
//
// Algorithm (verbatim from C#):
//
//   - Compute `Series.RootFolderPath = BestPath(series.Path, rootFolders)`.
//   - For each AutoTag:
//   - Group its specs by Go type, compute each group's DidMatch.
//   - If every group matches: every tag in `autoTag.Tags` that's
//     not already in `series.Tags` is added to `TagsToAdd`.
//   - Else if `RemoveTagsAutomatically=true`: every tag in
//     `autoTag.Tags` is added to `TagsToRemove` (regardless of
//     whether it's currently on the series).
//
// Empty `autoTags` returns an empty changes set without computing
// the BestPath (mirrors C# `if (autoTags.Empty()) return changes;`
// short-circuit at `:101-104`).
//
// Returns the delta as map-sets so duplicate tags from multiple
// matching AutoTags collapse to a single entry.
func ChangesFor(s *series.Series, autoTags []*Tag, rootFolders []rootfolders.Folder) AutoTaggingChanges {
	changes := NewAutoTaggingChanges()
	if len(autoTags) == 0 || s == nil {
		return changes
	}

	rootPath := rootfolders.BestPath(s.Path, rootFolders)
	input := Input{Series: s, RootFolderPath: rootPath}

	existing := intSet(s.Tags)

	for _, t := range autoTags {
		if t == nil {
			continue
		}
		match := autoTagMatches(t, input)
		switch {
		case match:
			for _, tag := range t.Tags {
				if !existing[tag] {
					changes.TagsToAdd[tag] = struct{}{}
				}
			}
		case t.RemoveTagsAutomatically:
			for _, tag := range t.Tags {
				changes.TagsToRemove[tag] = struct{}{}
			}
		}
	}
	return changes
}

// autoTagMatches replicates the per-AutoTag inner loop of
// AutoTaggingService.GetTagChanges (`:107-117`). Reuses the
// type-grouping helpers from spec.go.
func autoTagMatches(t *Tag, input Input) bool {
	groups := groupSpecsByType(t.Specifications, input)
	if len(groups) == 0 {
		// C# behaviour: an AutoTag with no specifications has
		// `specificationMatches.All(x => x.DidMatch)` evaluating
		// to `true` over an empty enumerable — vacuously true. So
		// such a tag would always match. Preserve verbatim.
		return true
	}
	for _, g := range groups {
		if !g.DidMatch() {
			return false
		}
	}
	return true
}

func intSet(xs []int) map[int]bool {
	out := make(map[int]bool, len(xs))
	for _, x := range xs {
		out[x] = true
	}
	return out
}
