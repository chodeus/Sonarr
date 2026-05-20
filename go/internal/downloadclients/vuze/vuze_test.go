package vuze_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/transmission"
	"sonarr.tv/sonarr/internal/downloadclients/vuze"
	"sonarr.tv/sonarr/internal/indexers"
)

// vuzeServer is the minimal Transmission RPC fake the Vuze tests
// drive. Transmission's RPC dance requires a 409 + X-Transmission-Session-Id
// handshake on the first request; subsequent requests replay the
// session id.
type vuzeServer struct {
	srv       *httptest.Server
	sessionID string
	rpcConfig string
	got409    atomic.Bool
}

func newVuzeServer(t *testing.T, rpcConfig string) *vuzeServer {
	t.Helper()
	h := &vuzeServer{
		sessionID: "test-session-id",
		rpcConfig: rpcConfig,
	}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid := r.Header.Get("X-Transmission-Session-Id")
		if sid == "" {
			// First request — 409 with session id.
			w.Header().Set("X-Transmission-Session-Id", h.sessionID)
			h.got409.Store(true)
			w.WriteHeader(http.StatusConflict)
			return
		}
		if sid != h.sessionID {
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
		// Sniff the method from the request — only session-get is
		// exercised by the Test path.
		if strings.Contains(string(body), `"method":"session-get"`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"result":"success","arguments":`+h.rpcConfig+`}`)
			return
		}
		// Fallback — empty success.
		_, _ = io.WriteString(w, `{"result":"success","arguments":{}}`)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *vuzeServer) settings() transmission.Settings {
	u, _ := url.Parse(h.srv.URL)
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n := 0
		for _, r := range p {
			n = n*10 + int(r-'0')
		}
		port = n
	}
	s := transmission.NewSettings()
	s.Host = u.Hostname()
	s.Port = port
	return s
}

func TestProvider_NameAndProtocol(t *testing.T) {
	t.Parallel()
	v := vuze.New(nil, transmission.NewSettings(), nil, nil)
	if v.Name() != "Vuze" {
		t.Errorf("Name: %q, want Vuze", v.Name())
	}
	if v.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol: %v, want torrent", v.Protocol())
	}
}

func TestProvider_TestSurfacesSettingsFailures(t *testing.T) {
	t.Parallel()
	s := transmission.NewSettings()
	s.Host = ""
	v := vuze.New(nil, s, nil, nil)
	failures, err := v.Test(context.Background())
	if err != nil {
		t.Errorf("Test: %v", err)
	}
	if len(failures) == 0 {
		t.Errorf("Test on empty Host: want at least one failure")
	}
}

func TestProvider_TestProbesProtocolVersionViaProxy(t *testing.T) {
	t.Parallel()
	h := newVuzeServer(t, `{"version":"5.0","rpc-version":"15"}`)
	proxy, err := transmission.NewProxy(h.srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	v := vuze.New(nil, h.settings(), proxy, nil)
	failures, err := v.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test on good protocol: %+v, want no failures", failures)
	}
}

func TestProvider_TestRejectsBelowMinProtocolVersion(t *testing.T) {
	t.Parallel()
	// rpc-version 13 → below the 14 floor → reject.
	h := newVuzeServer(t, `{"version":"4.0","rpc-version":"13"}`)
	proxy, err := transmission.NewProxy(h.srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	v := vuze.New(nil, h.settings(), proxy, nil)
	failures, err := v.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 {
		t.Errorf("Test on old protocol: want failure")
	}
}

func TestProvider_TestNetworkFailureSurfacesHostFailure(t *testing.T) {
	t.Parallel()
	h := newVuzeServer(t, `{"version":"5.0","rpc-version":"15"}`)
	h.srv.Close() // force connection failure
	proxy, err := transmission.NewProxy(h.srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	v := vuze.New(nil, h.settings(), proxy, nil)
	failures, err := v.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 || failures[0].PropertyName != "Host" {
		t.Errorf("Test on dead host: %+v, want Host failure", failures)
	}
}

// ============================================================================
// Registry registration.
// ============================================================================

func TestRegistry_VuzeRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(vuze.Implementation, vuze.ConfigContract) {
		t.Fatalf("registry has no entry for (%s, %s)", vuze.Implementation, vuze.ConfigContract)
	}
}

func TestRegistry_ConstructUsesTransmissionSettings(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: vuze.Implementation,
		ConfigContract: vuze.ConfigContract,
		Settings: `{
			"host":"vuze.example",
			"port":9091,
			"useSsl":false,
			"urlBase":"/transmission/",
			"username":"sonarr",
			"password":"hunter2",
			"tvCategory":"tv-sonarr"
		}`,
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if _, ok := p.(*vuze.Vuze); !ok {
		t.Fatalf("Construct: %T, want *vuze.Vuze", p)
	}
	if p.Name() != "Vuze" {
		t.Errorf("Name: %q, want Vuze (override of embedded Transmission)", p.Name())
	}
	if p.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol: %v, want torrent (inherited)", p.Protocol())
	}
}

func TestRegistry_ConstructBadJSON(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: vuze.Implementation,
		ConfigContract: vuze.ConfigContract,
		Settings:       `{bad json`,
	}
	if _, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{}); err == nil {
		t.Errorf("Construct(bad JSON): want error")
	}
}

func TestRegistry_ConstructNilRow(t *testing.T) {
	t.Parallel()
	if _, err := downloadclients.Default().Construct(nil, downloadclients.Dependencies{}); err == nil {
		t.Errorf("Construct(nil): want error")
	}
}
