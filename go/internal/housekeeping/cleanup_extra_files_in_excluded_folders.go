// CleanupExtraFilesInExcludedFolders housekeeper. Ports the C# task
// at `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupExtraFilesInExcludedFolders.cs`.
//
// Sweeps ExtraFiles rows that point into folders the DiskScanService
// now excludes (extras subfolders, hidden dotfolders, OS noise, ...).
// These rows are produced by historic disk scans that ran before the
// exclusion regex grew its current shape; a re-scan would silently
// skip them, but the existing rows persist forever otherwise.
//
// # C# bug + Go-port fix (slice 5.J133)
//
// The C# implementation at `:30-42` has a path-comparison bug:
//
//	var filteredExtraFiles = _diskScanService.FilterPaths(series.Path,
//	    extraFiles.Select(e => Path.Combine(series.Path, e.RelativePath)));
//	...
//	var excludedExtraFiles = extraFiles.Where(e =>
//	    !filteredExtraFiles.Contains(Path.Combine(e.RelativePath))).ToList();
//
// `Path.Combine(e.RelativePath)` is the .NET `params string[]` overload
// called with a single element, which returns `e.RelativePath`
// unchanged (just a separator-normalisation pass on Windows). So the
// `Contains` check compares a relative path against the filtered set
// of FULL paths — every membership test fails, every extra-file for
// the series gets marked as excluded, and DeleteMany nukes them all
// the moment one extra is in an excluded folder.
//
// The Go port fixes this by passing the full path on both sides of
// the Contains check. The fix is intentional — the C# behaviour is
// data-destructive for affected series, the task name describes the
// FIXED behaviour, and the upstream bug is reportable. If a future
// audit re-aligns to C# verbatim, swap the `fullPath` lookup back
// to `e.RelativePath` and add a TODO referencing this comment.

package housekeeping

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/extrafiles"
)

// ExtraFileSweepRepo narrows [*extrafiles.Repo] to the two methods
// the housekeeper reaches for. Pulled out so tests can inject a stub
// without constructing a full repo + DB.
type ExtraFileSweepRepo interface {
	AllBySeriesID(ctx context.Context, seriesID int) ([]extrafiles.ExtraFile, error)
	DeleteByIDs(ctx context.Context, ids []int) (int64, error)
}

// SeriesPaths narrows [*series.Service] to the AllSeriesPaths
// surface (map[seriesID]Path). Mirrors the C#
// `ISeriesService.GetAllSeriesPaths` call at `:25`.
type SeriesPaths interface {
	AllSeriesPaths(ctx context.Context) (map[int]string, error)
}

// PathFilter narrows the DiskScanService.FilterPaths surface
// ([mediafiles.FilterPaths] at production binding). The
// `filterExtras` flag is fixed at `true` because the C# call site at
// `:30` invokes the 2-arg FilterPaths overload, which defaults
// filterExtras to true in the C# tree.
type PathFilter func(basePath string, paths []string) ([]string, error)

// NewCleanupExtraFilesInExcludedFolders ports
// `Housekeepers/CleanupExtraFilesInExcludedFolders.cs:1-46`. Walks
// every series, looks up its ExtraFiles, runs them through the disk-
// scanner's exclusion filter, and deletes the rows that fail the
// filter.
func NewCleanupExtraFilesInExcludedFolders(
	repo ExtraFileSweepRepo,
	seriesPaths SeriesPaths,
	filter PathFilter,
	log *slog.Logger,
) (Task, error) {
	if repo == nil {
		return nil, fmt.Errorf("housekeeping: NewCleanupExtraFilesInExcludedFolders: nil ExtraFileSweepRepo")
	}
	if seriesPaths == nil {
		return nil, fmt.Errorf("housekeeping: NewCleanupExtraFilesInExcludedFolders: nil SeriesPaths")
	}
	if filter == nil {
		return nil, fmt.Errorf("housekeeping: NewCleanupExtraFilesInExcludedFolders: nil PathFilter")
	}
	if log == nil {
		log = slog.Default()
	}
	return &cleanupExtraFilesExcluded{repo: repo, seriesPaths: seriesPaths, filter: filter, log: log}, nil
}

type cleanupExtraFilesExcluded struct {
	repo        ExtraFileSweepRepo
	seriesPaths SeriesPaths
	filter      PathFilter
	log         *slog.Logger
}

// Name implements Task.
func (t *cleanupExtraFilesExcluded) Name() string {
	return "CleanupExtraFilesInExcludedFolders"
}

// Clean implements Task. Per-series: load every ExtraFile, build the
// full-path list, ask the filter for the kept-set, then delete any
// ExtraFile whose full path is not in the kept-set.
//
// Per-series errors propagate immediately — the C# tree's loop has no
// try/catch around the body, so a single bad series aborts the pass.
// (The HousekeepingService swallows the outer error, so the failure
// surfaces as a logged warning rather than a fatal exit.)
func (t *cleanupExtraFilesExcluded) Clean(ctx context.Context) error {
	allPaths, err := t.seriesPaths.AllSeriesPaths(ctx)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupExtraFilesInExcludedFolders: AllSeriesPaths: %w", err)
	}
	for seriesID, seriesPath := range allPaths {
		extras, err := t.repo.AllBySeriesID(ctx, seriesID)
		if err != nil {
			return fmt.Errorf("housekeeping: CleanupExtraFilesInExcludedFolders (series=%d): %w", seriesID, err)
		}
		if len(extras) == 0 {
			continue
		}
		fullPaths := make([]string, len(extras))
		for i, e := range extras {
			fullPaths[i] = filepath.Join(seriesPath, e.RelativePath)
		}
		kept, err := t.filter(seriesPath, fullPaths)
		if err != nil {
			return fmt.Errorf("housekeeping: CleanupExtraFilesInExcludedFolders (series=%d): filter: %w", seriesID, err)
		}
		if len(kept) == len(extras) {
			continue
		}
		keptSet := make(map[string]struct{}, len(kept))
		for _, p := range kept {
			keptSet[p] = struct{}{}
		}
		excluded := make([]int, 0, len(extras)-len(kept))
		for i, e := range extras {
			if _, ok := keptSet[fullPaths[i]]; ok {
				continue
			}
			excluded = append(excluded, e.ID)
		}
		if len(excluded) == 0 {
			continue
		}
		n, err := t.repo.DeleteByIDs(ctx, excluded)
		if err != nil {
			return fmt.Errorf("housekeeping: CleanupExtraFilesInExcludedFolders (series=%d): %w", seriesID, err)
		}
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: deleted ExtraFiles in excluded folders",
			slog.Int("series", seriesID),
			slog.Int64("rows", n))
	}
	return nil
}
