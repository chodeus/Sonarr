//go:build !unix

package disk

import "errors"

// availableSpace is the non-unix fallback for
// [Provider.GetAvailableSpace] — returns a not-implemented error so
// the caller's `if err != nil` branch fires. Mirrors C#
// `WindowsDiskProvider.GetAvailableSpace` returning null when the
// underlying API surface isn't ported yet.
//
// A real Windows implementation would use
// `golang.org/x/sys/windows.GetDiskFreeSpaceEx`; that lands when a
// Windows-deploying caller actually exercises this code path. For
// now the dev/CI matrix is unix-only.
func availableSpace(_ string) (int64, error) {
	return 0, errors.New("disk: GetAvailableSpace not implemented on this platform")
}
