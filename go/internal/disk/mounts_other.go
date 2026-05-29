//go:build !linux && !darwin && !freebsd && !windows

package disk

// platformGetMounts is a no-op on platforms without a mount-enumeration
// implementation — the diskspace optional-paths feature contributes
// nothing there.
func platformGetMounts() []MountInfo { return nil }
