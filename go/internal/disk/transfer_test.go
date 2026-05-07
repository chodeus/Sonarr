package disk_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

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

func TestProvider_FileExists(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()

	file := filepath.Join(tmp, "f.txt")
	writeFile(t, file, "hello")
	if !p.FileExists(file) {
		t.Errorf("FileExists(%s) = false, want true", file)
	}
	if p.FileExists(filepath.Join(tmp, "missing.txt")) {
		t.Errorf("FileExists(missing) = true, want false")
	}
	// A directory must NOT match FileExists.
	if p.FileExists(tmp) {
		t.Errorf("FileExists(tmp dir) = true, want false")
	}
}

func TestProvider_MoveFile_SameDir(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "payload")

	if err := p.MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if p.FileExists(src) {
		t.Errorf("source still exists after move")
	}
	if got := readFile(t, dst); got != "payload" {
		t.Errorf("dst content %q, want %q", got, "payload")
	}
}

func TestProvider_MoveFile_DstExists(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "src-payload")
	writeFile(t, dst, "dst-payload")

	// On macOS/Linux os.Rename overwrites silently; that matches the
	// C# `MoveFile(overwrite=true)` arm. Document the platform-level
	// reality rather than enforce a stricter Go-port behaviour.
	if err := p.MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := readFile(t, dst); got != "src-payload" {
		t.Errorf("dst content %q, want %q (rename overwrites)", got, "src-payload")
	}
}

func TestProvider_MoveFile_MissingSource(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "missing.txt")
	dst := filepath.Join(tmp, "dst.txt")
	err := p.MoveFile(src, dst)
	if err == nil {
		t.Fatal("MoveFile(missing) returned nil error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("MoveFile(missing) err = %v, want fs.ErrNotExist", err)
	}
}

func TestProvider_CopyFile_Basic(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "copyme")

	if err := p.CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if !p.FileExists(src) {
		t.Errorf("source missing after copy")
	}
	if got := readFile(t, dst); got != "copyme" {
		t.Errorf("dst content %q, want %q", got, "copyme")
	}
}

func TestProvider_CopyFile_DstExists(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "src")
	writeFile(t, dst, "dst")

	err := p.CopyFile(src, dst)
	if err == nil {
		t.Fatal("CopyFile(existing dst) returned nil; want fs.ErrExist")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("CopyFile err = %v, want fs.ErrExist", err)
	}
	// Existing dst content should be untouched on a refused overwrite.
	if got := readFile(t, dst); got != "dst" {
		t.Errorf("dst content %q, want %q (untouched)", got, "dst")
	}
}

func TestProvider_CopyFile_MissingSource(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "missing.txt")
	dst := filepath.Join(tmp, "dst.txt")
	err := p.CopyFile(src, dst)
	if err == nil {
		t.Fatal("CopyFile(missing src) returned nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CopyFile err = %v, want fs.ErrNotExist", err)
	}
	if p.FileExists(dst) {
		t.Errorf("dst created when src was missing")
	}
}

func TestProvider_TryCreateHardLink(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "linkme")

	if !p.TryCreateHardLink(src, dst) {
		t.Fatal("TryCreateHardLink returned false on same-fs target")
	}
	// Both inodes share content.
	if got := readFile(t, dst); got != "linkme" {
		t.Errorf("dst content %q, want %q", got, "linkme")
	}
	// Mutating src is observable through dst (proves it's a hardlink).
	writeFile(t, src, "mutated")
	if got := readFile(t, dst); got != "mutated" {
		t.Errorf("post-mutation dst content %q, want %q", got, "mutated")
	}
}

func TestProvider_TryCreateHardLink_DstExists(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	writeFile(t, src, "src")
	writeFile(t, dst, "dst")

	if p.TryCreateHardLink(src, dst) {
		t.Fatal("TryCreateHardLink returned true with existing dst")
	}
	if got := readFile(t, dst); got != "dst" {
		t.Errorf("dst content %q, want untouched %q", got, "dst")
	}
}

func TestProvider_TryCreateHardLink_MissingSource(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "missing.txt")
	dst := filepath.Join(tmp, "dst.txt")
	if p.TryCreateHardLink(src, dst) {
		t.Fatal("TryCreateHardLink returned true on missing src")
	}
}

func TestProvider_DeleteFile(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	file := filepath.Join(tmp, "f.txt")
	writeFile(t, file, "x")

	if err := p.DeleteFile(file); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if p.FileExists(file) {
		t.Errorf("file still exists after DeleteFile")
	}
}

func TestProvider_DeleteFile_Missing(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	err := p.DeleteFile(filepath.Join(tmp, "missing.txt"))
	if err == nil {
		t.Fatal("DeleteFile(missing) returned nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("DeleteFile err = %v, want fs.ErrNotExist", err)
	}
}

func TestProvider_GetDirectories(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	// Three subdirs and two files.
	for _, d := range []string{"alpha", "beta", "gamma"} {
		if err := os.Mkdir(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatalf("Mkdir(%s): %v", d, err)
		}
	}
	writeFile(t, filepath.Join(tmp, "f.txt"), "x")
	writeFile(t, filepath.Join(tmp, "g.txt"), "x")

	got, err := p.GetDirectories(tmp)
	if err != nil {
		t.Fatalf("GetDirectories: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d dirs, want 3: %v", len(got), got)
	}
	// Each entry should be an absolute path joining tmp + name.
	want := map[string]bool{
		filepath.Join(tmp, "alpha"): true,
		filepath.Join(tmp, "beta"):  true,
		filepath.Join(tmp, "gamma"): true,
	}
	for _, d := range got {
		if !want[d] {
			t.Errorf("unexpected dir in result: %q", d)
		}
	}
}

func TestProvider_GetDirectories_TopLevelOnly(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "a", "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := p.GetDirectories(tmp)
	if err != nil {
		t.Fatalf("GetDirectories: %v", err)
	}
	// Should return only "a", NOT "a/nested" — top-level only.
	if len(got) != 1 || got[0] != filepath.Join(tmp, "a") {
		t.Errorf("got %v, want only top-level [%s/a]", got, tmp)
	}
}

func TestProvider_GetDirectories_EmptyFolder(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	got, err := p.GetDirectories(tmp)
	if err != nil {
		t.Fatalf("GetDirectories(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty slice", got)
	}
	if got == nil {
		t.Errorf("got = nil, want non-nil empty slice (callers range without length check)")
	}
}

func TestProvider_GetDirectories_MissingPath(t *testing.T) {
	t.Parallel()
	p := newProvider()
	tmp := t.TempDir()
	_, err := p.GetDirectories(filepath.Join(tmp, "nonexistent"))
	if err == nil {
		t.Fatal("GetDirectories(missing) returned nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}
