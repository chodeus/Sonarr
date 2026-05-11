package rootfolders

import (
	"path/filepath"
	"strings"
)

// Pure-logic helpers ported from
// `src/NzbDrone.Core/RootFolders/RootFolderService.cs`. The
// service-layer wrapper (with disk-provider, series-paths
// lookup, namingconfig sub-folder-depth, cache, async
// disk-stat collection) lands when those dependencies arrive.
// The helpers here operate on `[]RootFolder` slices and path
// strings with no I/O.

// UnmappedFolder ports `RootFolders/UnmappedFolder.cs:1-9`. A
// directory observed under a RootFolder.Path that doesn't have a
// corresponding Series row.
//
// Populated by the future RootFolderService.GetUnmappedFolders
// disk-walking pass; defined here so the data shape is stable
// across the persistence + service-layer boundary.
type UnmappedFolder struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
}

// Folder is the in-memory typed sibling of the persisted
// `RootFolder` (storage form lives in rootfolders.go). Adds
// runtime-only fields the C# `RootFolder` carries via
// disk-provider calls.
//
// All runtime fields default to zero values when not populated;
// the service layer fills them in when callers ask for the
// `AllWithUnmappedFolders` / `Get(id, timeout)` shapes.
type Folder struct {
	ID              int              `json:"id"`
	Path            string           `json:"path"`
	Accessible      bool             `json:"accessible"`
	IsEmpty         bool             `json:"isEmpty"`
	FreeSpace       *int64           `json:"freeSpace"`
	TotalSpace      *int64           `json:"totalSpace"`
	UnmappedFolders []UnmappedFolder `json:"unmappedFolders"`
}

// specialFolderNames mirrors `RootFolderService.SpecialFolders` at
// `RootFolderService.cs:36-47`. Lowercased names that should be
// excluded from the unmapped-folders list — system / OS / NAS
// reserved directories.
var specialFolderNames = map[string]bool{
	"$recycle.bin":              true,
	"system volume information": true,
	"recycler":                  true,
	"lost+found":                true,
	".appledb":                  true,
	".appledesktop":             true,
	".appledouble":              true,
	"@eadir":                    true,
	".grab":                     true,
}

// IsSpecialFolder reports whether `name` is a recognised special /
// reserved folder. Case-insensitive (matches against lowercased
// `name`). Mirrors the C# `SpecialFolders.Contains(...)` check at
// `RootFolderService.cs:179`.
//
// `name` should be the bare folder name (no path components). The
// caller is responsible for extracting `filepath.Base(...)` first.
func IsSpecialFolder(name string) bool {
	return specialFolderNames[strings.ToLower(name)]
}

// IsParentPath reports whether `parent` is a directory ancestor of
// `child` — i.e., `child` lives inside or under `parent`. Mirrors
// the C# `string.IsParentPath(child)` extension semantically with
// platform-aware separators via `filepath.Clean`.
//
// Comparison is case-insensitive on platforms with
// case-insensitive filesystems by virtue of the C# default; the
// Go port uses `strings.EqualFold` after cleaning. Trailing
// separators are stripped on both sides.
//
// Returns false when either arg is empty after cleaning, OR when
// the two paths are equal (a path is not its own parent).
func IsParentPath(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	parent = strings.TrimRight(filepath.Clean(parent), `/\`)
	child = strings.TrimRight(filepath.Clean(child), `/\`)
	if parent == "" || child == "" {
		return false
	}
	if strings.EqualFold(parent, child) {
		return false
	}
	// `child` must start with `parent + separator`. Use a single
	// trailing separator (the platform's) plus a length check so
	// `/foo` doesn't accept `/foobar`.
	prefix := parent + string(filepath.Separator)
	if len(child) <= len(prefix) {
		return false
	}
	// Case-insensitive starts-with check.
	return strings.EqualFold(child[:len(prefix)], prefix)
}

// CleanPath ports the C# `string.GetCleanPath()` extension —
// idempotent path normaliser used by GetBestRootFolderPath
// before returning the result. Strips trailing separators and
// canonicalises via `filepath.Clean`. Empty input returns
// empty.
func CleanPath(p string) string {
	if p == "" {
		return ""
	}
	return strings.TrimRight(filepath.Clean(p), `/\`)
}

// BestPath ports
// `RootFolderService.GetBestRootFolderPathInternal(path)` at
// `:215-227`. Returns the longest-prefix RootFolder.Path that's a
// parent of `path`; falls back to `filepath.Dir(path)` when no
// folder matches.
//
// Mirrors the C# `MaxBy(r => r.Path.Length)` tie-break (longest
// path wins when multiple folders are ancestors — typical case
// is `/tv` vs `/tv/anime` both being parents of
// `/tv/anime/SeriesX`; the deeper `/tv/anime` wins).
//
// Returns the cleaned form (no trailing separators) per the C#
// `GetCleanPath()` postprocess.
//
// Performance: O(n) over folders. The C# version wraps this in a
// 24-hour cache (`_cache.Get(path, () => ..., TimeSpan.FromDays(1))`);
// the cache wrapper is deferred to the service-layer slice. Pure
// function callers can memoise.
func BestPath(path string, folders []Folder) string {
	var best Folder
	bestLen := 0
	for _, f := range folders {
		if !IsParentPath(f.Path, path) {
			continue
		}
		if len(f.Path) > bestLen {
			bestLen = len(f.Path)
			best = f
		}
	}
	if bestLen == 0 {
		return CleanPath(filepath.Dir(path))
	}
	return CleanPath(best.Path)
}

// FilterSpecialFolders removes UnmappedFolder entries whose name
// (the basename of Path, lowercased) is in the specialFolderNames
// set. Mirrors the C# `results.RemoveAll(...)` pass at `:179`.
//
// Returns a new slice; the input is not mutated.
func FilterSpecialFolders(folders []UnmappedFolder) []UnmappedFolder {
	out := make([]UnmappedFolder, 0, len(folders))
	for _, f := range folders {
		base := filepath.Base(f.Path)
		if !IsSpecialFolder(base) {
			out = append(out, f)
		}
	}
	return out
}
