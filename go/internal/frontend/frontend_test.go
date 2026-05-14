package frontend_test

import (
	"io"
	"io/fs"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/frontend"
)

// TestFS_ContainsIndex asserts the embedded bundle ships an
// index.html at its root. Future production builds replace this
// file with the real React shell; the test ensures the embed
// directive keeps working through that swap.
func TestFS_ContainsIndex(t *testing.T) {
	t.Parallel()
	f, err := frontend.FS().Open("index.html")
	if err != nil {
		t.Fatalf("open index.html: %v", err)
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "<html") {
		t.Errorf("index.html should contain <html: first 80 bytes = %q", body[:min(80, len(body))])
	}
}

// TestFS_NoPathTraversal pins the fs.Sub contract: the returned
// filesystem can't reach files outside the bundle directory. fs.Sub
// rejects `..` segments natively but the test locks the behaviour
// in place — a future package refactor that swaps fs.Sub for a
// hand-rolled wrapper would fail this.
func TestFS_NoPathTraversal(t *testing.T) {
	t.Parallel()
	if _, err := frontend.FS().Open("../frontend.go"); err == nil {
		t.Errorf("expected error opening ../frontend.go via the embedded FS")
	}
}

// TestFS_ListsRoot asserts the FS root is the bundle directory
// (not the parent /internal/frontend dir) — sanity check on the
// fs.Sub call.
func TestFS_ListsRoot(t *testing.T) {
	t.Parallel()
	entries, err := fs.ReadDir(frontend.FS(), ".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	foundIndex := false
	for _, e := range entries {
		if e.Name() == "index.html" {
			foundIndex = true
		}
	}
	if !foundIndex {
		t.Errorf("FS root should contain index.html; entries=%+v", entries)
	}
}
