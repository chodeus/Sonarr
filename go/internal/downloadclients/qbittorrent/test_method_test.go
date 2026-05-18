package qbittorrent_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
)

// ============================================================================
// QBittorrent.Test() — slice 5.G1c. Ports the connection +
// version-compat probe from C# QBittorrent.TestConnection at
// Download/Clients/QBittorrent/QBittorrent.cs:459-537.
// ============================================================================

// newQBit wires a QBittorrent provider against the httptest server.
func newQBit(t *testing.T, srv *httptest.Server, mutate func(s *qbittorrent.Settings)) *qbittorrent.QBittorrent {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := qbittorrent.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return qbittorrent.New(nil, s, proxy, nil)
}

// versionHandler returns an httptest server that serves the
// canonical /api/v2/auth/login + /api/v2/app/version path pair.
// loginBody / loginStatus / versionStatus / versionBody let
// individual tests vary the responses.
type versionHandler struct {
	loginStatus   int
	loginBody     string
	loginCookie   string
	versionStatus int
	versionBody   string

	loginCalls   atomic.Int32
	versionCalls atomic.Int32
}

func (h *versionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v2/auth/login":
		h.loginCalls.Add(1)
		if h.loginCookie != "" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: h.loginCookie})
		}
		status := h.loginStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		body := h.loginBody
		if body == "" {
			body = "Ok."
		}
		_, _ = io.WriteString(w, body)
	case "/api/v2/app/version":
		h.versionCalls.Add(1)
		status := h.versionStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		body := h.versionBody
		if body == "" {
			body = "v4.5.0"
		}
		_, _ = io.WriteString(w, body)
	default:
		http.NotFound(w, r)
	}
}

func TestQBit_Test_OK(t *testing.T) {
	t.Parallel()
	h := &versionHandler{loginCookie: "sid", versionBody: "v4.5.0"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "admin"
		s.Password = "secret"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures", failures)
	}
	if got := h.loginCalls.Load(); got != 1 {
		t.Errorf("login fired %d times, want 1", got)
	}
	if got := h.versionCalls.Load(); got != 1 {
		t.Errorf("version fired %d times, want 1", got)
	}
}

func TestQBit_Test_VersionFloor(t *testing.T) {
	t.Parallel()
	h := &versionHandler{loginCookie: "sid", versionBody: "v4.0.0"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for 4.0.0 < 4.1", failures)
	}
}

func TestQBit_Test_VersionFloorOnExactMinusOne(t *testing.T) {
	t.Parallel()
	// 4.1.0 is the floor — exactly at it is OK.
	h := &versionHandler{loginCookie: "sid", versionBody: "v4.1.0"}
	srv := httptest.NewServer(h)
	defer srv.Close()
	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want no Version failure at floor", failures)
	}
}

func TestQBit_Test_LoginFails401(t *testing.T) {
	t.Parallel()
	h := &versionHandler{loginStatus: http.StatusUnauthorized}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "wrong"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Username") {
		t.Errorf("Test() = %+v, want Username failure (auth pinned)", failures)
	}
	if h.versionCalls.Load() != 0 {
		t.Errorf("version fired despite login failure")
	}
}

func TestQBit_Test_LoginFailsBadCredsBody(t *testing.T) {
	t.Parallel()
	h := &versionHandler{loginBody: "Fails."}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "wrong"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Username") {
		t.Errorf("Test() = %+v, want Username failure", failures)
	}
}

func TestQBit_Test_IPBanned(t *testing.T) {
	t.Parallel()
	h := &versionHandler{loginStatus: http.StatusForbidden, loginBody: "User's IP is banned for too many failed login attempts"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test() = %+v, want Host failure for IP-banned", failures)
	}
	if !strings.Contains(failures[0].ErrorMessage, "banned") {
		t.Errorf("Test() failure message = %q, want ban mention", failures[0].ErrorMessage)
	}
}

func TestQBit_Test_VersionEndpoint404(t *testing.T) {
	t.Parallel()
	h := &versionHandler{loginCookie: "sid", versionStatus: http.StatusNotFound, versionBody: "not found"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test() = %+v, want Host failure for 404", failures)
	}
}

func TestQBit_Test_BadVersionString(t *testing.T) {
	t.Parallel()
	h := &versionHandler{loginCookie: "sid", versionBody: "not-a-version"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Version") {
		t.Errorf("Test() = %+v, want Version failure for unparseable body", failures)
	}
}

func TestQBit_Test_VersionUnprefixed(t *testing.T) {
	t.Parallel()
	// Some forks strip the 'v' prefix.
	h := &versionHandler{loginCookie: "sid", versionBody: "5.0.0"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures (5.0.0 unprefixed)", failures)
	}
}

func TestQBit_Test_SettingsValidateShortCircuits(t *testing.T) {
	t.Parallel()
	// Server fails the test if it sees any traffic.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s — Validate failures should short-circuit", r.URL.Path)
	}))
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Port = 0 // Validate failure
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if !hasFailure(failures, "Port") {
		t.Errorf("Test() = %+v, want Port failure", failures)
	}
}

func TestQBit_Test_ApiKeyAuthPath(t *testing.T) {
	t.Parallel()
	var seenAuth atomic.Value
	seenAuth.Store("")
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			t.Errorf("login fired despite ApiKey auth")
			return
		}
		seenAuth.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, "v4.5.0")
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k123"
	})
	failures, err := q.Test(context.Background())
	if err != nil {
		t.Fatalf("Test() err = %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test() = %+v, want no failures", failures)
	}
	if got := seenAuth.Load().(string); got != "Bearer k123" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer k123")
	}
}

func TestQBit_Test_TransportErrorReturnsError(t *testing.T) {
	t.Parallel()
	// Stand up + immediately tear down a server so the port is
	// closed when Test fires.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	srv.Close()

	q := newQBit(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	failures, err := q.Test(context.Background())
	// Transport error → classifyAuthError catches ErrUnavailable
	// and surfaces it as a Host TestFailure rather than letting
	// it escape as err. Match Transmission Test pattern at
	// internal/downloadclients/transmission/transmission.go.
	if err != nil {
		t.Fatalf("Test() err = %v (want failure surfaced via slice)", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test() = %+v, want Host failure on connection-refused", failures)
	}
}

func TestQBit_Test_NilProxyReturnsError(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	_, err := q.Test(context.Background())
	if err == nil {
		t.Fatal("Test() err = nil, want error for nil proxy")
	}
}
