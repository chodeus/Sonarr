//go:build windows

package refreshseries

import (
	"io/fs"
	"os"
)

// getActualCasing on Windows recovers the on-disk casing for each
// segment of `path`. Ports C# `PathExtensions.GetActualCasing` at
// `:213-236` together with the recursive `GetProperCapitalization`
// helper at `:194-211`.
//
// The actual walking + per-segment match logic lives in cross-platform
// `caseFixWalker` (pathcasing.go) so it's unit-testable on Linux/macOS
// via an injected `readDir` fake; this file just supplies the entry
// point + the real-filesystem `os.ReadDir` binding.
//
// Behaviour (mirrors C# `:215-218`):
//
//   - Empty input is returned unchanged.
//   - UNC paths and any leading-backslash path return unchanged.
//     The C# check is `path.StartsWith("\\")` which matches both
//     `\\server\share\...` and any `\foo` shape. We additionally
//     bail on forward-slash leaders for parity in case Go's path
//     handling normalises the separator on the way in.
//
// Otherwise hand off to `caseFixWalker` with the real `os.ReadDir`.
//
// J248.
func getActualCasing(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '\\' || path[0] == '/' {
		return path
	}
	return caseFixWalker(path, func(p string) ([]fs.DirEntry, error) {
		return os.ReadDir(p)
	})
}
