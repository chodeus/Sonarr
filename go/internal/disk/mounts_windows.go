//go:build windows

package disk

import (
	"strings"

	"golang.org/x/sys/windows"
)

// platformGetMounts enumerates logical drives via GetLogicalDriveStrings
// and classifies each by GetDriveType. DRIVE_FIXED → Fixed, DRIVE_REMOTE
// → Network; everything else (removable/cdrom/ramdisk/unknown) is Other
// and dropped by the diskspace filter. Any error degrades to nil.
func platformGetMounts() []MountInfo {
	n, err := windows.GetLogicalDriveStrings(0, nil)
	if err != nil || n == 0 {
		return nil
	}
	buf := make([]uint16, n)
	if _, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0]); err != nil {
		return nil
	}
	// The buffer is a sequence of NUL-terminated "C:\" strings ending in
	// a double NUL.
	var out []MountInfo
	for _, root := range splitNullTerminated(buf) {
		if root == "" {
			continue
		}
		dt := DriveTypeOther
		rootPtr, perr := windows.UTF16PtrFromString(root)
		if perr != nil {
			continue
		}
		switch windows.GetDriveType(rootPtr) {
		case windows.DRIVE_FIXED:
			dt = DriveTypeFixed
		case windows.DRIVE_REMOTE:
			dt = DriveTypeNetwork
		}
		out = append(out, MountInfo{RootDirectory: root, DriveType: dt})
	}
	return out
}

// splitNullTerminated splits a double-NUL-terminated UTF-16 multi-string
// into its component strings.
func splitNullTerminated(buf []uint16) []string {
	s := windows.UTF16ToString(buf)
	// UTF16ToString stops at the first NUL; walk segments manually.
	var out []string
	start := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] == 0 {
			if i > start {
				out = append(out, strings.TrimRight(windows.UTF16ToString(buf[start:i]), "\x00"))
			}
			start = i + 1
			if start < len(buf) && buf[start] == 0 {
				break
			}
		}
	}
	_ = s
	return out
}
