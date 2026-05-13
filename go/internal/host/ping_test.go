package host

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type probeFn func(ctx context.Context) error

func (f probeFn) All(ctx context.Context) error { return f(ctx) }

func newTestServer(t *testing.T, probe ConfigProbe) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Config{BindAddress: "127.0.0.1", Port: 0}, logger, probe, staticAPIKey("test-key"))
}

// staticAPIKey is a tiny APIKeyResolver for tests that don't care
// about runtime key rotation.
type staticAPIKey string

func (k staticAPIKey) APIKey(context.Context) (string, error) { return string(k), nil }

func TestPingHandler(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		method   string
		probeErr error
		wantCode int
		wantBody string
	}{
		{"get_ok", http.MethodGet, nil, http.StatusOK, `{"status":"OK"}`},
		{"get_err", http.MethodGet, errors.New("db down"), http.StatusInternalServerError, `{"status":"Error"}`},
		{"head_ok", http.MethodHead, nil, http.StatusOK, ""},
		{"head_err", http.MethodHead, errors.New("db down"), http.StatusInternalServerError, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newTestServer(t, probeFn(func(context.Context) error { return tc.probeErr }))
			req := httptest.NewRequest(tc.method, "/ping", nil)
			rr := httptest.NewRecorder()
			srv.buildMux().ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Fatalf("status: got %d want %d", rr.Code, tc.wantCode)
			}
			got := rr.Body.String()
			if got != tc.wantBody {
				t.Fatalf("body: got %q want %q", got, tc.wantBody)
			}
			if got != "" {
				ct := rr.Header().Get("Content-Type")
				if !strings.HasPrefix(ct, "application/json") {
					t.Fatalf("content-type: got %q want application/json; charset=utf-8", ct)
				}
			}
		})
	}
}

func TestPingUnsupportedMethod(t *testing.T) {
	t.Parallel()
	// POST /ping is not registered; ServeMux returns 405 for a
	// known path with an unsupported method (Go 1.22+ behaviour).
	srv := newTestServer(t, probeFn(func(context.Context) error { return nil }))
	req := httptest.NewRequest(http.MethodPost, "/ping", nil)
	rr := httptest.NewRecorder()
	srv.buildMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /ping; got %d", rr.Code)
	}
}
