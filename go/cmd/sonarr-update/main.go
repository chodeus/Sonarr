// Command sonarr-update applies a downloaded Sonarr update package to the
// running install. It replaces the C# NzbDrone.Update process
// (`src/NzbDrone.Update/UpdateApp.cs`).
//
// The running app spawns this helper (from the gated BuiltIn update
// path) with: `<processId> "<sandbox>" "<executingApplication>"
// <preservedArgs...>`. The helper backs up the existing install, mirrors
// the new build over it, then terminates the old process and restarts
// the app — via internal/updateengine, which keeps all process
// operations behind the ProcessController seam.
//
// See ../../CONVERSION.md for the rules every change must follow.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/buildinfo"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/updateengine"
)

func main() {
	showVersion := flag.Bool("version", false, "print build info and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.String())
		return
	}

	if err := run(flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "sonarr-update:", err)
		os.Exit(1)
	}
}

// run parses the updater arguments and applies the staged update. Ports
// `UpdateApp.Start` + `ParseArgs` + `GetInstallationDirectory`
// (`UpdateApp.cs:60-131`).
func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("args must be specified (<processId> <updateLocation> <executingApplication> [preserved...])")
	}

	processID, err := strconv.Atoi(args[0])
	if err != nil || processID <= 0 {
		return fmt.Errorf("invalid process ID %q", args[0])
	}

	// Unix arg layout (UpdateApp.ParseArgs:78-95): [1]=updateLocation
	// (sandbox), [2]=executingApplication, [3:]=preserved startup args.
	var updateLocation, executingApplication string
	var preserved []string
	if len(args) >= 3 {
		updateLocation = args[1]
		executingApplication = args[2]
		preserved = args[3:]
	}
	if updateLocation == "" || executingApplication == "" {
		return fmt.Errorf("updateLocation and executingApplication are required")
	}

	logger := slog.Default()
	logger.Info("sonarr-update: starting Sonarr Update Client",
		slog.Int("processId", processID),
		slog.String("updateLocation", updateLocation),
		slog.String("executingApplication", executingApplication))

	// Installation directory = the folder containing the running binary
	// (UpdateApp.GetInstallationDirectory:114-130).
	installationFolder := filepath.Dir(executingApplication)

	// Resolve the app-data folder from the preserved startup args (the
	// `/data` override survives the restart) so the appdata backup can
	// find config.xml + the database.
	appData := resolveAppData(preserved)

	paths := enginePaths{updateLocation: updateLocation, appData: appData}
	proc := newOSProcessController(preserved)
	diskProv := disk.NewProvider(logger)

	engine, err := updateengine.NewEngine(diskProv, proc, paths, logger)
	if err != nil {
		return fmt.Errorf("build update engine: %w", err)
	}

	if err := engine.Start(installationFolder, processID); err != nil {
		return err
	}
	logger.Info("sonarr-update: update completed successfully")
	return nil
}

// resolveAppData derives the app-data folder from the preserved startup
// arguments (honouring a `/data=` override), falling back to the OS
// convention. Best-effort — the appdata backup it feeds is itself
// best-effort.
func resolveAppData(preserved []string) string {
	ctx := appenv.ParseStartupArgs(preserved)
	folder, err := appenv.DetectAppFolder(ctx)
	if err != nil {
		return ""
	}
	return folder.AppData
}

// enginePaths satisfies updateengine.Paths from the sandbox
// (updateLocation) + app-data folder. The sandbox-relative folders
// mirror appenv.AppFolderInfo's update-path helpers (the sandbox passed
// here IS AppFolderInfo.UpdateSandboxFolder()).
type enginePaths struct {
	updateLocation string
	appData        string
}

func (p enginePaths) UpdatePackageFolder() string {
	return filepath.Join(p.updateLocation, "Sonarr")
}
func (p enginePaths) UpdateBackupFolder() string {
	return filepath.Join(p.updateLocation, "sonarr_backup")
}
func (p enginePaths) UpdateBackupAppDataFolder() string {
	return filepath.Join(p.updateLocation, "sonarr_appdata_backup")
}
func (p enginePaths) ConfigPath() string {
	if p.appData == "" {
		return ""
	}
	return filepath.Join(p.appData, "config.xml")
}
func (p enginePaths) DatabasePath() string {
	if p.appData == "" {
		return ""
	}
	return filepath.Join(p.appData, "sonarr.db")
}
