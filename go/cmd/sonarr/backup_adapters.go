// Adapters supporting the slice-3.BN (queue [16] sub-arc 16b)
// *backup.Service boot wire. The service reaches for several narrow
// surfaces (Filesystem with EnsureFolder/EmptyFolder/MoveFile/etc;
// AppFolders with the Sonarr-specific paths; VersionInfo) that the
// existing host singletons (*disk.Provider, *appenv.AppFolderInfo,
// internal/buildinfo) don't expose in the exact shape backup needs.

package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/backup"
	"sonarr.tv/sonarr/internal/buildinfo"
	"sonarr.tv/sonarr/internal/disk"
)

// backupFilesystemAdapter satisfies [backup.Filesystem] over
// *disk.Provider plus a handful of stdlib calls for the methods
// disk.Provider doesn't yet expose (EnsureFolder, EmptyFolder,
// CopyFile, MoveFile with overwrite, WriteAllText, DeleteFile).
//
// Goroutine-safe: stateless after construction.
type backupFilesystemAdapter struct {
	disk *disk.Provider
	log  *slog.Logger
}

func newBackupFilesystemAdapter(d *disk.Provider, log *slog.Logger) backupFilesystemAdapter {
	return backupFilesystemAdapter{disk: d, log: log}
}

// EnsureFolder mirrors C# `IDiskProvider.EnsureFolder` — creates
// the directory and any missing parents, returns nil if it already
// exists.
func (a backupFilesystemAdapter) EnsureFolder(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("backup.fs: EnsureFolder %q: %w", path, err)
	}
	return nil
}

func (a backupFilesystemAdapter) FolderExists(path string) bool   { return a.disk.FolderExists(path) }
func (a backupFilesystemAdapter) FolderWritable(path string) bool { return a.disk.FolderWritable(path) }

// EmptyFolder removes every file in path (non-recursive — the C#
// equivalent IDiskProvider.EmptyFolder also descends one level).
func (a backupFilesystemAdapter) EmptyFolder(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("backup.fs: EmptyFolder read %q: %w", path, err)
	}
	for _, e := range entries {
		full := filepath.Join(path, e.Name())
		if e.IsDir() {
			if err := os.RemoveAll(full); err != nil {
				return fmt.Errorf("backup.fs: EmptyFolder remove dir %q: %w", full, err)
			}
			continue
		}
		if err := os.Remove(full); err != nil {
			return fmt.Errorf("backup.fs: EmptyFolder remove %q: %w", full, err)
		}
	}
	return nil
}

func (a backupFilesystemAdapter) GetFiles(path string) ([]string, error) {
	files, err := a.disk.GetFiles(path, false)
	if err != nil {
		return nil, fmt.Errorf("backup.fs: GetFiles %q: %w", path, err)
	}
	return files, nil
}

func (a backupFilesystemAdapter) GetFileSize(path string) (int64, error) {
	return a.disk.GetFileSize(path)
}

func (a backupFilesystemAdapter) FileGetLastWrite(path string) (time.Time, error) {
	return a.disk.FileGetLastWrite(path)
}

func (a backupFilesystemAdapter) WriteAllText(path, contents string) error {
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("backup.fs: WriteAllText %q: %w", path, err)
	}
	return nil
}

func (a backupFilesystemAdapter) CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("backup.fs: CopyFile open src %q: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("backup.fs: CopyFile mkdir dst parent %q: %w", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("backup.fs: CopyFile open dst %q: %w", dst, err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("backup.fs: CopyFile copy: %w", err)
	}
	return nil
}

func (a backupFilesystemAdapter) MoveFile(src, dst string, overwrite bool) error {
	if overwrite {
		if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup.fs: MoveFile pre-clean %q: %w", dst, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("backup.fs: MoveFile mkdir dst parent %q: %w", filepath.Dir(dst), err)
	}
	if err := os.Rename(src, dst); err != nil {
		// Cross-device rename fallback: copy + delete src.
		if cpErr := backupCopyAndDelete(src, dst); cpErr != nil {
			return fmt.Errorf("backup.fs: MoveFile %q→%q rename and fallback both failed: %w", src, dst, errors.Join(err, cpErr))
		}
	}
	return nil
}

func backupCopyAndDelete(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return os.Remove(src)
}

func (a backupFilesystemAdapter) DeleteFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup.fs: DeleteFile %q: %w", path, err)
	}
	return nil
}

func (a backupFilesystemAdapter) DeleteFolder(path string, recursive bool) error {
	return a.disk.DeleteFolder(path, recursive)
}

// Compile-time guard — adapter satisfies backup.Filesystem.
var _ backup.Filesystem = backupFilesystemAdapter{}

// backupAppFoldersAdapter satisfies [backup.AppFolders] over
// *appenv.AppFolderInfo. The four methods map to AppDataPath /
// TempPath / ConfigPath / DatabaseRestorePath getters that exist
// on the host singleton in slightly different names.
type backupAppFoldersAdapter struct {
	folder appenv.AppFolderInfo
}

func (a backupAppFoldersAdapter) AppDataPath() string { return a.folder.AppData }
func (a backupAppFoldersAdapter) TempPath() string {
	if a.folder.Temp != "" {
		return a.folder.Temp
	}
	return os.TempDir()
}
func (a backupAppFoldersAdapter) ConfigPath() string { return a.folder.ConfigPath() }
func (a backupAppFoldersAdapter) DatabaseRestorePath() string {
	// The C# tree's IAppFolderInfo.GetDatabaseRestore() returns the
	// path Sonarr writes to so the next startup picks it up. For
	// the Go-port spike (Postgres-default) we stage to AppData +
	// "sonarr.db.restore" — the same convention the C# tree uses.
	return filepath.Join(a.folder.AppData, "sonarr.db.restore")
}

// Compile-time guard.
var _ backup.AppFolders = backupAppFoldersAdapter{}

// backupVersionInfoAdapter satisfies [backup.VersionInfo] from the
// build-time-stamped buildinfo.Version() string.
type backupVersionInfoAdapter struct{}

func (backupVersionInfoAdapter) Version() string { return buildinfo.String() }

// Compile-time guard.
var _ backup.VersionInfo = backupVersionInfoAdapter{}
