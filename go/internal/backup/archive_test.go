package backup_test

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/backup"
)

func TestZipArchiver_RoundTrip(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src1 := filepath.Join(tmp, "a.txt")
	src2 := filepath.Join(tmp, "b.txt")
	if err := os.WriteFile(src1, []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(tmp, "out.zip")
	if err := (backup.ZipArchiver{}).CreateZip(zipPath, []string{src1, src2}); err != nil {
		t.Fatalf("CreateZip: %v", err)
	}

	// Verify entries.
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) != 2 {
		t.Errorf("entries: %d want 2", len(r.File))
	}
	gotNames := map[string]bool{}
	for _, f := range r.File {
		gotNames[f.Name] = true
	}
	if !gotNames["a.txt"] || !gotNames["b.txt"] {
		t.Errorf("missing entries: %+v", gotNames)
	}

	// Extract to a fresh dir and verify contents round-trip.
	out := filepath.Join(tmp, "extracted")
	if err := (backup.ZipArchiver{}).Extract(zipPath, out); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "a.txt")); string(got) != "alpha" {
		t.Errorf("a.txt extracted = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "b.txt")); string(got) != "beta" {
		t.Errorf("b.txt extracted = %q", got)
	}
}

func TestZipArchiver_CreateEmpty(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "empty.zip")
	if err := (backup.ZipArchiver{}).CreateZip(zipPath, nil); err != nil {
		t.Fatalf("CreateZip empty: %v", err)
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) != 0 {
		t.Errorf("expected 0 entries, got %d", len(r.File))
	}
}

func TestZipArchiver_CreateMissingSourceFails(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := (backup.ZipArchiver{}).CreateZip(filepath.Join(tmp, "out.zip"), []string{filepath.Join(tmp, "missing.txt")}); err == nil {
		t.Fatal("want error for missing source")
	}
}

func TestZipArchiver_CreateEmptyZipPath(t *testing.T) {
	t.Parallel()
	if err := (backup.ZipArchiver{}).CreateZip("", nil); err == nil {
		t.Fatal("want error for empty zipPath")
	}
}

func TestZipArchiver_ExtractMissingZip(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	err := (backup.ZipArchiver{}).Extract(filepath.Join(tmp, "no.zip"), filepath.Join(tmp, "out"))
	if err == nil {
		t.Fatal("want error for missing zip")
	}
}

func TestZipArchiver_ExtractZipSlipRefused(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, err := w.Create("../escapee.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("haha")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "out")
	err = (backup.ZipArchiver{}).Extract(zipPath, out)
	if err == nil {
		t.Fatal("zip-slip should be refused")
	}
	if !contains(err.Error(), "zip-slip") {
		t.Errorf("error should mention zip-slip: %v", err)
	}
	// Confirm escapee.txt was not written.
	if _, err := os.Stat(filepath.Join(tmp, "escapee.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("escapee.txt was written; want absent: %v", err)
	}
}

func TestZipArchiver_ExtractEmptyArgs(t *testing.T) {
	t.Parallel()
	if err := (backup.ZipArchiver{}).Extract("", "/x"); err == nil {
		t.Error("empty zipPath")
	}
	if err := (backup.ZipArchiver{}).Extract("/x.zip", ""); err == nil {
		t.Error("empty destFolder")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
