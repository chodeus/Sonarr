//go:build !windows

package refreshseries

// getActualCasing on non-Windows platforms is a no-op. Mirrors the
// C# extension at `NzbDrone.Common/Extensions/PathExtensions.cs:215-218`
// which short-circuits `if OsInfo.IsNotWindows` and returns the input
// unchanged. POSIX filesystems are typically case-sensitive (ext4,
// XFS, APFS-default) so the on-disk casing IS the input casing —
// there's nothing to recover.
//
// J248.
func getActualCasing(path string) string {
	return path
}
