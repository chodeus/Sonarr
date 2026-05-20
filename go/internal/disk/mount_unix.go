//go:build linux || darwin || freebsd

package disk

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// platformGetMount reads mount info via the statfs(2) syscall.
//
// Linux: `unix.Statfs_t.Flags & unix.ST_RDONLY` (ST_RDONLY = 0x1).
// macOS / *BSD: `unix.Statfs_t.Flags & unix.MNT_RDONLY` (MNT_RDONLY = 0x1).
//
// The constant names differ but the bit value is `1` on both
// platforms in the kernel headers. golang.org/x/sys/unix exposes
// `ST_RDONLY` on Linux and `MNT_RDONLY` on Darwin/BSD — see
// readOnlyFlag for the per-OS selection (in mount_linux.go and
// mount_darwin.go).
//
// The mount Name + RootDirectory aren't directly returned by
// statfs(2). We surface the resolved path as both fields — matches
// what the healthcheck consumes and the C# wire shape
// (DriveInfo.Name == DriveInfo.RootDirectory.FullName on Unix).
func platformGetMount(path string) (*Mount, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return nil, fmt.Errorf("disk: Statfs(%q): %w", path, err)
	}
	return &Mount{
		Name:          path,
		RootDirectory: path,
		IsReadOnly:    isReadOnly(st),
	}, nil
}
