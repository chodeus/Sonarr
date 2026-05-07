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
// # Deferred (this slice ships only DeleteFile)
//
//   - DeleteFolder (`RecycleBinProvider.cs:41-68`) — requires
//     IDiskTransferService.TransferFolder which the Go disk port
//     hasn't shipped (slice 3.74 only ported TransferFile, the file
//     half of the C# DiskTransferService class). Lands when the
//     folder-transfer surface ports.
//   - Empty (`:139-160`) — requires
//     IDiskProvider.GetDirectories which `internal/disk/Provider`
//     hasn't shipped. Lands alongside the directory-walk surface.
//   - Cleanup (`:162-200`) — the `IExecute<CleanUpRecycleBinCommand>`
//     handler. Wired into the command bus's CleanUpRecycleBin slot
//     (currently a stub at `internal/commands/handlers/`). Lands
//     when the command-handler surface fills the stub.
//
// All three deferred surfaces are documented at type-level on
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
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// ConfigReader narrows `IConfigService` for the RecycleBin getter
// only. Production binding wires `*config.Provider` directly — its
// RecycleBin getter at `internal/config/provider.go` matches this
// surface.
//
// RecycleBinCleanupDays is NOT included because [Service.Cleanup]
// is deferred (see the package doc); the slice that lands Cleanup
// will widen this interface.
type ConfigReader interface {
	// RecycleBin returns the configured recycle-bin folder path.
	// Empty string disables the feature — DeleteFile falls back to a
	// hard delete via [DiskOps.DeleteFile].
	RecycleBin() string
}

// DiskOps narrows `IDiskProvider` for the four methods
// [Service.DeleteFile] reaches for. Production binding wires
// `*disk.Provider` — every method exists at the disk package after
// slice 3.74's transfer primitives landed.
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
	// "RecycleBin disabled" arm of DeleteFile.
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
