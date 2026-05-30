// Package disk is the Go port of `NzbDrone.Common.Disk.IDiskProvider`
// — the concrete filesystem accessor the rest of Sonarr uses to read,
// write, walk, and inspect on-disk paths. Phase 3 lands the slice
// DiskScanService needs (8 methods); the wider IDiskProvider surface
// (mounts, copy/move, hardlinks, symlink resolution, cross-device
// fallback) ports in subsequent slices as their callers reach Phase 3.
//
// # Platform shape
//
// Go's `os` and `path/filepath` already abstract the cross-platform
// path concerns the C# tree handles via `OsPath` and `PathExtensions`.
// Callers pass the path shape that's appropriate for the running OS;
// this package does not transcode separators. The OsPath helpers in
// `internal/ospath` remain available for callers that need to reason
// about both shapes (e.g. import/move workflows that drag a Windows
// path through a Linux daemon).
//
// # SetPermissions is platform-aware
//
// The Linux build enables real chmod(2)+chown(2). On macOS / Windows
// SetPermissions is a debug-logged no-op — matches the C# tree's
// runtime-OS branch at `DiskProviderBase.cs` and the Mono-only
// `Mono/Disk/DiskProvider.cs:69-114` chmod/chown impl, except that the
// Go port runs the no-op at compile-time via build tags rather than
// at runtime. The visible behaviour to a caller is identical:
// SetPermissions returns nil and the on-disk perms are unchanged on
// non-Linux, exactly as the C# WindowsDiskProvider override does.
package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Provider is the concrete filesystem implementation.
//
// Goroutine-safe — every method does its own I/O without touching
// shared state. The single Provider value is shared process-wide
// because there's no per-call configuration to thread.
type Provider struct {
	log *slog.Logger
}

// NewProvider constructs a Provider. log is used for the per-method
// best-effort warnings (RemoveEmptySubfolders per-subdir failures,
// non-Linux SetPermissions skip notice). Pass slog.Default() if you
// don't have a per-process logger.
func NewProvider(log *slog.Logger) *Provider {
	if log == nil {
		log = slog.Default()
	}
	return &Provider{log: log}
}

// FolderExists ports `DiskProviderBase.cs:95-99`. Returns false on
// any stat error — non-existent path, permission denied, or the path
// pointing at a regular file. The C# version throws on a malformed
// path; the Go port leans on os.Stat's error which the caller treats
// as "no folder" uniformly.
func (p *Provider) FolderExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.IsDir()
}

// FolderEmpty ports `DiskProviderBase.cs:146-151`. Returns true only
// when ReadDir succeeds AND yields zero entries. A read error
// (missing folder, EACCES) returns false — DiskScanService gates
// FolderEmpty behind FolderExists so a "missing folder mid-scan"
// race lands in the safer post-condition (don't run a destructive
// DeleteFolder).
func (p *Provider) FolderEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

// GetFiles ports `DiskProviderBase.cs:164-174`. Returns absolute
// paths of every regular file under path; recursive=true descends.
// Inaccessible subdirectories during the walk are silently skipped
// (the callback returns nil on err) — matches the C# `IgnoreInaccessible`
// EnumerationOptions setting. Returns a nil-safe empty slice on a
// successful walk that found no files; errors from the initial stat
// propagate wrapped.
func (p *Provider) GetFiles(path string, recursive bool) ([]string, error) {
	if !recursive {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("disk: GetFiles(%q): %w", path, err)
		}
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			out = append(out, filepath.Join(path, e.Name()))
		}
		return out, nil
	}
	if st, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("disk: GetFiles(%q, recursive): %w", path, err)
	} else if !st.IsDir() {
		return nil, fmt.Errorf("disk: GetFiles(%q, recursive): not a directory", path)
	}
	out := []string{}
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip inaccessible entries silently (C# IgnoreInaccessible).
			return nil
		}
		if d.IsDir() {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("disk: GetFiles(%q, recursive): %w", path, err)
	}
	return out, nil
}

// CreateFolder ports `DiskProviderBase.cs:215-219`. Idempotent —
// the C# `Directory.CreateDirectory` is a no-op for an existing
// folder, and `os.MkdirAll` matches that behaviour. New folders are
// created with mode 0755; the optional SetPermissions call after
// CreateFolder rewrites the perms when SetPermissionsLinux is set.
func (p *Provider) CreateFolder(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("disk: CreateFolder(%q): %w", path, err)
	}
	return nil
}

// RemoveEmptySubfolders ports `DiskProviderBase.cs:533-552` —
// depth-first sweep that deletes every subdirectory of path whose
// post-recursion contents are empty. The top-level path itself is
// not deleted (DiskScanService pairs this with a FolderEmpty +
// DeleteFolder for that). Per-subdir errors are logged via
// slog.Warn and swallowed so the sweep continues, matching the C#
// per-iteration try/catch at `:543-549`.
func (p *Provider) RemoveEmptySubfolders(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("disk: RemoveEmptySubfolders(%q): %w", path, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(path, e.Name())
		if err := p.RemoveEmptySubfolders(sub); err != nil {
			p.log.Warn("disk: failed to descend into directory", "path", sub, "err", err)
			continue
		}
		subEntries, err := os.ReadDir(sub)
		if err != nil {
			p.log.Warn("disk: failed to re-read directory after sweep", "path", sub, "err", err)
			continue
		}
		if len(subEntries) == 0 {
			if err := os.Remove(sub); err != nil {
				p.log.Warn("disk: failed to remove empty directory", "path", sub, "err", err)
			}
		}
	}
	return nil
}

// DeleteFolder ports `DiskProviderBase.cs:306-332`. recursive=true
// removes path and everything under it; recursive=false errors if
// path is non-empty. The C# version retries up to 3 times with 3s
// sleeps to dodge Windows AV/indexer holds — the Go port skips that
// retry loop because:
//
//   - Linux/macOS targets do not exhibit the post-Empty race the C#
//     workaround addresses.
//   - The retry would mask real errors (a network mount that loses
//     connectivity mid-delete).
//   - When Windows-as-a-first-class-target lands, this is the place
//     to add the same retry shape behind a build tag.
func (p *Provider) DeleteFolder(path string, recursive bool) error {
	if recursive {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("disk: DeleteFolder(%q, recursive): %w", path, err)
		}
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("disk: DeleteFolder(%q): %w", path, err)
	}
	return nil
}

// EmptyFolder deletes every entry (files + subdirectories) inside path
// while keeping path itself. Ports C# `IDiskProvider.EmptyFolder`
// (`Common/Disk/DiskProviderBase.cs`). A non-existent path is a no-op
// (nothing to empty). Deletion is best-effort: per-entry errors are
// collected and joined so one undeletable file (e.g. locked on Windows)
// doesn't abort the rest — matching the C# loop that deletes each file
// then each subfolder.
func (p *Provider) EmptyFolder(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("disk: EmptyFolder(%q): %w", path, err)
	}
	var errs []error
	for _, e := range entries {
		full := filepath.Join(path, e.Name())
		if e.IsDir() {
			if rmErr := os.RemoveAll(full); rmErr != nil {
				errs = append(errs, rmErr)
			}
			continue
		}
		if rmErr := os.Remove(full); rmErr != nil {
			errs = append(errs, rmErr)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("disk: EmptyFolder(%q): %w", path, errors.Join(errs...))
	}
	return nil
}

// SetPermissions ports the C# tree's two-stage SetPermissions call
// (`Mono/Disk/DiskProvider.cs:69-114` for the Linux body, the no-op
// Windows override). Linux performs chmod(2) + (when group is
// non-empty) chown(2) by group name; macOS / Windows / other-GOOS
// builds debug-log and return nil.
//
// mask must be an octal string ("755", "0755", "2755"); 3-char masks
// preserve setuid/setgid/sticky bits from the existing inode (mirrors
// the C# `mask.Length < 4` branch). 4-char masks set the high bits
// verbatim. Empty mask returns an error.
//
// chown sets gid only — uid stays untouched. Mirrors C#
// `Syscall.chown(path, -1, groupId)` at `:108`.
func (p *Provider) SetPermissions(path, mask, group string) error {
	return p.setPermissions(path, mask, group)
}

// GetFileSize ports `DiskProviderBase.cs:183-213`. Symlinks are
// followed via os.Stat (Go's default), matching the C#
// `fi.ResolveLinkTarget(true)` branch. Missing files surface as a
// wrapped error rather than the C# `FileNotFoundException` panic.
// Directories surface as an error so the size-update loop doesn't
// silently treat a stale row as a 4KB directory inode.
func (p *Provider) GetFileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("disk: GetFileSize(%q): %w", path, err)
	}
	if st.IsDir() {
		return 0, fmt.Errorf("disk: GetFileSize(%q): is a directory", path)
	}
	return st.Size(), nil
}

// FileGetLastWrite returns the file's last-modified time. Mirrors C#
// `IDiskProvider.FileGetLastWrite(string)` at
// `Common/Disk/IDiskProvider.cs` — used by
// [internal/episodeimport.NotUnpackingSpecification] to detect files
// that are still being unpacked (recent mtime → likely in flight).
//
// Returns the wrapped os.Stat error so callers can classify
// (`errors.Is(err, fs.ErrNotExist)` etc).
func (p *Provider) FileGetLastWrite(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, fmt.Errorf("disk: FileGetLastWrite(%q): %w", path, err)
	}
	return st.ModTime(), nil
}

// FileSetLastWriteTime mirrors `IDiskProvider.FileSetLastWriteTime` at
// `Common/Disk/IDiskProvider.cs`. Used by the slice-3.BK
// [episodefilemoving.UpdateService.ChangeFileDateForFile] to stamp
// the configured-air-date mtime onto a renamed file. The atime
// argument is set to the same value as mtime — Sonarr does not track
// access times (matches C# behaviour at the corresponding
// `File.SetLastWriteTime(...)` call).
func (p *Provider) FileSetLastWriteTime(path string, t time.Time) error {
	if err := os.Chtimes(path, t, t); err != nil {
		return fmt.Errorf("disk: FileSetLastWriteTime(%q): %w", path, err)
	}
	return nil
}

// FolderSetLastWriteTime mirrors `IDiskProvider.FolderSetLastWriteTime`
// at `Common/Disk/IDiskProvider.cs`. Used by the recycle-bin folder-move
// path (`RecycleBinProvider.DeleteFolder` at
// `src/NzbDrone.Core/MediaFiles/RecycleBinProvider.cs:59-60`) to stamp the
// recycle-time onto the moved folder so the retention sweep counts the
// window from when the folder was recycled. os.Chtimes operates on
// directories the same as on files; the atime argument is set equal to
// mtime (Sonarr does not track access times).
func (p *Provider) FolderSetLastWriteTime(path string, t time.Time) error {
	if err := os.Chtimes(path, t, t); err != nil {
		return fmt.Errorf("disk: FolderSetLastWriteTime(%q): %w", path, err)
	}
	return nil
}

// GetDirectories ports `DiskProviderBase.cs:153-162`. Returns
// absolute paths of every immediate subdirectory of path. Top-level
// only — non-recursive — matching the C# `Directory.EnumerateDirectories(path, "*")`
// shape.
//
// Inaccessible entries are silently skipped (the C# version sets
// `IgnoreInaccessible = true` on the EnumerationOptions; the Go port
// matches by skipping any entry that errors on read). Errors from
// the initial os.ReadDir propagate wrapped — a missing or
// non-directory path is the caller's problem to classify.
//
// The C# version also sets `AttributesToSkip = FileAttributes.System`;
// the Go port skips that filter because Linux/macOS targets don't
// have the System attribute, and a Windows port can layer the
// filter behind a build tag without changing this signature.
//
// Returns a nil-safe empty slice when the path is empty rather than
// nil — callers can range over the result without a length check.
func (p *Provider) GetDirectories(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("disk: GetDirectories(%q): %w", path, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(path, e.Name()))
	}
	return out, nil
}

// FolderWritable mirrors `DiskProviderBase.cs:127-144`. Probes write
// access by creating + immediately deleting a sentinel file inside
// the folder. Returns true on success, false on any error (missing
// folder, EACCES, EROFS, full filesystem). The sentinel content
// matches the C# message verbatim so the file is recognisable to
// users who interrupt the test mid-flight.
//
// Sentinel filename also matches the C# tree
// (`sonarr_write_test.txt`); preserving the exact name keeps any
// out-of-band scripts that grep server FS state cross-compatible
// between the C# and Go releases.
//
// Failures are debug-logged so a node-by-node troubleshooter can
// trace WHICH path the writable check rejected without flooding the
// info log on every healthcheck cycle.
func (p *Provider) FolderWritable(path string) bool {
	test := filepath.Join(path, "sonarr_write_test.txt")
	content := []byte(fmt.Sprintf("This file was created to verify if '%s' is writable. It should've been automatically deleted. Feel free to delete it.", path))
	if err := os.WriteFile(test, content, 0o644); err != nil {
		p.log.Debug("disk: FolderWritable probe failed on write", "path", path, "err", err)
		return false
	}
	if err := os.Remove(test); err != nil {
		p.log.Debug("disk: FolderWritable probe failed on delete", "path", path, "err", err)
		return false
	}
	return true
}

// GetTotalSize returns the total bytes the filesystem hosting `path`
// reports. Mirrors C# `IDiskProvider.GetTotalSize(string)` at
// `Common/Disk/IDiskProvider.cs:15` + the Mono-side
// `DiskProvider.cs:226-233` impl which routes through GetMount.TotalSize
// (statvfs `f_blocks * f_frsize`).
//
// The C# return is `long?` — null on a missing-mount / permission
// error. The Go port returns `(int64, error)` so the rootfolders
// caller can `if err == nil` and treat any error as "couldn't
// determine, surface defensively" (matches C# `mount?.TotalSize`
// = nil-on-failure).
//
// Implementation lives in build-tag-split files (`provider_unix.go` /
// `provider_windows.go`) so the native syscall path is selected at
// compile time. The dispatcher here delegates to `totalSize`.
func (p *Provider) GetTotalSize(path string) (int64, error) {
	return totalSize(path)
}

// GetAvailableSpace returns the free space on the filesystem hosting
// `path`, in bytes. Mirrors C# `IDiskProvider.GetAvailableSpace(string)`
// at `Common/Disk/IDiskProvider.cs`.
//
// The C# return is `long?` — null on a path-not-found / permission
// error. The Go port returns `(int64, error)` so the rootfolders
// caller (and import-side FreeSpaceSpecification) can `if err == nil`
// and treat any error as "couldn't determine, accept defensively"
// (matching C# `!freeSpace.HasValue`).
//
// Implementation lives in build-tag-split files
// (`provider_unix.go` / `provider_windows.go`) so the native syscall
// path is selected at compile time. The dispatcher here delegates to
// `availableSpace`.
func (p *Provider) GetAvailableSpace(path string) (int64, error) {
	return availableSpace(path)
}
