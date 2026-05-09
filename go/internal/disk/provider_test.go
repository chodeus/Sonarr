package disk_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/mediafiles"
)

func newProvider() *disk.Provider {
	return disk.NewProvider(slog.Default())
}

func TestProvider_SatisfiesMediafilesDiskProvider(t *testing.T) {
	t.Parallel()
	var _ mediafiles.DiskProvider = newProvider()
}

func TestProvider_FolderExists(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()

	if !p.FolderExists(tmp) {
		t.Errorf("FolderExists(%q) = false, want true", tmp)
	}
	missing := filepath.Join(tmp, "nope")
	if p.FolderExists(missing) {
		t.Errorf("FolderExists(%q missing) = true, want false", missing)
	}
	// A regular file is not a folder.
	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if p.FolderExists(f) {
		t.Errorf("FolderExists(file) = true, want false")
	}
}

func TestProvider_FolderEmpty(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()

	if !p.FolderEmpty(tmp) {
		t.Errorf("fresh tmp dir reports non-empty")
	}
	if err := os.WriteFile(filepath.Join(tmp, "x"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if p.FolderEmpty(tmp) {
		t.Errorf("FolderEmpty after writing a file = true, want false")
	}
	// Missing folder → false (defensive against rm-during-scan races).
	if p.FolderEmpty(filepath.Join(tmp, "missing")) {
		t.Errorf("FolderEmpty(missing) = true, want false")
	}
}

// makeTree builds a small fixture under root:
//
//	root/
//	  a.mkv
//	  b.txt
//	  sub/
//	    c.mkv
//	    nested/
//	      d.srt
func makeTree(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{"sub", filepath.Join("sub", "nested")} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	for _, rel := range []string{
		"a.mkv",
		"b.txt",
		filepath.Join("sub", "c.mkv"),
		filepath.Join("sub", "nested", "d.srt"),
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", rel, err)
		}
	}
}

func TestProvider_GetFiles_TopLevel(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	makeTree(t, tmp)

	files, err := p.GetFiles(tmp, false)
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	got := relSorted(t, tmp, files)
	want := []string{"a.mkv", "b.txt"}
	if !equalStringSlice(got, want) {
		t.Errorf("GetFiles non-recursive = %v, want %v", got, want)
	}
}

func TestProvider_GetFiles_Recursive(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	makeTree(t, tmp)

	files, err := p.GetFiles(tmp, true)
	if err != nil {
		t.Fatalf("GetFiles recursive: %v", err)
	}
	got := relSorted(t, tmp, files)
	want := []string{
		"a.mkv",
		"b.txt",
		filepath.Join("sub", "c.mkv"),
		filepath.Join("sub", "nested", "d.srt"),
	}
	if !equalStringSlice(got, want) {
		t.Errorf("GetFiles recursive = %v, want %v", got, want)
	}
}

func TestProvider_GetFiles_EmptyFolderNonNil(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()

	for _, rec := range []bool{false, true} {
		files, err := p.GetFiles(tmp, rec)
		if err != nil {
			t.Fatalf("GetFiles rec=%v: %v", rec, err)
		}
		if files == nil {
			t.Errorf("GetFiles rec=%v on empty dir = nil, want non-nil empty slice", rec)
		}
		if len(files) != 0 {
			t.Errorf("GetFiles rec=%v on empty dir len = %d, want 0", rec, len(files))
		}
	}
}

func TestProvider_GetFiles_MissingFolder(t *testing.T) {
	t.Parallel()
	p := newProvider()
	missing := filepath.Join(t.TempDir(), "nope")
	for _, rec := range []bool{false, true} {
		_, err := p.GetFiles(missing, rec)
		if err == nil {
			t.Errorf("GetFiles(%q, rec=%v) err = nil, want non-nil", missing, rec)
		}
	}
}

func TestProvider_CreateFolder(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "a", "b", "c")

	if err := p.CreateFolder(target); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatalf("post-Create stat: %v", err)
	}
	if !st.IsDir() {
		t.Errorf("post-Create not a directory")
	}
	// Idempotent: second call must not error.
	if err := p.CreateFolder(target); err != nil {
		t.Errorf("CreateFolder repeat: %v", err)
	}
}

func TestProvider_RemoveEmptySubfolders(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	// tmp/empty1/, tmp/empty2/empty3/, tmp/withFile/inner/, plus a file under withFile.
	for _, d := range []string{
		"empty1",
		filepath.Join("empty2", "empty3"),
		filepath.Join("withFile", "inner"),
	} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "withFile", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := p.RemoveEmptySubfolders(tmp); err != nil {
		t.Fatalf("RemoveEmptySubfolders: %v", err)
	}

	// empty1/ → deleted.
	if _, err := os.Stat(filepath.Join(tmp, "empty1")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("empty1 should be removed, got err=%v", err)
	}
	// empty2/ → deleted (post-recursion empty3 vanishes, then empty2 is empty too).
	if _, err := os.Stat(filepath.Join(tmp, "empty2")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("empty2 should be removed, got err=%v", err)
	}
	// withFile/ → kept (has keep.txt).
	if _, err := os.Stat(filepath.Join(tmp, "withFile", "keep.txt")); err != nil {
		t.Errorf("withFile/keep.txt should remain, got err=%v", err)
	}
	// withFile/inner/ → deleted (was empty post-recursion).
	if _, err := os.Stat(filepath.Join(tmp, "withFile", "inner")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("withFile/inner should be removed, got err=%v", err)
	}
	// tmp itself → kept (not deleted by RemoveEmptySubfolders, only its subdirs).
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("tmp root removed, want kept (RemoveEmptySubfolders touches subdirs only)")
	}
}

func TestProvider_DeleteFolder(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	// recursive=true removes a non-empty tree.
	root := filepath.Join(tmp, "tree")
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "x"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := p.DeleteFolder(root, true); err != nil {
		t.Fatalf("DeleteFolder recursive: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("recursive DeleteFolder didn't remove tree, err=%v", err)
	}

	// recursive=false on an empty folder succeeds.
	empty := filepath.Join(tmp, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("MkdirAll empty: %v", err)
	}
	if err := p.DeleteFolder(empty, false); err != nil {
		t.Fatalf("DeleteFolder non-recursive empty: %v", err)
	}

	// recursive=false on a non-empty folder errors.
	full := filepath.Join(tmp, "full")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("MkdirAll full: %v", err)
	}
	if err := os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := p.DeleteFolder(full, false); err == nil {
		t.Errorf("DeleteFolder non-recursive on non-empty = nil, want error")
	}
}

func TestProvider_GetFileSize(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()

	f := filepath.Join(tmp, "x.txt")
	body := []byte("hello world")
	if err := os.WriteFile(f, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := p.GetFileSize(f)
	if err != nil {
		t.Fatalf("GetFileSize: %v", err)
	}
	if got != int64(len(body)) {
		t.Errorf("GetFileSize = %d, want %d", got, len(body))
	}

	// Missing file → error.
	if _, err := p.GetFileSize(filepath.Join(tmp, "nope")); err == nil {
		t.Errorf("GetFileSize(missing) = nil err, want non-nil")
	}
	// Directory → error.
	if _, err := p.GetFileSize(tmp); err == nil {
		t.Errorf("GetFileSize(dir) = nil err, want non-nil")
	}
}

// TestProvider_SetPermissions_NonLinuxNoOp is the macOS / Windows
// build's contract: SetPermissions returns nil and leaves the
// on-disk mode unchanged. The Linux build runs the real chmod path
// — see TestProvider_SetPermissions_LinuxChmod (build-tagged).
func TestProvider_SetPermissions_NonLinuxNoOp(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux build tests SetPermissions in TestProvider_SetPermissions_LinuxChmod")
	}
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "folder")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	before, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if err := p.SetPermissions(target, "700", ""); err != nil {
		t.Errorf("SetPermissions on non-Linux returned err: %v", err)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if before.Mode() != after.Mode() {
		t.Errorf("SetPermissions on non-Linux changed mode: before=%v after=%v",
			before.Mode(), after.Mode())
	}
}

// TestProvider_SetPermissions_BadMask exercises the parse path that
// runs on every OS — the linux body parses first, then chmods, so a
// bad mask errors before any I/O. The non-Linux body short-circuits
// to a no-op without parsing, so this test is gated to Linux.
func TestProvider_SetPermissions_BadMaskLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("non-Linux SetPermissions is a no-op; mask is not parsed")
	}
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "folder")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	for _, mask := range []string{"", "abc", "999", "12345"} {
		if err := p.SetPermissions(target, mask, ""); err == nil {
			t.Errorf("SetPermissions(mask=%q) = nil err, want non-nil", mask)
		}
	}
}

// --- helpers --------------------------------------------------------

func relSorted(t *testing.T, root string, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("filepath.Rel(%q,%q): %v", root, p, err)
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestProvider_FileGetLastWrite(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()

	path := filepath.Join(tmp, "f.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := p.FileGetLastWrite(path)
	if err != nil {
		t.Fatalf("FileGetLastWrite: %v", err)
	}
	if got.IsZero() {
		t.Errorf("FileGetLastWrite returned zero time")
	}
	// mtime should be within a reasonable window of "now".
	if delta := time.Since(got); delta < 0 || delta > time.Minute {
		t.Errorf("mtime delta = %v, want within 1m of now", delta)
	}

	// Missing file → wrapped fs.ErrNotExist.
	if _, err := p.FileGetLastWrite(filepath.Join(tmp, "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: err = %v, want fs.ErrNotExist", err)
	}
}

func TestProvider_GetAvailableSpace(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("GetAvailableSpace not implemented on Windows in this slice")
	}
	p := newProvider()
	tmp := t.TempDir()
	got, err := p.GetAvailableSpace(tmp)
	if err != nil {
		t.Fatalf("GetAvailableSpace: %v", err)
	}
	if got <= 0 {
		t.Errorf("GetAvailableSpace = %d, want > 0 (this filesystem must have some space)", got)
	}

	// Missing path → error (statfs fails).
	if _, err := p.GetAvailableSpace(filepath.Join(tmp, "nope")); err == nil {
		t.Errorf("missing path: want error")
	}
}

func TestProvider_FolderWritable(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()

	if !p.FolderWritable(tmp) {
		t.Errorf("FolderWritable on TempDir = false, want true")
	}
	// Sentinel must not leak — successful probe deletes it before returning.
	if _, err := os.Stat(filepath.Join(tmp, "sonarr_write_test.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("write sentinel survived a successful probe; stat err = %v", err)
	}

	// Missing folder → false (write fails).
	if p.FolderWritable(filepath.Join(tmp, "nope")) {
		t.Errorf("FolderWritable(missing) = true, want false")
	}

	// Read-only folder → false. POSIX-mode-bit removal is enough on
	// Linux/macOS; Windows ignores the read-only bit on directories so
	// skip there.
	if runtime.GOOS != "windows" {
		ro := filepath.Join(tmp, "ro")
		if err := os.Mkdir(ro, 0o555); err != nil {
			t.Fatalf("Mkdir read-only: %v", err)
		}
		// Restore writable mode at end so t.TempDir cleanup succeeds.
		t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
		// Skip when running as root — the kernel ignores 0o555 for uid=0.
		if os.Geteuid() != 0 {
			if p.FolderWritable(ro) {
				t.Errorf("FolderWritable(read-only) = true, want false")
			}
		}
	}
}

func TestProvider_GetTotalSize(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("GetTotalSize not implemented on Windows in this slice")
	}
	p := newProvider()
	tmp := t.TempDir()
	got, err := p.GetTotalSize(tmp)
	if err != nil {
		t.Fatalf("GetTotalSize: %v", err)
	}
	if got <= 0 {
		t.Errorf("GetTotalSize = %d, want > 0 (filesystem must report a non-zero size)", got)
	}

	// Total size must be at least the available space (modulo
	// reservation accounting). Loose floor protects against the cast
	// going negative on a 32-bit Bsize platform.
	avail, err := p.GetAvailableSpace(tmp)
	if err != nil {
		t.Fatalf("GetAvailableSpace: %v", err)
	}
	if got < avail {
		t.Errorf("GetTotalSize=%d < GetAvailableSpace=%d, want total >= avail", got, avail)
	}

	// Missing path → error (statfs fails).
	if _, err := p.GetTotalSize(filepath.Join(tmp, "nope")); err == nil {
		t.Errorf("missing path: want error")
	}
}
