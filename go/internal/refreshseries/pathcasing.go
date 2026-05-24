package refreshseries

import (
	"errors"
	"path/filepath"
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

// getActualCasing ports the Windows-only second half of C#
// `RefreshSeriesService.cs:119` `series.Path = series.Path.GetActualCasing()`,
// where `GetActualCasing` is the extension defined at C#
// `NzbDrone.Common/Extensions/PathExtensions.cs:213-236`.
//
// On non-Windows the C# extension is a no-op (returns input
// unchanged at `:215-218 if OsInfo.IsNotWindows || path.StartsWith("\\")`).
// On Windows it walks parent directories enumerating
// `parentDir.GetDirectories(dirInfo.Name)[0].Name` so the on-disk
// casing recovers regardless of what the user typed in the UI when
// setting the series path.
//
// For the Go-port this slice ships the no-op behaviour on ALL
// platforms — implementing the Windows parent-walk casing recovery
// requires Win32 directory enumeration semantics (FindFirstFile and
// friends) which is a non-trivial slice on its own and is irrelevant
// on Linux containers where Sonarr primarily runs. When that slice
// lands it can split this function into build-tagged files
// (`pathcasing_windows.go` + `pathcasing_other.go`) without changing
// the call site at RefreshSeriesInfo.
//
// J208.
func getActualCasing(path string) string {
	return path
}
