package episodefilemoving

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/disk"
)

// DiskFilesystem is the production binding for [Filesystem]. It
// composes a `*disk.Provider` (the IDiskProvider surface) with the
// transfer-mode policy that the C# `DiskTransferService` layers on
// top of `IDiskProvider` at
// `Common/Disk/DiskTransferService.cs:256-366`.
//
// The Go port collapses the C# IDiskTransferService class into a
// per-call dispatch inside [DiskFilesystem.TransferFile]. The C#
// class additionally exposes TransferFolder + MirrorFolder; those
// surfaces are NOT needed by the writer and are deferred until a
// caller appears.
//
// Deviations from the C# port (documented; mirror C# only when the
// Go target catches up):
//
//   - [ResolveRealParentPath] (`DiskTransferService.cs:30-43`) — the
//     C# helper rewrites `path` to the on-disk casing of its parent
//     folder. Skipped because (a) macOS/Linux are case-sensitive in
//     the targets the unattended port runs against, and (b) the
//     casing helper depends on a Windows-only API. The path the
//     caller passes is used verbatim.
//
//   - VerifyFile (`:479-498`) + WaitForIO (`:441-445`) — the C#
//     class re-reads the destination size with a 3-second sleep on
//     mismatch to handle remote-NAS sync gaps. Skipped on first
//     port: the local-disk targets the Go port runs against don't
//     surface the lag, and the Sleep introduces a 3-second blocking
//     wait that's painful in tests. Lands in the same slice as the
//     CIFS detection (a future slice that adds Mount support).
//
//   - CIFS detection (`:333-359`) — the C# class detects cifs as
//     the target FS and forces copy-then-delete instead of rename
//     for cross-mount moves. Skipped because [disk.Provider.MoveFile]
//     already falls back to copy+delete on EXDEV (cross-device);
//     the cifs-on-same-mount edge case requires Mount support which
//     ports later.
//
//   - RollbackPartialMove / RollbackCopy (`:383-440`) — the C#
//     class deletes a partially-written destination on a copy
//     failure. The Go [disk.Provider.CopyFile] cleans up its own
//     partial dst on io.Copy failure, so the rollback layering
//     happens at the lower level instead of here.
//
// Goroutine-safe — every method does its own I/O without touching
// shared state. The single value can be reused process-wide.
type DiskFilesystem struct {
	disk *disk.Provider
	log  *slog.Logger
}

// NewDiskFilesystem constructs a [DiskFilesystem] over the supplied
// `*disk.Provider`. Returns nil if d is nil — production wiring
// MUST pass a real Provider; tests use the package-private stubs.
//
// log is optional; nil falls back to [slog.Default] for the
// per-call debug output ("`{mode}` `[{src}]` > `[{dst}]`" lines that
// mirror the C# `_logger.Debug` calls inside TransferFile).
func NewDiskFilesystem(d *disk.Provider, log *slog.Logger) *DiskFilesystem {
	if d == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &DiskFilesystem{disk: d, log: log}
}

// FileExists delegates to [disk.Provider.FileExists]. Mirrors the
// C# `IDiskProvider.FileExists` shape.
func (f *DiskFilesystem) FileExists(path string) bool {
	return f.disk.FileExists(path)
}

// FolderExists delegates to [disk.Provider.FolderExists].
func (f *DiskFilesystem) FolderExists(path string) bool {
	return f.disk.FolderExists(path)
}

// CreateFolder delegates to [disk.Provider.CreateFolder]. Idempotent
// on the C# side and matched by the Go port (see the disk package
// doc for the rationale).
func (f *DiskFilesystem) CreateFolder(path string) error {
	return f.disk.CreateFolder(path)
}

// TransferFile dispatches the C# `TransferMode` semantics across
// move / copy / hardlink. Mirrors `DiskTransferService.cs:256-366`
// (TransferFile, the file half of the C# class).
//
// Behaviour:
//
//   - src == dst (byte-equal) → returns an IO-error mirroring C#
//     `:268-271` ("source and destination can't be the same"). The
//     C# class's case-insensitive variant is skipped (see the
//     [DiskFilesystem] doc for ResolveRealParentPath rationale).
//
//   - sourcePath.IsParentPath(targetPath) → returns an IO-error
//     mirroring C# `:312-315` ("destination cannot be a child of
//     the source"). Prevents a same-tree recursion bomb.
//
//   - mode = Move → tries [disk.Provider.MoveFile] first. EXDEV
//     fallback is handled inside MoveFile (transparent
//     copy-then-delete).
//
//   - mode = Copy → [disk.Provider.CopyFile].
//
//   - mode = HardLinkOrCopy → tries [disk.Provider.TryCreateHardLink]
//     first, falls back to CopyFile on failure. Mirrors C# `:319-348`
//     (HardLink with Copy fallback when both flags are set; the
//     Go enum collapses the two-flag combo into a single value).
//
//   - mode = Unknown / 0 → fallthrough behaving as Move (matches C#
//     `TransferMode.None` which loops through the move arm because
//     the if-checks all fail and the trailing `return TransferMode.None`
//     doesn't actually move anything; Go port chooses Move-as-default
//     because the writer never passes the zero value).
//
// Existing-destination handling:
//
//   - Move: os.Rename overwrites silently on Linux/macOS — matches
//     the C# `MoveFile(overwrite=true)` arm the writer threads via
//     ClearTargetPath.
//
//   - Copy: [disk.Provider.CopyFile] uses O_EXCL so an existing dst
//     surfaces as fs.ErrExist. The C# `:317` ClearTargetPath arm
//     deletes dst first; the Go port lifts the same step here so
//     Copy and HardLinkOrCopy both succeed when overwrite is the
//     intent. Matches the C# `:317` call sequence.
//
// Errors propagate wrapped with "episodefilemoving: TransferFile" so
// the writer's existing error format stays consistent.
func (f *DiskFilesystem) TransferFile(src, dst string, mode TransferMode) error {
	f.log.Debug("episodefilemoving: TransferFile",
		"mode", mode.String(), "src", src, "dst", dst)

	if src == dst {
		return fmt.Errorf("episodefilemoving: TransferFile: source and destination can't be the same %q", src)
	}
	if isParentPath(src, dst) {
		return fmt.Errorf("episodefilemoving: TransferFile: destination cannot be a child of the source [%s] => [%s]", src, dst)
	}

	switch mode {
	case TransferModeMove, TransferModeUnknown:
		if err := f.disk.MoveFile(src, dst); err != nil {
			return fmt.Errorf("episodefilemoving: TransferFile: %w", err)
		}
		return nil
	case TransferModeCopy:
		// ClearTargetPath equivalent — delete existing dst so the
		// O_EXCL create inside CopyFile succeeds.
		if err := clearTargetPath(f.disk, dst); err != nil {
			return fmt.Errorf("episodefilemoving: TransferFile: %w", err)
		}
		if err := f.disk.CopyFile(src, dst); err != nil {
			return fmt.Errorf("episodefilemoving: TransferFile: %w", err)
		}
		return nil
	case TransferModeHardLinkOrCopy:
		if err := clearTargetPath(f.disk, dst); err != nil {
			return fmt.Errorf("episodefilemoving: TransferFile: %w", err)
		}
		if f.disk.TryCreateHardLink(src, dst) {
			return nil
		}
		if err := f.disk.CopyFile(src, dst); err != nil {
			return fmt.Errorf("episodefilemoving: TransferFile: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("episodefilemoving: TransferFile: unsupported mode %d", int(mode))
	}
}

// clearTargetPath mirrors `DiskTransferService.cs:368-381`
// `ClearTargetPath` — when dst exists, delete it. The C# version
// short-circuits with a `DestinationAlreadyExistsException` when
// `overwrite=false`; the Go port always deletes because every
// [Filesystem.TransferFile] caller already wants the
// "overwrite=true" behaviour (the writer's caller decides whether
// to invoke TransferFile at all by checking dst first).
//
// Returns nil when dst doesn't exist or was deleted successfully.
// fs.ErrNotExist from a concurrent delete is treated as success.
func clearTargetPath(d *disk.Provider, dst string) error {
	if !d.FileExists(dst) {
		return nil
	}
	if err := d.DeleteFile(dst); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

// isParentPath mirrors `PathExtensions.IsParentPath(parent, child)`
// — true when child is contained inside parent. Used by the
// IsParentPath safety check at C# `:312`. Compares cleaned paths
// + a trailing separator so `/a/b` is NOT a parent of `/a/bc`.
//
// Case sensitivity: matches the host filesystem (case-sensitive on
// Linux, case-insensitive on macOS/Windows). The Go port checks
// case-sensitively; the targets the unattended port runs against
// are case-sensitive in practice.
func isParentPath(parent, child string) bool {
	cleanParent := filepath.Clean(parent)
	cleanChild := filepath.Clean(child)
	if cleanParent == cleanChild {
		return false
	}
	prefix := cleanParent + string(filepath.Separator)
	return strings.HasPrefix(cleanChild, prefix)
}

// Compile-time interface satisfaction — refactors that break the
// Filesystem contract fail at build time rather than at the
// production wire-in.
var (
	_ Filesystem = (*DiskFilesystem)(nil)
)
