package httpclient

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
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

// URL returns a stdlib *url.URL form suitable for net/http transports.
// Returns an error for SOCKS proxies — those need golang.org/x/net/proxy
// which isn't wired in this slice. Flagged as a follow-up at the dispatcher
// (sub-arc 1c only handles HTTP/HTTPS proxies).
func (p *ProxySettings) URL() (*url.URL, error) {
	if p == nil {
		return nil, ErrNoProxy
	}
	switch p.Type {
	case ProxyHTTP:
	case ProxySOCKS4, ProxySOCKS5:
		return nil, ErrUnsupportedProxyType
	default:
		return nil, fmt.Errorf("unknown proxy type: %s", p.Type)
	}
	scheme := "http"
	u := &url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", p.Host, p.Port),
	}
	if p.Username != "" || p.Password != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u, nil
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

// isBypassedHost applies BypassLocalAddress + BypassFilter wildcards
// (`*.host`). Designed to match the C# bypass behaviour at
// HttpProxySettingsProvider.ShouldProxyBeBypassed (analysis card
// http-instrumentation-misc.md §1, HTTP-Q3 — CIDR support is NOT included
// in this slice; flagged as follow-up).
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
	for _, pattern := range p.BypassList() {
		// The C# parser injects a leading `;` for `*.host` entries. Strip
		// it before glob matching.
		pattern = strings.TrimPrefix(pattern, ";")
		if matchHostGlob(pattern, host) {
			return true
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

// ErrUnsupportedProxyType is returned by ProxySettings.URL() for SOCKS
// types until the dispatcher is wired to golang.org/x/net/proxy.
var ErrUnsupportedProxyType = errors.New("httpclient: SOCKS proxy not yet supported (sub-arc 1c handles HTTP/HTTPS only)")
