package main

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"sonarr.tv/sonarr/internal/updateengine"
)

// osProcessController is the PRODUCTION process seam for the updater —
// the only place that performs the real, irreversible self-replace
// actions: terminating the running Sonarr process and spawning the new
// one. Ports the Unix path of C# `TerminateNzbDrone` / `StartNzbDrone` /
// `DetectApplicationType` (the Windows-service machinery is out of scope
// for the Go port, which runs as a normal process under systemd/Docker).
//
// It is exercised ONLY when the sonarr-update binary actually runs (i.e.
// spawned by the gated BuiltIn update path). The updateengine unit tests
// never use it — they stub the ProcessController so no real process is
// ever killed or started.
type osProcessController struct {
	preserved []string
	logger    *slog.Logger
}

func newOSProcessController(preserved []string) *osProcessController {
	return &osProcessController{preserved: preserved, logger: slog.Default()}
}

// Exists reports whether the process is alive. On Unix, signal 0 probes
// the pid without affecting it (the C# IProcessProvider.Exists(pid)).
func (c *osProcessController) Exists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// ExistsByName reports whether a process matching name is running,
// best-effort via `pgrep -x` (the C# IProcessProvider.Exists(name) used
// to detect an external auto-restart). A lookup failure → false.
func (c *osProcessController) ExistsByName(name string) bool {
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// DetectAppType ports `DetectApplicationType.GetAppType`. The Go port
// always runs as a normal process (no Windows service host).
func (c *osProcessController) DetectAppType() updateengine.AppType {
	return updateengine.AppTypeNormal
}

// Terminate ports the Unix arm of `TerminateNzbDrone.Terminate` — kill
// the running app by pid (SIGTERM, then SIGKILL fallback). REAL kill;
// only runs when the helper executes for real.
func (c *osProcessController) Terminate(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Already gone or no permission — try a hard kill, ignore error.
		_ = proc.Kill()
	}
	return nil
}

// Start ports the Unix arm of `StartNzbDrone.Start` — relaunch the app
// from the installation folder, detached, carrying the preserved startup
// arguments plus the nobrowser flag. REAL spawn; only runs for real.
func (c *osProcessController) Start(_ updateengine.AppType, installationFolder string) error {
	exeName := "Sonarr"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	path := filepath.Join(installationFolder, exeName)

	args := append([]string{}, c.preserved...)
	if !containsFlag(args, "nobrowser") {
		args = append(args, "/nobrowser")
	}

	cmd := exec.Command(path, args...)
	cmd.Dir = installationFolder
	// Detach into a new process group so the relaunched app survives this
	// helper exiting (Setpgid is the Unix analogue of the C# detached
	// SpawnNewProcess; the Go updater targets Unix hosts).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.logger.Info("sonarr-update: starting Sonarr", slog.String("path", path))
	return cmd.Start()
}

func containsFlag(args []string, name string) bool {
	for _, a := range args {
		if strings.TrimLeft(a, "-/") == name {
			return true
		}
	}
	return false
}

// compile-time guard.
var _ updateengine.ProcessController = (*osProcessController)(nil)
