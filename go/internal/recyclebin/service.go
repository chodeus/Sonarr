// Package recyclebin is the Go port of
// `NzbDrone.Core.MediaFiles.RecycleBinProvider`
// (`src/NzbDrone.Core/MediaFiles/RecycleBinProvider.cs:1-225`).
// Recycles or hard-deletes episode files (and other library
// artefacts) after the writer / upgrader has finished with them.
//
// This slice (3.76) lands the public [Service.DeleteFile] method —
// the one method `internal/upgrademediafiles` reaches for via its
// [upgrademediafiles.RecycleBinProvider] narrow interface (slice
// 3.72 noopRecycleBinProvider). Production wiring can now bind a
// real Service in place of the noop default.
//
// # Deferred
//
//   - DeleteFolder (`RecycleBinProvider.cs:41-68`) — requires
//     IDiskTransferService.TransferFolder which the Go disk port
//     hasn't shipped (slice 3.74 only ported TransferFile, the file
//     half of the C# DiskTransferService class). Lands when the
//     folder-transfer surface ports.
//   - Empty (`:139-160`) — requires
//     IDiskProvider.GetDirectories which `internal/disk/Provider`
//     hasn't shipped. Lands alongside the directory-walk surface.
//
// Slice 3.77 lands [Service.Cleanup] (`:162-200`) — the
// `IExecute<CleanUpRecycleBinCommand>` handler delegates here. The
// remaining two deferred surfaces are documented at type-level on
// [Service] so a future slice has a clear plan-of-attack pointer.
//
// # Wiring
//
// Production wiring composes:
//
//   - `*config.Provider` — exposes RecycleBin() at
//     `internal/config/provider.go:RecycleBin` (added in this slice).
//   - `*disk.Provider` — exposes DeleteFile / CreateFolder /
//     FileExists / MoveFile from slice 3.74's transfer.go +
//     provider.go.
//
// The Service composes these into the
// [upgrademediafiles.RecycleBinProvider] shape (one method —
// DeleteFile(path, subfolder) → recycleBinPath, error). A
// compile-time guard in service_test.go asserts the satisfaction.
package recyclebin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

// ConfigReader narrows `IConfigService` for the two RecycleBin
// getters [Service.DeleteFile] and [Service.Cleanup] reach for.
// Production binding wires `*config.Provider` directly — its
// RecycleBin + RecycleBinCleanupDays getters at
// `internal/config/provider.go` match this surface.
type ConfigReader interface {
	// RecycleBin returns the configured recycle-bin folder path.
	// Empty string disables the feature — DeleteFile falls back to a
	// hard delete via [DiskOps.DeleteFile], Cleanup early-returns
	// matching C# `RecycleBinProvider.cs:164-167`.
	RecycleBin() string
	// RecycleBinCleanupDays returns the retention window in days.
	// Zero disables Cleanup entirely — matches the C#
	// `cleanupDays == 0` short-circuit at
	// `RecycleBinProvider.cs:172-176`. Default at `*config.Provider`
	// is 7 days per C# `ConfigService.cs:108`.
	RecycleBinCleanupDays() int
}

// DiskOps narrows `IDiskProvider` for the seven methods
// [Service.DeleteFile] and [Service.Cleanup] reach for. Production
// binding wires `*disk.Provider` — every method exists at the disk
// package after slice 3.74's transfer primitives landed.
//
// FileSetLastWriteTime is NOT included because the C# class's
// SetLastWriteTime helper at `:202-218` is best-effort (any IO error
// is swallowed). The Go port collapses the post-recycle mtime
// update into a no-op until the disk port adds a
// FileSetLastWriteTime primitive — the recycle-bin file shows up
// with whatever mtime os.Rename / os.Link copied, which is exactly
// the move-time mtime the operator expects.
type DiskOps interface {
	// DeleteFile removes a regular file. Used by the
	// "RecycleBin disabled" arm of DeleteFile and the per-file
	// expiry pass inside Cleanup.
	DeleteFile(path string) error
	// CreateFolder makes the destination subfolder under the
	// recycle-bin root.
	CreateFolder(path string) error
	// FileExists reports whether the path is a regular file. Used
	// by the destination-collision loop that suffixes "_2", "_3",
	// etc. when an earlier recycle landed at the same destination.
	FileExists(path string) bool
	// MoveFile renames src → dst. Used by the
	// "RecycleBin enabled" arm of DeleteFile.
	MoveFile(src, dst string) error
	// GetFiles returns absolute paths of every regular file under
	// path. recursive=true descends. Used by Cleanup to enumerate
	// recycle-bin contents.
	GetFiles(path string, recursive bool) ([]string, error)
	// FileGetLastWrite returns the file's last-modified time. Used
	// by Cleanup to test the per-file retention window.
	FileGetLastWrite(path string) (time.Time, error)
	// RemoveEmptySubfolders sweeps path bottom-up, deleting any
	// subfolder that ended up empty after Cleanup's per-file pass.
	RemoveEmptySubfolders(path string) error
}

// RecycleBinError mirrors `RecycleBinException` raised at the C#
// `CreateFolder` and `TransferFile` arms (`RecycleBinProvider.cs:103,
// :128`). Wraps the underlying disk error so callers can
// `errors.Is(err, ErrRecycleBin)` to classify the failure.
type RecycleBinError struct {
	Op  string // "create-folder" / "move-file"
	Src string
	Dst string
	Err error
}

func (e *RecycleBinError) Error() string {
	if e.Op == "create-folder" {
		return fmt.Sprintf("recyclebin: unable to create folder %q for the file %q: %v", e.Dst, filepath.Base(e.Src), e.Err)
	}
	return fmt.Sprintf("recyclebin: unable to move %q to the recycling bin %q: %v", e.Src, e.Dst, e.Err)
}

func (e *RecycleBinError) Unwrap() error { return e.Err }

// ErrRecycleBin is the sentinel callers can `errors.Is` against.
// Mirrors the C# `RecycleBinException` shape — every
// [RecycleBinError] wraps this so a single Is check classifies the
// failure regardless of which inner step (CreateFolder / MoveFile)
// triggered it.
//
// The orchestrator at `episodeimport/import_approved.go` already
// has an [episodeimport.ErrRecycleBin] sentinel for the upgrader's
// DeleteFile failure arm — this package's sentinel is the
// disk-level cousin. The orchestrator catches via the upgrader's
// returned error so a future binding can bridge the two with an
// errors.Join or errors.Is wrapper if needed.
var ErrRecycleBin = errors.New("recyclebin: failed")

// Service is the C# `RecycleBinProvider` port. Stateless — every
// method delegates to the bound config / disk dependencies, so a
// single Service value is reused process-wide.
//
// Goroutine-safe — no mutable state.
type Service struct {
	cfg  ConfigReader
	disk DiskOps
	log  *slog.Logger
}

// NewService constructs a Service. cfg and disk must be non-nil;
// nil falls through to a nil Service so a misconfigured production
// binding fails at the next call site rather than silently
// no-opping. log is optional — nil falls back to [slog.Default].
//
// Mirrors the Phase-3 nil-Service-on-nil-dep convention from
// episodeimport/aggregation.go and mediafileattributes.NewService.
func NewService(cfg ConfigReader, disk DiskOps, log *slog.Logger) *Service {
	if cfg == nil || disk == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{cfg: cfg, disk: disk, log: log}
}

// DeleteFile ports `RecycleBinProvider.cs:70-137`. Recycles a file —
// or hard-deletes it when the RecycleBin config is empty.
//
// Behaviour:
//
//   - RecycleBin == ""           → hard-delete via [DiskOps.DeleteFile].
//     Mirrors C# `:75-88`. Returns ("", nil) on success.
//
//   - RecycleBin != ""           → CreateFolder(<bin>/<subfolder>),
//     suffix-loop to find a non-colliding destination, MoveFile
//     src → dst. Mirrors C# `:89-136`. Returns (dst, nil) on
//     success.
//
// Errors:
//
//   - CreateFolder failure → wrapped in [RecycleBinError]
//     (Op="create-folder"). errors.Is(err, ErrRecycleBin) → true.
//     Mirrors C# `:100-104` `RecycleBinException` raise.
//
//   - MoveFile failure → wrapped in [RecycleBinError]
//     (Op="move-file"). errors.Is(err, ErrRecycleBin) → true.
//     Mirrors C# `:125-129` `RecycleBinException` raise.
//
//   - Hard-delete failure → propagated as-is (the C# `:84` call
//     doesn't wrap; the Go port preserves that surface).
//
// The C# `OsInfo.IsWindows` GetFileAttributes log line at `:79-82`
// is skipped (no Windows-first-class support). The post-recycle
// SetLastWriteTime helper at `:131` is also skipped — see [DiskOps]
// doc for why.
func (s *Service) DeleteFile(path, subfolder string) (string, error) {
	bin := strings.TrimSpace(s.cfg.RecycleBin())
	if bin == "" {
		s.log.Info("recyclebin: feature disabled, hard-deleting", "path", path)
		if err := s.disk.DeleteFile(path); err != nil {
			return "", err
		}
		s.log.Debug("recyclebin: file permanently deleted", "path", path)
		return "", nil
	}

	destFolder := filepath.Join(bin, subfolder)
	dest := filepath.Join(destFolder, filepath.Base(path))

	s.log.Debug("recyclebin: creating folder", "folder", destFolder)
	if err := s.disk.CreateFolder(destFolder); err != nil {
		s.log.Error("recyclebin: unable to create destination folder",
			"folder", destFolder, "file", filepath.Base(path), "err", err)
		return "", &RecycleBinError{
			Op:  "create-folder",
			Src: path,
			Dst: destFolder,
			Err: errors.Join(ErrRecycleBin, err),
		}
	}

	// Suffix-loop ports the C# `:106-118` collision-resolution.
	// When dest already exists we append "_2", "_3", … to the base
	// name (preserving the extension) until we find a free slot.
	dest = uniqueDest(s.disk, destFolder, filepath.Base(path), dest)

	s.log.Debug("recyclebin: moving to bin", "src", path, "dst", dest)
	if err := s.disk.MoveFile(path, dest); err != nil {
		s.log.Error("recyclebin: unable to move file to bin",
			"src", path, "dst", dest, "err", err)
		return "", &RecycleBinError{
			Op:  "move-file",
			Src: path,
			Dst: dest,
			Err: errors.Join(ErrRecycleBin, err),
		}
	}

	s.log.Debug("recyclebin: file moved", "dst", dest)
	return dest, nil
}

// Cleanup ports `RecycleBinProvider.cs:162-200`. Walks the
// configured recycle-bin folder and deletes every file whose
// last-write-time is older than [ConfigReader.RecycleBinCleanupDays]
// days. After the per-file pass, sweeps empty subfolders.
//
// Early-return arms (matches the C# short-circuits):
//
//   - RecycleBin == ""              → log "not configured, cannot
//     cleanup", return nil. Mirrors C# `:164-167`.
//   - RecycleBinCleanupDays == 0    → log "automatic cleanup
//     disabled", return nil. Mirrors C# `:172-176`.
//
// Per-file behaviour (matches C# `:183-195`):
//
//   - mtime + cleanupDays > now → file hasn't expired; log at debug,
//     skip.
//   - mtime + cleanupDays ≤ now → file expired; log at debug, call
//     [DiskOps.DeleteFile]. Per-file errors are logged at warn-level
//     and the loop continues so a single broken inode doesn't abort
//     the whole sweep — matches the C# tree's looser semantics
//     (the C# arm doesn't catch but `_logger.Debug` calls don't
//     unwind the foreach because the error path is at the
//     `_diskProvider.DeleteFile` syscall).
//
// Errors:
//
//   - GetFiles failure on the recycle-bin root → propagated wrapped
//     so the handler can log it. The C# tree doesn't catch
//     here either, so a missing recycle-bin root surfaces.
//   - RemoveEmptySubfolders failure → propagated wrapped (rare,
//     but we don't want to silently leave empty dirs).
//
// ctx is reserved for a future cancellation arm — the current C#
// loop has no cancellation surface, so the Go port checks ctx.Err()
// at the top of each iteration to stay responsive on a long sweep.
func (s *Service) Cleanup(ctx context.Context) error {
	bin := strings.TrimSpace(s.cfg.RecycleBin())
	if bin == "" {
		s.log.Info("recyclebin: feature not configured, cannot cleanup")
		return nil
	}

	cleanupDays := s.cfg.RecycleBinCleanupDays()
	if cleanupDays == 0 {
		s.log.Info("recyclebin: automatic cleanup disabled (cleanupDays=0)")
		return nil
	}

	s.log.Info("recyclebin: removing items older than retention window",
		"cleanup_days", cleanupDays, "bin", bin)

	files, err := s.disk.GetFiles(bin, true)
	if err != nil {
		return fmt.Errorf("recyclebin: Cleanup GetFiles(%q): %w", bin, err)
	}

	now := time.Now().UTC()
	expiry := time.Duration(cleanupDays) * 24 * time.Hour
	var removed, skipped int

	for _, file := range files {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("recyclebin: Cleanup cancelled: %w", cerr)
		}
		mtime, err := s.disk.FileGetLastWrite(file)
		if err != nil {
			// Mirrors the looser C# semantics — log + skip a single
			// broken inode rather than aborting the sweep.
			s.log.Warn("recyclebin: skip file with unreadable mtime",
				"file", file, "err", err)
			skipped++
			continue
		}
		if mtime.Add(expiry).After(now) {
			s.log.Debug("recyclebin: file hasn't expired yet, skipping",
				"file", file, "mtime", mtime)
			skipped++
			continue
		}
		s.log.Debug("recyclebin: file expired, deleting",
			"file", file, "mtime", mtime)
		if err := s.disk.DeleteFile(file); err != nil {
			s.log.Warn("recyclebin: per-file DeleteFile failed (continuing)",
				"file", file, "err", err)
			continue
		}
		removed++
	}

	if err := s.disk.RemoveEmptySubfolders(bin); err != nil {
		return fmt.Errorf("recyclebin: Cleanup RemoveEmptySubfolders(%q): %w", bin, err)
	}

	s.log.Debug("recyclebin: cleanup complete",
		"removed", removed, "skipped", skipped)
	return nil
}

// uniqueDest ports the C# collision-resolution loop at
// `RecycleBinProvider.cs:106-118`. Returns the original dest if
// FileExists is false; otherwise appends "_2", "_3", … to the base
// name (preserving the extension) until a free slot is found.
//
// Extracted as a free function so the suffix logic is unit-testable
// without spinning up the full Service.
func uniqueDest(disk DiskOps, destFolder, baseName, dest string) string {
	if !disk.FileExists(dest) {
		return dest
	}
	ext := filepath.Ext(baseName)
	stem := strings.TrimSuffix(baseName, ext)
	for index := 2; ; index++ {
		var candidate string
		if ext == "" {
			candidate = filepath.Join(destFolder, fmt.Sprintf("%s_%d", baseName, index))
		} else {
			candidate = filepath.Join(destFolder, fmt.Sprintf("%s_%d%s", stem, index, ext))
		}
		if !disk.FileExists(candidate) {
			return candidate
		}
	}
}
