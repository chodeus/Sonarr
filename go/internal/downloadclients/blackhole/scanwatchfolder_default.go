package blackhole

import (
	"fmt"
	"os"
	"time"
)

// DefaultDisk returns the production [Disk] implementation —
// backed by [os.Stat] + [os.ReadDir]. Test code in this package
// uses fake disks; production wiring (Phase 4 polling loop)
// constructs the scanner with this default.
//
// [Disk.IsFileLocked] always returns (false, nil): see the
// divergence note on the Disk interface. The
// downloading→completed promotion still gates on the grace-
// period state machine, so a 30-second writer remains in
// Downloading status that long before promoting.
func DefaultDisk() Disk { return defaultDisk{} }

type defaultDisk struct{}

// ListDirectories returns absolute paths of every direct-child
// directory under path. Errors propagate wrapped.
func (defaultDisk) ListDirectories(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("blackhole: ListDirectories(%q): %w", path, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, joinPath(path, e.Name()))
	}
	return out, nil
}

// ListFiles returns absolute paths of every file under path.
// recursive=true descends into subdirectories.
func (defaultDisk) ListFiles(path string, recursive bool) ([]string, error) {
	if !recursive {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("blackhole: ListFiles(%q): %w", path, err)
		}
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			out = append(out, joinPath(path, e.Name()))
		}
		return out, nil
	}
	var out []string
	err := walkDir(path, func(p string, isDir bool) {
		if !isDir {
			out = append(out, p)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("blackhole: ListFiles(%q, recursive=true): %w", path, err)
	}
	return out, nil
}

// FileSize returns the byte size from os.Stat.
func (defaultDisk) FileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// FileModTime returns the file's last-modified time from os.Stat.
func (defaultDisk) FileModTime(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

// FolderModTime returns the folder's last-modified time from os.Stat.
func (defaultDisk) FolderModTime(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

// IsFileLocked always returns (false, nil). See the Disk interface
// for the documented divergence — POSIX has no portable equivalent
// to the C# `File.Open(FileShare.None)` probe, and approximating it
// with advisory flock would surface false positives for any media
// player that's previewing the in-flight file.
func (defaultDisk) IsFileLocked(string) (bool, error) { return false, nil }

// joinPath is filepath.Join inlined to keep the import graph tight
// (no extra import line in this small file).
func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	if dir[len(dir)-1] == '/' || dir[len(dir)-1] == '\\' {
		return dir + name
	}
	return dir + string(os.PathSeparator) + name
}

// walkDir is a minimal os.ReadDir-based recursive walk. Returns
// the first error encountered. Symlinks are not followed.
func walkDir(root string, visit func(path string, isDir bool)) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := joinPath(root, e.Name())
		if e.IsDir() {
			visit(full, true)
			if err := walkDir(full, visit); err != nil {
				return err
			}
		} else {
			visit(full, false)
		}
	}
	return nil
}
