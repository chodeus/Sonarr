package disk

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileExists reports whether the supplied path is a regular file.
//
// Mirrors `IDiskProvider.FileExists(string)` at the C# side
// (`Common/Disk/DiskProviderBase.cs:88-93`). False on any stat error
// — non-existent path, permission denied, or the path being a
// directory. Symlinks are followed (Go's os.Stat default), matching
// the C# `File.Exists` semantics.
func (p *Provider) FileExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.Mode().IsRegular()
}

// MoveFile ports `DiskProviderBase.cs:419-447`. Move via os.Rename;
// on cross-device errors (EXDEV — common when src and dst are on
// different filesystems) the call falls back to copy-then-delete so
// the C# `IOException("source and target on different drives")` arm
// is replaced by a transparent fallback.
//
// The C# version takes an `overwrite` parameter; the Go port handles
// overwrite separately at the [DiskTransferService] policy layer
// (the C# `ClearTargetPath` shape) so MoveFile here always errors if
// dst already exists. Callers that want overwrite semantics should
// remove dst first.
//
// Errors propagate wrapped. Errors from the rename arm preserve the
// original syscall error for `errors.Is(err, fs.ErrExist)` etc.
func (p *Provider) MoveFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		// Cross-device rename — copy-then-delete fallback.
		if isCrossDevice(err) {
			if cerr := p.CopyFile(src, dst); cerr != nil {
				return fmt.Errorf("disk: MoveFile(%q, %q): cross-device fallback copy: %w", src, dst, cerr)
			}
			if rerr := os.Remove(src); rerr != nil {
				return fmt.Errorf("disk: MoveFile(%q, %q): cross-device fallback delete: %w", src, dst, rerr)
			}
			return nil
		}
		return fmt.Errorf("disk: MoveFile(%q, %q): %w", src, dst, err)
	}
	return nil
}

// CopyFile ports `DiskProviderBase.cs:412-417`. Streams src → dst
// via io.Copy; preserves the source's file mode bits. Errors from
// open/create/copy propagate wrapped.
//
// The C# version takes an `overwrite` parameter; the Go port leaves
// overwrite to the [DiskTransferService] policy layer (mirrors the
// C# `ClearTargetPath` shape). The dst create uses O_EXCL so an
// existing dst surfaces as fs.ErrExist; callers wanting overwrite
// semantics must DeleteFile(dst) first.
func (p *Provider) CopyFile(src, dst string) error {
	srcF, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("disk: CopyFile open src(%q): %w", src, err)
	}
	defer func() { _ = srcF.Close() }()

	srcInfo, err := srcF.Stat()
	if err != nil {
		return fmt.Errorf("disk: CopyFile stat src(%q): %w", src, err)
	}

	dstF, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, srcInfo.Mode().Perm())
	if err != nil {
		return fmt.Errorf("disk: CopyFile open dst(%q): %w", dst, err)
	}

	if _, err := io.Copy(dstF, srcF); err != nil {
		_ = dstF.Close()
		// Best-effort cleanup so a partial file isn't left behind.
		_ = os.Remove(dst)
		return fmt.Errorf("disk: CopyFile io.Copy(%q→%q): %w", src, dst, err)
	}
	if err := dstF.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("disk: CopyFile close dst(%q): %w", dst, err)
	}
	return nil
}

// TryCreateHardLink ports `DiskProviderBase.cs:478-499`. Returns
// true on success, false on any failure (cross-device, target FS
// doesn't support hardlinks, dst already exists). Mirrors the C#
// `bool` return — failures are policy-handled by the caller (the
// HardLinkOrCopy mode falls back to copy).
//
// Logs at debug on failure so cross-device hard-link attempts don't
// drown the production log; the caller's fallback path is the
// observable signal.
func (p *Provider) TryCreateHardLink(src, dst string) bool {
	if err := os.Link(src, dst); err != nil {
		p.log.Debug("disk: TryCreateHardLink failed", "src", src, "dst", dst, "err", err)
		return false
	}
	return true
}

// MoveFolder ports `DiskProviderBase.cs:344-358`. Renames a folder
// (and everything inside it) from src to dst. Uses [os.Rename] which
// handles entire-directory rename as a single atomic operation when
// src + dst live on the same filesystem.
//
// Cross-device fallback: on EXDEV, falls back to a recursive copy of
// the directory tree via [filepath.Walk] + DeleteFolder(src, true).
// The fallback is slower (per-file copy) but functionally
// equivalent. Mirrors C# DiskTransferService's cross-mount handling.
//
// Errors propagate wrapped. The dst-already-exists case from
// os.Rename surfaces as the underlying syscall error so callers can
// `errors.Is(err, fs.ErrExist)`.
//
// Slice 5.D9 port pre-work for the eventual /api/v3/series/{id}?moveFiles=true
// cascade that fires when the React UI's rename-folder dialog
// supplies a new Path.
func (p *Provider) MoveFolder(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		if isCrossDevice(err) {
			if cerr := p.copyFolderRecursive(src, dst); cerr != nil {
				return fmt.Errorf("disk: MoveFolder(%q, %q): cross-device fallback copy: %w", src, dst, cerr)
			}
			if rerr := p.DeleteFolder(src, true); rerr != nil {
				return fmt.Errorf("disk: MoveFolder(%q, %q): cross-device fallback delete: %w", src, dst, rerr)
			}
			return nil
		}
		return fmt.Errorf("disk: MoveFolder(%q, %q): %w", src, dst, err)
	}
	return nil
}

// TransferFolder moves the CONTENTS of src into dst (creating dst if
// needed), then removes the now-empty src. Ports the move-mode of C#
// `DiskTransferService.TransferFolder(src, dst, TransferMode.Move)`
// (`Common/Disk/DiskTransferService.cs`) as used by the in-place updater
// to merge the extracted update-client tree into the sandbox root.
//
// Unlike [MoveFolder] (which renames src→dst and requires dst not to
// exist), TransferFolder merges into an EXISTING dst: each direct child
// of src is moved to dst/<name>. Subdirectories that already exist in
// dst are merged recursively; files are moved (cross-device-safe via
// [MoveFile]/[MoveFolder]). An existing dst FILE that collides with a
// src file is overwritten (the updater intends to replace it).
func (p *Provider) TransferFolder(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("disk: TransferFolder(%q, %q): mkdir dst: %w", src, dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("disk: TransferFolder(%q, %q): read src: %w", src, dst, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if _, derr := os.Stat(d); derr == nil {
				// dst subdir exists — merge recursively.
				if merr := p.TransferFolder(s, d); merr != nil {
					return merr
				}
				continue
			}
			if merr := p.MoveFolder(s, d); merr != nil {
				return merr
			}
			continue
		}
		// Regular file (or symlink): overwrite an existing dst.
		if _, derr := os.Stat(d); derr == nil {
			if rerr := os.Remove(d); rerr != nil {
				return fmt.Errorf("disk: TransferFolder(%q, %q): clear existing %q: %w", src, dst, d, rerr)
			}
		}
		if merr := p.MoveFile(s, d); merr != nil {
			return merr
		}
	}
	// Remove the now-(recursively-)empty src tree.
	if rerr := os.RemoveAll(src); rerr != nil {
		return fmt.Errorf("disk: TransferFolder(%q, %q): remove src: %w", src, dst, rerr)
	}
	return nil
}

// copyFolderRecursive walks src and replicates the directory tree
// rooted at dst, copying every regular file via CopyFile. Symlinks
// inside the tree are NOT followed — they're recreated as symlinks
// at dst (matches the C# DiskProvider's symlink-preserve behaviour
// at DiskProviderBase.cs:298-302). Empty directories are recreated
// with the source's mode bits.
//
// Errors abort the walk early — callers see the underlying file's
// error wrapped with the walk path for diagnostics.
func (p *Provider) copyFolderRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %q: %w", path, walkErr)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("rel %q: %w", path, err)
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
		case info.Mode()&os.ModeSymlink != 0:
			link, lerr := os.Readlink(path)
			if lerr != nil {
				return fmt.Errorf("readlink %q: %w", path, lerr)
			}
			if err := os.Symlink(link, target); err != nil {
				return fmt.Errorf("symlink %q→%q: %w", target, link, err)
			}
		default:
			if err := p.CopyFile(path, target); err != nil {
				return fmt.Errorf("copyfile %q→%q: %w", path, target, err)
			}
		}
		return nil
	})
}

// DeleteFile ports `DiskProviderBase.cs:359-371`. Removes a regular
// file. Errors propagate wrapped — callers can `errors.Is(err,
// fs.ErrNotExist)` to distinguish a missing file. The C# version
// also handles read-only attribute clearing on Windows; the Go port
// defers Windows-as-first-class to a later slice (the macOS/Linux
// targets the unattended port runs against don't surface the
// read-only blocker).
func (p *Provider) DeleteFile(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("disk: DeleteFile(%q): %w", path, err)
	}
	return nil
}

// isCrossDevice classifies an os.Rename error as the EXDEV /
// "different drive" arm. EXDEV is "invalid cross-device link" on
// Linux and "cross-device link" on macOS — both surfaces match the
// substring set below. Avoids a syscall.Errno test so the file
// stays GOOS-agnostic.
//
// The C# version surfaces cross-device as `IOException("different
// drives")`; the Go port turns it into a transparent
// copy-then-delete inside MoveFile, mirroring how
// DiskTransferService.cs:350-359 handles cross-cifs-mount moves
// (copy verified + delete source).
func isCrossDevice(err error) bool {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		err = linkErr.Err
	}
	msg := err.Error()
	return strings.Contains(msg, "cross-device link") ||
		strings.Contains(msg, "Cross-device link") ||
		strings.Contains(msg, "invalid cross-device link")
}
