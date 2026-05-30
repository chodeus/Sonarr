package updateengine_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/updateengine"
)

// stubProc records process operations WITHOUT performing them.
type stubProc struct {
	existsPID    map[int]bool
	existsByName bool
	appType      updateengine.AppType
	terminated   []int
	starts       int
	startErr     error
}

func (s *stubProc) Exists(pid int) bool                 { return s.existsPID[pid] }
func (s *stubProc) ExistsByName(string) bool            { return s.existsByName }
func (s *stubProc) DetectAppType() updateengine.AppType { return s.appType }
func (s *stubProc) Terminate(pid int) error {
	s.terminated = append(s.terminated, pid)
	return nil
}
func (s *stubProc) Start(updateengine.AppType, string) error {
	s.starts++
	return s.startErr
}

type testPaths struct {
	pkg, backup, appdataBackup, config, db string
}

func (p testPaths) UpdatePackageFolder() string       { return p.pkg }
func (p testPaths) UpdateBackupFolder() string        { return p.backup }
func (p testPaths) UpdateBackupAppDataFolder() string { return p.appdataBackup }
func (p testPaths) ConfigPath() string                { return p.config }
func (p testPaths) DatabasePath() string              { return p.db }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// scenario builds an install dir (old build), a package folder (new
// build), config + db, and an engine wired to a real disk.Provider with
// a no-op restart wait.
func scenario(t *testing.T, proc *stubProc) (*updateengine.Engine, string, testPaths) {
	t.Helper()
	root := t.TempDir()
	install := filepath.Join(root, "install")
	pkg := filepath.Join(root, "pkg")
	paths := testPaths{
		pkg:           pkg,
		backup:        filepath.Join(root, "backup"),
		appdataBackup: filepath.Join(root, "appdata_backup"),
		config:        filepath.Join(root, "config.xml"),
		db:            filepath.Join(root, "sonarr.db"),
	}
	// Old install + new package.
	writeFile(t, filepath.Join(install, "Sonarr"), "old-elf")
	writeFile(t, filepath.Join(install, "stale"), "remove-me")
	writeFile(t, filepath.Join(pkg, "Sonarr"), "new-elf")
	writeFile(t, filepath.Join(pkg, "lib", "x.so"), "new-lib")
	// Appdata to back up.
	writeFile(t, paths.config, "<Config/>")
	writeFile(t, paths.db, "sqlite-bytes")

	eng, err := updateengine.NewEngine(disk.NewProvider(slog.New(slog.DiscardHandler)), proc, paths, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	eng.WithRestartWait(2, func(time.Duration) {}) // no real sleep
	return eng, install, paths
}

func TestEngine_Start_HappyPath(t *testing.T) {
	t.Parallel()
	proc := &stubProc{existsPID: map[int]bool{42: true}, existsByName: true} // external auto-restart succeeds
	eng, install, paths := scenario(t, proc)

	if err := eng.Start(install, 42); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// New build mirrored over install; stale file removed.
	if got := readFile(t, filepath.Join(install, "Sonarr")); got != "new-elf" {
		t.Errorf("install/Sonarr = %q, want new-elf", got)
	}
	if got := readFile(t, filepath.Join(install, "lib", "x.so")); got != "new-lib" {
		t.Errorf("install/lib/x.so = %q, want new-lib", got)
	}
	if _, err := os.Stat(filepath.Join(install, "stale")); !os.IsNotExist(err) {
		t.Error("stale install file should be removed by the mirror")
	}
	// Backup captured the OLD build.
	if got := readFile(t, filepath.Join(paths.backup, "Sonarr")); got != "old-elf" {
		t.Errorf("backup/Sonarr = %q, want old-elf (pre-upgrade)", got)
	}
	// Appdata backed up.
	if got := readFile(t, filepath.Join(paths.appdataBackup, "config.xml")); got != "<Config/>" {
		t.Errorf("appdata config backup = %q", got)
	}
	if got := readFile(t, filepath.Join(paths.appdataBackup, "sonarr.db")); got != "sqlite-bytes" {
		t.Errorf("appdata db backup = %q", got)
	}
	// Process was terminated; external restart succeeded → no manual Start.
	if len(proc.terminated) == 0 {
		t.Error("expected the old process to be terminated")
	}
	if proc.starts != 0 {
		t.Errorf("manual Start called %d times, want 0 (external restart)", proc.starts)
	}
}

func TestEngine_Start_ManualRestartWhenNoExternalSupervisor(t *testing.T) {
	t.Parallel()
	proc := &stubProc{existsPID: map[int]bool{42: true}, existsByName: false} // never auto-restarts
	eng, install, _ := scenario(t, proc)
	if err := eng.Start(install, 42); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if proc.starts != 1 {
		t.Errorf("manual Start called %d times, want 1 (no external supervisor)", proc.starts)
	}
}

func TestEngine_Verify_Failures(t *testing.T) {
	t.Parallel()

	// pid not existing.
	proc := &stubProc{existsPID: map[int]bool{}}
	eng, install, _ := scenario(t, proc)
	if err := eng.Start(install, 42); err == nil {
		t.Error("expected error when process doesn't exist")
	}

	// pid < 1.
	proc2 := &stubProc{existsPID: map[int]bool{0: true}}
	eng2, install2, _ := scenario(t, proc2)
	if err := eng2.Start(install2, 0); err == nil {
		t.Error("expected error for invalid pid")
	}

	// nonexistent target folder.
	proc3 := &stubProc{existsPID: map[int]bool{42: true}}
	eng3, _, _ := scenario(t, proc3)
	if err := eng3.Start(filepath.Join(t.TempDir(), "nope"), 42); err == nil {
		t.Error("expected error for missing target folder")
	}
}

func TestEngine_Start_MissingPackageFolder(t *testing.T) {
	t.Parallel()
	proc := &stubProc{existsPID: map[int]bool{42: true}}
	root := t.TempDir()
	install := filepath.Join(root, "install")
	writeFile(t, filepath.Join(install, "Sonarr"), "old")
	paths := testPaths{
		pkg:           filepath.Join(root, "missing_pkg"), // does NOT exist
		backup:        filepath.Join(root, "backup"),
		appdataBackup: filepath.Join(root, "ab"),
		config:        filepath.Join(root, "config.xml"),
		db:            filepath.Join(root, "sonarr.db"),
	}
	eng, err := updateengine.NewEngine(disk.NewProvider(slog.New(slog.DiscardHandler)), proc, paths, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(install, 42); err == nil {
		t.Error("expected error when the update package folder is missing")
	}
}

func TestNewEngine_NilDeps(t *testing.T) {
	t.Parallel()
	if _, err := updateengine.NewEngine(nil, &stubProc{}, testPaths{}, nil); err == nil {
		t.Error("nil disk should error")
	}
	if _, err := updateengine.NewEngine(disk.NewProvider(slog.New(slog.DiscardHandler)), nil, testPaths{}, nil); err == nil {
		t.Error("nil proc should error")
	}
}
