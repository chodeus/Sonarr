package instrumentation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// RotatingWriter is an `io.WriteCloser` that wraps an on-disk log
// file and rolls it over when the file exceeds `MaxBytes`. Mirrors
// the C# NLog `FileTarget` archive-on-size behaviour that ships in
// `NzbDroneLogger` — rotation is performed by renaming the current
// file to a numbered backup (.1, .2, …) and opening a fresh empty
// file at the original path. Stale backups beyond `MaxBackups` are
// deleted on rotation.
//
// Audit fix C.6. Wired as `instrumentation.NewLogger(w, level)` with
// a `*RotatingWriter` in place of `os.Stderr` once the
// production-mode boot path picks `<data>/logs/sonarr.txt` (a
// follow-up will move main.go to it; the writer itself ships today
// so future work doesn't have to redo the rotation primitive).
//
// Concurrency: every Write acquires a mutex; rotation is performed
// inline so concurrent writers don't tear logs across file
// boundaries. Close drains and releases the underlying file.
type RotatingWriter struct {
	// Path is the absolute path of the current log file. Required.
	Path string
	// MaxBytes is the size threshold; once `Write` would push the
	// current file's byte count past this, rotation fires BEFORE
	// the write. Defaults to 10 MiB when zero.
	MaxBytes int64
	// MaxBackups is the maximum number of `.N` archives to keep.
	// Older archives are deleted on rotation. Defaults to 5 when
	// zero. Set to negative to keep every archive.
	MaxBackups int

	mu      sync.Mutex
	file    *os.File
	written int64
}

// DefaultMaxBytes mirrors NLog's `ArchiveAboveSize` default (10 MiB).
const DefaultMaxBytes int64 = 10 * 1024 * 1024

// DefaultMaxBackups mirrors the C# NzbDroneLogger.ArchiveMaximum.
const DefaultMaxBackups = 5

// Write appends `p` to the active log file. Rotates first if the
// pending write would exceed MaxBytes. Errors from rotation propagate
// to the caller without writing — better to drop a single log line
// than to write past the threshold or corrupt the rotation state.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	maxBytes := w.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if w.file == nil {
		if err := w.openLocked(); err != nil {
			return 0, err
		}
	}
	if w.written+int64(len(p)) > maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.written += int64(n)
	return n, err
}

// Close drains and closes the underlying file. Subsequent Writes
// re-open the file lazily.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	w.written = 0
	return err
}

func (w *RotatingWriter) openLocked() error {
	if w.Path == "" {
		return fmt.Errorf("instrumentation: RotatingWriter: empty Path")
	}
	if err := os.MkdirAll(filepath.Dir(w.Path), 0o755); err != nil {
		return fmt.Errorf("instrumentation: mkdir log dir: %w", err)
	}
	f, err := os.OpenFile(w.Path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("instrumentation: open log file: %w", err)
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("instrumentation: stat log file: %w", err)
	}
	w.file = f
	w.written = stat.Size()
	return nil
}

func (w *RotatingWriter) rotateLocked() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("instrumentation: close before rotate: %w", err)
		}
		w.file = nil
	}
	// Find the highest existing backup number so the new rotation
	// pushes onto the top.
	existing := listBackups(w.Path)
	next := 1
	if len(existing) > 0 {
		next = existing[len(existing)-1] + 1
	}
	if err := os.Rename(w.Path, fmt.Sprintf("%s.%d", w.Path, next)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("instrumentation: rename on rotate: %w", err)
	}
	// Prune to MaxBackups. After the rename above the highest backup
	// is `next`; existing is the prior list. Build the post-rotation
	// list and trim the oldest.
	max := w.MaxBackups
	if max == 0 {
		max = DefaultMaxBackups
	}
	if max > 0 {
		all := make([]int, 0, len(existing)+1)
		all = append(all, existing...)
		all = append(all, next)
		for len(all) > max {
			oldest := all[0]
			_ = os.Remove(fmt.Sprintf("%s.%d", w.Path, oldest))
			all = all[1:]
		}
	}
	return w.openLocked()
}

// listBackups returns the sorted backup numbers present alongside
// the given log file. Helper exported via internal-package tests.
func listBackups(path string) []int {
	dir := filepath.Dir(path)
	base := filepath.Base(path) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var nums []int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, base) {
			continue
		}
		tail := strings.TrimPrefix(name, base)
		n, err := strconv.Atoi(tail)
		if err != nil {
			continue
		}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums
}

// Compile-time guard.
var _ io.WriteCloser = (*RotatingWriter)(nil)
