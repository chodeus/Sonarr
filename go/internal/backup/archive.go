package backup

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ZipArchiver is the production [Archiver] — uses the Go stdlib
// archive/zip. Goroutine-safe.
//
// Mirrors the C# `IArchiveService` surface narrow to two methods.
// The C# impl is in NzbDrone.Common; the Go-port lifts the pair
// into this package to keep the dep graph tight (no separate
// archive helper package yet).
type ZipArchiver struct{}

// CreateZip writes a flat zip at zipPath containing one entry per
// sourceFile (path is the absolute source; entry name is the
// basename — matches the C# `IArchiveService.CreateZip` shape).
// Existing zip is overwritten.
func (ZipArchiver) CreateZip(zipPath string, sourceFiles []string) error {
	if zipPath == "" {
		return errors.New("backup.ZipArchiver: empty zip path")
	}
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		return fmt.Errorf("backup.ZipArchiver: mkdir parent: %w", err)
	}
	out, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: create zip: %w", err)
	}
	defer func() { _ = out.Close() }()
	w := zip.NewWriter(out)
	defer func() { _ = w.Close() }()
	for _, src := range sourceFiles {
		if err := addZipEntry(w, src); err != nil {
			return err
		}
	}
	return nil
}

// addZipEntry copies one file into the zip, naming the entry by
// basename only (flat archive).
func addZipEntry(w *zip.Writer, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: open %q: %w", src, err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: stat %q: %w", src, err)
	}
	hdr, err := zip.FileInfoHeader(st)
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: header %q: %w", src, err)
	}
	hdr.Name = filepath.Base(src)
	hdr.Method = zip.Deflate
	entry, err := w.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: create header %q: %w", src, err)
	}
	if _, err := io.Copy(entry, f); err != nil {
		return fmt.Errorf("backup.ZipArchiver: copy %q: %w", src, err)
	}
	return nil
}

// Extract unpacks zipPath into destFolder. Creates destFolder + any
// nested directories. Refuses zip entries whose normalized path
// escapes destFolder (zip-slip guard).
func (ZipArchiver) Extract(zipPath, destFolder string) error {
	if zipPath == "" || destFolder == "" {
		return errors.New("backup.ZipArchiver: empty zipPath or destFolder")
	}
	if err := os.MkdirAll(destFolder, 0o755); err != nil {
		return fmt.Errorf("backup.ZipArchiver: mkdir dest: %w", err)
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: open zip: %w", err)
	}
	defer func() { _ = r.Close() }()
	cleanDest, err := filepath.Abs(destFolder)
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: abs dest: %w", err)
	}
	for _, f := range r.File {
		if err := extractEntry(f, cleanDest); err != nil {
			return err
		}
	}
	return nil
}

// extractEntry handles one zip entry. Skips zero-byte directory
// entries (reflected by trailing slash in the entry name).
func extractEntry(f *zip.File, destRoot string) error {
	target := filepath.Join(destRoot, f.Name)
	rel, err := filepath.Rel(destRoot, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("backup.ZipArchiver: zip-slip refused: %q", f.Name)
	}
	if strings.HasSuffix(f.Name, "/") {
		return os.MkdirAll(target, f.Mode())
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("backup.ZipArchiver: mkdir entry parent: %w", err)
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: open entry %q: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("backup.ZipArchiver: create out %q: %w", target, err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("backup.ZipArchiver: copy %q: %w", target, err)
	}
	return nil
}
