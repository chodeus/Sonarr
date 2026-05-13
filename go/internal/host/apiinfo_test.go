package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIInfoHandler(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, probeFn(func(context.Context) error { return nil }))
	// Unwrapped — the middleware is exercised in TestRequireAPIKey;
	// here we verify the body and headers the handler produces on
	// its own.
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rr := httptest.NewRecorder()
	srv.apiInfoHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	want := `{"current":"v5","deprecated":["v3"]}`
	if got := rr.Body.String(); got != want {
		t.Errorf("body: got %q want %q", got, want)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}
}

func TestAPIInfoMountedBehindAPIKey(t *testing.T) {
	t.Parallel()
	// End-to-end check that buildMux wires the middleware around
	// /api: a request without the key must 401, with the key must
	// 200.
	srv := newTestServer(t, probeFn(func(context.Context) error { return nil }))
	mux := srv.buildMux()

	noKey := httptest.NewRequest(http.MethodGet, "/api", nil)
	rrNoKey := httptest.NewRecorder()
	mux.ServeHTTP(rrNoKey, noKey)
	if rrNoKey.Code != http.StatusUnauthorized {
		t.Errorf("no-key status: got %d want 401", rrNoKey.Code)
	}

	withKey := httptest.NewRequest(http.MethodGet, "/api", nil)
	withKey.Header.Set("X-Api-Key", "test-key")
	rrAuthed := httptest.NewRecorder()
	mux.ServeHTTP(rrAuthed, withKey)
	if rrAuthed.Code != http.StatusOK {
		t.Errorf("authed status: got %d want 200", rrAuthed.Code)
	}
}
