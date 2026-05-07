// Package mediafileattributes is the Go port of
// `NzbDrone.Core.MediaFiles.MediaFileAttributeService`
// (`src/NzbDrone.Core/MediaFiles/MediaFileAttributeService.cs:1-95`).
// It applies post-transfer file/folder permissions and (on Windows)
// folder mtime updates after the writer moves an episode file into
// the series tree.
//
// This slice (3.75) lands the [Service] body. It satisfies the
// [episodefilemoving.AttributeWriter] seam slice 3.70 defined; the
// production binding can now wire a real attribute service in place
// of the writer's noop default.
//
// # Deviations from C#
//
//   - SetFolderLastWriteTime is a no-op on non-Windows. Mirrors the
//     C# `if (OsInfo.IsWindows)` arm at `:69-75` — Linux/macOS
//     targets are the only ones the Go port runs against, so the
//     guard is hardcoded rather than runtime-dispatched. Documented
//     on [Service.SetFolderLastWriteTime] so a future Windows-first
//     port knows where to add the call back.
//
//   - SetFilePermissions on Windows would call
//     `_diskProvider.InheritFolderPermissions` (`:33-58`); the Go
//     port skips that branch entirely. The non-Windows arm
//     (SetMonoPermissions, `:78-95`) is what the Go port executes
//     for every call, gated by [ConfigReader.SetPermissionsLinux].
//
//   - The C# class swallows exceptions inside SetMonoPermissions
//     with a `_logger.Warn(ex, ...)` call at `:90-94`. The Go port
//     mirrors that — chmod/chown failures log at warn-level and
//     return nil so the writer's caller continues. The
//     [AttributeWriter] interface returns error so a future
//     stricter binding can surface failures, but this Service
//     matches C#'s "best-effort" semantics.
//
// # Wiring
//
// Production wiring composes a `*config.Provider` (which already
// exposes the SetPermissionsLinux / ChmodFolder / ChownGroup
// getters at `internal/config/provider.go:223-252`) with a
// `*disk.Provider` (which exposes SetPermissions at
// `internal/disk/provider.go:216`). Both already exist; this slice
// only adds the Service that composes them into the
// [episodefilemoving.AttributeWriter] shape.
package mediafileattributes

import (
	"errors"
	"io/fs"
	"log/slog"
	"time"
)

// ConfigReader is the slice of `IConfigService` the C#
// MediaFileAttributeService reaches for. Three getters drive the
// SetMonoPermissions branch (`MediaFileAttributeService.cs:78-94`):
//
//   - SetPermissionsLinux gates whether the chmod/chown runs at
//     all (`:80`). False → return without touching the file.
//   - ChmodFolder is the octal mode passed to
//     `IDiskProvider.SetPermissions` (`:86`). Default "755".
//   - ChownGroup is the group name passed alongside (`:86`). Empty
//     string skips the chown leg.
//
// Production binding wires `*config.Provider` directly — its
// SetPermissionsLinux / ChmodFolder / ChownGroup getters at
// `internal/config/provider.go:223-252` match this surface.
type ConfigReader interface {
	SetPermissionsLinux() bool
	ChmodFolder() string
	ChownGroup() string
}

// DiskOps is the slice of `IDiskProvider` the C# class reaches for.
// Just SetPermissions (the chmod/chown leg). FolderSetLastWriteTime
// is NOT included because the C# class only calls it on Windows
// (`:71-73`); the Go port's [Service.SetFolderLastWriteTime] is a
// no-op so the dep stays narrow.
//
// Production binding wires `*disk.Provider` — its SetPermissions
// at `internal/disk/provider.go:216` matches this surface.
type DiskOps interface {
	SetPermissions(path, mask, group string) error
}

// Service is the C# `MediaFileAttributeService` port. Stateless —
// every method delegates to the bound config / disk dependencies,
// so a single Service value is reused process-wide.
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
// The nil-Service-on-nil-dep convention matches the rest of the
// Phase-3 services (see `episodeimport/aggregation.go` — every
// aggregator's NewService returns nil if the required dep is nil).
func NewService(cfg ConfigReader, disk DiskOps, log *slog.Logger) *Service {
	if cfg == nil || disk == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{cfg: cfg, disk: disk, log: log}
}

// SetFilePermissions ports `MediaFileAttributeService.cs:32-59`.
// On Windows the C# class calls `IDiskProvider.InheritFolderPermissions`;
// the Go port skips that branch entirely (no Windows-first-class
// support today). On non-Windows it falls through to the
// SetMonoPermissions logic — gated by [ConfigReader.SetPermissionsLinux],
// applies [ConfigReader.ChmodFolder] + [ConfigReader.ChownGroup]
// via [DiskOps.SetPermissions].
//
// Errors from SetPermissions are logged at warn-level and swallowed
// (return nil). Mirrors the C# `_logger.Warn(ex, ...)` arm at
// `:90-94`. fs.ErrNotExist is logged at debug-level instead — a
// missing file mid-import is benign and the writer's normal flow
// will surface it elsewhere.
func (s *Service) SetFilePermissions(path string) error {
	return s.setMonoPermissions(path, "file")
}

// SetFolderPermissions ports `MediaFileAttributeService.cs:61-67`.
// Identical to [Service.SetFilePermissions] on non-Windows — the C#
// branch on Windows is a no-op for folder permissions (only files
// inherit). The Go port collapses both into the same SetMonoPermissions
// call.
func (s *Service) SetFolderPermissions(path string) error {
	return s.setMonoPermissions(path, "folder")
}

// SetFolderLastWriteTime ports `MediaFileAttributeService.cs:69-75`.
// The C# class only updates the folder mtime on Windows; on
// non-Windows the call is a no-op. The Go port matches that exactly
// — Linux/macOS callers get a debug-level log line and an immediate
// nil return. Documented behaviour so a future Windows-first port
// knows where to wire the call back via os.Chtimes.
//
// Returns nil on the non-Windows no-op path. Reserved error return
// value lets a future Windows-aware port surface os.Chtimes errors.
func (s *Service) SetFolderLastWriteTime(path string, t time.Time) error {
	s.log.Debug("mediafileattributes: SetFolderLastWriteTime no-op (non-Windows)",
		"path", path, "time", t)
	return nil
}

// setMonoPermissions ports the SetMonoPermissions helper at
// `MediaFileAttributeService.cs:78-94`. Gated by
// [ConfigReader.SetPermissionsLinux]; applies
// [ConfigReader.ChmodFolder] + [ConfigReader.ChownGroup] via
// [DiskOps.SetPermissions]. Errors log + swallow.
//
// kind is a label for the log line ("file" / "folder") so the
// failure is attributable without the caller threading extra
// context.
func (s *Service) setMonoPermissions(path, kind string) error {
	if !s.cfg.SetPermissionsLinux() {
		return nil
	}
	mask := s.cfg.ChmodFolder()
	group := s.cfg.ChownGroup()
	if err := s.disk.SetPermissions(path, mask, group); err != nil {
		// Missing file mid-import is benign — log at debug. Other
		// errors (EPERM, EACCES, malformed mask) are warn-level so
		// operators see them but the writer continues. Swallow per
		// C# best-effort semantics at `:90-94`.
		if errors.Is(err, fs.ErrNotExist) {
			s.log.Debug("mediafileattributes: target missing",
				"kind", kind, "path", path, "err", err)
			return nil
		}
		s.log.Warn("mediafileattributes: SetPermissions failed",
			"kind", kind, "path", path, "mask", mask, "group", group, "err", err)
		return nil
	}
	return nil
}
