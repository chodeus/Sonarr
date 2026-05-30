package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/deploymentinfo"
)

// This file ports the side-effecting orchestration of C#
// `InstallUpdateService.InstallUpdate` / `InstallUpdateWithScript` /
// `GetUpdaterArgs` (`Update/InstallUpdateService.cs:82-223`). Every
// irreversible action (download, extract, folder transfer, process
// spawn) is reached through an injected seam so tests exercise the full
// flow against temp dirs + stubs and NEVER perform a real
// download / spawn / self-replace. The whole path is gated upstream by
// GetUpdatePackage (J507), which returns nil for external/Docker/non-
// auto installs — so a default install never reaches here.

// ErrUpdateFolderNotWritable mirrors C# `UpdateFolderNotWritableException`.
var ErrUpdateFolderNotWritable = errors.New("update: folder not writable")

// ErrUpdateVerificationFailed mirrors C# `UpdateVerificationFailedException`.
var ErrUpdateVerificationFailed = errors.New("update: verification failed")

// ErrUpdateFailed mirrors C# `UpdateFailedException` (script-mode misconfig).
var ErrUpdateFailed = errors.New("update: failed")

// oneGigabyte is the temp-folder free-space warning threshold
// (C#:111 `1.Gigabytes()`).
const oneGigabyte = 1 << 30

// InstallDisk is the disk surface InstallUpdate reaches. *disk.Provider
// satisfies it.
type InstallDisk interface {
	FolderWritable(path string) bool
	FolderExists(path string) bool
	FileExists(path string) bool
	DeleteFolder(path string, recursive bool) error
	GetTotalSize(path string) (int64, error)
	TransferFolder(src, dst string) error
	SetPermissions(path, mask, group string) error
}

// Downloader downloads a URL to a destination file. The httpclient
// *realClient satisfies it via DownloadFile.
type Downloader interface {
	DownloadFile(ctx context.Context, url, fileName string) error
}

// Backupper takes the pre-update backup. Production binds an adapter
// over backup.Service.Backup(ctx, backup.TypeUpdate) — kept narrow so
// internal/update doesn't import internal/backup.
type Backupper interface {
	BackupForUpdate(ctx context.Context) error
}

// ProcessStarter spawns the updater (or the user's update script). This
// is THE irreversible self-replace seam: production binds an os/exec
// adapter; tests stub it and assert the exe + args WITHOUT spawning.
type ProcessStarter interface {
	Start(ctx context.Context, path, args string) error
}

// UpdatePaths supplies the sandbox + update-client folder layout.
// An adapter over appenv.AppFolderInfo satisfies it.
type UpdatePaths interface {
	TempFolder() string
	UpdateSandboxFolder() string
	UpdateClientFolder() string
	UpdateClientExePath() string
}

// RunDeps bundles the side-effecting seams wired post-construction via
// [Installer.SetRunDeps], keeping NewInstaller's signature stable.
type RunDeps struct {
	Disk       InstallDisk
	Downloader Downloader
	// Extract defaults to the package-level [Extract] when nil.
	Extract              func(archivePath, dest string) error
	Backup               Backupper
	Starter              ProcessStarter
	Paths                UpdatePaths
	ExecutingApplication string
	PreservedArguments   string
}

// SetRunDeps wires the install-runtime seams. Required before
// InstallUpdate is called; GetUpdatePackage / EnsureAppDataSafety /
// EnsureValidBranch do not need them.
func (i *Installer) SetRunDeps(d RunDeps) {
	if d.Extract == nil {
		d.Extract = Extract
	}
	i.run = &d
}

// InstallUpdate ports `InstallUpdateService.InstallUpdate` (`:82-175`).
// Returns true when an install is in progress (the updater/script has
// been spawned and the app should expect to restart), false when the
// install was skipped (dev environment / missing update client). Errors
// map to the C# exceptions (folder-not-writable / verification-failed /
// update-failed).
func (i *Installer) InstallUpdate(ctx context.Context, pkg *Package) (bool, error) {
	if i.run == nil {
		return false, errors.New("update: InstallUpdate called before SetRunDeps")
	}
	if pkg == nil {
		return false, errors.New("update: InstallUpdate nil package")
	}
	d := i.run

	if err := i.EnsureAppDataSafety(); err != nil {
		return false, err
	}

	mech, err := i.mechanism(ctx)
	if err != nil {
		return false, err
	}

	if mech != deploymentinfo.MechanismScript {
		uiFolder := filepath.Join(i.startupDir, "UI")
		if !d.Disk.FolderWritable(i.startupDir) {
			return false, fmt.Errorf("%w: startup folder '%s'", ErrUpdateFolderNotWritable, i.startupDir)
		}
		if !d.Disk.FolderWritable(uiFolder) {
			return false, fmt.Errorf("%w: UI folder '%s'", ErrUpdateFolderNotWritable, uiFolder)
		}
	}

	if strings.HasSuffix(i.startupDir, "_output") {
		i.logger.Debug("update: running in developer environment, not updating")
		return false, nil
	}

	temp := d.Paths.TempFolder()
	sandbox := d.Paths.UpdateSandboxFolder()

	if size, serr := d.Disk.GetTotalSize(temp); serr == nil && size < oneGigabyte {
		i.logger.Warn("update: temporary location has less than 1 GB free space; Sonarr may not be able to update itself",
			slog.String("temp", temp))
	}

	packageDest := filepath.Join(sandbox, pkg.FileName)

	if d.Disk.FolderExists(sandbox) {
		i.logger.Info("update: deleting old update files")
		if err := d.Disk.DeleteFolder(sandbox, true); err != nil {
			return false, fmt.Errorf("update: clear sandbox: %w", err)
		}
	}

	i.logger.Info("update: downloading update", slog.String("version", pkg.Version))
	if err := d.Downloader.DownloadFile(ctx, pkg.URL, packageDest); err != nil {
		return false, fmt.Errorf("update: download package: %w", err)
	}

	i.logger.Info("update: verifying update package")
	ok, err := Verify(pkg, packageDest)
	if err != nil {
		return false, fmt.Errorf("update: verify: %w", err)
	}
	if !ok {
		return false, fmt.Errorf("%w: update file '%s' is invalid", ErrUpdateVerificationFailed, packageDest)
	}
	i.logger.Info("update: update package verified successfully")

	i.logger.Info("update: extracting update package")
	if err := d.Extract(packageDest, sandbox); err != nil {
		return false, fmt.Errorf("update: extract: %w", err)
	}

	i.EnsureValidBranch(ctx, pkg)

	if err := d.Backup.BackupForUpdate(ctx); err != nil {
		return false, fmt.Errorf("update: pre-update backup: %w", err)
	}

	if mech == deploymentinfo.MechanismScript {
		return i.installWithScript(ctx, sandbox)
	}

	// BuiltIn: stage the update-client helper into the sandbox root,
	// confirm it exists, mark it executable, then spawn it.
	i.logger.Info("update: preparing client")
	if err := d.Disk.TransferFolder(d.Paths.UpdateClientFolder(), sandbox); err != nil {
		return false, fmt.Errorf("update: transfer update client: %w", err)
	}

	exe := d.Paths.UpdateClientExePath()
	if !d.Disk.FileExists(exe) {
		i.logger.Warn("update: update client does not exist, aborting update", slog.String("path", exe))
		return false, nil
	}

	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if err := d.Disk.SetPermissions(exe, "755", ""); err != nil {
			i.logger.Warn("update: set update-client permissions", slog.Any("err", err))
		}
	}

	i.logger.Info("update: starting update client; Sonarr will restart shortly", slog.String("path", exe))
	if err := d.Starter.Start(ctx, exe, i.updaterArgs(sandbox)); err != nil {
		return false, fmt.Errorf("update: start update client: %w", err)
	}
	return true, nil
}

// installWithScript ports `InstallUpdateService.InstallUpdateWithScript`
// (`:196-215`). Delegates the install to the operator's configured
// update script after removing the bundled update client.
func (i *Installer) installWithScript(ctx context.Context, sandbox string) (bool, error) {
	d := i.run
	scriptPath, err := i.cfg.UpdateScriptPath(ctx)
	if err != nil {
		return false, fmt.Errorf("update: read UpdateScriptPath: %w", err)
	}
	if strings.TrimSpace(scriptPath) == "" {
		return false, fmt.Errorf("%w: update script has not been defined", ErrUpdateFailed)
	}
	if !d.Disk.FileExists(scriptPath) {
		return false, fmt.Errorf("%w: update script '%s' does not exist", ErrUpdateFailed, scriptPath)
	}

	i.logger.Info("update: removing bundled update client")
	if err := d.Disk.DeleteFolder(d.Paths.UpdateClientFolder(), true); err != nil {
		i.logger.Warn("update: remove update client", slog.Any("err", err))
	}

	i.logger.Info("update: starting update script", slog.String("script", scriptPath))
	if err := d.Starter.Start(ctx, scriptPath, i.updaterArgs(sandbox)); err != nil {
		return false, fmt.Errorf("update: start update script: %w", err)
	}
	return true, nil
}

// updaterArgs ports `InstallUpdateService.GetUpdaterArgs` (`:217-223`):
// `<processId> "<sandbox>" "<executingApplication>" <preservedArguments>`.
// The sandbox + executing-application paths are quoted; the preserved
// startup arguments are appended verbatim.
func (i *Installer) updaterArgs(sandbox string) string {
	processID := strconv.Itoa(os.Getpid())
	sandboxTrimmed := strings.TrimRight(sandbox, string(os.PathSeparator))
	parts := []string{
		processID,
		wrapInQuotes(sandboxTrimmed),
		wrapInQuotes(i.run.ExecutingApplication),
		i.run.PreservedArguments,
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// wrapInQuotes ports the C# `StringExtensions.WrapInQuotes` — double-
// quotes a value so a path with spaces survives the args split.
func wrapInQuotes(s string) string {
	return `"` + s + `"`
}
