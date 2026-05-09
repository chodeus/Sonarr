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
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
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

// seriesPathBuilderAdapter implements [series.SeriesPathBuilder] by
// composing [organizer.GetSeriesFolder] (the slice-2 token-replace
// pipeline) + [rootfolders.Service.GetBestRootFolderPath] +
// [namingconfig.Service.GetConfig] for the user-configured folder
// patterns.
//
// Mirrors the C# `SeriesPathBuilder.BuildPath` at
// `Tv/SeriesPathBuilder.cs:28-58`. Two arms:
//
//  1. `useExistingRelativeFolder=true` AND `series.Path` non-empty:
//     resolve the best root folder for the existing series.Path via
//     rootfolders.Service.GetBestRootFolderPath; if it's a parent of
//     series.Path, return RootFolderPath + relative-from-root path.
//     Else (no parent match — the series.Path lives outside any
//     known root) log a warning + return RootFolderPath + the
//     basename of series.Path (the C# `series.Path.GetDirectoryName()`
//     fallback at `:53` — note: C# .GetDirectoryName() is misleadingly
//     named; the Sonarr extension strips the trailing path component
//     to give the folder name, which is filepath.Base in Go-port
//     terms when the input is a directory path).
//  2. Otherwise: RootFolderPath + organizer.GetSeriesFolder using the
//     naming-config's SeriesFolderFormat. Mirrors C# `:41`
//     `_fileNameBuilder.GetSeriesFolder(series)`.
//
// Empty RootFolderPath returns an error (mirrors the C# argument-
// exception throw at `:30-33`); the C# tree raises which would crash
// the bulk-update loop, so the Go port surfaces a typed error and
// the caller (series.Service.UpdateMany) propagates.
// seriesPathBuilderNamingReader narrows [*namingconfig.Service] to the
// one method this adapter needs (GetConfig). Defined as an interface
// so tests can inject a fixed-config stub without spinning up a
// namingconfig DB.
type seriesPathBuilderNamingReader interface {
	GetConfig(ctx context.Context) (*namingconfig.Config, error)
}

// seriesPathBuilderRootFolderResolver narrows [*rootfolders.Service]
// to GetBestRootFolderPath. Same posture — stub-friendly without
// the rootfolders.Service dep tree.
type seriesPathBuilderRootFolderResolver interface {
	GetBestRootFolderPath(ctx context.Context, path string) (string, error)
}

type seriesPathBuilderAdapter struct {
	naming      seriesPathBuilderNamingReader
	rootFolders seriesPathBuilderRootFolderResolver
	log         *slog.Logger
}

// BuildPath ports `SeriesPathBuilder.BuildPath`. See
// [seriesPathBuilderAdapter] doc-comment for the algorithm.
func (a seriesPathBuilderAdapter) BuildPath(ctx context.Context, s *series.Series, useExistingRelativeFolder bool) (string, error) {
	if s == nil {
		return "", errors.New("series path builder: nil series")
	}
	if strings.TrimSpace(s.RootFolderPath) == "" {
		return "", errors.New("series path builder: root folder was not provided")
	}

	if useExistingRelativeFolder && strings.TrimSpace(s.Path) != "" {
		best, err := a.rootFolders.GetBestRootFolderPath(ctx, s.Path)
		if err != nil {
			return "", fmt.Errorf("series path builder: GetBestRootFolderPath: %w", err)
		}
		if best != "" && isParentPath(best, s.Path) {
			rel, err := filepath.Rel(best, s.Path)
			if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				return filepath.Join(s.RootFolderPath, rel), nil
			}
		}
		// No parent match — fall through to the basename arm. Mirrors
		// C# `:53-57` warning + GetDirectoryName fallback.
		if a.log != nil {
			a.log.Warn("series path builder: cannot resolve relative path; using basename",
				"seriesPath", s.Path, "bestRoot", best)
		}
		return filepath.Join(s.RootFolderPath, filepath.Base(s.Path)), nil
	}

	cfg, err := a.naming.GetConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("series path builder: namingconfig.GetConfig: %w", err)
	}
	opts := organizer.FolderOptions{
		SeriesFolderFormat: defaultIfEmpty(cfg.SeriesFolderFormat, "{Series Title}"),
		// SeasonFolderFormat / SpecialsFolderFormat are not used by
		// GetSeriesFolder but FolderOptions is shared with
		// GetSeasonFolder; populate for forward compatibility so a
		// future helper that reads them sees the same options.
		SeasonFolderFormat:   cfg.SeasonFolderFormat,
		SpecialsFolderFormat: cfg.SpecialsFolderFormat,
		// Clean defaults to a zero CleanNameOptions — the FolderOptions
		// docstring says callers configure this; for the series-folder
		// path we use the default which is what the C# tree's
		// FileNameBuilder does for the GetSeriesFolder call site.
	}
	folder := organizer.GetSeriesFolder(s, opts)
	return filepath.Join(s.RootFolderPath, folder), nil
}

// isParentPath reports whether `parent` is a path-prefix of `child`
// in the directory sense (strict — equal paths return false to match
// C# `IsParentPath` semantics where a path is not its own parent).
// Both inputs are expected to be cleaned absolute paths in production;
// the helper still calls filepath.Clean defensively for tests.
func isParentPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "" && !strings.HasPrefix(rel, "..")
}

// defaultIfEmpty returns `s` when non-empty, else `fallback`.
func defaultIfEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// Compile-time guards.
var (
	_ series.TagChangeProvider = seriesTagChangeProviderAdapter{}
	_ series.SeriesPathBuilder = seriesPathBuilderAdapter{}
)
