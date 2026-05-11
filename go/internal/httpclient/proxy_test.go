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

func TestProxySettings_URL_SOCKS5ReturnsSchemedURL(t *testing.T) {
	// Slice 4.AA (queue [16]): SOCKS proxies now return a typed URL.
	// HTTP proxies use Transport.Proxy; SOCKS uses Transport.DialContext
	// via SOCKSDialer — URL() is informational + cache-key only.
	p := NewProxySettings(ProxySOCKS5, "127.0.0.1", 1080, "", false, "", "")
	u, err := p.URL()
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if u.Scheme != "socks5" {
		t.Errorf("scheme = %q, want socks5", u.Scheme)
	}
	if u.Host != "127.0.0.1:1080" {
		t.Errorf("host = %q, want 127.0.0.1:1080", u.Host)
	}
}

func TestProxySettings_URL_SOCKS4ReturnsSchemedURL(t *testing.T) {
	p := NewProxySettings(ProxySOCKS4, "10.0.0.1", 1080, "", false, "", "")
	u, err := p.URL()
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if u.Scheme != "socks4" {
		t.Errorf("scheme = %q, want socks4", u.Scheme)
	}
}

func TestProxySettings_IsSOCKS(t *testing.T) {
	if !NewProxySettings(ProxySOCKS5, "h", 1, "", false, "", "").IsSOCKS() {
		t.Error("SOCKS5 should report IsSOCKS=true")
	}
	if !NewProxySettings(ProxySOCKS4, "h", 1, "", false, "", "").IsSOCKS() {
		t.Error("SOCKS4 should report IsSOCKS=true")
	}
	if NewProxySettings(ProxyHTTP, "h", 1, "", false, "", "").IsSOCKS() {
		t.Error("HTTP should report IsSOCKS=false")
	}
}

func TestProxySettings_SOCKSDialer_ReturnsDialerForSOCKS(t *testing.T) {
	p := NewProxySettings(ProxySOCKS5, "127.0.0.1", 1080, "", false, "user", "pass")
	d, err := p.SOCKSDialer()
	if err != nil {
		t.Fatalf("SOCKSDialer: %v", err)
	}
	if d == nil {
		t.Fatal("nil dialer")
	}
}

func TestProxySettings_SOCKSDialer_RejectsHTTPProxy(t *testing.T) {
	p := NewProxySettings(ProxyHTTP, "h", 8080, "", false, "", "")
	_, err := p.SOCKSDialer()
	if !errors.Is(err, ErrUnsupportedProxyType) {
		t.Errorf("err = %v, want ErrUnsupportedProxyType", err)
	}
}

func TestStaticProxyProvider_BypassCIDR(t *testing.T) {
	// Slice 4.AA: CIDR bypass for literal IP hosts.
	settings := NewProxySettings(ProxyHTTP, "proxy.example", 8080, "10.0.0.0/8, 192.168.1.0/24", false, "", "")
	prov := StaticProxyProvider{Settings: settings}
	cases := []struct {
		uri      string
		bypassed bool
	}{
		{"http://10.0.0.5/x", true},      // matches 10.0.0.0/8
		{"http://10.255.255.1/x", true},  // matches 10.0.0.0/8
		{"http://192.168.1.50/x", true},  // matches 192.168.1.0/24
		{"http://192.168.2.50/x", false}, // outside 192.168.1.0/24
		{"http://172.16.0.1/x", false},   // outside both CIDRs
		{"http://example.com/x", false},  // hostname — no CIDR match (DNS not resolved here)
	}
	for _, tc := range cases {
		uri := MustParseURI(tc.uri)
		got := prov.GetProxySettings(uri)
		if (got == nil) != tc.bypassed {
			t.Errorf("uri %q: bypassed=%v want=%v", tc.uri, got == nil, tc.bypassed)
		}
	}
}

func TestStaticProxyProvider_BypassCIDRDoesNotResolveDNS(t *testing.T) {
	// Explicit guard: a CIDR pattern + a hostname should NOT trigger
	// a DNS lookup at bypass-decision time. Slice 4.AA design note.
	settings := NewProxySettings(ProxyHTTP, "proxy.example", 8080, "127.0.0.0/8", false, "", "")
	prov := StaticProxyProvider{Settings: settings}
	got := prov.GetProxySettings(MustParseURI("http://localhost/x"))
	if got == nil {
		t.Error("localhost (hostname, not IP) should NOT be bypassed by 127.0.0.0/8 CIDR — DNS not resolved")
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
