package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProxyType_String(t *testing.T) {
	cases := map[ProxyType]string{
		ProxyHTTP:   "Http",
		ProxySOCKS4: "Socks4",
		ProxySOCKS5: "Socks5",
	}
	for typ, want := range cases {
		if got := typ.String(); got != want {
			t.Errorf("ProxyType(%d).String() = %q want %q", typ, got, want)
		}
	}
}

func TestNewProxySettings_BlankHostDefaults(t *testing.T) {
	p := NewProxySettings(ProxyHTTP, "", 8080, "", false, "", "")
	if p.Host != "127.0.0.1" {
		t.Errorf("Host = %q want 127.0.0.1", p.Host)
	}
}

func TestProxySettings_KeyStable(t *testing.T) {
	p1 := NewProxySettings(ProxyHTTP, "proxy.example", 3128, "", false, "alice", "secret")
	p2 := NewProxySettings(ProxyHTTP, "proxy.example", 3128, "", false, "alice", "secret")
	if p1.Key() != p2.Key() {
		t.Errorf("equal settings produced unequal keys: %q vs %q", p1.Key(), p2.Key())
	}
	p3 := NewProxySettings(ProxyHTTP, "proxy.example", 3128, "", false, "alice", "different")
	if p1.Key() == p3.Key() {
		t.Error("differing password should produce different keys")
	}
}

func TestProxySettings_URL_HTTPWithCreds(t *testing.T) {
	p := NewProxySettings(ProxyHTTP, "proxy.example", 8080, "", false, "alice", "secret")
	u, err := p.URL()
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "http" || u.Host != "proxy.example:8080" {
		t.Errorf("URL = %q", u.String())
	}
	want := url.UserPassword("alice", "secret").String()
	if u.User.String() != want {
		t.Errorf("creds = %q want %q", u.User.String(), want)
	}
}

func TestProxySettings_URL_SOCKSReturnsErr(t *testing.T) {
	p := NewProxySettings(ProxySOCKS5, "127.0.0.1", 1080, "", false, "", "")
	_, err := p.URL()
	if !errors.Is(err, ErrUnsupportedProxyType) {
		t.Errorf("expected ErrUnsupportedProxyType, got %v", err)
	}
}

func TestProxySettings_BypassList_LeadingSemicolonForWildcard(t *testing.T) {
	p := NewProxySettings(ProxyHTTP, "proxy.example", 8080, "*.local, intranet.example, *direct", false, "", "")
	got := p.BypassList()
	want := []string{";*.local", "intranet.example", ";*direct"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] %q vs %q", i, got[i], want[i])
		}
	}
}

func TestStaticProxyProvider_BypassLocalhost(t *testing.T) {
	settings := NewProxySettings(ProxyHTTP, "proxy.example", 8080, "", true, "", "")
	prov := StaticProxyProvider{Settings: settings}
	cases := []struct {
		uri      string
		expected bool // true = bypassed (returns nil)
	}{
		{"https://localhost/x", true},
		{"https://127.0.0.1/x", true},
		{"http://internal.local/x", true},
		{"https://api.indexer.tld/x", false},
	}
	for _, tc := range cases {
		uri := MustParseURI(tc.uri)
		got := prov.GetProxySettings(uri)
		if (got == nil) != tc.expected {
			t.Errorf("uri %q: bypass=%v want=%v", tc.uri, got == nil, tc.expected)
		}
	}
}

func TestStaticProxyProvider_BypassFilterWildcard(t *testing.T) {
	settings := NewProxySettings(ProxyHTTP, "proxy.example", 8080, "*.indexer.tld", false, "", "")
	prov := StaticProxyProvider{Settings: settings}
	if prov.GetProxySettings(MustParseURI("https://api.indexer.tld/x")) != nil {
		t.Error("expected api.indexer.tld to be bypassed by *.indexer.tld")
	}
	if prov.GetProxySettings(MustParseURI("https://api.other.tld/x")) == nil {
		t.Error("api.other.tld should NOT be bypassed")
	}
}

func TestStaticProxyProvider_GetDefault_Unfiltered(t *testing.T) {
	s := NewProxySettings(ProxyHTTP, "proxy.example", 8080, "", true, "", "")
	prov := StaticProxyProvider{Settings: s}
	// Default is unfiltered even when host matches bypass.
	if prov.GetDefault() != s {
		t.Error("GetDefault should return the configured Settings unfiltered")
	}
}

func TestNoopProxyProvider(t *testing.T) {
	var p ProxyProvider = NoopProxyProvider{}
	if p.GetProxySettings(MustParseURI("https://x/")) != nil {
		t.Error("noop provider must return nil")
	}
	if p.GetDefault() != nil {
		t.Error("noop default must be nil")
	}
}

// End-to-end: dispatcher routes through a configured HTTP proxy.
func TestDispatcher_ProxyRoutes(t *testing.T) {
	// Stand up a "proxy" httptest.Server. When the dispatcher uses an HTTP
	// proxy it sends absolute-URL requests; we capture which target URL
	// arrived to confirm routing.
	var seen string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.String()
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()

	pURL, _ := url.Parse(proxy.URL)
	settings := NewProxySettings(ProxyHTTP, pURL.Hostname(), pickPort(t, pURL), "", false, "", "")
	d := NewDispatcherWithOptions(DispatcherOptions{
		UserAgent:     "Sonarr/test",
		ProxyProvider: StaticProxyProvider{Settings: settings},
	})
	req, _ := NewRequest("http://example.invalid/path?q=1")
	resp, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.ResponseData) != "via-proxy" {
		t.Errorf("body = %q (proxy not consulted?)", resp.ResponseData)
	}
	if !strings.Contains(seen, "example.invalid") {
		t.Errorf("proxy did not see the original target URL: %q", seen)
	}
}

// pickPort extracts the integer port from a URL with a non-empty Port.
func pickPort(t *testing.T, u *url.URL) int {
	t.Helper()
	port := u.Port()
	if port == "" {
		t.Fatal("URL has no port")
	}
	var n int
	for _, c := range port {
		n = n*10 + int(c-'0')
	}
	return n
}
