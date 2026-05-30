// Package updateengine ports the C# `NzbDrone.Update.UpdateEngine`
// subtree — the logic of the standalone `sonarr-update` helper that the
// running app spawns to apply a downloaded update. It backs up the
// existing install, mirrors the new build over it, and (via an injected
// process seam) terminates the old process and starts the new one.
//
// SAFETY: every process operation (terminate / start / existence /
// app-type detection) goes through the [ProcessController] seam so the
// file-move/backup/restore logic is unit-testable against temp dirs
// WITHOUT ever killing a process or self-replacing a real binary. The
// production os/exec ProcessController is wired in cmd/sonarr-update; it
// is invoked only by the gated BuiltIn update path, never by tests.
//
// Ports `UpdateEngine/{InstallUpdateService,BackupAndRestore,
// BackupAppData,DetectApplicationType,DetectExistingVersion,
// StartNzbDrone,TerminateNzbDrone}.cs`.
package updateengine

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AppType ports C# `NzbDrone.Update.AppType` — how the app is run, which
// determines how it is restarted.
type AppType int

const (
	// AppTypeNormal is the default (and the only Unix) app type.
	AppTypeNormal AppType = iota
	// AppTypeConsole is the Windows console host.
	AppTypeConsole
	// AppTypeService is the Windows service host.
	AppTypeService
)

// processNameSonarr / processNameConsole mirror the C#
// ProcessProvider.SONARR_PROCESS_NAME / SONARR_CONSOLE_PROCESS_NAME used
// for the post-restart existence checks.
const (
	processNameSonarr  = "Sonarr"
	processNameConsole = "Sonarr.Console"
)

// ProcessController is the process-management seam — THE self-replace/
// restart boundary. Production binds an os/exec adapter (cmd/sonarr-
// update); tests stub it and never spawn/kill a real process.
//
//   - Exists / ExistsByName mirror IProcessProvider.Exists.
//   - DetectAppType ports DetectApplicationType.GetAppType (Unix →
//     Normal; Windows → Service/Console/Normal).
//   - Terminate ports TerminateNzbDrone.Terminate (Unix: KillAll the
//     Sonarr processes + Kill the pid; Windows: stop the service +
//     KillAll) — the OS specifics live in the adapter.
//   - Start ports StartNzbDrone.Start (relaunch the app for the app
//     type).
type ProcessController interface {
	Exists(pid int) bool
	ExistsByName(name string) bool
	DetectAppType() AppType
	Terminate(pid int) error
	Start(appType AppType, installationFolder string) error
}

// DiskMirror is the file-op seam. *disk.Provider satisfies it.
type DiskMirror interface {
	FolderExists(path string) bool
	CreateFolder(path string) error
	EmptyFolder(path string) error
	MirrorFolder(src, dst string) (int, error)
	CopyFile(src, dst string) error
	FileExists(path string) bool
	SetPermissions(path, mask, group string) error
}

// Paths supplies the sandbox + backup + appdata layout the engine reads.
// An adapter over appenv.AppFolderInfo satisfies it.
type Paths interface {
	UpdatePackageFolder() string
	UpdateBackupFolder() string
	UpdateBackupAppDataFolder() string
	ConfigPath() string
	DatabasePath() string
}

// Engine ports `UpdateEngine.InstallUpdateService`.
type Engine struct {
	disk   DiskMirror
	proc   ProcessController
	paths  Paths
	logger *slog.Logger

	// restartWait + sleep make the post-terminate auto-restart wait
	// loop deterministic in tests (C# sleeps 1s × 10). Production uses
	// time.Sleep + 10 iterations.
	restartWait int
	sleep       func(time.Duration)
}

// NewEngine binds the engine's seams. disk/proc/paths are required.
func NewEngine(disk DiskMirror, proc ProcessController, paths Paths, logger *slog.Logger) (*Engine, error) {
	if disk == nil || proc == nil || paths == nil {
		return nil, errors.New("updateengine: nil dependency")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		disk:        disk,
		proc:        proc,
		paths:       paths,
		logger:      logger,
		restartWait: 10,
		sleep:       time.Sleep,
	}, nil
}

// WithRestartWait overrides the post-terminate restart-wait loop count +
// sleep function (tests pass a no-op sleep to avoid real delays).
func (e *Engine) WithRestartWait(iterations int, sleep func(time.Duration)) *Engine {
	if iterations > 0 {
		e.restartWait = iterations
	}
	if sleep != nil {
		e.sleep = sleep
	}
	return e
}

// verify ports `InstallUpdateService.Verify` (`:55-84`).
func (e *Engine) verify(targetFolder string, processID int) error {
	e.logger.Info("updateengine: verifying requirements before update")
	if strings.TrimSpace(targetFolder) == "" {
		return errors.New("updateengine: target folder can not be null or empty")
	}
	if !e.disk.FolderExists(targetFolder) {
		return fmt.Errorf("updateengine: target folder doesn't exist %q", targetFolder)
	}
	if processID < 1 {
		return fmt.Errorf("updateengine: invalid process ID: %d", processID)
	}
	if !e.proc.Exists(processID) {
		return fmt.Errorf("updateengine: process with ID doesn't exist %d", processID)
	}
	if !e.disk.FolderExists(e.paths.UpdatePackageFolder()) {
		return fmt.Errorf("updateengine: update folder doesn't exist %q", e.paths.UpdatePackageFolder())
	}
	return nil
}

// Start ports `InstallUpdateService.Start` (`:86-171`). It applies the
// staged update to installationFolder for the app running as processID:
// back up the install + appdata, mirror the new build over the install,
// fix permissions, then terminate the old process and (re)start the new
// one. On a copy failure the install is rolled back from the backup.
func (e *Engine) Start(installationFolder string, processID int) error {
	e.logger.Info("updateengine: installation folder", slog.String("folder", installationFolder))

	if err := e.verify(installationFolder, processID); err != nil {
		return err
	}

	// C#:93-97 — when launched from <root>/bin/Sonarr, the real install
	// root is the parent.
	if strings.HasSuffix(installationFolder, filepath.Join("bin", "Sonarr")) {
		installationFolder = filepath.Dir(filepath.Dir(installationFolder))
		e.logger.Info("updateengine: fixed installation folder", slog.String("folder", installationFolder))
	}

	appType := e.proc.DetectAppType()
	isWindows := runtime.GOOS == "windows"

	if isWindows {
		if err := e.proc.Terminate(processID); err != nil {
			e.logger.Warn("updateengine: terminate (windows pre-backup)", slog.Any("err", err))
		}
	}

	copyErr := func() error {
		if err := e.backupInstall(installationFolder); err != nil {
			return err
		}
		e.backupAppData()

		if isWindows && (e.proc.ExistsByName(processNameConsole) || e.proc.ExistsByName(processNameSonarr)) {
			e.logger.Error("updateengine: Sonarr was restarted prematurely by an external process")
			return errSkipRestart
		}

		e.logger.Info("updateengine: copying new files to target folder")
		if _, err := e.disk.MirrorFolder(e.paths.UpdatePackageFolder(), installationFolder); err != nil {
			e.logger.Error("updateengine: failed to copy upgrade package to target folder; rolling back", slog.Any("err", err))
			if _, rerr := e.disk.MirrorFolder(e.paths.UpdateBackupFolder(), installationFolder); rerr != nil {
				e.logger.Error("updateengine: rollback failed", slog.Any("err", rerr))
			} else {
				e.logger.Info("updateengine: rolled back upgrade")
			}
			return fmt.Errorf("updateengine: copy package: %w", err)
		}

		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			e.setExecutable(filepath.Join(installationFolder, "Sonarr"))
			e.setExecutable(filepath.Join(installationFolder, "ffprobe"))
		}
		return nil
	}()

	if errors.Is(copyErr, errSkipRestart) {
		return nil
	}

	// finally — restart (C#:142-170). The copy error is returned AFTER
	// the restart attempt so the app comes back up even on a failed copy
	// (the backup was restored above).
	e.restart(appType, installationFolder, processID, isWindows)

	if copyErr != nil {
		return copyErr
	}
	return nil
}

// errSkipRestart is the internal sentinel for the Windows
// premature-restart short-circuit (C#:116-120 returns without restart).
var errSkipRestart = errors.New("updateengine: external process restarted Sonarr prematurely")

// restart ports the finally-block of `InstallUpdateService.Start`
// (`:142-170`).
func (e *Engine) restart(appType AppType, installationFolder string, processID int, isWindows bool) {
	if isWindows {
		if err := e.proc.Start(appType, installationFolder); err != nil {
			e.logger.Error("updateengine: start (windows)", slog.Any("err", err))
		}
		return
	}

	// Unix: terminate the old process, then wait for an external
	// supervisor (systemd/docker) to auto-restart; start manually if it
	// doesn't.
	if err := e.proc.Terminate(processID); err != nil {
		e.logger.Warn("updateengine: terminate (unix)", slog.Any("err", err))
	}

	e.logger.Info("updateengine: waiting for external auto-restart")
	for i := 0; i < e.restartWait; i++ {
		e.sleep(time.Second)
		if e.proc.ExistsByName(processNameSonarr) {
			e.logger.Info("updateengine: Sonarr was restarted by an external process")
			return
		}
	}

	if !e.proc.ExistsByName(processNameSonarr) {
		if err := e.proc.Start(appType, installationFolder); err != nil {
			e.logger.Error("updateengine: start (unix)", slog.Any("err", err))
		}
	}
}

// backupInstall ports `BackupAndRestore.Backup` (`:28-32`).
func (e *Engine) backupInstall(source string) error {
	e.logger.Info("updateengine: creating backup of existing installation")
	if _, err := e.disk.MirrorFolder(source, e.paths.UpdateBackupFolder()); err != nil {
		return fmt.Errorf("updateengine: backup install: %w", err)
	}
	return nil
}

// backupAppData ports `BackupAppData.Backup` (`:32-55`). Best-effort —
// a backup failure is logged, not propagated (faithful to the C# catch).
func (e *Engine) backupAppData() {
	e.logger.Info("updateengine: backing up appdata (database/config)")
	dst := e.paths.UpdateBackupAppDataFolder()
	if e.disk.FolderExists(dst) {
		if err := e.disk.EmptyFolder(dst); err != nil {
			e.logger.Warn("updateengine: empty appdata backup folder", slog.Any("err", err))
		}
	} else if err := e.disk.CreateFolder(dst); err != nil {
		e.logger.Warn("updateengine: create appdata backup folder", slog.Any("err", err))
		return
	}

	if err := e.copyIfExists(e.paths.ConfigPath(), filepath.Join(dst, "config.xml")); err != nil {
		e.logger.Error("updateengine: couldn't back up config", slog.Any("err", err))
		return
	}
	if err := e.copyIfExists(e.paths.DatabasePath(), filepath.Join(dst, "sonarr.db")); err != nil {
		e.logger.Error("updateengine: couldn't back up database", slog.Any("err", err))
	}
}

func (e *Engine) copyIfExists(src, dst string) error {
	if !e.disk.FileExists(src) {
		return nil
	}
	return e.disk.CopyFile(src, dst)
}

func (e *Engine) setExecutable(path string) {
	if !e.disk.FileExists(path) {
		return
	}
	if err := e.disk.SetPermissions(path, "755", ""); err != nil {
		e.logger.Warn("updateengine: set executable", slog.String("path", path), slog.Any("err", err))
	}
}
