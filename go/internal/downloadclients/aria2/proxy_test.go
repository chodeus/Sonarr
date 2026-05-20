package aria2_test

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

	"sonarr.tv/sonarr/internal/downloadclients/aria2"
)

// aria2Server is the minimal XML-RPC fake — accepts POST + emits a
// configurable response body. Captures the last methodName +
// request body for assertions.
type aria2Server struct {
	srv        *httptest.Server
	calls      atomic.Int32
	lastMethod atomic.Value // string
	lastPath   atomic.Value // string
	lastBody   atomic.Value // string
	respBody   atomic.Value // string
	respStatus atomic.Int32 // 0 → 200
}

func newAria2Server(t *testing.T, respBody string) *aria2Server {
	t.Helper()
	h := &aria2Server{}
	h.respBody.Store(respBody)
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls.Add(1)
		h.lastPath.Store(r.URL.Path)
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
		h.lastBody.Store(string(body))
		if i := strings.Index(string(body), "<methodName>"); i >= 0 {
			rest := string(body)[i+len("<methodName>"):]
			if j := strings.Index(rest, "</methodName>"); j >= 0 {
				h.lastMethod.Store(rest[:j])
			}
		}
		if s := int(h.respStatus.Load()); s != 0 {
			w.WriteHeader(s)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, h.respBody.Load().(string))
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *aria2Server) settings() aria2.Settings {
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
	s := aria2.NewSettings()
	s.Host = u.Hostname()
	s.Port = port
	s.RpcPath = "/rpc"
	return s
}

// sampleGetVersionResponse — aria2.getVersion returns a struct with
// "version" + "enabledFeatures" members.
const sampleGetVersionResponse = `<?xml version="1.0"?>
<methodResponse><params><param><value><struct>
<member><name>version</name><value><string>1.36.0</string></value></member>
<member><name>enabledFeatures</name><value><array><data>
<value><string>Async DNS</string></value>
<value><string>BitTorrent</string></value>
</data></array></value></member>
</struct></value></param></params></methodResponse>`

const sampleFaultResponse = `<?xml version="1.0"?>
<methodResponse><fault><value><struct>
<member><name>faultCode</name><value><int>1</int></value></member>
<member><name>faultString</name><value><string>Unauthorized</string></value></member>
</struct></value></fault></methodResponse>`

func TestProxy_GetVersionReadsStructField(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, sampleGetVersionResponse)
	p := aria2.NewProxy(h.srv.Client(), nil)
	got, err := p.GetVersion(context.Background(), h.settings())
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got != "1.36.0" {
		t.Errorf("version: got %q, want 1.36.0", got)
	}
	if m, _ := h.lastMethod.Load().(string); m != "aria2.getVersion" {
		t.Errorf("methodName: got %q, want aria2.getVersion", m)
	}
	// SecretToken is sent as the first arg ("token:<value>").
	if body, _ := h.lastBody.Load().(string); !strings.Contains(body, "<string>token:MySecretToken</string>") {
		t.Errorf("token arg missing from body: %s", body)
	}
}

func TestProxy_GetVersionPathPreservesRpcPath(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, sampleGetVersionResponse)
	p := aria2.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.RpcPath = "/jsonrpc"
	if _, err := p.GetVersion(context.Background(), s); err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if path, _ := h.lastPath.Load().(string); path != "/jsonrpc" {
		t.Errorf("path: got %q, want /jsonrpc", path)
	}
}

func TestProxy_GetVersionPathDefaultsToRpc(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, sampleGetVersionResponse)
	p := aria2.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.RpcPath = "" // empty → default to /rpc
	if _, err := p.GetVersion(context.Background(), s); err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if path, _ := h.lastPath.Load().(string); path != "/rpc" {
		t.Errorf("default RpcPath: path = %q, want /rpc", path)
	}
}

func TestProxy_FaultSurfacesAsTypedError(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, sampleFaultResponse)
	p := aria2.NewProxy(h.srv.Client(), nil)
	_, err := p.GetVersion(context.Background(), h.settings())
	if err == nil {
		t.Fatalf("GetVersion: want fault error")
	}
	var fault *aria2.FaultError
	if !errors.As(err, &fault) {
		t.Fatalf("error type: got %T, want *FaultError", err)
	}
	if fault.Code != 1 || !strings.Contains(fault.Message, "Unauthorized") {
		t.Errorf("fault details: %+v", fault)
	}
}

func TestProxy_HTTPErrorPropagates(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, "broken")
	h.respStatus.Store(http.StatusInternalServerError)
	p := aria2.NewProxy(h.srv.Client(), nil)
	if _, err := p.GetVersion(context.Background(), h.settings()); err == nil {
		t.Errorf("500: want error")
	}
}

func TestProxy_MissingVersionFieldErrors(t *testing.T) {
	t.Parallel()
	resp := `<?xml version="1.0"?>
<methodResponse><params><param><value><struct>
<member><name>otherField</name><value><string>x</string></value></member>
</struct></value></param></params></methodResponse>`
	h := newAria2Server(t, resp)
	p := aria2.NewProxy(h.srv.Client(), nil)
	if _, err := p.GetVersion(context.Background(), h.settings()); err == nil {
		t.Errorf("missing 'version' field: want error")
	}
}

// Provider.Test integration via the proxy.
func TestProvider_TestProbesVersionViaProxy(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, sampleGetVersionResponse)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	failures, err := a.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test on good version: %+v, want no failures", failures)
	}
}

func TestProvider_TestRejectsBelowMinVersion(t *testing.T) {
	t.Parallel()
	resp := `<?xml version="1.0"?>
<methodResponse><params><param><value><struct>
<member><name>version</name><value><string>1.10.0</string></value></member>
</struct></value></param></params></methodResponse>`
	h := newAria2Server(t, resp)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	failures, err := a.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 || failures[0].PropertyName != "Host" {
		t.Errorf("Test on old version: %+v, want Host failure", failures)
	}
}

func TestProvider_TestJ110_FloorAlignedTo134(t *testing.T) {
	t.Parallel()
	// J110 — version floor bumped from 1.18 to 1.34 to match C#
	// Aria2.cs:237. 1.30.0 sits between the old and new floors,
	// so it was previously accepted but is now rejected.
	resp := `<?xml version="1.0"?>
<methodResponse><params><param><value><struct>
<member><name>version</name><value><string>1.30.0</string></value></member>
</struct></value></param></params></methodResponse>`
	h := newAria2Server(t, resp)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	failures, err := a.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 || failures[0].PropertyName != "Host" {
		t.Errorf("Test on 1.30.0: %+v, want Host failure (new floor=1.34)", failures)
	}
	if !strings.Contains(failures[0].ErrorMessage, "1.34.0") {
		t.Errorf("error message = %q, want it to reference 1.34.0", failures[0].ErrorMessage)
	}
}

func TestProvider_TestJ110_AcceptsAtNewFloor(t *testing.T) {
	t.Parallel()
	// Exact-floor version (1.34.0) passes.
	resp := `<?xml version="1.0"?>
<methodResponse><params><param><value><struct>
<member><name>version</name><value><string>1.34.0</string></value></member>
</struct></value></param></params></methodResponse>`
	h := newAria2Server(t, resp)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	failures, err := a.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test on 1.34.0: %+v, want no failures (at-floor)", failures)
	}
}

func TestProvider_TestNetworkFailureSurfacesHostFailure(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, sampleGetVersionResponse)
	h.srv.Close() // force connection failure
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	failures, err := a.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 || failures[0].PropertyName != "Host" {
		t.Errorf("Test on dead host: %+v, want Host failure", failures)
	}
}
