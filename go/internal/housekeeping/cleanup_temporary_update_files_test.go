package housekeeping_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/housekeeping"
)

// fakeUpdateSandboxDisk records every method call so tests can
// assert what the housekeeper asked the disk to do without going
// through real filesystem ops.
type fakeUpdateSandboxDisk struct {
	exists        bool
	deleted       []string
	delErr        error
	statedFolders []string
}

func (f *fakeUpdateSandboxDisk) FolderExists(path string) bool {
	f.statedFolders = append(f.statedFolders, path)
	return f.exists
}

func (f *fakeUpdateSandboxDisk) DeleteFolder(path string, _ bool) error {
	f.deleted = append(f.deleted, path)
	return f.delErr
}

func TestCleanupTemporaryUpdateFiles_FolderMissingIsNoOp(t *testing.T) {
	t.Parallel()
	disk := &fakeUpdateSandboxDisk{exists: false}
	folder := appenv.AppFolderInfo{Temp: "/tmp"}
	task, err := housekeeping.NewCleanupTemporaryUpdateFiles(folder, disk, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := task.Name(); got != "CleanupTemporaryUpdateFiles" {
		t.Errorf("Name() = %q", got)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(disk.deleted) != 0 {
		t.Errorf("DeleteFolder should not have been called; got %v", disk.deleted)
	}
	if want := filepath.Join("/tmp", "sonarr_update"); len(disk.statedFolders) != 1 || disk.statedFolders[0] != want {
		t.Errorf("FolderExists called with %v, want %q", disk.statedFolders, want)
	}
}

func TestCleanupTemporaryUpdateFiles_FolderPresentDeletes(t *testing.T) {
	t.Parallel()
	disk := &fakeUpdateSandboxDisk{exists: true}
	folder := appenv.AppFolderInfo{Temp: "/tmp"}
	task, err := housekeeping.NewCleanupTemporaryUpdateFiles(folder, disk, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	want := filepath.Join("/tmp", "sonarr_update")
	if len(disk.deleted) != 1 || disk.deleted[0] != want {
		t.Errorf("deleted = %v, want [%q]", disk.deleted, want)
	}
}

func TestCleanupTemporaryUpdateFiles_DeleteErrorPropagates(t *testing.T) {
	t.Parallel()
	disk := &fakeUpdateSandboxDisk{exists: true, delErr: errors.New("EIO")}
	folder := appenv.AppFolderInfo{Temp: "/tmp"}
	task, err := housekeeping.NewCleanupTemporaryUpdateFiles(folder, disk, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from DeleteFolder")
	}
	if !errors.Is(err, disk.delErr) {
		t.Errorf("error chain missing disk err: %v", err)
	}
}

func TestCleanupTemporaryUpdateFiles_EmptyTempRejected(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupTemporaryUpdateFiles(appenv.AppFolderInfo{}, nil, nil); err == nil {
		t.Errorf("expected error on empty AppFolderInfo.Temp")
	}
}

func TestCleanupTemporaryUpdateFiles_NilDiskUsesOSDefault(t *testing.T) {
	t.Parallel()
	// Real-disk smoke test: create a sandbox under t.TempDir(),
	// confirm the OSUpdateSandboxDisk default removes it.
	temp := t.TempDir()
	sandbox := filepath.Join(temp, "sonarr_update")
	if err := os.MkdirAll(filepath.Join(sandbox, "child"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "child", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	folder := appenv.AppFolderInfo{Temp: temp}
	task, err := housekeeping.NewCleanupTemporaryUpdateFiles(folder, nil, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, err := os.Stat(sandbox); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sandbox still exists after Clean: %v", err)
	}
}

func TestOSUpdateSandboxDisk_FolderExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	disk := housekeeping.OSUpdateSandboxDisk{}
	if !disk.FolderExists(dir) {
		t.Errorf("FolderExists(%q) = false, want true", dir)
	}
	if disk.FolderExists(file) {
		t.Errorf("FolderExists(regular file) = true, want false")
	}
	if disk.FolderExists(filepath.Join(dir, "nonexistent")) {
		t.Errorf("FolderExists(missing) = true, want false")
	}
}
