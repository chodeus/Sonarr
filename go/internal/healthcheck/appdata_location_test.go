package healthcheck_test

import (
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/healthcheck"
)

// TestAppDataLocation_AppDataInsideStartupWarns mirrors the C# guard
// at `AppDataLocationCheck.cs:19-27` — startup folder is a parent of
// the app-data folder. This is the dangerous configuration the check
// is designed to warn against.
func TestAppDataLocation_AppDataInsideStartupWarns(t *testing.T) {
	t.Parallel()
	startup := t.TempDir()
	appData := filepath.Join(startup, "data")
	folder := appenv.AppFolderInfo{Startup: startup, AppData: appData}

	c := healthcheck.NewAppDataLocationCheck(folder)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("expected ResultWarning, got %v: %+v", got.Type, got)
	}
	if got.Reason != healthcheck.ReasonAppDataLocation {
		t.Errorf("Reason = %v, want ReasonAppDataLocation", got.Reason)
	}
	wantSuffix := "#updating-will-not-be-possible-to-prevent-deleting-appdata-on-update"
	if got.WikiURL == "" || got.WikiURL[len(got.WikiURL)-len(wantSuffix):] != wantSuffix {
		t.Errorf("WikiURL = %q, want suffix %q", got.WikiURL, wantSuffix)
	}
}

// TestAppDataLocation_AppDataEqualsStartupWarns covers the second
// branch of the C# guard — startup and app-data are the SAME folder.
func TestAppDataLocation_AppDataEqualsStartupWarns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	folder := appenv.AppFolderInfo{Startup: dir, AppData: dir}
	c := healthcheck.NewAppDataLocationCheck(folder)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("equal folders: expected Warning, got %+v", got)
	}
}

// TestAppDataLocation_DistinctFoldersOK is the happy path — startup
// and app-data are disjoint.
func TestAppDataLocation_DistinctFoldersOK(t *testing.T) {
	t.Parallel()
	startup := t.TempDir()
	appData := t.TempDir()
	folder := appenv.AppFolderInfo{Startup: startup, AppData: appData}
	c := healthcheck.NewAppDataLocationCheck(folder)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("disjoint folders: expected OK, got %+v", got)
	}
}

// TestAppDataLocation_DefaultsAreScheduledAndStartup pins the
// HealthCheckBase defaults — both flags true.
func TestAppDataLocation_DefaultsAreScheduledAndStartup(t *testing.T) {
	t.Parallel()
	c := healthcheck.NewAppDataLocationCheck(appenv.AppFolderInfo{})
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup = false, want true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule = false, want true")
	}
	if c.Name() != "AppDataLocationCheck" {
		t.Errorf("Name = %q, want %q", c.Name(), "AppDataLocationCheck")
	}
}
