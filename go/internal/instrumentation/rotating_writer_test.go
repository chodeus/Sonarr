package instrumentation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriter_FirstWriteCreatesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &RotatingWriter{Path: filepath.Join(dir, "sonarr.log"), MaxBytes: 1 << 20}
	defer func() { _ = w.Close() }()
	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(w.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(body) != "hello\n" {
		t.Errorf("body = %q", body)
	}
}

func TestRotatingWriter_RotatesAtThreshold(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &RotatingWriter{Path: filepath.Join(dir, "sonarr.log"), MaxBytes: 16, MaxBackups: 3}
	defer func() { _ = w.Close() }()

	// Two writes of 10 bytes each → second triggers rotation.
	if _, err := w.Write([]byte("0123456789")); err != nil {
		t.Fatalf("Write1: %v", err)
	}
	if _, err := w.Write([]byte("abcdefghij")); err != nil {
		t.Fatalf("Write2: %v", err)
	}

	// After rotation, the .1 backup should hold the first write and
	// the active file should hold the second.
	body1, err := os.ReadFile(w.Path + ".1")
	if err != nil {
		t.Fatalf("ReadFile .1: %v", err)
	}
	if string(body1) != "0123456789" {
		t.Errorf(".1 body = %q", body1)
	}
	body, err := os.ReadFile(w.Path)
	if err != nil {
		t.Fatalf("ReadFile current: %v", err)
	}
	if string(body) != "abcdefghij" {
		t.Errorf("current body = %q", body)
	}
}

func TestRotatingWriter_PrunesOldBackups(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &RotatingWriter{Path: filepath.Join(dir, "sonarr.log"), MaxBytes: 4, MaxBackups: 2}
	defer func() { _ = w.Close() }()

	// Four writes of 4 bytes each — every Write triggers rotation.
	// Expected: .3 + .4 survive (most recent two backups), plus the
	// active file holds the last write. Wait — actually after the
	// 4th write rotates, the new active is empty until that write
	// happens. So sequence:
	//   W1 "AAAA" → active=AAAA (no rotation; written=4 == max, not >)
	//   W2 "BBBB" → would push to 8 > 4, rotate: AAAA→.1, active=BBBB
	//   W3 "CCCC" → rotate: BBBB→.2, active=CCCC
	//   W4 "DDDD" → rotate: CCCC→.3, active=DDDD; with MaxBackups=2
	//               .1 gets pruned.
	for _, s := range []string{"AAAA", "BBBB", "CCCC", "DDDD"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatalf("Write %q: %v", s, err)
		}
	}

	if _, err := os.Stat(w.Path + ".1"); !os.IsNotExist(err) {
		t.Errorf(".1 should be pruned; err = %v", err)
	}
	for _, n := range []string{".2", ".3"} {
		if _, err := os.Stat(w.Path + n); err != nil {
			t.Errorf("expected %s to exist: %v", w.Path+n, err)
		}
	}
	body, _ := os.ReadFile(w.Path)
	if string(body) != "DDDD" {
		t.Errorf("current body = %q", body)
	}
}

func TestRotatingWriter_DefaultsApply(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// MaxBytes=0 → DefaultMaxBytes (10 MiB), so 10 small writes
	// shouldn't rotate.
	w := &RotatingWriter{Path: filepath.Join(dir, "sonarr.log")}
	defer func() { _ = w.Close() }()
	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte("line\n")); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if _, err := os.Stat(w.Path + ".1"); !os.IsNotExist(err) {
		t.Errorf("default MaxBytes should not have triggered rotation")
	}
}

func TestRotatingWriter_CloseAllowsReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &RotatingWriter{Path: filepath.Join(dir, "sonarr.log"), MaxBytes: 1 << 20}
	if _, err := w.Write([]byte("first\n")); err != nil {
		t.Fatalf("Write1: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := w.Write([]byte("second\n")); err != nil {
		t.Fatalf("Write2: %v", err)
	}
	body, _ := os.ReadFile(w.Path)
	// File is opened with O_APPEND, so the second open appends to
	// the existing first-write content.
	if !strings.Contains(string(body), "first") || !strings.Contains(string(body), "second") {
		t.Errorf("body = %q", body)
	}
}

func TestRotatingWriter_RejectsEmptyPath(t *testing.T) {
	t.Parallel()
	w := &RotatingWriter{}
	if _, err := w.Write([]byte("oops")); err == nil {
		t.Errorf("expected error on empty path")
	}
}
