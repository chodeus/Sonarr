package disk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProvider_EmptyFolder_DeletesFilesAndSubdirsKeepsFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// a few files + a populated subdir
	for _, n := range []string{"sonarr.txt", "sonarr.0.txt", "sonarr.1.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("log"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "archive")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "old.txt"), []byte("x"), 0o644)

	p := &Provider{}
	if err := p.EmptyFolder(dir); err != nil {
		t.Fatalf("EmptyFolder: %v", err)
	}
	// folder still exists...
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("folder must remain: err=%v", err)
	}
	// ...but is empty.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("folder not empty: %d entries remain", len(entries))
	}
}

func TestProvider_EmptyFolder_MissingPathIsNoOp(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	if err := p.EmptyFolder(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("missing path should be a no-op, got %v", err)
	}
}
