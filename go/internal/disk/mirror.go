package disk

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MirrorFolder makes targetPath mirror sourcePath: target subfolders +
// files absent from source are deleted, source subfolders are mirrored
// recursively, and source files are copied over (overwriting) unless an
// identical copy already exists. Returns the number of files copied.
//
// Ports C# `DiskTransferService.MirrorFolder`
// (`Common/Disk/DiskTransferService.cs`) — the operation the in-place
// updater uses to (a) back up the existing install, (b) copy the new
// build over the install, and (c) roll back from the backup on failure.
// Unlike [Provider.TransferFolder] (a one-way move-merge) MirrorFolder
// makes target an exact copy of source, removing extras.
//
// The C# ShouldIgnore filter is preserved: `.nfs*` temp files/folders,
// `debug.log`, and `*.socket` files are skipped on both the delete and
// copy passes (they are runtime artefacts, not part of the package).
func (p *Provider) MirrorFolder(sourcePath, targetPath string) (int, error) {
	filesCopied := 0

	if !p.FolderExists(targetPath) {
		if err := p.CreateFolder(targetPath); err != nil {
			return filesCopied, fmt.Errorf("disk: MirrorFolder: create target %q: %w", targetPath, err)
		}
	}

	srcDirs, srcFiles, err := splitDir(sourcePath)
	if err != nil {
		return filesCopied, fmt.Errorf("disk: MirrorFolder: read source %q: %w", sourcePath, err)
	}
	tgtDirs, tgtFiles, err := splitDir(targetPath)
	if err != nil {
		return filesCopied, fmt.Errorf("disk: MirrorFolder: read target %q: %w", targetPath, err)
	}

	// Delete target subfolders not present in source.
	for _, td := range tgtDirs {
		if shouldIgnoreFolder(td) || containsName(srcDirs, td) {
			continue
		}
		if err := p.DeleteFolder(filepath.Join(targetPath, td), true); err != nil {
			return filesCopied, fmt.Errorf("disk: MirrorFolder: delete extra folder %q: %w", td, err)
		}
	}

	// Mirror source subfolders recursively.
	for _, sd := range srcDirs {
		if shouldIgnoreFolder(sd) {
			continue
		}
		n, err := p.MirrorFolder(filepath.Join(sourcePath, sd), filepath.Join(targetPath, sd))
		filesCopied += n
		if err != nil {
			return filesCopied, err
		}
	}

	// Delete target files not present in source.
	for _, tf := range tgtFiles {
		if shouldIgnoreFile(tf) || containsName(srcFiles, tf) {
			continue
		}
		if err := p.DeleteFile(filepath.Join(targetPath, tf)); err != nil {
			return filesCopied, fmt.Errorf("disk: MirrorFolder: delete extra file %q: %w", tf, err)
		}
	}

	// Copy source files over (overwrite), skipping byte-identical ones.
	for _, sf := range srcFiles {
		if shouldIgnoreFile(sf) {
			continue
		}
		src := filepath.Join(sourcePath, sf)
		dst := filepath.Join(targetPath, sf)
		same, err := p.compareFiles(src, dst)
		if err != nil {
			return filesCopied, err
		}
		if same {
			continue
		}
		if p.FileExists(dst) {
			if err := p.DeleteFile(dst); err != nil {
				return filesCopied, fmt.Errorf("disk: MirrorFolder: clear target file %q: %w", dst, err)
			}
		}
		if err := p.CopyFile(src, dst); err != nil {
			return filesCopied, fmt.Errorf("disk: MirrorFolder: copy %q: %w", sf, err)
		}
		filesCopied++
	}

	return filesCopied, nil
}

// splitDir reads dir and returns its immediate subdirectory names and
// regular-file names separately.
func splitDir(dir string) (dirs, files []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		} else {
			files = append(files, e.Name())
		}
	}
	return dirs, files, nil
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// shouldIgnoreFolder ports C# `ShouldIgnore(DirectoryInfo)` — skip NFS
// temp folders.
func shouldIgnoreFolder(name string) bool {
	return strings.HasPrefix(name, ".nfs")
}

// shouldIgnoreFile ports C# `ShouldIgnore(FileInfo)` — skip NFS temp
// files, debug.log, and unix domain sockets.
func shouldIgnoreFile(name string) bool {
	return strings.HasPrefix(name, ".nfs") || name == "debug.log" || strings.HasSuffix(name, ".socket")
}

// compareFiles ports C# `CompareFiles` — true when both files exist,
// have equal size, and identical contents (64 KiB chunk compare).
func (p *Provider) compareFiles(sourceFile, targetFile string) (bool, error) {
	if !p.FileExists(sourceFile) || !p.FileExists(targetFile) {
		return false, nil
	}
	ss, err := p.GetFileSize(sourceFile)
	if err != nil {
		return false, fmt.Errorf("disk: compareFiles: size %q: %w", sourceFile, err)
	}
	ts, err := p.GetFileSize(targetFile)
	if err != nil {
		return false, fmt.Errorf("disk: compareFiles: size %q: %w", targetFile, err)
	}
	if ss != ts {
		return false, nil
	}

	sf, err := os.Open(sourceFile)
	if err != nil {
		return false, fmt.Errorf("disk: compareFiles: open %q: %w", sourceFile, err)
	}
	defer func() { _ = sf.Close() }()
	tf, err := os.Open(targetFile)
	if err != nil {
		return false, fmt.Errorf("disk: compareFiles: open %q: %w", targetFile, err)
	}
	defer func() { _ = tf.Close() }()

	const chunk = 64 * 1024
	sbuf := make([]byte, chunk)
	tbuf := make([]byte, chunk)
	for {
		sn, serr := io.ReadFull(sf, sbuf)
		tn, terr := io.ReadFull(tf, tbuf)
		if sn != tn || !bytes.Equal(sbuf[:sn], tbuf[:tn]) {
			return false, nil
		}
		if isEOF(serr) && isEOF(terr) {
			return true, nil
		}
		if serr != nil && !isEOF(serr) {
			return false, fmt.Errorf("disk: compareFiles: read %q: %w", sourceFile, serr)
		}
		if terr != nil && !isEOF(terr) {
			return false, fmt.Errorf("disk: compareFiles: read %q: %w", targetFile, terr)
		}
	}
}

func isEOF(err error) bool {
	return err == io.EOF || err == io.ErrUnexpectedEOF
}
