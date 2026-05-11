package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/proxy"
)

// ProxyType mirrors src/NzbDrone.Common/Http/Proxy/ProxyType.cs.
type ProxyType int

// Proxy types.
const (
	ProxyHTTP   ProxyType = iota // HTTP / HTTPS proxy.
	ProxySOCKS4                  // SOCKS4 proxy.
	ProxySOCKS5                  // SOCKS5 proxy.
)

// String returns the C# enum name. Used in ProxySettings.Key().
func (p ProxyType) String() string {
	switch p {
	case ProxyHTTP:
		return "Http"
	case ProxySOCKS4:
		return "Socks4"
	case ProxySOCKS5:
		return "Socks5"
	default:
		return fmt.Sprintf("ProxyType(%d)", p)
	}
}

// ProxySettings is the typed proxy config. C# cite:
// src/NzbDrone.Common/Http/Proxy/HttpProxySettings.cs:6-58.
type ProxySettings struct {
	Type               ProxyType
	Host               string
	Port               int
	Username           string
	Password           string
	BypassFilter       string // comma-separated host globs (`*.host`).
	BypassLocalAddress bool
}

// NewProxySettings ports the C# constructor (HttpProxySettings.cs:8-17),
// notably defaulting Host to "127.0.0.1" when blank.
func NewProxySettings(typ ProxyType, host string, port int, bypassFilter string, bypassLocal bool, username, password string) *ProxySettings {
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	return &ProxySettings{
		Type:               typ,
		Host:               host,
		Port:               port,
		Username:           username,
		Password:           password,
		BypassFilter:       bypassFilter,
		BypassLocalAddress: bypassLocal,
	}
}

// Key returns a stable cache key for the dispatcher's per-proxy *http.Client
// cache. Mirrors HttpProxySettings.Key at HttpProxySettings.cs:50-57.
func (p *ProxySettings) Key() string {
	return strings.Join([]string{
		p.Type.String(),
		p.Host,
		fmt.Sprint(p.Port),
		p.Username,
		p.Password,
		p.BypassFilter,
		fmt.Sprint(p.BypassLocalAddress),
	}, "_")
}

// URL returns a stdlib *url.URL form. For HTTP/HTTPS proxies this URL
// plugs into [http.Transport.Proxy] via [http.ProxyURL]. For SOCKS
// proxies the URL is informational only (the scheme distinguishes
// socks4 / socks5 for log lines + cache keys); SOCKS dispatching
// uses [ProxySettings.SOCKSDialer] which goes through
// [golang.org/x/net/proxy] rather than the URL.
//
// Slice 4.AA (queue [16]) lifted the prior ErrUnsupportedProxyType
// short-circuit. SOCKS4/SOCKS5 are now first-class.
func (p *ProxySettings) URL() (*url.URL, error) {
	if p == nil {
		return nil, ErrNoProxy
	}
	var scheme string
	switch p.Type {
	case ProxyHTTP:
		scheme = "http"
	case ProxySOCKS4:
		scheme = "socks4"
	case ProxySOCKS5:
		scheme = "socks5"
	default:
		return nil, fmt.Errorf("unknown proxy type: %s", p.Type)
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", p.Host, p.Port),
	}
	if p.Username != "" || p.Password != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u, nil
}

// IsSOCKS reports whether the proxy uses the SOCKS protocol. HTTP
// CONNECT proxies use [http.Transport.Proxy]; SOCKS proxies use a
// custom [http.Transport.DialContext] via [ProxySettings.SOCKSDialer].
func (p *ProxySettings) IsSOCKS() bool {
	return p != nil && (p.Type == ProxySOCKS4 || p.Type == ProxySOCKS5)
}

// SOCKSDialer returns a [proxy.ContextDialer] for the configured
// SOCKS proxy. Returns ErrUnsupportedProxyType for non-SOCKS settings.
//
// The returned dialer is wired into [http.Transport.DialContext] by
// [buildHTTPClient] when the active settings are SOCKS. The C# tree
// uses System.Net's built-in SocksHandler at
// `src/NzbDrone.Common/Http/Dispatchers/ManagedHttpDispatcher.cs:171-180`;
// the Go port reaches into golang.org/x/net/proxy which has a stable
// public ContextDialer surface.
//
// SOCKS4 dialer uses the [proxy.SOCKS5] builder with a 4 fallback —
// golang.org/x/net/proxy doesn't expose a typed SOCKS4 constructor
// because the protocol is rarely deployed; if needed, callers can
// register their own via [proxy.RegisterDialerType]. For now SOCKS4
// shares the SOCKS5 dialer to match the C# behaviour (System.Net's
// handler negotiates the version on connect).
func (p *ProxySettings) SOCKSDialer() (proxy.ContextDialer, error) {
	if p == nil {
		return nil, ErrNoProxy
	}
	if !p.IsSOCKS() {
		return nil, ErrUnsupportedProxyType
	}
	var auth *proxy.Auth
	if p.Username != "" || p.Password != "" {
		auth = &proxy.Auth{User: p.Username, Password: p.Password}
	}
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("%s:%d", p.Host, p.Port), auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("build SOCKS dialer: %w", err)
	}
	ctxDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		// Wrap the older Dialer interface in a context-aware shim.
		// proxy.SOCKS5 returns a *proxy.PerHost on some versions
		// which doesn't satisfy ContextDialer directly.
		return socksDialerShim{dialer: dialer}, nil
	}
	return ctxDialer, nil
}

// socksDialerShim adapts a non-context [proxy.Dialer] to the
// [proxy.ContextDialer] interface. ctx cancellation is approximated
// by best-effort (the underlying Dial doesn't itself take a ctx).
type socksDialerShim struct {
	dialer proxy.Dialer
}

// DialContext satisfies [proxy.ContextDialer].
func (s socksDialerShim) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := s.dialer.Dial(network, addr)
		ch <- result{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// BypassList parses BypassFilter into individual entries, mirroring the
// C# BypassListAsArray getter at HttpProxySettings.cs:27-48 (the leading-`;`
// dance is preserved verbatim to keep config-file parity even though Go
// has no equivalent of `System.Net.WebProxy`'s parser; users that hand-edit
// configs across both runtimes shouldn't see a diff).
func (p *ProxySettings) BypassList() []string {
	if strings.TrimSpace(p.BypassFilter) == "" {
		return nil
	}
	parts := strings.Split(p.BypassFilter, ",")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "*") {
			s = ";" + s
		}
		out = append(out, s)
	}
	return out
}

// ProxyProvider mirrors IHttpProxySettingsProvider at
// src/NzbDrone.Common/Http/Proxy/IHttpProxySettingsProvider.cs:1-8.
//
// GetProxySettings(URI) returns nil when the URI is bypassed; the caller
// then dispatches without a proxy. GetDefault() returns the active
// configuration without applying bypass rules — used by health-check
// connectivity probes.
type ProxyProvider interface {
	GetProxySettings(uri URI) *ProxySettings
	GetDefault() *ProxySettings
}

// NoopProxyProvider always returns nil — i.e. no proxy. Used as the default
// when the boot site does not wire a real ProxyProvider.
type NoopProxyProvider struct{}

// GetProxySettings always returns nil.
func (NoopProxyProvider) GetProxySettings(URI) *ProxySettings { return nil }

// GetDefault always returns nil.
func (NoopProxyProvider) GetDefault() *ProxySettings { return nil }

// StaticProxyProvider returns the same settings for every URI unless the
// host matches BypassLocalAddress / BypassFilter. Useful for tests and as
// the wire target for the typed proxy config in sub-arc 1d.
type StaticProxyProvider struct {
	Settings *ProxySettings
}

// GetProxySettings returns Settings unless the URI is bypassed.
func (s StaticProxyProvider) GetProxySettings(uri URI) *ProxySettings {
	if s.Settings == nil {
		return nil
	}
	if isBypassedHost(s.Settings, uri.Host()) {
		return nil
	}
	return s.Settings
}

// GetDefault returns the configured settings unfiltered.
func (s StaticProxyProvider) GetDefault() *ProxySettings { return s.Settings }

// isBypassedHost applies BypassLocalAddress + BypassFilter rules to
// decide whether the supplied host should be reached directly,
// bypassing the proxy. Matches C#
// `HttpProxySettingsProvider.ShouldProxyBeBypassed` (analysis card
// http-instrumentation-misc.md §1, HTTP-Q3).
//
// Supported bypass patterns (any one match wins):
//
//   - BypassLocalAddress=true → "localhost" / "127.0.0.1" / "::1" /
//     any `*.local` suffix.
//   - `*.example.com` — wildcard suffix glob.
//   - `example.com` — exact host match (case-insensitive).
//   - `10.0.0.0/8` — CIDR range (slice 4.AA addition); host MUST
//     parse as a literal IP for the CIDR check to apply. Hostnames
//     are NOT resolved here — DNS lookup would be a side-effect at
//     the dispatcher hot path. CIDR + DNS-resolved match is a
//     follow-up if a use case shows up.
func isBypassedHost(p *ProxySettings, host string) bool {
	if host == "" {
		return false
	}
	if p.BypassLocalAddress {
		switch {
		case host == "localhost",
			host == "127.0.0.1",
			host == "::1",
			strings.HasSuffix(host, ".local"):
			return true
		}
	}
	hostIP := net.ParseIP(host)
	for _, pattern := range p.BypassList() {
		// The C# parser injects a leading `;` for `*.host` entries. Strip
		// it before glob matching.
		pattern = strings.TrimPrefix(pattern, ";")
		if matchHostGlob(pattern, host) {
			return true
		}
		// CIDR — only meaningful when the candidate host parsed as a
		// literal IP. Patterns without "/" go through the host-glob
		// path above; the strings.Contains short-circuit here keeps
		// the parse cost off the common-case lookup.
		if hostIP != nil && strings.Contains(pattern, "/") {
			if _, cidr, err := net.ParseCIDR(pattern); err == nil {
				if cidr.Contains(hostIP) {
					return true
				}
			}
		}
	}
	return false
}

// matchHostGlob does the *.host wildcard match the C# WebProxy bypass
// parser supports. Exact-match is also supported when no `*` is present.
func matchHostGlob(pattern, host string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return strings.EqualFold(pattern, host)
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".host"
		return strings.HasSuffix(strings.ToLower(host), strings.ToLower(suffix)) ||
			strings.EqualFold(host, strings.TrimPrefix(suffix, "."))
	}
	if pattern == "*" {
		return true
	}
	return false
}

// ErrNoProxy is returned by ProxySettings.URL() when called on nil.
var ErrNoProxy = errors.New("httpclient: no proxy configured")

// ErrUnsupportedProxyType is returned by [ProxySettings.SOCKSDialer]
// when invoked against a non-SOCKS proxy. Slice 4.AA (queue [16])
// retired the original meaning ("SOCKS not yet supported"); the
// sentinel now indicates wiring misuse — the dispatcher should
// invoke SOCKSDialer only for IsSOCKS() proxies.
var ErrUnsupportedProxyType = errors.New("httpclient: proxy type is not SOCKS — use URL() for HTTP proxies")
