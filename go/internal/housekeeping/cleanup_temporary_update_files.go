// CleanupTemporaryUpdateFiles housekeeper. Ports the C# task at
// `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupTemporaryUpdateFiles.cs`.
//
// Sweeps the on-disk update sandbox left behind by the in-place
// updater. The updater stages a working tree under
// `<Temp>/sonarr_update/` while applying a new release; on a clean
// exit it removes the folder itself. A crash or kill during the
// staging window can leave the folder behind, growing the temp
// partition over time. This housekeeper removes the folder
// unconditionally on every pass — the updater isn't running while
// the main process is running, so the sandbox is always stale data
// when this fires.

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"sonarr.tv/sonarr/internal/appenv"
)

// UpdateSandboxDisk narrows the disk surface this task reaches for.
// Tests inject a fake; the production binding is [OSUpdateSandboxDisk]
// which bridges to os.Stat / os.RemoveAll.
type UpdateSandboxDisk interface {
	// FolderExists returns true when the supplied path resolves to
	// an extant directory. False for files, missing paths, or any
	// stat-time error.
	FolderExists(path string) bool

	// DeleteFolder removes the supplied directory and (when
	// recursive) every child path. Errors propagate so the
	// housekeeper can log them; the C# tree's DiskProvider.DeleteFolder
	// is similarly non-swallowing.
	DeleteFolder(path string, recursive bool) error
}

// NewCleanupTemporaryUpdateFiles ports
// `Housekeepers/CleanupTemporaryUpdateFiles.cs:1-28`. Removes the
// update sandbox folder under <Temp>/sonarr_update/ when present.
//
// folder.Temp must be set; an empty Temp panics-by-misuse rather
// than silently doing nothing (signal a wiring bug at boot rather
// than letting the housekeeper run as a no-op forever). disk
// defaults to [OSUpdateSandboxDisk] if nil — tests inject a fake.
func NewCleanupTemporaryUpdateFiles(folder appenv.AppFolderInfo, disk UpdateSandboxDisk, log *slog.Logger) (Task, error) {
	if folder.Temp == "" {
		return nil, errors.New("housekeeping: NewCleanupTemporaryUpdateFiles: AppFolderInfo.Temp is empty")
	}
	if disk == nil {
		disk = OSUpdateSandboxDisk{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &cleanupTemporaryUpdateFiles{folder: folder, disk: disk, log: log}, nil
}

type cleanupTemporaryUpdateFiles struct {
	folder appenv.AppFolderInfo
	disk   UpdateSandboxDisk
	log    *slog.Logger
}

// Name implements Task.
func (t *cleanupTemporaryUpdateFiles) Name() string {
	return "CleanupTemporaryUpdateFiles"
}

// Clean implements Task. Single guard + recursive delete. No error
// when the folder doesn't exist (the common case — most installs
// never leak a sandbox).
func (t *cleanupTemporaryUpdateFiles) Clean(ctx context.Context) error {
	sandbox := t.folder.UpdateSandboxFolder()
	if !t.disk.FolderExists(sandbox) {
		return nil
	}
	if err := t.disk.DeleteFolder(sandbox, true); err != nil {
		return fmt.Errorf("housekeeping: CleanupTemporaryUpdateFiles: %w", err)
	}
	t.log.LogAttrs(ctx, slog.LevelInfo,
		"housekeeping: removed stale update sandbox",
		slog.String("path", sandbox))
	return nil
}

// OSUpdateSandboxDisk is the production [UpdateSandboxDisk] backed
// by the stdlib os package. Zero value is usable.
type OSUpdateSandboxDisk struct{}

// FolderExists implements UpdateSandboxDisk. os.Stat-based check
// that returns false on any error (including "not exist" and
// permission denied) and on non-directory entries.
func (OSUpdateSandboxDisk) FolderExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// DeleteFolder implements UpdateSandboxDisk. recursive=true uses
// os.RemoveAll; recursive=false uses os.Remove. The housekeeper
// always passes true, so the false-branch exists only for parity
// with the C# DiskProvider signature.
func (OSUpdateSandboxDisk) DeleteFolder(path string, recursive bool) error {
	if recursive {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}
