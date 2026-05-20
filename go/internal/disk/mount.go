package disk

// Mount is the subset of mount-point info the healthcheck layer
// consumes. Mirrors `NzbDrone.Common.Disk.IMount` (subset):
//
//   - C# `IMount.Name` → [Mount.Name] (human-readable identifier:
//     the mount-point path on Unix, the volume label on Windows).
//   - C# `IMount.RootDirectory` → [Mount.RootDirectory] (the mount
//     root path).
//   - C# `IMount.MountOptions.IsReadOnly` → [Mount.IsReadOnly] —
//     true iff the mount is mounted with the read-only flag.
//
// The full C# `IMount` surface (DriveType, AvailableFreeSpace,
// TotalSize, VolumeLabel, DriveFormat, IsRoot) isn't ported in this
// slice — none of the currently-ported healthchecks consume them.
// Future consumers can extend Mount in place.
type Mount struct {
	Name          string
	RootDirectory string
	IsReadOnly    bool
}

// GetMount returns the [Mount] backing the given path. The actual
// implementation is platform-specific (mount_unix.go for Linux +
// macOS via golang.org/x/sys/unix; mount_windows.go for Windows
// via GetVolumeInformation). Returns nil + error when the lookup
// fails (e.g. path doesn't exist, syscall returned an error).
//
// Mirrors `NzbDrone.Common.Disk.DiskProviderBase.GetMount(string)`
// at `DiskProviderBase.cs:491-525`. The C# version handles symlinks
// + bind mounts via mount-table parsing; the Go-port slice uses
// statfs directly, which DOES follow symlinks (stat semantic).
// Bind-mount detection is a follow-up if a downstream report
// surfaces a gap.
func (p *Provider) GetMount(path string) (*Mount, error) {
	return platformGetMount(path)
}
