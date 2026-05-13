package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/buildinfo"
	"sonarr.tv/sonarr/internal/datastore"
)

// buildSystemStatusContext gathers the boot-static fields the
// /api/v3/system/status endpoint emits. Mirrors the C# aggregation
// scattered across `SystemController.GetStatus`, `RuntimeInfo`,
// `OsInfo`, and `IAppFolderInfo`.
//
// Platform detection is intentionally minimal here:
//
//   - isLinux/isOsx/isWindows derive from runtime.GOOS.
//   - isDocker is /.dockerenv (the standard Docker marker file).
//     isContainerized matches — k8s/podman/containerd would extend
//     this in a follow-up if needed.
//   - isAdmin is Unix-only (`os.Geteuid() == 0`); Windows returns
//     false until the windows-API path lands.
//   - isUserInteractive is a stdout-char-device check (works for
//     terminals + ttys, false when stdout is redirected/piped).
//   - OS version is intentionally the runtime.GOARCH suffix only —
//     the full /etc/os-release / sw_vers / registry parsing is DEFERRED.
//
// Database version uses each engine's own SELECT (sqlite_version() /
// version()); MigrationVersion comes from the goose schema_migrations
// row via datastore.CurrentVersion.
func buildSystemStatusContext(ctx context.Context, db *datastore.DB, folder appenv.AppFolderInfo, startTime time.Time) (apiv3.SystemStatusContext, error) {
	migration, err := datastore.CurrentVersion(ctx, db)
	if err != nil {
		return apiv3.SystemStatusContext{}, fmt.Errorf("system/status: migration version: %w", err)
	}

	dbVersion, err := queryDatabaseVersion(ctx, db)
	if err != nil {
		return apiv3.SystemStatusContext{}, fmt.Errorf("system/status: db version: %w", err)
	}

	exePath, _ := os.Executable() // fall through with empty string on rare failures

	return apiv3.SystemStatusContext{
		Version:           buildinfo.Version,
		BuildTime:         parseBuildTime(buildinfo.BuildDate),
		StartTime:         startTime,
		StartupPath:       exePath,
		AppData:           folder.AppData,
		OsName:            osDisplayName(runtime.GOOS),
		OsVersion:         runtime.GOARCH,
		RuntimeName:       "Go",
		RuntimeVersion:    runtime.Version(),
		DatabaseType:      string(db.Engine),
		DatabaseVersion:   dbVersion,
		MigrationVersion:  int(migration),
		IsDebug:           buildinfo.Version == "dev",
		IsProduction:      buildinfo.Version != "dev",
		IsAdmin:           runtime.GOOS != "windows" && os.Geteuid() == 0,
		IsUserInteractive: stdoutIsTerminal(),
		IsLinux:           runtime.GOOS == "linux",
		IsOsx:             runtime.GOOS == "darwin",
		IsWindows:         runtime.GOOS == "windows",
		IsDocker:          fileExists("/.dockerenv"),
		IsContainerized:   fileExists("/.dockerenv"),
		Mode:              "console",
	}, nil
}

// queryDatabaseVersion runs the engine-specific version query.
func queryDatabaseVersion(ctx context.Context, db *datastore.DB) (string, error) {
	var q string
	switch db.Engine {
	case datastore.EngineSQLite:
		q = "SELECT sqlite_version()"
	case datastore.EnginePostgres:
		q = "SELECT version()"
	default:
		return "", fmt.Errorf("unsupported engine: %q", db.Engine)
	}
	var v string
	if err := db.SQL.QueryRowContext(ctx, q).Scan(&v); err != nil {
		return "", err
	}
	return v, nil
}

// parseBuildTime converts the ldflag-injected `BuildDate` (RFC3339)
// to time.Time. Dev builds set it to "unknown" — those return zero
// time which JSON-marshals as "0001-01-01T00:00:00Z", matching C#
// `DateTime.MinValue` for the same case.
func parseBuildTime(s string) time.Time {
	if s == "" || s == "unknown" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// osDisplayName returns a friendly name per runtime.GOOS, matching
// what `Environment.OSVersion.Platform` produces on the C# side for
// the recorded golden's macOS host ("Darwin" / "Linux" / "Windows").
func osDisplayName(goos string) string {
	switch goos {
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	case "freebsd":
		return "FreeBSD"
	}
	return strings.Title(goos) //nolint:staticcheck // small ASCII display name; strings.Title is OK
}

// stdoutIsTerminal reports whether stdout is attached to a terminal.
// Pure-stdlib via os.File.Stat — character-device mode indicates a
// tty.
func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
