package deluge_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/deluge"
)

// ============================================================================
// Deluge.Test() — slice 5.G2c. Ports C# Deluge.TestConnection at
// Deluge.cs:276-323. No version floor (matches C# which doesn't
// enforce one); auth/connectivity failures pinned per the C#
// branches.
// ============================================================================

type testHandler struct {
	versionBody  string
	versionError string // non-empty → returns error object
	authResult   bool   // false → bad-password path
	loginCalls   atomic.Int32
	versionCalls atomic.Int32
	preLoginPath func(w http.ResponseWriter, r *http.Request) bool
}

func newTestHandler() *testHandler {
	return &testHandler{
		versionBody: `"2.1.1"`,
		authResult:  true,
	}
}

func (h *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.preLoginPath != nil && h.preLoginPath(w, r) {
		return
	}
	body, _ := io.ReadAll(r.Body)
	bs := string(body)
	switch {
	case stringContains(bs, `"method":"auth.login"`):
		h.loginCalls.Add(1)
		if !h.authResult {
			_, _ = io.WriteString(w, `{"result":false,"error":null,"id":1}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sess"})
		_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
	case stringContains(bs, `"method":"daemon.info"`):
		h.versionCalls.Add(1)
		if h.versionError != "" {
			_, _ = io.WriteString(w, `{"result":null,"error":{"code":99,"message":"`+h.versionError+`"},"id":2}`)
			return
		}
		_, _ = io.WriteString(w, `{"result":`+h.versionBody+`,"error":null,"id":2}`)
	default:
		http.NotFound(w, r)
	}
}

func stringContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func newDelugeForTest(t *testing.T, srv *httptest.Server, mutate func(*deluge.Settings)) *deluge.Deluge {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := deluge.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return deluge.New(nil, s, proxy, nil)
}

func TestTest_HappyPath(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures", failures)
	}
	if h.loginCalls.Load() != 1 {
		t.Errorf("login fired %d times, want 1", h.loginCalls.Load())
	}
	if h.versionCalls.Load() != 1 {
		t.Errorf("daemon.info fired %d times, want 1", h.versionCalls.Load())
	}
}

func TestTest_BadPasswordPinsToPasswordField(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.authResult = false
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Password") {
		t.Errorf("Test() = %+v, want Password failure", failures)
	}
	if h.versionCalls.Load() != 0 {
		t.Errorf("daemon.info fired despite auth failure")
	}
}

func TestTest_SettingsValidateShortCircuits(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s — Validate failures should short-circuit", r.URL.Path)
	}))
	defer srv.Close()

	d := newDelugeForTest(t, srv, func(s *deluge.Settings) {
		s.Port = 0 // Validate failure
	})
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Port") {
		t.Errorf("Test() = %+v, want Port failure", failures)
	}
}

func TestTest_TransportErrorReturnsHostFailure(t *testing.T) {
	t.Parallel()
	// Stand up + immediately tear down a server so the port is
	// closed when Test fires.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test() = %+v, want Host failure on connection-refused", failures)
	}
}

func TestTest_MalformedVersionResultsInVersionFailure(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.versionBody = `12345` // numeric, not string
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for non-string result", failures)
	}
}

func TestTest_EmptyVersionStringFails(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.versionBody = `"   "`
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for empty string", failures)
	}
}

func TestTest_DaemonInfoRPCFailureBubbles(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	h.versionError = "internal explosion"
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	// Non-auth RPC failure (code=99) doesn't match any
	// classifyAuthError arm → bubbles as a non-nil error.
	_, err := d.Test(context.Background())
	if err == nil {
		t.Fatal("Test() err = nil, want RPC failure")
	}
}

func TestTest_NilProxyReturnsError(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	_, err := d.Test(context.Background())
	if err == nil {
		t.Fatal("Test(nil proxy) err = nil, want error")
	}
}

func TestTest_DaemonInfoVersion1xAccepted(t *testing.T) {
	t.Parallel()
	// Deluge 1.x is still functional with the JSON-RPC API; the C#
	// tree doesn't enforce a version floor, neither does the Go
	// port.
	h := newTestHandler()
	h.versionBody = `"1.3.15"`
	srv := httptest.NewServer(h)
	defer srv.Close()

	d := newDelugeForTest(t, srv, nil)
	failures, err := d.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures for Deluge 1.3.x", failures)
	}
}
