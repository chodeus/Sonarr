package host

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServer_RunListenAndShutdown(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind test listener: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Config{}, logger, probeFn(func(context.Context) error { return nil }),
		staticAPIKey("test-key"),
		WithListener(listener),
		WithShutdownTimeout(time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	base := "http://" + listener.Addr().String()
	if err := waitForReady(base+"/ping", 2*time.Second); err != nil {
		cancel()
		t.Fatalf("server did not become ready: %v", err)
	}

	resp, err := http.Get(base + "/ping")
	if err != nil {
		cancel()
		t.Fatalf("GET /ping: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("/ping status: got %d want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Run did not return after context cancel")
	}
}

func TestConfig_Listen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		bind string
		port int
		want string
	}{
		{"*", 8989, ":8989"},
		{"", 8989, ":8989"},
		{"  ", 8989, ":8989"},
		{"127.0.0.1", 8989, "127.0.0.1:8989"},
		{"::1", 8989, "[::1]:8989"},
	}
	for _, tc := range cases {
		got := Config{BindAddress: tc.bind, Port: tc.port}.Listen()
		if got != tc.want {
			t.Errorf("Config{%q,%d}.Listen() = %q, want %q", tc.bind, tc.port, got, tc.want)
		}
	}
}

func waitForReady(url string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
