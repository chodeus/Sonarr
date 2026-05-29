//go:build darwin || freebsd

package disk

import "golang.org/x/sys/unix"

// platformGetMounts enumerates mounts via getfsstat(2) and classifies
// each by its filesystem-type name. MNT_NOWAIT avoids blocking on slow
// network mounts. Any error degrades to nil (no optional disks).
func platformGetMounts() []MountInfo {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil || n <= 0 {
		return nil
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil
	}
	if n > len(buf) {
		n = len(buf)
	}
	out := make([]MountInfo, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, MountInfo{
			RootDirectory: unix.ByteSliceToString(buf[i].Mntonname[:]),
			DriveType:     driveTypeForFsType(unix.ByteSliceToString(buf[i].Fstypename[:])),
		})
	}
	return out
}
