//go:build unix

package disk

import (
	"fmt"
	"syscall"
)

// availableSpace is the unix-family (Linux + Darwin + BSDs)
// implementation of [Provider.GetAvailableSpace]. Returns the bytes
// available to a non-root user on the filesystem hosting `path`.
//
// `Bavail` is preferred over `Bfree` to match the reservation-aware
// reading C# `DriveInfo.AvailableFreeSpace` returns on .NET Core
// (which itself calls `statvfs(3)` under the hood on POSIX).
//
// Errors are wrapped with the package prefix; the caller usually
// just `if err == nil`s the result (per `IDiskProvider.GetAvailableSpace`'s
// `long?` semantic) but the wrap keeps the diagnostic chain intact for
// log lines.
func availableSpace(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("disk: GetAvailableSpace(%q): %w", path, err)
	}
	// stat.Bavail and stat.Bsize have different concrete types on
	// linux (both int64) vs darwin (uint64 + int32). Casting both to
	// int64 first lets the multiplication run in a single signed
	// type without silently overflowing on a 32-bit Bsize.
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

// totalSize is the unix-family implementation of
// [Provider.GetTotalSize]. Mirrors the C# Mono-side
// `DiskProvider.cs:226-233` which reads `mount.TotalSize` —
// `f_blocks * f_frsize` per the Linux statvfs(3) man page.
//
// `Blocks` is the data + metadata + reserved-for-root total (matching
// what `df` reports as the "Size" column); `Bsize` is the
// fundamental block size. Both cast to int64 before multiplication
// for cross-platform consistency (Bsize is uint64 on Linux, int32
// on darwin).
func totalSize(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("disk: GetTotalSize(%q): %w", path, err)
	}
	return int64(stat.Blocks) * int64(stat.Bsize), nil
}
