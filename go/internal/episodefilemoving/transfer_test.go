package episodefilemoving_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/episodefilemoving"
)

func newDiskFs(t *testing.T) *episodefilemoving.DiskFilesystem {
	t.Helper()
	return episodefilemoving.NewDiskFilesystem(disk.NewProvider(slog.Default()), slog.Default())
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile(%s): %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile(%s): %v", path, err)
	}
	return string(b)
}

func TestDiskFilesystem_NilProvider(t *testing.T) {
	t.Parallel()
	if got := episodefilemoving.NewDiskFilesystem(nil, slog.Default()); got != nil {
		t.Errorf("NewDiskFilesystem(nil) = %v, want nil", got)
	}
}

func TestDiskFilesystem_FileExists(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	file := filepath.Join(tmp, "f.txt")
	writeFile(t, file, "hi")
	if !fsys.FileExists(file) {
		t.Errorf("FileExists = false, want true")
	}
	if fsys.FileExists(filepath.Join(tmp, "missing.txt")) {
		t.Errorf("FileExists(missing) = true, want false")
	}
	if fsys.FileExists(tmp) {
		t.Errorf("FileExists(dir) = true, want false")
	}
}

func TestDiskFilesystem_FolderExists(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	if !fsys.FolderExists(tmp) {
		t.Errorf("FolderExists(tmp) = false")
	}
	if fsys.FolderExists(filepath.Join(tmp, "missing-dir")) {
		t.Errorf("FolderExists(missing) = true")
	}
}

func TestDiskFilesystem_CreateFolder(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "a", "b", "c")
	if err := fsys.CreateFolder(target); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if !fsys.FolderExists(target) {
		t.Errorf("CreateFolder didn't make folder")
	}
	// Idempotent.
	if err := fsys.CreateFolder(target); err != nil {
		t.Errorf("re-create errored: %v", err)
	}
}

func TestTransferFile_Move(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "moved")

	if err := fsys.TransferFile(src, dst, episodefilemoving.TransferModeMove); err != nil {
		t.Fatalf("TransferFile: %v", err)
	}
	if fsys.FileExists(src) {
		t.Errorf("source still exists after Move")
	}
	if got := readFile(t, dst); got != "moved" {
		t.Errorf("dst content %q, want %q", got, "moved")
	}
}

func TestTransferFile_Copy(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "copied")

	if err := fsys.TransferFile(src, dst, episodefilemoving.TransferModeCopy); err != nil {
		t.Fatalf("TransferFile: %v", err)
	}
	if !fsys.FileExists(src) {
		t.Errorf("source missing after Copy")
	}
	if got := readFile(t, dst); got != "copied" {
		t.Errorf("dst content %q, want %q", got, "copied")
	}
}

func TestTransferFile_Copy_OverwritesExistingDst(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "src-payload")
	writeFile(t, dst, "old-dst-payload")

	if err := fsys.TransferFile(src, dst, episodefilemoving.TransferModeCopy); err != nil {
		t.Fatalf("TransferFile: %v", err)
	}
	if got := readFile(t, dst); got != "src-payload" {
		t.Errorf("dst content %q, want %q (clearTargetPath should overwrite)", got, "src-payload")
	}
}

func TestTransferFile_HardLinkOrCopy_HardLinks(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "linked")

	if err := fsys.TransferFile(src, dst, episodefilemoving.TransferModeHardLinkOrCopy); err != nil {
		t.Fatalf("TransferFile: %v", err)
	}
	// Detect hardlink by sharing inode (sys-specific); the simplest
	// portable signal is that mutating src mutates dst (both inodes
	// are the same regular file).
	srcInfo, _ := os.Stat(src)
	dstInfo, _ := os.Stat(dst)
	if !os.SameFile(srcInfo, dstInfo) {
		t.Errorf("expected hard-link (same inode), got distinct files")
	}
}

func TestTransferFile_HardLinkOrCopy_OverwritesExistingDst(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "fresh-src")
	writeFile(t, dst, "stale-dst")

	if err := fsys.TransferFile(src, dst, episodefilemoving.TransferModeHardLinkOrCopy); err != nil {
		t.Fatalf("TransferFile: %v", err)
	}
	if got := readFile(t, dst); got != "fresh-src" {
		t.Errorf("dst content %q, want %q (existing dst should be cleared first)", got, "fresh-src")
	}
}

func TestTransferFile_SameSourceAndDest(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	writeFile(t, path, "x")

	err := fsys.TransferFile(path, path, episodefilemoving.TransferModeMove)
	if err == nil {
		t.Fatal("TransferFile(src==dst) returned nil; want error")
	}
}

func TestTransferFile_DstChildOfSrc(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	child := filepath.Join(parent, "child.txt")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	err := fsys.TransferFile(parent, child, episodefilemoving.TransferModeMove)
	if err == nil {
		t.Fatal("TransferFile(parent → child) returned nil; want error")
	}
}

func TestTransferFile_MissingSource(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "missing.txt")
	dst := filepath.Join(tmp, "dst.txt")

	err := fsys.TransferFile(src, dst, episodefilemoving.TransferModeMove)
	if err == nil {
		t.Fatal("TransferFile(missing src) returned nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestTransferFile_UnsupportedMode(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "x")

	err := fsys.TransferFile(src, dst, episodefilemoving.TransferMode(99))
	if err == nil {
		t.Fatal("TransferFile(bogus mode) returned nil")
	}
}

func TestTransferFile_UnknownMode_BehavesAsMove(t *testing.T) {
	t.Parallel()
	fsys := newDiskFs(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "x")

	if err := fsys.TransferFile(src, dst, episodefilemoving.TransferModeUnknown); err != nil {
		t.Fatalf("TransferFile(Unknown): %v", err)
	}
	if fsys.FileExists(src) {
		t.Errorf("Unknown mode didn't move (src still present)")
	}
	if !fsys.FileExists(dst) {
		t.Errorf("Unknown mode didn't move (dst missing)")
	}
}
