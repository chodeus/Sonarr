package lifecycle_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/lifecycle"
)

// shortSockPath returns a path under the OS temp dir short enough to
// fit the unix-datagram-socket path limit (104 chars on macOS / 108
// on Linux). t.TempDir() paths under macOS commonly exceed that
// limit, which triggers `bind: invalid argument`. Using /tmp directly
// + a per-test base name keeps us inside the budget.
func shortSockPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(os.TempDir(), t.Name()+".sock")
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

func TestWritePIDFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sonarr.pid")
	if err := lifecycle.WritePIDFile(path); err != nil {
		t.Fatalf("WritePIDFile: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		t.Fatalf("parse pid: %v body=%q", err, body)
	}
	if got != os.Getpid() {
		t.Errorf("pid = %d want %d", got, os.Getpid())
	}
}

func TestWritePIDFile_EmptyPath(t *testing.T) {
	t.Parallel()
	if err := lifecycle.WritePIDFile(""); err == nil {
		t.Errorf("expected error on empty path")
	}
}

func TestRemovePIDFile_MissingIsNoOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := lifecycle.RemovePIDFile(filepath.Join(dir, "nope.pid")); err != nil {
		t.Errorf("missing-pid removal should be no-op; got %v", err)
	}
	if err := lifecycle.RemovePIDFile(""); err != nil {
		t.Errorf("empty-path removal should be no-op; got %v", err)
	}
}

func TestRemovePIDFile_DeletesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sonarr.pid")
	if err := lifecycle.WritePIDFile(path); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := lifecycle.RemovePIDFile(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone; err = %v", err)
	}
}

func TestSDNotifyReady_NoSocketIsNoOp(t *testing.T) {
	// Not parallel — t.Setenv mutates process state.
	t.Setenv("NOTIFY_SOCKET", "")
	if err := lifecycle.SDNotifyReady(); err != nil {
		t.Errorf("no socket: should be no-op; got %v", err)
	}
}

func TestSDNotifyReady_WritesToSocket(t *testing.T) {
	sockPath := shortSockPath(t)

	// Set up a unix datagram listener.
	addr := &net.UnixAddr{Net: "unixgram", Name: sockPath}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = conn.Close() }()

	t.Setenv("NOTIFY_SOCKET", sockPath)

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		done <- buf[:n]
	}()

	if err := lifecycle.SDNotifyReady(); err != nil {
		t.Fatalf("SDNotifyReady: %v", err)
	}
	got := <-done
	if string(got) != "READY=1\n" {
		t.Errorf("payload = %q", got)
	}
}

func TestSDNotifyStopping_WritesToSocket(t *testing.T) {
	sockPath := shortSockPath(t)
	addr := &net.UnixAddr{Net: "unixgram", Name: sockPath}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = conn.Close() }()

	t.Setenv("NOTIFY_SOCKET", sockPath)

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		done <- buf[:n]
	}()

	if err := lifecycle.SDNotifyStopping(); err != nil {
		t.Fatalf("SDNotifyStopping: %v", err)
	}
	got := <-done
	if string(got) != "STOPPING=1\n" {
		t.Errorf("payload = %q", got)
	}
}
