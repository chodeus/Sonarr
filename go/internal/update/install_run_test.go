package update_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/update"
)

// --- run-dep stubs ---

type stubDownloader struct {
	payload []byte // copied to dest on DownloadFile
	err     error
}

func (s stubDownloader) DownloadFile(_ context.Context, _, dest string) error {
	if s.err != nil {
		return s.err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, s.payload, 0o644)
}

type stubBackup struct{ called bool }

func (s *stubBackup) BackupForUpdate(context.Context) error { s.called = true; return nil }

type stubStarter struct {
	started bool
	path    string
	args    string
}

func (s *stubStarter) Start(_ context.Context, path, args string) error {
	s.started = true
	s.path = path
	s.args = args
	return nil
}

type testPaths struct{ temp, sandbox string }

func (p testPaths) TempFolder() string          { return p.temp }
func (p testPaths) UpdateSandboxFolder() string { return p.sandbox }
func (p testPaths) UpdateClientFolder() string {
	return filepath.Join(p.sandbox, "Sonarr", "Sonarr.Update")
}
func (p testPaths) UpdateClientExePath() string { return filepath.Join(p.sandbox, "Sonarr.Update") }

// buildClientZip builds a zip whose layout, after Extract → sandbox +
// TransferFolder(clientFolder → sandbox), lands the update-client exe at
// UpdateClientExePath. Returns (zipBytes, sha256Hex).
func buildClientZip(t *testing.T) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	zp := filepath.Join(dir, "pkg.zip")
	writeZip(t, zp, map[string]string{
		"Sonarr/Sonarr.Update/Sonarr.Update": "#!/bin/sh\necho updating",
		"Sonarr/Sonarr.dll":                  "binary",
	})
	b, err := os.ReadFile(zp)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])
}

func runInstaller(t *testing.T, cfg *stubConfig, paths testPaths, dl update.Downloader, starter update.ProcessStarter, backup update.Backupper, startupDir string) *update.Installer {
	t.Helper()
	inst, err := update.NewInstaller(stubChecker{}, cfg, stubDeploy{}, startupDir, filepath.Join(t.TempDir(), "appdata"), "5.0.0.1", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}
	inst.SetRunDeps(update.RunDeps{
		Disk:                 disk.NewProvider(slog.New(slog.DiscardHandler)),
		Downloader:           dl,
		Backup:               backup,
		Starter:              starter,
		Paths:                paths,
		ExecutingApplication: "/opt/sonarr/Sonarr",
		PreservedArguments:   "/data=/var/lib/sonarr",
	})
	return inst
}

func writableStartup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "UI"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstallUpdate_BuiltInHappyPath(t *testing.T) {
	t.Parallel()
	payload, hash := buildClientZip(t)
	sandbox := filepath.Join(t.TempDir(), "sonarr_update")
	paths := testPaths{temp: t.TempDir(), sandbox: sandbox}
	cfg := &stubConfig{auto: true, mechanism: "BuiltIn", branch: "main"}
	starter := &stubStarter{}
	backup := &stubBackup{}
	inst := runInstaller(t, cfg, paths, stubDownloader{payload: payload}, starter, backup, writableStartup(t))

	pkg := &update.Package{Version: "5.0.0.2", Branch: "main", FileName: "Sonarr.zip", URL: "http://x/Sonarr.zip", Hash: hash}
	inProgress, err := inst.InstallUpdate(context.Background(), pkg)
	if err != nil {
		t.Fatalf("InstallUpdate: %v", err)
	}
	if !inProgress {
		t.Fatal("expected inProgress=true")
	}
	if !backup.called {
		t.Error("pre-update backup not taken")
	}
	if !starter.started {
		t.Fatal("update client not started")
	}
	if starter.path != paths.UpdateClientExePath() {
		t.Errorf("started %q, want %q", starter.path, paths.UpdateClientExePath())
	}
	// args: "<pid> \"<sandbox>\" \"<exe>\" /data=..."
	if !strings.Contains(starter.args, "\""+sandbox+"\"") {
		t.Errorf("args missing quoted sandbox: %q", starter.args)
	}
	if !strings.Contains(starter.args, "/data=/var/lib/sonarr") {
		t.Errorf("args missing preserved arguments: %q", starter.args)
	}
	// The exe really landed at UpdateClientExePath.
	if _, statErr := os.Stat(paths.UpdateClientExePath()); statErr != nil {
		t.Errorf("update client exe not staged: %v", statErr)
	}
}

func TestInstallUpdate_ScriptMode(t *testing.T) {
	t.Parallel()
	payload, hash := buildClientZip(t)
	sandbox := filepath.Join(t.TempDir(), "sonarr_update")
	paths := testPaths{temp: t.TempDir(), sandbox: sandbox}

	// A real script file on disk (script-mode requires it to exist).
	scriptPath := filepath.Join(t.TempDir(), "update.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &stubConfig{auto: true, mechanism: "Script", branch: "main", scriptPath: scriptPath}
	starter := &stubStarter{}
	inst := runInstaller(t, cfg, paths, stubDownloader{payload: payload}, starter, &stubBackup{}, writableStartup(t))

	pkg := &update.Package{Version: "5.0.0.2", Branch: "main", FileName: "Sonarr.zip", Hash: hash}
	inProgress, err := inst.InstallUpdate(context.Background(), pkg)
	if err != nil {
		t.Fatalf("InstallUpdate(script): %v", err)
	}
	if !inProgress || !starter.started {
		t.Fatal("script mode should spawn the script")
	}
	if starter.path != scriptPath {
		t.Errorf("started %q, want script %q", starter.path, scriptPath)
	}
}

func TestInstallUpdate_ScriptModeUndefinedScript(t *testing.T) {
	t.Parallel()
	payload, hash := buildClientZip(t)
	paths := testPaths{temp: t.TempDir(), sandbox: filepath.Join(t.TempDir(), "sonarr_update")}
	cfg := &stubConfig{auto: true, mechanism: "Script", branch: "main", scriptPath: ""}
	starter := &stubStarter{}
	inst := runInstaller(t, cfg, paths, stubDownloader{payload: payload}, starter, &stubBackup{}, writableStartup(t))

	pkg := &update.Package{Version: "5.0.0.2", Branch: "main", FileName: "Sonarr.zip", Hash: hash}
	_, err := inst.InstallUpdate(context.Background(), pkg)
	if !errors.Is(err, update.ErrUpdateFailed) {
		t.Fatalf("undefined script: got %v, want ErrUpdateFailed", err)
	}
	if starter.started {
		t.Error("script must not spawn when undefined")
	}
}

func TestInstallUpdate_VerificationFailure(t *testing.T) {
	t.Parallel()
	payload, _ := buildClientZip(t)
	paths := testPaths{temp: t.TempDir(), sandbox: filepath.Join(t.TempDir(), "sonarr_update")}
	cfg := &stubConfig{auto: true, mechanism: "BuiltIn", branch: "main"}
	starter := &stubStarter{}
	inst := runInstaller(t, cfg, paths, stubDownloader{payload: payload}, starter, &stubBackup{}, writableStartup(t))

	// Wrong hash → verification fails, never spawns.
	pkg := &update.Package{Version: "5.0.0.2", Branch: "main", FileName: "Sonarr.zip", Hash: "deadbeef"}
	_, err := inst.InstallUpdate(context.Background(), pkg)
	if !errors.Is(err, update.ErrUpdateVerificationFailed) {
		t.Fatalf("got %v, want ErrUpdateVerificationFailed", err)
	}
	if starter.started {
		t.Error("must not spawn on verification failure")
	}
}

func TestInstallUpdate_DevEnvironmentSkips(t *testing.T) {
	t.Parallel()
	payload, hash := buildClientZip(t)
	paths := testPaths{temp: t.TempDir(), sandbox: filepath.Join(t.TempDir(), "sonarr_update")}
	cfg := &stubConfig{auto: true, mechanism: "BuiltIn", branch: "main"}
	starter := &stubStarter{}
	// Startup folder ending in _output → dev env, skip.
	dev := filepath.Join(t.TempDir(), "bin_output")
	if err := os.MkdirAll(filepath.Join(dev, "UI"), 0o755); err != nil {
		t.Fatal(err)
	}
	inst := runInstaller(t, cfg, paths, stubDownloader{payload: payload}, starter, &stubBackup{}, dev)

	pkg := &update.Package{Version: "5.0.0.2", Branch: "main", FileName: "Sonarr.zip", Hash: hash}
	inProgress, err := inst.InstallUpdate(context.Background(), pkg)
	if err != nil {
		t.Fatalf("dev env: %v", err)
	}
	if inProgress || starter.started {
		t.Fatal("dev environment must skip install (no spawn)")
	}
}

func TestInstallUpdate_AppDataNestedAborts(t *testing.T) {
	t.Parallel()
	startup := writableStartup(t)
	inst, err := update.NewInstaller(stubChecker{}, &stubConfig{mechanism: "BuiltIn"}, stubDeploy{},
		startup, filepath.Join(startup, "config"), "5.0.0.1", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	inst.SetRunDeps(update.RunDeps{
		Disk:    disk.NewProvider(slog.New(slog.DiscardHandler)),
		Paths:   testPaths{temp: t.TempDir(), sandbox: filepath.Join(t.TempDir(), "s")},
		Starter: &stubStarter{}, Downloader: stubDownloader{}, Backup: &stubBackup{},
	})
	_, err = inst.InstallUpdate(context.Background(), &update.Package{Version: "5.0.0.2"})
	if !errors.Is(err, update.ErrUpdateAborted) {
		t.Fatalf("nested appdata: got %v, want ErrUpdateAborted", err)
	}
}

func TestInstallUpdate_RequiresRunDeps(t *testing.T) {
	t.Parallel()
	inst := newInstaller(t, stubChecker{}, &stubConfig{mechanism: "BuiltIn"}, stubDeploy{}, "5.0.0.1")
	if _, err := inst.InstallUpdate(context.Background(), &update.Package{}); err == nil {
		t.Fatal("InstallUpdate without SetRunDeps should error")
	}
}
