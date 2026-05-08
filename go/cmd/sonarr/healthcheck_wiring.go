// Boot-time adapters that bridge the production services into the
// `internal/healthcheck` per-check narrow interfaces. Same posture as
// `decisionengine_wiring.go` / `downloadedimport_wiring.go`: the
// adapter only exists where a real service exists but its surface
// differs from what a check needs.
//
// The healthcheck.Service check list is fixed at construction time
// (see [healthcheck.NewService]); each new check requires a boot-site
// constructor invocation. Adapters live here so main.go's serve()
// stays focused on ordering / dependency wiring.

package main

import (
	"context"
	"fmt"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/rootfolders"
)

// rootFolderHealthResolverAdapter bridges *rootfolders.Repo to
// [healthcheck.RootFolderResolver] without dragging in the full
// *rootfolders.Service — which still needs the disk.Provider
// methods (FolderWritable / GetTotalSize) that haven't ported yet.
//
// The adapter mirrors the slice-3.96 [rootFolderFinderAdapter]
// (`downloadedimport_wiring.go:512`) but preserves the (string,
// error) tuple — RootFolderCheck's narrow interface keeps the error
// channel so a transient DB failure surfaces as a Warning rather
// than silently coercing to "no match".
//
// On Repo.All error the adapter wraps and returns; the check
// translates a non-nil error into a Warning row, matching the C#
// `RootFolderService.GetBestRootFolderPath` behaviour at
// `RootFolderService.cs:195-227`.
//
// Replaces with a Service-wrapping adapter once
// [disk.Provider.FolderWritable] + [disk.Provider.GetTotalSize] port.
type rootFolderHealthResolverAdapter struct {
	repo *rootfolders.Repo
}

// GetBestRootFolderPath delegates to the package-level
// [rootfolders.BestPath] free fn after pulling every persisted
// folder. Mirrors the [rootfolders.Service.GetBestRootFolderPath]
// body at `rootfolders/service.go:271-294` but skips the per-path
// cache (the healthcheck cadence is bounded — startup +
// SeriesUpdated/Deleted/RefreshComplete events — and avoiding the
// cache keeps the adapter stateless / goroutine-safe without an
// additional mutex).
func (a rootFolderHealthResolverAdapter) GetBestRootFolderPath(ctx context.Context, path string) (string, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return "", fmt.Errorf("rootfolders: GetBestRootFolderPath: %w", err)
	}
	folders := make([]rootfolders.Folder, 0, len(rows))
	for _, r := range rows {
		folders = append(folders, rootfolders.Folder{ID: r.ID, Path: r.Path})
	}
	return rootfolders.BestPath(path, folders), nil
}

// Compile-time guard.
var _ healthcheck.RootFolderResolver = rootFolderHealthResolverAdapter{}
