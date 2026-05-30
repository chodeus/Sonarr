package update_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/update"
)

// writeZip builds a small zip at path with the given name→content map.
func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeTarGz builds a small tar.gz at path with the given name→content map.
func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "Sonarr.zip")
	writeZip(t, zipPath, map[string]string{
		"Sonarr/Sonarr.dll": "binary",
		"Sonarr/UI/index":   "html",
	})
	dest := filepath.Join(dir, "out")
	if err := update.Extract(zipPath, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "Sonarr", "Sonarr.dll"))
	if err != nil || string(got) != "binary" {
		t.Fatalf("extracted dll = %q (%v)", got, err)
	}
	got2, _ := os.ReadFile(filepath.Join(dest, "Sonarr", "UI", "index"))
	if string(got2) != "html" {
		t.Fatalf("extracted UI = %q", got2)
	}
}

func TestExtractTarGz(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "Sonarr.tar.gz")
	writeTarGz(t, tgzPath, map[string]string{
		"Sonarr/Sonarr": "elf",
		"Sonarr/UI/app": "js",
	})
	dest := filepath.Join(dir, "out")
	if err := update.Extract(tgzPath, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "Sonarr", "Sonarr"))
	if err != nil || string(got) != "elf" {
		t.Fatalf("extracted binary = %q (%v)", got, err)
	}
}

func TestExtractRejectsZipSlip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	writeZip(t, zipPath, map[string]string{
		"../escape.txt": "pwned",
	})
	dest := filepath.Join(dir, "out")
	if err := update.Extract(zipPath, dest); err == nil {
		t.Fatal("expected zip-slip rejection")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("zip-slip wrote a file outside dest")
	}
}

func TestExtractRejectsTarSlip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "evil.tar.gz")
	writeTarGz(t, tgzPath, map[string]string{
		"../escape.txt": "pwned",
	})
	dest := filepath.Join(dir, "out")
	if err := update.Extract(tgzPath, dest); err == nil {
		t.Fatal("expected tar-slip rejection")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("tar-slip wrote a file outside dest")
	}
}

func TestExtractEmptyArgs(t *testing.T) {
	t.Parallel()
	if err := update.Extract("", "x"); err == nil {
		t.Error("empty archivePath should error")
	}
	if err := update.Extract("x", ""); err == nil {
		t.Error("empty dest should error")
	}
}

// --- Verify ---

func sha256Hex(t *testing.T, data string) string {
	t.Helper()
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

func TestVerifyMatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pkgPath := filepath.Join(dir, "pkg.tar.gz")
	if err := os.WriteFile(pkgPath, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Case-insensitive match (upper-cased expected hash).
	pkg := &update.Package{Hash: sha256Hex(t, "payload")}
	ok, err := update.Verify(pkg, pkgPath)
	if err != nil || !ok {
		t.Fatalf("Verify match: got (%v,%v), want (true,nil)", ok, err)
	}
}

func TestVerifyMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pkgPath := filepath.Join(dir, "pkg.tar.gz")
	if err := os.WriteFile(pkgPath, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := &update.Package{Hash: sha256Hex(t, "different")}
	ok, err := update.Verify(pkg, pkgPath)
	if err != nil || ok {
		t.Fatalf("Verify mismatch: got (%v,%v), want (false,nil)", ok, err)
	}
}

func TestVerifyBlankHashNeverMatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pkgPath := filepath.Join(dir, "pkg.tar.gz")
	if err := os.WriteFile(pkgPath, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := update.Verify(&update.Package{Hash: ""}, pkgPath)
	if err != nil || ok {
		t.Fatalf("blank hash: got (%v,%v), want (false,nil)", ok, err)
	}
}

func TestVerifyMissingFile(t *testing.T) {
	t.Parallel()
	if _, err := update.Verify(&update.Package{Hash: "abc"}, filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing file should error")
	}
}
