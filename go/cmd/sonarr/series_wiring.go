// Boot-time adapters for `internal/series` deferred-dependency
// surfaces. The series.Service exposes optional setters for two
// dependencies the C# IoC graph treats as mandatory:
//
//   - SetEpisodeMonitorCascade (slice 3.AB) — *episodes.Service
//     satisfies via Go's structural typing.
//   - SetTagChangeProvider (slice 3.AB) — needs a small adapter that
//     composes *autotagging.Service.All + *rootfolders.Repo.All +
//     the package-level autotagging.ChangesFor free fn.
//
// Adapters that materialise an optional dependency live here; the
// straight-through cascade wire stays inline in main.go.

package main

import (
	"context"
	"fmt"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/series"
)

// seriesTagChangeProviderAdapter implements
// [series.TagChangeProvider] by composing
// [autotagging.Service.All] + [rootfolders.Repo.All] + the
// package-level [autotagging.ChangesFor] free fn.
//
// Mirrors the C# `AutoTaggingService.GetTagChanges(series)` call at
// `SeriesService.cs:281` — the C# class injects the full
// IAutoTaggingService whose GetTagChanges performs the same fan-out
// internally. The Go-port autotagging.Service deliberately does NOT
// expose GetTagChanges (the `// future GetTagChanges shim` comment
// at `internal/autotagging/service.go:29-33` flags it as a host
// concern); composing here keeps the adapter pinned to the
// host-side wiring boundary.
//
// Talks directly to *rootfolders.Repo (NOT the full Service) for
// the same reason as the slice-3.96 rootFolderFinderAdapter: the
// full Service requires disk.Provider methods (FolderWritable +
// GetTotalSize) that haven't ported yet (FAIL-CLOSED #8). The
// underlying ChangesFor only needs the Folder list — no disk reads.
//
// On rootfolders.Repo.All error the adapter returns (nil, nil, err)
// — the C# tree similarly bubbles the lookup failure up the call
// chain at SeriesService.cs:281; UpdateSeries propagates the error.
// On autotagging.Service.All error: same propagation.
type seriesTagChangeProviderAdapter struct {
	autoTags    *autotagging.Service
	rootFolders *rootfolders.Repo
}

// GetTagChanges mirrors [series.TagChangeProvider.GetTagChanges].
// Empty / nil tag set short-circuits to (empty, empty, nil) without
// touching rootfolders (matches the autotagging.ChangesFor
// `if len(autoTags) == 0` short-circuit at `internal/autotagging/changes.go:60`).
func (a seriesTagChangeProviderAdapter) GetTagChanges(ctx context.Context, s *series.Series) (toAdd, toRemove map[int]struct{}, err error) {
	autoTags, err := a.autoTags.All(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("series tag-change adapter: autoTags.All: %w", err)
	}
	if len(autoTags) == 0 {
		empty := autotagging.NewAutoTaggingChanges()
		return empty.TagsToAdd, empty.TagsToRemove, nil
	}
	rfRows, err := a.rootFolders.All(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("series tag-change adapter: rootfolders.All: %w", err)
	}
	folders := make([]rootfolders.Folder, 0, len(rfRows))
	for _, r := range rfRows {
		folders = append(folders, rootfolders.Folder{ID: r.ID, Path: r.Path})
	}
	changes := autotagging.ChangesFor(s, autoTags, folders)
	return changes.TagsToAdd, changes.TagsToRemove, nil
}

// Compile-time guard.
var _ series.TagChangeProvider = seriesTagChangeProviderAdapter{}
