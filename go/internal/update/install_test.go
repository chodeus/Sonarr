package update_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/deploymentinfo"
	"sonarr.tv/sonarr/internal/update"
)

// --- stubs ---

type stubChecker struct {
	pkg *update.Package
	err error
}

func (s stubChecker) AvailableUpdate(context.Context) (*update.Package, error) {
	return s.pkg, s.err
}

type stubConfig struct {
	auto       bool
	mechanism  string
	branch     string
	scriptPath string
	savedTo    string
	saveErr    error
}

func (s *stubConfig) UpdateScriptPath(context.Context) (string, error) {
	return s.scriptPath, nil
}

func (s *stubConfig) UpdateAutomatically(context.Context) (bool, error) {
	return s.auto, nil
}
func (s *stubConfig) UpdateMechanism(context.Context) (string, error) {
	return s.mechanism, nil
}
func (s *stubConfig) Branch(context.Context) (string, error) {
	return s.branch, nil
}
func (s *stubConfig) SetBranch(_ context.Context, b string) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.savedTo = b
	return nil
}

type stubDeploy struct {
	pkgMech  deploymentinfo.UpdateMechanism
	external bool
}

func (s stubDeploy) PackageUpdateMechanism() deploymentinfo.UpdateMechanism { return s.pkgMech }
func (s stubDeploy) IsExternalUpdateMechanism() bool                        { return s.external }

func newInstaller(t *testing.T, c update.Checker, cfg update.InstallConfig, dep update.DeploymentProvider, build string) *update.Installer {
	t.Helper()
	inst, err := update.NewInstaller(c, cfg, dep, "/opt/sonarr", "/var/lib/sonarr", build, nil)
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}
	return inst
}

// --- GetUpdatePackage gates ---

func TestGetUpdatePackage_NoUpdate(t *testing.T) {
	t.Parallel()
	inst := newInstaller(t, stubChecker{pkg: nil}, &stubConfig{auto: true, mechanism: "BuiltIn"}, stubDeploy{}, "5.0.0.1")
	pkg, err := inst.GetUpdatePackage(context.Background(), false, false)
	if err != nil || pkg != nil {
		t.Fatalf("got (%v,%v), want (nil,nil)", pkg, err)
	}
}

func TestGetUpdatePackage_CheckerError(t *testing.T) {
	t.Parallel()
	inst := newInstaller(t, stubChecker{err: errors.New("boom")}, &stubConfig{auto: true, mechanism: "BuiltIn"}, stubDeploy{}, "5.0.0.1")
	if _, err := inst.GetUpdatePackage(context.Background(), false, false); err == nil {
		t.Fatal("expected error from checker")
	}
}

func TestGetUpdatePackage_MajorUpdateBlockedUnlessAllowed(t *testing.T) {
	t.Parallel()
	pkg := &update.Package{Version: "6.0.0.1", Branch: "main"}
	cfg := &stubConfig{auto: true, mechanism: "BuiltIn"}
	inst := newInstaller(t, stubChecker{pkg: pkg}, cfg, stubDeploy{}, "5.0.0.1")

	// Major bump, not allowed → nil.
	got, err := inst.GetUpdatePackage(context.Background(), true, false)
	if err != nil || got != nil {
		t.Fatalf("major blocked: got (%v,%v), want (nil,nil)", got, err)
	}
	// Major bump, allowed → returns the package.
	got, err = inst.GetUpdatePackage(context.Background(), true, true)
	if err != nil || got == nil {
		t.Fatalf("major allowed: got (%v,%v), want package", got, err)
	}
}

func TestGetUpdatePackage_AutoDisabledNonManual(t *testing.T) {
	t.Parallel()
	pkg := &update.Package{Version: "5.0.0.2", Branch: "main"}
	cfg := &stubConfig{auto: false, mechanism: "BuiltIn"}
	inst := newInstaller(t, stubChecker{pkg: pkg}, cfg, stubDeploy{}, "5.0.0.1")

	// Auto off + scheduled trigger → no install.
	got, _ := inst.GetUpdatePackage(context.Background(), false, false)
	if got != nil {
		t.Fatalf("auto-off scheduled: got %v, want nil", got)
	}
	// Auto off + manual trigger → bypasses the gate.
	got, _ = inst.GetUpdatePackage(context.Background(), true, false)
	if got == nil {
		t.Fatal("auto-off manual: want package (manual bypass)")
	}
}

func TestGetUpdatePackage_BuiltInInDocker(t *testing.T) {
	t.Parallel()
	pkg := &update.Package{Version: "5.0.0.2", Branch: "main"}
	cfg := &stubConfig{auto: true, mechanism: "BuiltIn"}
	dep := stubDeploy{pkgMech: deploymentinfo.MechanismDocker, external: true}
	inst := newInstaller(t, stubChecker{pkg: pkg}, cfg, dep, "5.0.0.1")
	got, _ := inst.GetUpdatePackage(context.Background(), false, false)
	if got != nil {
		t.Fatalf("builtin-in-docker: got %v, want nil", got)
	}
}

func TestGetUpdatePackage_BuiltInExternalMechanism(t *testing.T) {
	t.Parallel()
	pkg := &update.Package{Version: "5.0.0.2", Branch: "main"}
	cfg := &stubConfig{auto: true, mechanism: "BuiltIn"}
	dep := stubDeploy{pkgMech: deploymentinfo.MechanismApt, external: true}
	inst := newInstaller(t, stubChecker{pkg: pkg}, cfg, dep, "5.0.0.1")
	got, _ := inst.GetUpdatePackage(context.Background(), false, false)
	if got != nil {
		t.Fatalf("builtin+external: got %v, want nil", got)
	}
}

func TestGetUpdatePackage_NonScriptExternalMechanism(t *testing.T) {
	t.Parallel()
	pkg := &update.Package{Version: "5.0.0.2", Branch: "main"}
	// External mechanism configured + external deployment → no-op.
	cfg := &stubConfig{auto: true, mechanism: "External"}
	dep := stubDeploy{pkgMech: deploymentinfo.MechanismApt, external: true}
	inst := newInstaller(t, stubChecker{pkg: pkg}, cfg, dep, "5.0.0.1")
	got, _ := inst.GetUpdatePackage(context.Background(), false, false)
	if got != nil {
		t.Fatalf("external mechanism: got %v, want nil", got)
	}
}

func TestGetUpdatePackage_ScriptWithExternalDeployment_Installs(t *testing.T) {
	t.Parallel()
	// Script mechanism is explicitly allowed even on an external
	// deployment (the else-if guards `mech != Script`).
	pkg := &update.Package{Version: "5.0.0.2", Branch: "main"}
	cfg := &stubConfig{auto: true, mechanism: "Script"}
	dep := stubDeploy{pkgMech: deploymentinfo.MechanismApt, external: true}
	inst := newInstaller(t, stubChecker{pkg: pkg}, cfg, dep, "5.0.0.1")
	got, _ := inst.GetUpdatePackage(context.Background(), false, false)
	if got == nil {
		t.Fatal("script+external: want package (script bypasses external gate)")
	}
}

func TestGetUpdatePackage_BuiltInHappyPath(t *testing.T) {
	t.Parallel()
	pkg := &update.Package{Version: "5.0.0.2", Branch: "main"}
	cfg := &stubConfig{auto: true, mechanism: "BuiltIn"}
	dep := stubDeploy{pkgMech: deploymentinfo.MechanismBuiltIn, external: false}
	inst := newInstaller(t, stubChecker{pkg: pkg}, cfg, dep, "5.0.0.1")
	got, err := inst.GetUpdatePackage(context.Background(), false, false)
	if err != nil || got == nil {
		t.Fatalf("happy path: got (%v,%v), want package", got, err)
	}
}

// --- EnsureAppDataSafety ---

func TestEnsureAppDataSafety(t *testing.T) {
	t.Parallel()
	cfg := &stubConfig{}
	dep := stubDeploy{}
	chk := stubChecker{}

	// Safe: appdata outside startup.
	safe, _ := update.NewInstaller(chk, cfg, dep, "/opt/sonarr", "/var/lib/sonarr", "5.0.0.1", nil)
	if err := safe.EnsureAppDataSafety(); err != nil {
		t.Fatalf("disjoint paths should be safe, got %v", err)
	}

	// Unsafe: appdata inside startup.
	nested, _ := update.NewInstaller(chk, cfg, dep, "/opt/sonarr", filepath.Join("/opt/sonarr", "config"), "5.0.0.1", nil)
	if err := nested.EnsureAppDataSafety(); err == nil {
		t.Fatal("nested appdata should abort")
	}

	// Unsafe: appdata == startup.
	equal, _ := update.NewInstaller(chk, cfg, dep, "/opt/sonarr", "/opt/sonarr", "5.0.0.1", nil)
	if err := equal.EnsureAppDataSafety(); err == nil {
		t.Fatal("equal appdata/startup should abort")
	}
}

// --- EnsureValidBranch ---

func TestEnsureValidBranch_RedirectsOnDifference(t *testing.T) {
	t.Parallel()
	cfg := &stubConfig{branch: "main"}
	inst := newInstaller(t, stubChecker{}, cfg, stubDeploy{}, "5.0.0.1")
	inst.EnsureValidBranch(context.Background(), &update.Package{Branch: "develop"})
	if cfg.savedTo != "develop" {
		t.Fatalf("savedTo = %q, want develop", cfg.savedTo)
	}
}

func TestEnsureValidBranch_NoopWhenSame(t *testing.T) {
	t.Parallel()
	cfg := &stubConfig{branch: "main"}
	inst := newInstaller(t, stubChecker{}, cfg, stubDeploy{}, "5.0.0.1")
	inst.EnsureValidBranch(context.Background(), &update.Package{Branch: "main"})
	if cfg.savedTo != "" {
		t.Fatalf("savedTo = %q, want unchanged", cfg.savedTo)
	}
}

func TestEnsureValidBranch_SwallowsSaveError(t *testing.T) {
	t.Parallel()
	cfg := &stubConfig{branch: "main", saveErr: errors.New("disk full")}
	inst := newInstaller(t, stubChecker{}, cfg, stubDeploy{}, "5.0.0.1")
	// Must not panic / propagate (C# try/catch swallows).
	inst.EnsureValidBranch(context.Background(), &update.Package{Branch: "develop"})
}
