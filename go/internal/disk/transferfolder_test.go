package disk_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProvider_TransferFolder_MergesIntoExistingDst(t *testing.T) {
	p := newProvider()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")

	// src has a nested tree; dst pre-exists with an overlapping file.
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "a.txt"), "new-a")
	writeFile(t, filepath.Join(src, "sub", "b.txt"), "new-b")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dst, "a.txt"), "old-a") // must be overwritten
	writeFile(t, filepath.Join(dst, "keep.txt"), "keep")

	if err := p.TransferFolder(src, dst); err != nil {
		t.Fatalf("TransferFolder: %v", err)
	}

	if got := readFile(t, filepath.Join(dst, "a.txt")); got != "new-a" {
		t.Errorf("a.txt = %q, want new-a (overwritten)", got)
	}
	if got := readFile(t, filepath.Join(dst, "sub", "b.txt")); got != "new-b" {
		t.Errorf("sub/b.txt = %q, want new-b", got)
	}
	if got := readFile(t, filepath.Join(dst, "keep.txt")); got != "keep" {
		t.Errorf("keep.txt = %q, want preserved", got)
	}
	// src is removed after the transfer.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be removed, stat err = %v", err)
	}
}

func TestProvider_TransferFolder_CreatesDst(t *testing.T) {
	p := newProvider()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "newdst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "x"), "data")
	if err := p.TransferFolder(src, dst); err != nil {
		t.Fatalf("TransferFolder: %v", err)
	}
	if got := readFile(t, filepath.Join(dst, "x")); got != "data" {
		t.Errorf("x = %q, want data", got)
	}
}
