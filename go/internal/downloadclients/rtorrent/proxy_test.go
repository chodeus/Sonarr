package rtorrent_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/rtorrent"
)

// xmlrpcServer is the minimal scgi-shaped fake — accepts POST /<urlBase>,
// reads the methodCall body, and emits a configurable response.
type xmlrpcServer struct {
	srv        *httptest.Server
	calls      atomic.Int32
	lastMethod atomic.Value // string
	lastPath   atomic.Value // string
	lastUser   atomic.Value // string
	respBody   atomic.Value // string
	respStatus atomic.Int32 // 0 → 200
}

func newXMLRPCServer(t *testing.T, respBody string) *xmlrpcServer {
	t.Helper()
	h := &xmlrpcServer{}
	h.respBody.Store(respBody)
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls.Add(1)
		h.lastPath.Store(r.URL.Path)
		if u, _, ok := r.BasicAuth(); ok {
			h.lastUser.Store(u)
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
		// Pull <methodName> from the body — primitive matching but
		// adequate for the unit-test boundary.
		if i := strings.Index(string(body), "<methodName>"); i >= 0 {
			rest := string(body)[i+len("<methodName>"):]
			if j := strings.Index(rest, "</methodName>"); j >= 0 {
				h.lastMethod.Store(rest[:j])
			}
		}
		if s := int(h.respStatus.Load()); s != 0 {
			w.WriteHeader(s)
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, h.respBody.Load().(string))
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *xmlrpcServer) settings(urlBase string) rtorrent.Settings {
	u, _ := url.Parse(h.srv.URL)
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		_, _ = strParseInt(p, &port)
	}
	s := rtorrent.NewSettings()
	s.Host = u.Hostname()
	s.Port = port
	s.UrlBase = urlBase
	return s
}

func strParseInt(s string, out *int) (int, error) {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a digit")
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return n, nil
}

const sampleVersionResponse = `<?xml version="1.0"?>
<methodResponse><params><param><value><string>0.9.8</string></value></param></params></methodResponse>`

const sampleFaultResponse = `<?xml version="1.0"?>
<methodResponse><fault><value><struct>
<member><name>faultCode</name><value><int>-503</int></value></member>
<member><name>faultString</name><value><string>method not found</string></value></member>
</struct></value></fault></methodResponse>`

func TestProxy_GetVersionReturnsString(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, sampleVersionResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	got, err := p.GetVersion(context.Background(), h.settings("RPC2"))
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got != "0.9.8" {
		t.Errorf("Version: got %q, want 0.9.8", got)
	}
	if m, _ := h.lastMethod.Load().(string); m != "system.client_version" {
		t.Errorf("methodName: got %q, want system.client_version", m)
	}
	if p, _ := h.lastPath.Load().(string); p != "/RPC2" {
		t.Errorf("path: got %q, want /RPC2", p)
	}
}

func TestProxy_GetVersionEmptyStringDefaultsToZero(t *testing.T) {
	t.Parallel()
	resp := `<?xml version="1.0"?>
<methodResponse><params><param><value><string></string></value></param></params></methodResponse>`
	h := newXMLRPCServer(t, resp)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	got, err := p.GetVersion(context.Background(), h.settings("RPC2"))
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got != "0.0.0" {
		t.Errorf("empty-string version: got %q, want 0.0.0 fallback", got)
	}
}

func TestProxy_FaultSurfacedAsTypedError(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, sampleFaultResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	_, err := p.GetVersion(context.Background(), h.settings("RPC2"))
	if err == nil {
		t.Fatalf("GetVersion: want fault error")
	}
	var fault *rtorrent.FaultError
	if !errors.As(err, &fault) {
		t.Fatalf("error type: got %T (%v), want *FaultError", err, err)
	}
	if fault.Code != -503 {
		t.Errorf("Code: got %d, want -503", fault.Code)
	}
	if !strings.Contains(fault.Message, "method not found") {
		t.Errorf("Message: %q", fault.Message)
	}
}

func TestProxy_BasicAuthAppliedWhenSet(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, sampleVersionResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.Username = "sonarr"
	s.Password = "hunter2"
	if _, err := p.GetVersion(context.Background(), s); err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if u, _ := h.lastUser.Load().(string); u != "sonarr" {
		t.Errorf("Basic-Auth user: got %q, want sonarr", u)
	}
}

func TestProxy_UnauthorizedTypedError(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, "")
	h.respStatus.Store(http.StatusUnauthorized)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	_, err := p.GetVersion(context.Background(), h.settings("RPC2"))
	if !errors.Is(err, rtorrent.ErrUnauthorized) {
		t.Errorf("401: got %v, want ErrUnauthorized", err)
	}
}

func TestProxy_HTTPErrorPropagates(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, "upstream broken")
	h.respStatus.Store(http.StatusInternalServerError)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	if _, err := p.GetVersion(context.Background(), h.settings("RPC2")); err == nil {
		t.Errorf("500: want error")
	}
}

func TestProxy_DefaultUrlBaseWhenEmpty(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, sampleVersionResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("")
	// Empty UrlBase should fall back to "RPC2".
	if _, err := p.GetVersion(context.Background(), s); err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if path, _ := h.lastPath.Load().(string); path != "/RPC2" {
		t.Errorf("default UrlBase: path = %q, want /RPC2", path)
	}
}
