//go:build linux

package disk

import "os"

// platformGetMounts reads /proc/mounts and classifies each entry. An
// unreadable mount table yields nil (no optional disks).
func platformGetMounts() []MountInfo {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	return parseProcMounts(f)
}
