package disk_test

import (
	"log/slog"
	"runtime"
	"testing"

	"sonarr.tv/sonarr/internal/disk"
)

// GetMount on a writable t.TempDir is the smoke baseline — IsReadOnly
// must be false. The mount root + name are platform-specific; we just
// pin the writable behaviour.
func TestProvider_GetMount_WritableTempDir(t *testing.T) {
	t.Parallel()
	p := disk.NewProvider(slog.Default())
	tmp := t.TempDir()

	m, err := p.GetMount(tmp)
	if err != nil {
		t.Fatalf("GetMount(%q): %v", tmp, err)
	}
	if m == nil {
		t.Fatal("GetMount: nil Mount with nil error")
	}
	if m.IsReadOnly {
		t.Errorf("IsReadOnly: got true, want false (TempDir is writable)")
	}
	if m.Name == "" {
		t.Errorf("Name: got empty, want non-empty")
	}
	if m.RootDirectory == "" {
		t.Errorf("RootDirectory: got empty, want non-empty")
	}
}

// GetMount on a non-existent path surfaces an error. The exact error
// is OS-specific (ENOENT on Unix, file-not-found on Windows), so we
// just assert non-nil.
func TestProvider_GetMount_MissingPath(t *testing.T) {
	t.Parallel()
	p := disk.NewProvider(slog.Default())
	missing := "/this/path/should/not/exist/anywhere/sonarr-mount-test"
	if runtime.GOOS == "windows" {
		missing = `Z:\definitely-not-real\sonarr-mount-test`
	}
	if _, err := p.GetMount(missing); err == nil {
		t.Errorf("GetMount(%q): want error, got nil", missing)
	}
}
