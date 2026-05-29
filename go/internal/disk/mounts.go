package disk

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// DriveType classifies an enumerated mount for the diskspace
// optional-paths filter. Mirrors the subset of C# `System.IO.DriveType`
// the diskspace service checks (`DiskSpaceService.cs:69` keeps Fixed +
// Network; everything else is dropped).
type DriveType int

const (
	// DriveTypeUnknown — could not classify.
	DriveTypeUnknown DriveType = iota
	// DriveTypeFixed — a local block-device filesystem.
	DriveTypeFixed
	// DriveTypeNetwork — a remote/network filesystem (NFS, SMB, …).
	DriveTypeNetwork
	// DriveTypeOther — pseudo / ram / removable / bind etc. — dropped by
	// the diskspace optional-paths filter.
	DriveTypeOther
)

// MountInfo is one enumerated mount point. Mirrors the subset of C#
// `IMount` the diskspace service reads (RootDirectory + DriveType).
type MountInfo struct {
	RootDirectory string
	DriveType     DriveType
}

// GetMounts enumerates the system's mount points. Mirrors C#
// `IDiskProvider.GetMounts()` (`DiskSpaceService.cs:69` consumer).
// Platform-specific: Linux parses /proc/mounts, Darwin/BSD use
// getfsstat(2), Windows enumerates logical drives; unsupported
// platforms return nil (the optional-paths feature simply contributes
// nothing). Errors are swallowed — an unreadable mount table degrades
// to "no optional disks", never an API failure.
func (p *Provider) GetMounts() []MountInfo { return platformGetMounts() }

// networkFsTypes / fixedFsTypes classify unix filesystem-type strings.
// Anything not listed is DriveTypeOther (pseudo/ram/bind/removable) and
// is dropped by the diskspace filter.
var networkFsTypes = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smbfs": true, "smb": true,
	"smb2": true, "afpfs": true, "webdav": true, "fuse.sshfs": true,
}

var fixedFsTypes = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true,
	"zfs": true, "reiserfs": true, "jfs": true, "f2fs": true, "fuseblk": true,
	"vfat": true, "exfat": true, "ntfs": true, "ntfs3": true, "msdos": true,
	"apfs": true, "hfs": true, "hfsplus": true,
}

// driveTypeForFsType maps a unix fstype string to a DriveType.
func driveTypeForFsType(fs string) DriveType {
	switch fs = strings.ToLower(fs); {
	case networkFsTypes[fs]:
		return DriveTypeNetwork
	case fixedFsTypes[fs]:
		return DriveTypeFixed
	default:
		return DriveTypeOther
	}
}

// parseProcMounts parses Linux /proc/mounts content into MountInfo,
// classifying each line by its fstype (field 3). Lines are
// `device mountpoint fstype options dump pass`; the mountpoint's octal
// escapes are decoded. Factored out of the syscall path so it can be
// unit-tested with a fixture.
func parseProcMounts(r io.Reader) []MountInfo {
	var out []MountInfo
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		out = append(out, MountInfo{
			RootDirectory: unescapeProcMount(fields[1]),
			DriveType:     driveTypeForFsType(fields[2]),
		})
	}
	return out
}

// unescapeProcMount decodes the octal escapes /proc/mounts uses for
// special characters in path fields (e.g. `\040` space, `\011` tab,
// `\012` newline, `\134` backslash).
func unescapeProcMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
