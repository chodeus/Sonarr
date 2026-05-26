package refreshseries

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// normalizePath ports the first half of C# `RefreshSeriesService.cs:118`
// `series.Path = new DirectoryInfo(series.Path).FullName`. Returns the
// canonical absolute form of `path` with redundant separators, "."
// segments, and ".." segments resolved.
//
// .NET's DirectoryInfo.FullName resolves relative paths against the
// process working directory; we match via filepath.Abs(filepath.Clean(...)).
// Empty input returns an error matching C# DirectoryInfo's empty-path
// throw — callers absorb via the C# `:121-124` try/catch warn-log shape.
//
// Symlinks are NOT resolved (filepath.EvalSymlinks would chase them;
// C# DirectoryInfo.FullName does not). The Sonarr install can sit at
// the tail of a symlinked path and the user expects that link to
// persist verbatim.
//
// J208.
func normalizePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("refreshseries: empty path")
	}
	cleaned := filepath.Clean(path)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// caseFixWalker is the cross-platform-testable core of `getActualCasing`.
// It walks the parent chain of `path`, recursively recovering on-disk
// casing for each segment via case-insensitive match against the
// supplied readDir callback. Mirrors C# `PathExtensions.GetProperCapitalization`
// at `:194-211` (the recursive helper that backs `GetActualCasing`).
//
// Behaviour rules (mirrored from C# verbatim):
//
//   - The root (when `filepath.Dir(p) == p`, i.e. drive letter on
//     Windows or "/" on POSIX) is upper-cased. Matches C# `:199`
//     `dirInfo.Name.ToUpper()`. POSIX root "/" upper-cases to itself
//     so this is a no-op on Linux/macOS for the root step.
//   - For each non-root segment we call `readDir(parentCased)` and
//     scan for a case-insensitive match against the leaf segment. If
//     found, we use the on-disk casing; otherwise we keep the input
//     casing (matches C# `:204-207` where folderName defaults to
//     `dirInfo.Name` when the directory doesn't exist).
//   - A readDir error (parent missing / permission denied) preserves
//     input casing for that segment AND continues the walk down with
//     the case-corrected ancestor prefix. Matches the absorb-and-keep-
//     walking posture of the C# recursion (it relies on `DirectoryInfo`
//     not throwing for missing parents).
//
// The injected `readDir` parameter exists so unit tests can exercise
// the algorithm with a fake filesystem on any host platform — the
// build-tagged callers (`pathcasing_windows.go`, `pathcasing_other.go`)
// pass `os.ReadDir` when invoking for real.
//
// J248.
func caseFixWalker(path string, readDir func(string) ([]fs.DirEntry, error)) string {
	parent := filepath.Dir(path)
	if parent == path {
		// Root reached (drive letter on Windows, "/" on POSIX).
		return strings.ToUpper(path)
	}
	name := filepath.Base(path)
	parentCased := caseFixWalker(parent, readDir)

	if entries, err := readDir(parentCased); err == nil {
		for _, e := range entries {
			if strings.EqualFold(e.Name(), name) {
				return filepath.Join(parentCased, e.Name())
			}
		}
	}
	return filepath.Join(parentCased, name)
}
