package appenv

import (
	"path/filepath"
	"testing"
)

func TestUpdateClientPaths(t *testing.T) {
	a := AppFolderInfo{Temp: "/tmp", Startup: "/opt/sonarr", AppData: "/var/lib/sonarr"}

	sandbox := filepath.Join("/tmp", "sonarr_update")
	if got := a.UpdateSandboxFolder(); got != sandbox {
		t.Errorf("UpdateSandboxFolder = %q, want %q", got, sandbox)
	}
	if got, want := a.UpdatePackageFolder(), filepath.Join(sandbox, "Sonarr"); got != want {
		t.Errorf("UpdatePackageFolder = %q, want %q", got, want)
	}
	if got, want := a.UpdateClientFolder(), filepath.Join(sandbox, "Sonarr", "Sonarr.Update"); got != want {
		t.Errorf("UpdateClientFolder = %q, want %q", got, want)
	}
	// UpdateClientExePath resolves directly inside the sandbox root, with
	// a .exe suffix only on Windows.
	wantExe := filepath.Join(sandbox, "Sonarr.Update")
	if CurrentOs == OsWindows {
		wantExe += ".exe"
	}
	if got := a.UpdateClientExePath(); got != wantExe {
		t.Errorf("UpdateClientExePath = %q, want %q", got, wantExe)
	}
}
