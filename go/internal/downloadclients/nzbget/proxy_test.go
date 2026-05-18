package nzbget_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/nzbget"
)

// settingsFor returns a Settings pointed at the supplied test
// server. Username/Password set per-test as needed.
func settingsFor(t *testing.T, srv *httptest.Server) nzbget.Settings {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	s := nzbget.NewSettings()
	s.Host = u.Hostname()
	s.Port = port
	s.UseSsl = false
	return s
}

func TestProxy_BuildBaseURL_HTTP(t *testing.T) {
	t.Parallel()
	p, err := nzbget.NewProxy(http.DefaultClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := nzbget.NewSettings()
	if got, want := p.BuildBaseURL(s), "http://localhost:6789/jsonrpc"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_BuildBaseURL_HTTPS_WithBase(t *testing.T) {
	t.Parallel()
	p, _ := nzbget.NewProxy(http.DefaultClient, nil)
	s := nzbget.NewSettings()
	s.Host = "nzbget.example"
	s.Port = 6790
	s.UseSsl = true
	s.UrlBase = "/nzbget/"
	if got, want := p.BuildBaseURL(s), "https://nzbget.example:6790/nzbget/jsonrpc"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_BuildBaseURL_RootUrlBaseIgnored(t *testing.T) {
	t.Parallel()
	p, _ := nzbget.NewProxy(http.DefaultClient, nil)
	s := nzbget.NewSettings()
	s.Host = "h"
	s.Port = 1
	s.UrlBase = "/"
	if got, want := p.BuildBaseURL(s), "http://h:1/jsonrpc"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

func TestProxy_BuildBaseURL_UrlBaseLeadingSlashAdded(t *testing.T) {
	t.Parallel()
	p, _ := nzbget.NewProxy(http.DefaultClient, nil)
	s := nzbget.NewSettings()
	s.Host = "h"
	s.Port = 1
	s.UrlBase = "nzbget"
	if got, want := p.BuildBaseURL(s), "http://h:1/nzbget/jsonrpc"; got != want {
		t.Errorf("BuildBaseURL = %q, want %q", got, want)
	}
}

// rpcEnvelope is the request shape every JSON-RPC test handler
// parses out of the incoming POST body.
type rpcEnvelope struct {
	Version string          `json:"version"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func readEnvelope(t *testing.T, r *http.Request) rpcEnvelope {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var env rpcEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	return env
}

func TestProxy_Call_HappyPathNoAuth(t *testing.T) {
	t.Parallel()
	var (
		calls atomic.Int32
		seen  atomic.Value
	)
	seen.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		seen.Store(r.Header.Get("Authorization"))
		if r.URL.Path != "/jsonrpc" {
			t.Errorf("path = %q, want /jsonrpc", r.URL.Path)
		}
		env := readEnvelope(t, r)
		if env.Version != "1.1" {
			t.Errorf("version = %q, want 1.1", env.Version)
		}
		if env.Method != "version" {
			t.Errorf("method = %q, want version", env.Method)
		}
		_, _ = io.WriteString(w, `{"version":"1.1","result":"24.2"}`)
	}))
	defer srv.Close()

	p, _ := nzbget.NewProxy(srv.Client(), nil)
	raw, err := p.Call(context.Background(), "version", nil, settingsFor(t, srv))
	if err != nil {
		t.Fatalf("Call() err = %v", err)
	}
	var v string
	_ = json.Unmarshal(raw, &v)
	if v != "24.2" {
		t.Errorf("result = %q, want 24.2", v)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
	// No auth header attached when Username+Password empty.
	if got := seen.Load().(string); got != "" {
		t.Errorf("Authorization = %q, want empty (no creds)", got)
	}
}

func TestProxy_Call_HTTPBasicAuthAttached(t *testing.T) {
	t.Parallel()
	var seen atomic.Value
	seen.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"version":"1.1","result":"ok"}`)
	}))
	defer srv.Close()

	p, _ := nzbget.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "admin"
	s.Password = "secret"
	if _, err := p.Call(context.Background(), "version", nil, s); err != nil {
		t.Fatalf("Call() err = %v", err)
	}
	got := seen.Load().(string)
	if !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("Authorization = %q, want Basic prefix", got)
	}
	encoded := strings.TrimPrefix(got, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != "admin:secret" {
		t.Errorf("decoded auth = %q, want admin:secret", decoded)
	}
}

func TestProxy_Call_AuthAttachedWhenOnlyOneCredSet(t *testing.T) {
	t.Parallel()
	// Some NZBGet installs run with username-only OR password-only
	// configuration. The proxy should still attach the Basic header
	// (with the empty side as empty) so the server can reject
	// cleanly.
	var seen atomic.Value
	seen.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"version":"1.1","result":"ok"}`)
	}))
	defer srv.Close()
	p, _ := nzbget.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.Username = "admin"
	s.Password = ""
	if _, err := p.Call(context.Background(), "version", nil, s); err != nil {
		t.Fatalf("Call() err = %v", err)
	}
	if got := seen.Load().(string); !strings.HasPrefix(got, "Basic ") {
		t.Errorf("Authorization = %q, want Basic prefix", got)
	}
}

func TestProxy_Call_ParamsForwardedAsArray(t *testing.T) {
	t.Parallel()
	var seenParams atomic.Value
	seenParams.Store(json.RawMessage(nil))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := readEnvelope(t, r)
		seenParams.Store(env.Params)
		_, _ = io.WriteString(w, `{"version":"1.1","result":true}`)
	}))
	defer srv.Close()
	p, _ := nzbget.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "editqueue",
		[]any{"HistoryDelete", 0, "", 42}, settingsFor(t, srv))
	if err != nil {
		t.Fatalf("Call() err = %v", err)
	}
	raw := seenParams.Load().(json.RawMessage)
	var got []any
	_ = json.Unmarshal(raw, &got)
	if len(got) != 4 || got[0] != "HistoryDelete" || got[3] != float64(42) {
		t.Errorf("params = %v, want [HistoryDelete 0  42]", got)
	}
}

func TestProxy_Call_RPCErrorSurfacesRPCFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"1.1","error":{"name":"InvalidParameter","code":2,"message":"bad param"}}`)
	}))
	defer srv.Close()
	p, _ := nzbget.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "method", nil, settingsFor(t, srv))
	if !errors.Is(err, nzbget.ErrRPCFailure) {
		t.Errorf("Call() err = %v, want ErrRPCFailure", err)
	}
	if !strings.Contains(err.Error(), "bad param") {
		t.Errorf("err = %v, want message text", err)
	}
}

func TestProxy_Call_401SurfacesAuthFailed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p, _ := nzbget.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "version", nil, settingsFor(t, srv))
	if !errors.Is(err, nzbget.ErrAuthFailed) {
		t.Errorf("Call() err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_Call_403SurfacesAuthFailed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	p, _ := nzbget.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "version", nil, settingsFor(t, srv))
	if !errors.Is(err, nzbget.ErrAuthFailed) {
		t.Errorf("Call() err = %v, want ErrAuthFailed", err)
	}
}

func TestProxy_Call_5xxSurfacesUnexpected(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "internal")
	}))
	defer srv.Close()
	p, _ := nzbget.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "version", nil, settingsFor(t, srv))
	if !errors.Is(err, nzbget.ErrUnexpected) {
		t.Errorf("Call() err = %v, want ErrUnexpected", err)
	}
}

func TestProxy_Call_TransportErrorWrapsUnavailable(t *testing.T) {
	t.Parallel()
	p, _ := nzbget.NewProxy(http.DefaultClient, nil)
	s := nzbget.NewSettings()
	s.Host = "127.0.0.1"
	s.Port = 1 // nothing listens
	_, err := p.Call(context.Background(), "version", nil, s)
	if !errors.Is(err, nzbget.ErrUnavailable) {
		t.Errorf("Call() err = %v, want wrapped ErrUnavailable", err)
	}
}

func TestProxy_Call_MalformedJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{not json`)
	}))
	defer srv.Close()
	p, _ := nzbget.NewProxy(srv.Client(), nil)
	_, err := p.Call(context.Background(), "version", nil, settingsFor(t, srv))
	if err == nil {
		t.Fatal("Call() err = nil, want parse error")
	}
}

func TestProxy_NewProxy_NilDoer(t *testing.T) {
	t.Parallel()
	if _, err := nzbget.NewProxy(nil, nil); err == nil {
		t.Fatal("NewProxy(nil) err = nil, want error")
	}
}
