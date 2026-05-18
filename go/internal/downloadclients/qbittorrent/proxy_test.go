package qbittorrent_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
)

// settingsFor returns a Settings pointed at the supplied test
// server. ApiKey/Username/Password are set per-test.
func settingsFor(t *testing.T, srv *httptest.Server) qbittorrent.Settings {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	s := qbittorrent.NewSettings()
	s.Host = u.Hostname()
	s.Port = port
	s.UseSsl = false
	return s
}

func TestProxy_BuildBaseURL_HTTP(t *testing.T) {
	t.Parallel()
	p, err := qbittorrent.NewProxy(http.DefaultClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := qbittorrent.NewSettings()
	s.Host = "localhost"
	s.Port = 8080
	if got, want := p.BuildBaseURL(s), "http://localhost:8080"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_BuildBaseURL_HTTPS_WithBase(t *testing.T) {
	t.Parallel()
	p, _ := qbittorrent.NewProxy(http.DefaultClient, nil)
	s := qbittorrent.NewSettings()
	s.Host = "qbit.example"
	s.Port = 8443
	s.UseSsl = true
	s.UrlBase = "/qbittorrent/"
	if got, want := p.BuildBaseURL(s), "https://qbit.example:8443/qbittorrent"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_BuildBaseURL_UrlBaseNoLeadingSlash(t *testing.T) {
	t.Parallel()
	p, _ := qbittorrent.NewProxy(http.DefaultClient, nil)
	s := qbittorrent.NewSettings()
	s.Host = "h"
	s.Port = 1
	s.UrlBase = "qbittorrent"
	if got, want := p.BuildBaseURL(s), "http://h:1/qbittorrent"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_EnsureAuth_NoCredentials_NoLogin(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Errorf("unexpected request to %s", r.URL.Path)
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	sid, err := p.EnsureAuth(context.Background(), settingsFor(t, srv), false)
	if err != nil {
		t.Fatalf("EnsureAuth() err = %v", err)
	}
	if sid != "" {
		t.Errorf("EnsureAuth() = %q, want empty (no creds → no login)", sid)
	}
	if calls.Load() != 0 {
		t.Errorf("login fired %d times, want 0", calls.Load())
	}
}

func TestProxy_EnsureAuth_ApiKey_NoLogin(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Errorf("unexpected request to %s", r.URL.Path)
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.ApiKey = "key123"
	sid, err := p.EnsureAuth(context.Background(), s, false)
	if err != nil {
		t.Fatalf("EnsureAuth(ApiKey) err = %v", err)
	}
	if sid != "" {
		t.Errorf("EnsureAuth(ApiKey) = %q, want empty (ApiKey bypasses login)", sid)
	}
	if calls.Load() != 0 {
		t.Errorf("login fired %d times for ApiKey path, want 0", calls.Load())
	}
}

func TestProxy_EnsureAuth_LoginSuccess_CachesCookie(t *testing.T) {
	t.Parallel()
	var loginCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		loginCalls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("login method = %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.FormValue("username"); got != "admin" {
			t.Errorf("username = %q, want admin", got)
		}
		if got := r.FormValue("password"); got != "secret" {
			t.Errorf("password = %q, want secret", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q, want form-urlencoded", got)
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session-abc"})
		_, _ = io.WriteString(w, "Ok.")
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "admin"
	s.Password = "secret"

	sid, err := p.EnsureAuth(context.Background(), s, false)
	if err != nil {
		t.Fatalf("EnsureAuth() err = %v", err)
	}
	if sid != "session-abc" {
		t.Errorf("EnsureAuth() = %q, want session-abc", sid)
	}

	// Second call uses the cache.
	sid2, err := p.EnsureAuth(context.Background(), s, false)
	if err != nil {
		t.Fatalf("EnsureAuth()#2 err = %v", err)
	}
	if sid2 != "session-abc" {
		t.Errorf("EnsureAuth()#2 = %q, want cache hit session-abc", sid2)
	}
	if got := loginCalls.Load(); got != 1 {
		t.Errorf("login fired %d times, want 1 (cache hit)", got)
	}

	// force=true bypasses cache and fires a fresh login.
	sid3, err := p.EnsureAuth(context.Background(), s, true)
	if err != nil {
		t.Fatalf("EnsureAuth(force) err = %v", err)
	}
	if sid3 != "session-abc" {
		t.Errorf("EnsureAuth(force) = %q, want session-abc", sid3)
	}
	if got := loginCalls.Load(); got != 2 {
		t.Errorf("login fired %d times after force=true, want 2", got)
	}
}

func TestProxy_EnsureAuth_BadCredsReturnsFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "Fails.")
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "admin"
	s.Password = "wrong"
	_, err := p.EnsureAuth(context.Background(), s, false)
	if !errors.Is(err, qbittorrent.ErrAuthFailed) {
		t.Errorf("EnsureAuth(bad creds) err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_EnsureAuth_HTTP401(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "u"
	s.Password = "p"
	_, err := p.EnsureAuth(context.Background(), s, false)
	if !errors.Is(err, qbittorrent.ErrAuthFailed) {
		t.Errorf("EnsureAuth(401) err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_EnsureAuth_IPBanned_HTTP403(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "User's IP is banned for too many failed login attempts")
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "u"
	s.Password = "p"
	_, err := p.EnsureAuth(context.Background(), s, false)
	if !errors.Is(err, qbittorrent.ErrIPBanned) {
		t.Errorf("EnsureAuth(banned) err = %v, want ErrIPBanned", err)
	}
}

func TestProxy_EnsureAuth_IPBanned_HTTP200(t *testing.T) {
	t.Parallel()
	// Some qBittorrent builds return 200 OK with an IP-banned body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "Your IP is banned.")
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "u"
	s.Password = "p"
	_, err := p.EnsureAuth(context.Background(), s, false)
	if !errors.Is(err, qbittorrent.ErrIPBanned) {
		t.Errorf("EnsureAuth(banned 200) err = %v, want ErrIPBanned", err)
	}
}

func TestProxy_EnsureAuth_NoSIDCookie(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "Ok.")
		// No Set-Cookie.
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "u"
	s.Password = "p"
	_, err := p.EnsureAuth(context.Background(), s, false)
	if !errors.Is(err, qbittorrent.ErrAuthFailed) {
		t.Errorf("EnsureAuth(no cookie) err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_DoRequest_LoginThenReplaysCookie(t *testing.T) {
	t.Parallel()
	var (
		loginCalls atomic.Int32
		apiCalls   atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			loginCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-xyz"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			apiCalls.Add(1)
			c, err := r.Cookie("SID")
			if err != nil || c.Value != "sid-xyz" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, "v4.5.0")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "admin"
	s.Password = "secret"

	for i := 0; i < 3; i++ {
		status, body, err := p.DoRequest(context.Background(), http.MethodGet, "/api/v2/app/version", nil, "", s)
		if err != nil {
			t.Fatalf("DoRequest()#%d err = %v", i, err)
		}
		if status != 200 || string(body) != "v4.5.0" {
			t.Errorf("DoRequest()#%d = (%d,%q), want (200,v4.5.0)", i, status, body)
		}
	}
	if got := loginCalls.Load(); got != 1 {
		t.Errorf("login fired %d times across 3 calls, want 1", got)
	}
	if got := apiCalls.Load(); got != 3 {
		t.Errorf("api fired %d times, want 3", got)
	}
}

func TestProxy_DoRequest_403TriggersReauthOnce(t *testing.T) {
	t.Parallel()
	var (
		loginCalls atomic.Int32
		apiCalls   atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			n := loginCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-" + strconv.Itoa(int(n))})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			apiCalls.Add(1)
			c, _ := r.Cookie("SID")
			// First two calls (initial + first reauth replay) — accept only sid-2 to force reauth on sid-1.
			if c == nil || c.Value != "sid-2" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, "Forbidden")
				return
			}
			_, _ = io.WriteString(w, "v4.5.0")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "admin"
	s.Password = "secret"

	status, body, err := p.DoRequest(context.Background(), http.MethodGet, "/api/v2/app/version", nil, "", s)
	if err != nil {
		t.Fatalf("DoRequest() err = %v", err)
	}
	if status != 200 || string(body) != "v4.5.0" {
		t.Errorf("DoRequest = (%d,%q), want (200,v4.5.0)", status, body)
	}
	if got := loginCalls.Load(); got != 2 {
		t.Errorf("login fired %d times, want 2 (initial + reauth)", got)
	}
	if got := apiCalls.Load(); got != 2 {
		t.Errorf("api fired %d times, want 2 (403 + 200)", got)
	}
}

func TestProxy_DoRequest_403WithoutCreds_NoReauthLoop(t *testing.T) {
	t.Parallel()
	var apiCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			t.Errorf("login fired despite empty creds")
			return
		}
		apiCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	status, _, err := p.DoRequest(context.Background(), http.MethodGet, "/api/v2/app/version", nil, "", s)
	if err != nil {
		t.Fatalf("DoRequest() err = %v", err)
	}
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
	if got := apiCalls.Load(); got != 1 {
		t.Errorf("api fired %d times, want 1 (no reauth loop)", got)
	}
}

func TestProxy_DoRequest_ApiKey_AttachesBearerHeader(t *testing.T) {
	t.Parallel()
	var seen atomic.Value
	seen.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			t.Errorf("login fired despite ApiKey auth")
			return
		}
		seen.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, "ok-body")
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.ApiKey = "key-xyz"

	status, body, err := p.DoRequest(context.Background(), http.MethodGet, "/api/v2/app/version", nil, "", s)
	if err != nil {
		t.Fatalf("DoRequest() err = %v", err)
	}
	if status != 200 || string(body) != "ok-body" {
		t.Errorf("DoRequest = (%d,%q), want (200,ok-body)", status, body)
	}
	if got := seen.Load().(string); got != "Bearer key-xyz" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer key-xyz")
	}
}

func TestProxy_DoRequest_PostBody(t *testing.T) {
	t.Parallel()
	var seen atomic.Value
	seen.Store("")
	var contentType atomic.Value
	contentType.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		body, _ := io.ReadAll(r.Body)
		seen.Store(string(body))
		contentType.Store(r.Header.Get("Content-Type"))
		_, _ = io.WriteString(w, "ack")
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "u"
	s.Password = "p"
	body := strings.NewReader("hashes=abc&deleteFiles=true")
	status, resp, err := p.DoRequest(context.Background(), http.MethodPost,
		"/api/v2/torrents/delete", body, "application/x-www-form-urlencoded", s)
	if err != nil {
		t.Fatalf("DoRequest() err = %v", err)
	}
	if status != 200 || string(resp) != "ack" {
		t.Errorf("DoRequest = (%d,%q), want (200,ack)", status, resp)
	}
	if got := seen.Load().(string); got != "hashes=abc&deleteFiles=true" {
		t.Errorf("server saw body %q, want %q", got, "hashes=abc&deleteFiles=true")
	}
	if got := contentType.Load().(string); got != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want form-urlencoded", got)
	}
}

func TestProxy_DoRequest_PathPrefixedAutomatically(t *testing.T) {
	t.Parallel()
	var seenPath atomic.Value
	seenPath.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath.Store(r.URL.Path)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.ApiKey = "k"
	_, _, err := p.DoRequest(context.Background(), http.MethodGet, "api/v2/app/version", nil, "", s)
	if err != nil {
		t.Fatalf("DoRequest() err = %v", err)
	}
	if got := seenPath.Load().(string); got != "/api/v2/app/version" {
		t.Errorf("seen path = %q, want %q", got, "/api/v2/app/version")
	}
}

func TestProxy_DoRequest_TransportErrorWrapsUnavailable(t *testing.T) {
	t.Parallel()
	p, _ := qbittorrent.NewProxy(http.DefaultClient, nil)
	s := qbittorrent.NewSettings()
	// Reserved test address — RFC 2606 / unreachable.
	s.Host = "127.0.0.1"
	s.Port = 1 // privileged + nothing listens
	s.ApiKey = "k"
	_, _, err := p.DoRequest(context.Background(), http.MethodGet, "/api/v2/app/version", nil, "", s)
	if err == nil {
		t.Fatal("DoRequest() err = nil, want unavailable")
	}
	if !errors.Is(err, qbittorrent.ErrUnavailable) {
		t.Errorf("err = %v, want wrapped ErrUnavailable", err)
	}
}

func TestProxy_NewProxy_NilDoer(t *testing.T) {
	t.Parallel()
	if _, err := qbittorrent.NewProxy(nil, nil); err == nil {
		t.Fatal("NewProxy(nil) err = nil, want error")
	}
}
