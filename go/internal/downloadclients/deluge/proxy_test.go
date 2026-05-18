package deluge_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/deluge"
)

// settingsFor returns a Settings pointed at the supplied test
// server. Password set per-test as needed.
func settingsFor(t *testing.T, srv *httptest.Server) deluge.Settings {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	s := deluge.NewSettings()
	s.Host = u.Hostname()
	s.Port = port
	s.UseSsl = false
	return s
}

func TestProxy_BuildBaseURL_HTTP(t *testing.T) {
	t.Parallel()
	p, err := deluge.NewProxy(http.DefaultClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := deluge.NewSettings()
	if got, want := p.BuildBaseURL(s), "http://localhost:8112/json"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_BuildBaseURL_HTTPS_WithBase(t *testing.T) {
	t.Parallel()
	p, _ := deluge.NewProxy(http.DefaultClient, nil)
	s := deluge.NewSettings()
	s.Host = "deluge.example"
	s.Port = 8443
	s.UseSsl = true
	s.UrlBase = "/deluge/"
	if got, want := p.BuildBaseURL(s), "https://deluge.example:8443/deluge/json"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_BuildBaseURL_UrlBaseLeadingSlashAdded(t *testing.T) {
	t.Parallel()
	p, _ := deluge.NewProxy(http.DefaultClient, nil)
	s := deluge.NewSettings()
	s.Host = "h"
	s.Port = 1
	s.UrlBase = "deluge"
	if got, want := p.BuildBaseURL(s), "http://h:1/deluge/json"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_BuildBaseURL_RootUrlBaseIgnored(t *testing.T) {
	t.Parallel()
	p, _ := deluge.NewProxy(http.DefaultClient, nil)
	s := deluge.NewSettings()
	s.Host = "h"
	s.Port = 1
	s.UrlBase = "/"
	if got, want := p.BuildBaseURL(s), "http://h:1/json"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

// jsonRPCEnvelope is the body shape every JSON-RPC test handler
// parses out of the incoming request.
type jsonRPCEnvelope struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	ID     int             `json:"id"`
}

func readEnvelope(t *testing.T, r *http.Request) jsonRPCEnvelope {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var env jsonRPCEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	return env
}

func TestProxy_EnsureAuth_HappyPath(t *testing.T) {
	t.Parallel()
	var loginCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		if env.Method != "auth.login" {
			t.Errorf("method = %q, want auth.login", env.Method)
		}
		var params []string
		_ = json.Unmarshal(env.Params, &params)
		if len(params) != 1 || params[0] != "deluge" {
			t.Errorf("params = %v, want [deluge]", params)
		}
		loginCalls.Add(1)
		http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sess-abc"})
		_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
	}))
	defer srv.Close()

	p, _ := deluge.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)

	sid, err := p.EnsureAuth(context.Background(), s, false)
	if err != nil {
		t.Fatalf("EnsureAuth() err = %v", err)
	}
	if sid != "sess-abc" {
		t.Errorf("sid = %q, want sess-abc", sid)
	}

	// Second call hits cache.
	sid2, err := p.EnsureAuth(context.Background(), s, false)
	if err != nil {
		t.Fatalf("EnsureAuth()#2 err = %v", err)
	}
	if sid2 != "sess-abc" {
		t.Errorf("sid2 = %q, want sess-abc (cache hit)", sid2)
	}
	if got := loginCalls.Load(); got != 1 {
		t.Errorf("login fired %d times, want 1", got)
	}

	// force=true re-fires.
	sid3, err := p.EnsureAuth(context.Background(), s, true)
	if err != nil {
		t.Fatalf("EnsureAuth(force) err = %v", err)
	}
	if sid3 != "sess-abc" {
		t.Errorf("sid3 = %q, want sess-abc", sid3)
	}
	if got := loginCalls.Load(); got != 2 {
		t.Errorf("login fired %d times after force, want 2", got)
	}
}

func TestProxy_EnsureAuth_BadPasswordReturnsFalse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":false,"error":null,"id":1}`)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Password = "wrong"
	_, err := p.EnsureAuth(context.Background(), s, false)
	if !errors.Is(err, deluge.ErrAuthFailed) {
		t.Errorf("EnsureAuth(bad pwd) err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_EnsureAuth_ErrorObjectFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":null,"error":{"code":3,"message":"boom"},"id":1}`)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	_, err := p.EnsureAuth(context.Background(), settingsFor(t, srv), false)
	if !errors.Is(err, deluge.ErrAuthFailed) {
		t.Errorf("EnsureAuth(error obj) err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_EnsureAuth_NoCookieFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	_, err := p.EnsureAuth(context.Background(), settingsFor(t, srv), false)
	if !errors.Is(err, deluge.ErrAuthFailed) {
		t.Errorf("EnsureAuth(no cookie) err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_EnsureAuth_HTTP401(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	_, err := p.EnsureAuth(context.Background(), settingsFor(t, srv), false)
	if !errors.Is(err, deluge.ErrAuthFailed) {
		t.Errorf("EnsureAuth(401) err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_Call_HappyPath(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		calls.Add(1)
		switch env.Method {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sess"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":`+strconv.Itoa(env.ID)+`}`)
		case "daemon.get_version":
			// Verify the session cookie is replayed.
			c, _ := r.Cookie("_session_id")
			if c == nil || c.Value != "sess" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, `{"result":"2.1.1","error":null,"id":`+strconv.Itoa(env.ID)+`}`)
		default:
			t.Errorf("unexpected method %q", env.Method)
		}
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)

	raw, err := p.Call(context.Background(), "daemon.get_version", nil, s)
	if err != nil {
		t.Fatalf("Call() err = %v", err)
	}
	var version string
	_ = json.Unmarshal(raw, &version)
	if version != "2.1.1" {
		t.Errorf("version = %q, want 2.1.1", version)
	}
	// auth.login + daemon.get_version = 2 calls total.
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func TestProxy_Call_RPCErrorSurfacesRPCFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		if env.Method == "auth.login" {
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "s"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
			return
		}
		_, _ = io.WriteString(w, `{"result":null,"error":{"code":5,"message":"some failure"},"id":2}`)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "core.something", nil, settingsFor(t, srv))
	if !errors.Is(err, deluge.ErrRPCFailure) {
		t.Errorf("Call() err = %v, want ErrRPCFailure", err)
	}
}

func TestProxy_Call_AuthRequiredTriggersReauth(t *testing.T) {
	t.Parallel()
	var (
		loginCalls  atomic.Int32
		methodCalls atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		if env.Method == "auth.login" {
			loginCalls.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "fresh"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
			return
		}
		methodCalls.Add(1)
		c, _ := r.Cookie("_session_id")
		// First call surfaces "auth required" (code=1) to force a
		// reauth; the replay reads the fresh cookie and succeeds.
		if c == nil || c.Value != "fresh" || methodCalls.Load() == 1 {
			_, _ = io.WriteString(w, `{"result":null,"error":{"code":1,"message":"Not authenticated"},"id":1}`)
			return
		}
		_, _ = io.WriteString(w, `{"result":"ok","error":null,"id":2}`)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	// Pre-prime cache with a stale cookie so the first attempt
	// goes through with a stale session that triggers error code 1.
	s := settingsFor(t, srv)
	if _, err := p.EnsureAuth(context.Background(), s, false); err != nil {
		t.Fatalf("seed EnsureAuth: %v", err)
	}
	// At this point the cache holds "fresh"; but methodCalls.Load() == 0,
	// so the server returns error code 1 on the FIRST RPC. The Call()
	// reauth branch then re-fires auth.login + replays.
	got, err := p.Call(context.Background(), "core.something", nil, s)
	if err != nil {
		t.Fatalf("Call() err = %v", err)
	}
	var v string
	_ = json.Unmarshal(got, &v)
	if v != "ok" {
		t.Errorf("Call() result = %q, want ok", v)
	}
	if loginCalls.Load() != 2 {
		t.Errorf("login fired %d times, want 2 (seed + reauth)", loginCalls.Load())
	}
	if methodCalls.Load() != 2 {
		t.Errorf("method fired %d times, want 2 (initial + reauth replay)", methodCalls.Load())
	}
}

func TestProxy_Call_SessionExpiredTriggersReauth(t *testing.T) {
	t.Parallel()
	var (
		methodCalls atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		if env.Method == "auth.login" {
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sess"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
			return
		}
		methodCalls.Add(1)
		if methodCalls.Load() == 1 {
			_, _ = io.WriteString(w, `{"result":null,"error":{"code":2,"message":"session expired"},"id":2}`)
			return
		}
		_, _ = io.WriteString(w, `{"result":"after-reauth","error":null,"id":3}`)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	got, err := p.Call(context.Background(), "core.something", nil, settingsFor(t, srv))
	if err != nil {
		t.Fatalf("Call() err = %v", err)
	}
	var v string
	_ = json.Unmarshal(got, &v)
	if v != "after-reauth" {
		t.Errorf("Call() = %q, want after-reauth", v)
	}
}

func TestProxy_Call_ReauthExhaustionSurfacesAuthFailed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		if env.Method == "auth.login" {
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "s"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
			return
		}
		// Every RPC keeps returning auth-required even after reauth.
		_, _ = io.WriteString(w, `{"result":null,"error":{"code":1,"message":"Not authenticated"},"id":2}`)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "core.something", nil, settingsFor(t, srv))
	if !errors.Is(err, deluge.ErrAuthFailed) {
		t.Errorf("Call() err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_Call_ParamsForwardedAsJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		if env.Method == "auth.login" {
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "s"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
			return
		}
		var got []any
		_ = json.Unmarshal(env.Params, &got)
		if len(got) != 2 || got[0] != "hash123" || got[1] != true {
			t.Errorf("params = %v, want [hash123 true]", got)
		}
		_, _ = io.WriteString(w, `{"result":null,"error":null,"id":2}`)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "core.remove_torrent",
		[]any{"hash123", true}, settingsFor(t, srv))
	if err != nil {
		t.Fatalf("Call() err = %v", err)
	}
}

func TestProxy_Call_TransportErrorWrapsUnavailable(t *testing.T) {
	t.Parallel()
	p, _ := deluge.NewProxy(http.DefaultClient, nil)
	s := deluge.NewSettings()
	s.Host = "127.0.0.1"
	s.Port = 1 // privileged + nothing listens
	_, err := p.Call(context.Background(), "daemon.info", nil, s)
	if !errors.Is(err, deluge.ErrUnavailable) {
		t.Errorf("err = %v, want wrapped ErrUnavailable", err)
	}
}

func TestProxy_Call_NonOKStatusSurfacesUnexpected(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		if env.Method == "auth.login" {
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "s"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	p, _ := deluge.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "core.something", nil, settingsFor(t, srv))
	if !errors.Is(err, deluge.ErrUnexpected) {
		t.Errorf("Call() err = %v, want ErrUnexpected", err)
	}
}

func TestProxy_NewProxy_NilDoer(t *testing.T) {
	t.Parallel()
	if _, err := deluge.NewProxy(nil, nil); err == nil {
		t.Fatal("NewProxy(nil) err = nil, want error")
	}
}
