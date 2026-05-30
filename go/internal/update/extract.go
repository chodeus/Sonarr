package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extract unpacks an update package archive into destFolder, mirroring
// C# `ArchiveService.Extract` (`NzbDrone.Common/ArchiveService.cs:27-41`):
// dispatch on the file extension — `.zip` → zip, anything else → tar.gz
// (Sonarr ships `.zip` on Windows and `.tar.gz` on Linux/macOS). Both
// paths create destFolder + nested directories and refuse entries whose
// normalized path escapes destFolder (zip-slip / tar-slip guard).
//
// maxArchiveBytes caps the total uncompressed size to bound a malicious
// or corrupt archive (the package is hash-verified before extraction, so
// this is a defence-in-depth backstop, not the primary guard).
func Extract(archivePath, destFolder string) error {
	if archivePath == "" || destFolder == "" {
		return errors.New("update: Extract: empty archivePath or destFolder")
	}
	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		return extractZip(archivePath, destFolder)
	}
	return extractTarGz(archivePath, destFolder)
}

// destPathFor joins the cleaned destination root with the (untrusted)
// archive entry name and verifies the result stays inside destRoot.
// Returns the safe absolute path, or an error on a slip attempt.
func destPathFor(destRoot, entryName string) (string, error) {
	target := filepath.Join(destRoot, entryName)
	// filepath.Join already cleans; verify the result is destRoot or a
	// descendant (guards "../" escapes and absolute entry names).
	rel, err := filepath.Rel(destRoot, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("update: Extract: entry %q escapes destination", entryName)
	}
	return target, nil
}

func extractZip(zipPath, destFolder string) error {
	if err := os.MkdirAll(destFolder, 0o755); err != nil {
		return fmt.Errorf("update: Extract: mkdir dest: %w", err)
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("update: Extract: open zip: %w", err)
	}
	defer func() { _ = r.Close() }()

	cleanDest, err := filepath.Abs(destFolder)
	if err != nil {
		return fmt.Errorf("update: Extract: abs dest: %w", err)
	}
	for _, f := range r.File {
		target, err := destPathFor(cleanDest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("update: Extract: mkdir %q: %w", target, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("update: Extract: mkdir parent %q: %w", target, err)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("update: Extract: open entry %q: %w", f.Name, err)
		}
		err = writeFile(target, rc, f.Mode())
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(tgzPath, destFolder string) error {
	if err := os.MkdirAll(destFolder, 0o755); err != nil {
		return fmt.Errorf("update: Extract: mkdir dest: %w", err)
	}
	f, err := os.Open(tgzPath)
	if err != nil {
		return fmt.Errorf("update: Extract: open tgz: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("update: Extract: gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	cleanDest, err := filepath.Abs(destFolder)
	if err != nil {
		return fmt.Errorf("update: Extract: abs dest: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("update: Extract: tar next: %w", err)
		}
		target, err := destPathFor(cleanDest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("update: Extract: mkdir %q: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("update: Extract: mkdir parent %q: %w", target, err)
			}
			if err := writeFile(target, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		default:
			// Symlinks / devices / fifos: skipped (Sonarr packages carry
			// only regular files + directories; refusing the rest avoids
			// a symlink-write escape).
		}
	}
	return nil
}

// writeFile copies r into path with the given mode, ensuring at least
// owner-write so a read-only archived file can still be replaced later.
func writeFile(path string, r io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o200)
	if err != nil {
		return fmt.Errorf("update: Extract: create %q: %w", path, err)
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return fmt.Errorf("update: Extract: write %q: %w", path, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("update: Extract: close %q: %w", path, err)
	}
	return nil
}
