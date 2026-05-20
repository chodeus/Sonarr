package lifecycle

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// WritePIDFile writes the running process's PID to `path`. Creates
// the parent directory if missing. Mirrors C# Sonarr's NLog
// `PidFile` target — Linux package maintainers (DEB/RPM) rely on
// the PID file for systemd's `PIDFile=` directive and for cron-
// monitored service health.
//
// Audit fix C.4.
func WritePIDFile(path string) error {
	if path == "" {
		return fmt.Errorf("lifecycle: WritePIDFile: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("lifecycle: mkdir pid dir: %w", err)
	}
	data := strconv.Itoa(os.Getpid()) + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		return fmt.Errorf("lifecycle: write pid file: %w", err)
	}
	return nil
}

// RemovePIDFile deletes the PID file at `path`. No-op when the file
// is already missing. Call once during shutdown.
func RemovePIDFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lifecycle: remove pid file: %w", err)
	}
	return nil
}

// SDNotifyReady writes `READY=1` to the systemd `$NOTIFY_SOCKET`
// when set (i.e. when the service is running under
// `Type=notify`). Returns nil on every other platform / when the
// env var is unset — systemd integration is opportunistic, never
// required. Safe to call multiple times.
//
// Mirrors the `Sd_Notify(0, "READY=1")` invocation Linux Sonarr
// packages run on startup; the Go port avoids the `github.com/coreos/
// go-systemd` dependency by writing the datagram directly (the
// protocol is a single newline-separated key/value packet on a unix
// datagram socket).
//
// Audit fix C.4.
func SDNotifyReady() error {
	return sdNotify("READY=1\n")
}

// SDNotifyStopping writes `STOPPING=1` to systemd if connected.
// Best-effort; errors are returned for the caller to log.
func SDNotifyStopping() error {
	return sdNotify("STOPPING=1\n")
}

func sdNotify(payload string) error {
	socketPath := os.Getenv("NOTIFY_SOCKET")
	if socketPath == "" {
		return nil
	}
	addr := &net.UnixAddr{Net: "unixgram", Name: socketPath}
	// Abstract-namespace sockets are denoted by a leading '@' in
	// $NOTIFY_SOCKET on Linux. The unix(7) syntax for those is a
	// NUL byte at position 0 — `net.ResolveUnixAddr` doesn't perform
	// that translation, so do it manually.
	if len(socketPath) > 0 && socketPath[0] == '@' {
		addr.Name = "\x00" + socketPath[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		return fmt.Errorf("lifecycle: dial NOTIFY_SOCKET: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(payload)); err != nil {
		return fmt.Errorf("lifecycle: write to NOTIFY_SOCKET: %w", err)
	}
	return nil
}
