package main

import (
	"path/filepath"
	"testing"
)

func TestRun_ArgValidation(t *testing.T) {
	// All of these fail during argument validation, BEFORE the update
	// engine is built — so no process is probed/killed and no files move.
	cases := [][]string{
		nil,                // no args
		{"abc"},            // non-numeric pid
		{"0"},              // non-positive pid
		{"-5"},             // negative pid
		{"123"},            // missing updateLocation + executingApplication
		{"123", "loc"},     // missing executingApplication
		{"123", "", "exe"}, // empty updateLocation
		{"123", "loc", ""}, // empty executingApplication
	}
	for _, args := range cases {
		if err := run(args); err == nil {
			t.Errorf("run(%v) = nil, want validation error", args)
		}
	}
}

func TestEnginePaths(t *testing.T) {
	p := enginePaths{updateLocation: "/tmp/sonarr_update", appData: "/var/lib/sonarr"}
	if got, want := p.UpdatePackageFolder(), filepath.Join("/tmp/sonarr_update", "Sonarr"); got != want {
		t.Errorf("UpdatePackageFolder = %q, want %q", got, want)
	}
	if got, want := p.UpdateBackupFolder(), filepath.Join("/tmp/sonarr_update", "sonarr_backup"); got != want {
		t.Errorf("UpdateBackupFolder = %q, want %q", got, want)
	}
	if got, want := p.UpdateBackupAppDataFolder(), filepath.Join("/tmp/sonarr_update", "sonarr_appdata_backup"); got != want {
		t.Errorf("UpdateBackupAppDataFolder = %q, want %q", got, want)
	}
	if got, want := p.ConfigPath(), filepath.Join("/var/lib/sonarr", "config.xml"); got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
	if got, want := p.DatabasePath(), filepath.Join("/var/lib/sonarr", "sonarr.db"); got != want {
		t.Errorf("DatabasePath = %q, want %q", got, want)
	}

	// Empty appData → empty config/db (best-effort skip).
	empty := enginePaths{updateLocation: "/x"}
	if empty.ConfigPath() != "" || empty.DatabasePath() != "" {
		t.Error("empty appData should yield empty config/db paths")
	}
}

func TestContainsFlag(t *testing.T) {
	if !containsFlag([]string{"/data=/x", "/nobrowser"}, "nobrowser") {
		t.Error("should find /nobrowser")
	}
	if containsFlag([]string{"/data=/x"}, "nobrowser") {
		t.Error("should not find nobrowser")
	}
}
