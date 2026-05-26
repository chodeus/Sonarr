//go:build !windows

package refreshseries

import "testing"

// TestGetActualCasing_NoOpOnNonWindows pins the C#-equivalent posture
// at `PathExtensions.cs:215-218` (`if OsInfo.IsNotWindows` → return
// input). Per-segment casing recovery is meaningless on case-sensitive
// POSIX filesystems where the on-disk casing IS the input casing.
//
// J248. Companion to TestGetActualCasing_WalksParents in
// pathcasing_windows_test.go.
func TestGetActualCasing_NoOpOnNonWindows(t *testing.T) {
	t.Parallel()
	cases := []string{
		"/tmp/refresh/show",
		"/MixedCase/Path",
		"",
		"relative",
	}
	for _, in := range cases {
		if got := getActualCasing(in); got != in {
			t.Errorf("getActualCasing(%q) = %q, want %q (no-op on non-Windows)", in, got, in)
		}
	}
}
