package disk_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProvider_MirrorFolder_MakesTargetMatchSource(t *testing.T) {
	p := newProvider()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")

	// Source tree.
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "Sonarr"), "new-elf")
	writeFile(t, filepath.Join(src, "bin", "lib.so"), "lib-v2")

	// Pre-existing target with stale + extra content.
	if err := os.MkdirAll(filepath.Join(dst, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "obsolete"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dst, "Sonarr"), "old-elf")           // must be overwritten
	writeFile(t, filepath.Join(dst, "bin", "lib.so"), "lib-v1")     // must be overwritten
	writeFile(t, filepath.Join(dst, "stale.txt"), "remove-me")      // extra file → removed
	writeFile(t, filepath.Join(dst, "obsolete", "x"), "remove-dir") // extra dir → removed

	copied, err := p.MirrorFolder(src, dst)
	if err != nil {
		t.Fatalf("MirrorFolder: %v", err)
	}
	if copied != 2 {
		t.Errorf("filesCopied = %d, want 2", copied)
	}
	if got := readFile(t, filepath.Join(dst, "Sonarr")); got != "new-elf" {
		t.Errorf("Sonarr = %q, want new-elf", got)
	}
	if got := readFile(t, filepath.Join(dst, "bin", "lib.so")); got != "lib-v2" {
		t.Errorf("bin/lib.so = %q, want lib-v2", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.txt")); !os.IsNotExist(err) {
		t.Error("extra file should be deleted")
	}
	if _, err := os.Stat(filepath.Join(dst, "obsolete")); !os.IsNotExist(err) {
		t.Error("extra dir should be deleted")
	}
}

func TestProvider_MirrorFolder_SkipsIdenticalFiles(t *testing.T) {
	p := newProvider()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "same"), "identical")
	writeFile(t, filepath.Join(dst, "same"), "identical") // byte-identical → not recopied
	writeFile(t, filepath.Join(src, "diff"), "v2")
	writeFile(t, filepath.Join(dst, "diff"), "v1")

	copied, err := p.MirrorFolder(src, dst)
	if err != nil {
		t.Fatalf("MirrorFolder: %v", err)
	}
	if copied != 1 {
		t.Errorf("filesCopied = %d, want 1 (only the changed file)", copied)
	}
}

func TestProvider_MirrorFolder_CreatesTarget(t *testing.T) {
	p := newProvider()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "fresh")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "a"), "data")
	copied, err := p.MirrorFolder(src, dst)
	if err != nil || copied != 1 {
		t.Fatalf("MirrorFolder fresh: copied=%d err=%v", copied, err)
	}
	if got := readFile(t, filepath.Join(dst, "a")); got != "data" {
		t.Errorf("a = %q", got)
	}
}

func TestProvider_MirrorFolder_IgnoresRuntimeArtefacts(t *testing.T) {
	p := newProvider()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "Sonarr"), "elf")
	// Target runtime artefacts that must NOT be deleted (ShouldIgnore).
	writeFile(t, filepath.Join(dst, "debug.log"), "logs")
	writeFile(t, filepath.Join(dst, "app.socket"), "sock")
	writeFile(t, filepath.Join(dst, ".nfs0001"), "nfs")

	if _, err := p.MirrorFolder(src, dst); err != nil {
		t.Fatalf("MirrorFolder: %v", err)
	}
	for _, f := range []string{"debug.log", "app.socket", ".nfs0001"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err != nil {
			t.Errorf("ignored artefact %q should be preserved: %v", f, err)
		}
	}
}
