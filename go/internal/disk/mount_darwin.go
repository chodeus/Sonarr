//go:build darwin || freebsd

package disk

import "golang.org/x/sys/unix"

// isReadOnly returns true iff the Statfs result indicates the mount
// is read-only. macOS / *BSD expose the flag as `MNT_RDONLY` (0x1)
// in the `Flags` field — sys/mount.h:
//
//	#define MNT_RDONLY 0x00000001
//
// matches the BSD VFS read-only mount flag.
func isReadOnly(st unix.Statfs_t) bool {
	return st.Flags&unix.MNT_RDONLY != 0
}
