package appenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AppFolderInfo is the canonical path bundle the rest of the binary
// reads — data folder (where config.xml + the SQLite DBs live),
// startup folder (the directory the binary itself is in, used for
// loading sibling assets), and temp folder.
//
// Mirrors NzbDrone.Common.EnvironmentInfo.AppFolderInfo. The C# field
// LegacyAppDataFolder (used to migrate v3 NzbDrone installs) is
// deliberately omitted at Phase 1 — that's a Phase 6 cutover concern.
type AppFolderInfo struct {
	AppData string // <data>/Sonarr (or whatever --data overrides to)
	Startup string // directory of the running binary
	Temp    string // os.TempDir()
}

// ConfigPath returns the full path to config.xml inside the app data
// directory. Mirrors `IAppFolderInfo.GetConfigPath` from the C#
// extension method (`StringExtensions.GetConfigPath`).
func (a AppFolderInfo) ConfigPath() string {
	return filepath.Join(a.AppData, "config.xml")
}

// MainDBPath returns the path to the main SQLite database file.
// Mirrors `IAppFolderInfo.GetDatabase` for the main DB role. C#
// hard-codes the file name to "<InstanceName>.db" — the Go port
// uses the same convention; callers wanting Postgres pass the
// PG DSN through configfile instead.
func (a AppFolderInfo) MainDBPath(instanceName string) string {
	return filepath.Join(a.AppData, instanceName+".db")
}

// LogDBPath returns the path to the log SQLite database file.
// Mirrors the log-DB role of `IAppFolderInfo.GetDatabase`.
func (a AppFolderInfo) LogDBPath(instanceName string) string {
	return filepath.Join(a.AppData, instanceName+"-log.db")
}

// UpdateSandboxFolder returns the directory where the in-place
// updater stages its working tree. Mirrors the C# extension method
// `PathExtensions.GetUpdateSandboxFolder` at
// `src/NzbDrone.Common/Extensions/PathExtensions.cs:343-346`:
// `Path.Combine(TempFolder, "sonarr_update")`.
//
// The C# constant adds a trailing path separator
// (`UPDATE_SANDBOX_FOLDER_NAME = "sonarr_update" + DirectorySeparatorChar`).
// The Go port omits the trailing separator because filepath.Join /
// os.RemoveAll behave the same with or without it; callers that
// concatenate further paths should keep using filepath.Join.
func (a AppFolderInfo) UpdateSandboxFolder() string {
	return filepath.Join(a.Temp, "sonarr_update")
}

// UpdatePackageFolder is the extracted-package subtree inside the
// sandbox. Mirrors `PathExtensions.GetUpdatePackageFolder` (`:368-371`):
// `Path.Combine(GetUpdateSandboxFolder(), "Sonarr")`.
func (a AppFolderInfo) UpdatePackageFolder() string {
	return filepath.Join(a.UpdateSandboxFolder(), "Sonarr")
}

// UpdateClientFolder is where the `Sonarr.Update` helper binary is
// extracted. Mirrors `PathExtensions.GetUpdateClientFolder` (`:373-376`):
// `Path.Combine(GetUpdatePackageFolder(), "Sonarr.Update")`.
func (a AppFolderInfo) UpdateClientFolder() string {
	return filepath.Join(a.UpdatePackageFolder(), "Sonarr.Update")
}

// UpdateClientExePath is the path the in-place updater spawns. Mirrors
// `PathExtensions.GetUpdateClientExePath` (`:378-381`):
// `Path.Combine(GetUpdateSandboxFolder(), "Sonarr.Update").ProcessNameToExe()`
// — note this resolves directly inside the sandbox root (the helper is
// moved there from UpdateClientFolder by the install transfer step), with
// a `.exe` suffix on Windows.
func (a AppFolderInfo) UpdateClientExePath() string {
	return processNameToExe(filepath.Join(a.UpdateSandboxFolder(), "Sonarr.Update"))
}

// processNameToExe ports `PathExtensions.ProcessNameToExe` (`:298-306`):
// appends `.exe` on Windows, otherwise returns the name unchanged.
func processNameToExe(processName string) string {
	if CurrentOs == OsWindows {
		return processName + ".exe"
	}
	return processName
}

// DetectAppFolder builds an AppFolderInfo for the current process.
// startupCtx may be nil; if it carries a `data` arg, that path
// becomes AppData verbatim. Otherwise the OS-conventional location
// is used:
//
//   - Linux / BSD: $XDG_CONFIG_HOME/Sonarr or $HOME/.config/Sonarr
//   - macOS:       $HOME/.config/Sonarr (matching the existing
//     C# Mono behaviour where ApplicationData resolves to ~/.config)
//   - Windows:     %PROGRAMDATA%\Sonarr (matching CommonApplicationData)
//
// Returns an error if the home / programdata directory cannot be
// resolved AND no --data override was provided.
func DetectAppFolder(startupCtx *StartupContext) (AppFolderInfo, error) {
	startup, err := executableDir()
	if err != nil {
		return AppFolderInfo{}, err
	}
	dataDir, err := resolveDataDir(startupCtx)
	if err != nil {
		return AppFolderInfo{}, err
	}
	return AppFolderInfo{
		AppData: dataDir,
		Startup: startup,
		Temp:    os.TempDir(),
	}, nil
}

func resolveDataDir(ctx *StartupContext) (string, error) {
	if ctx != nil {
		if v, ok := ctx.Args[StartupArgData]; ok && v != "" {
			return v, nil
		}
	}
	switch CurrentOs {
	case OsWindows:
		return windowsAppData()
	case OsDarwin, OsLinux, OsBSD:
		return unixAppData()
	}
	return "", fmt.Errorf("appenv: unsupported runtime os %d", CurrentOs)
}

// windowsAppData mirrors `Environment.SpecialFolder.CommonApplicationData`
// = `%PROGRAMDATA%`. Falls back to `C:\ProgramData` when the env var
// is missing (matches what Windows itself falls back to).
func windowsAppData() (string, error) {
	if v := os.Getenv("PROGRAMDATA"); v != "" {
		return filepath.Join(v, "Sonarr"), nil
	}
	return filepath.Join(`C:\ProgramData`, "Sonarr"), nil
}

// unixAppData prefers $XDG_CONFIG_HOME/Sonarr, falling back to
// $HOME/.config/Sonarr. macOS ports of Sonarr have historically
// stored config under ~/.config rather than the standard
// ~/Library/Application Support, so the Go port preserves the
// ~/.config layout for migration compatibility.
func unixAppData() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "Sonarr"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("appenv: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "Sonarr"), nil
}

// executableDir returns the absolute directory containing the
// running binary. Mirrors C# `new FileInfo(Assembly.GetExecutingAssembly()
// .Location).Directory.FullName`.
func executableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("appenv: locate executable: %w", err)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", fmt.Errorf("appenv: abs(%s): %w", exe, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Resolve-symlinks failure is recoverable: fall back to the
		// abs path. Some test environments run binaries via a
		// dangling /proc/<pid>/exe-style link that EvalSymlinks
		// rejects.
		if !errors.Is(err, os.ErrNotExist) {
			resolved = abs
		}
	}
	return filepath.Dir(resolved), nil
}
