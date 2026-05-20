//go:build linux

package disk

import "golang.org/x/sys/unix"

// isReadOnly returns true iff the Statfs result indicates the mount
// is read-only. Linux exposes the flag as `ST_RDONLY` (0x1) in the
// `Flags` field — sys/statvfs.h:
//
//	#define ST_RDONLY 1
//
// matches Linux kernel `MS_RDONLY` (mount flag).
func isReadOnly(st unix.Statfs_t) bool {
	return st.Flags&unix.ST_RDONLY != 0
}
