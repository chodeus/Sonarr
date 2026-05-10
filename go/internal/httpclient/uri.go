// Package httpclient is the Go port of NzbDrone.Common.Http.
//
// C# cite: src/NzbDrone.Common/Http/HttpUri.cs:1-278.
//
// Sonarr's HttpUri wraps System.Uri with a custom relative-merge operator
// that does NOT follow RFC 3986 strictly: when the right-hand side has only
// a path, the result keeps the left-hand scheme/host/port and merges the
// path via CombineRelativePath (LastIndexOf('/') split). The Go port mirrors
// that behaviour exactly so indexers/import-list templates that rely on it
// produce the same URLs as the C# build.
package httpclient

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// URI is the Sonarr-flavoured URL value. It wraps net/url.URL but exposes
// the C# HttpUri surface (Scheme/Host/Port/Path/Query/Fragment getters,
// CombinePath, SetQuery, AddQueryParam, AddQueryParams, and Join which
// ports the `operator +` semantics from HttpUri.cs:258-276).
//
// The struct is immutable: every mutating helper returns a fresh URI.
type URI struct {
	full string
	u    *url.URL
}

// ParseURI parses a string into a URI. Mirrors the C# ctor at HttpUri.cs:18.
// Empty input yields an empty URI (no error) — same as the C# `_uri ?? string.Empty`.
func ParseURI(s string) (URI, error) {
	if s == "" {
		return URI{}, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return URI{}, fmt.Errorf("uri didn't match expected pattern: %s: %w", s, err)
	}
	return URI{full: s, u: u}, nil
}

// MustParseURI is the panic variant for compile-time-known URLs (factory
// builders, tests). Do NOT use on user input.
func MustParseURI(s string) URI {
	uri, err := ParseURI(s)
	if err != nil {
		panic(err)
	}
	return uri
}

// String returns the full URI text. Empty for the zero value.
func (u URI) String() string { return u.full }

// IsZero reports whether the URI is the zero value.
func (u URI) IsZero() bool { return u.full == "" }

// Scheme returns the URI scheme (e.g. "https") without the trailing colon.
func (u URI) Scheme() string {
	if u.u == nil {
		return ""
	}
	return u.u.Scheme
}

// Host returns the bare host (no port).
func (u URI) Host() string {
	if u.u == nil {
		return ""
	}
	return u.u.Hostname()
}

// Port returns the explicit port if present, or 0 if the URI did not
// specify one. Mirrors the C# `int? Port` semantics by returning (0,false).
func (u URI) Port() (int, bool) {
	if u.u == nil {
		return 0, false
	}
	p := u.u.Port()
	if p == "" {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(p, "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

// Path returns the URI path component (no query, no fragment).
func (u URI) Path() string {
	if u.u == nil {
		return ""
	}
	return u.u.EscapedPath()
}

// Query returns the raw query string (no leading '?').
func (u URI) Query() string {
	if u.u == nil {
		return ""
	}
	return u.u.RawQuery
}

// Fragment returns the fragment (no leading '#'). The Go url package
// decodes fragments by default; for parity with C# `match.Groups["fragment"]`
// (raw text post-`#`) we prefer RawFragment when set, falling back to the
// decoded form.
func (u URI) Fragment() string {
	if u.u == nil {
		return ""
	}
	if u.u.RawFragment != "" {
		return u.u.RawFragment
	}
	return u.u.Fragment
}

// CombinePath returns a new URI with the relative path appended to the
// existing path component. Mirrors HttpUri.cs:138-156: trims a trailing
// slash from the base, leading slash from the relative, joins with "/".
func (u URI) CombinePath(rel string) URI {
	return rebuild(u.Scheme(), u.Host(), portOpt(u), CombinePath(u.Path(), rel), u.Query(), u.Fragment())
}

// CombinePath ports the static helper at HttpUri.cs:143-156.
func CombinePath(basePath, relativePath string) string {
	if strings.TrimSpace(relativePath) == "" {
		return basePath
	}
	if strings.TrimSpace(basePath) == "" {
		return relativePath
	}
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(relativePath, "/")
}

// SetQuery returns a fresh URI with the query replaced. Empty string clears
// the query. Mirrors HttpUri.cs:180-183.
func (u URI) SetQuery(q string) URI {
	return rebuild(u.Scheme(), u.Host(), portOpt(u), u.Path(), q, u.Fragment())
}

// AddQueryParam returns a fresh URI with `key=value` appended to the query.
// Both sides are URL-escaped via url.QueryEscape, mirroring the C# Uri.EscapeDataString
// semantics for query parameters (HttpUri.cs:185-195).
func (u URI) AddQueryParam(key string, value any) URI {
	pair := url.QueryEscape(key) + "=" + url.QueryEscape(fmt.Sprint(value))
	q := u.Query()
	if q != "" {
		pair = q + "&" + pair
	}
	return u.SetQuery(pair)
}

// AddQueryParams returns a fresh URI with multiple key/value pairs appended.
// Mirrors HttpUri.cs:197-215. Order is preserved.
func (u URI) AddQueryParams(pairs []QueryPair) URI {
	var b strings.Builder
	b.WriteString(u.Query())
	for _, p := range pairs {
		if b.Len() != 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.Key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.Value))
	}
	return u.SetQuery(b.String())
}

// QueryPair is an ordered key/value used by AddQueryParams.
type QueryPair struct {
	Key   string
	Value string
}

// Join ports the `operator +` from HttpUri.cs:258-276.
//
// The semantics differ from RFC 3986 / *url.URL.ResolveReference:
//   - relative.Scheme set       → return relative as-is
//   - relative.Host set         → keep base.Scheme; everything else from relative
//   - relative.Path set         → keep base.Scheme/Host/Port; combine paths via
//     CombineRelativePath (LastIndexOf('/') split)
//   - else                      → keep base.Scheme/Host/Port/Path; replace
//     query+fragment from relative
func (u URI) Join(rel URI) URI {
	if rel.Scheme() != "" {
		return rel
	}
	if rel.Host() != "" {
		return rebuild(u.Scheme(), rel.Host(), portOpt(rel), rel.Path(), rel.Query(), rel.Fragment())
	}
	if strings.TrimSpace(rel.Path()) != "" {
		return rebuild(u.Scheme(), u.Host(), portOpt(u), combineRelativePath(u.Path(), rel.Path()), rel.Query(), rel.Fragment())
	}
	return rebuild(u.Scheme(), u.Host(), portOpt(u), u.Path(), rel.Query(), rel.Fragment())
}

// combineRelativePath ports HttpUri.cs:158-178. Note: this is NOT the public
// CombinePath. The relative-merge path uses LastIndexOf('/') so a base of
// "/a/b/c" merges with "x" to "/a/b/x" (not "/a/b/c/x").
func combineRelativePath(basePath, relativePath string) string {
	if strings.TrimSpace(relativePath) == "" {
		return basePath
	}
	if strings.HasPrefix(relativePath, "/") {
		return relativePath
	}
	idx := strings.LastIndex(basePath, "/")
	if idx >= 0 {
		return basePath[:idx] + "/" + relativePath
	}
	return relativePath
}

// rebuild assembles a URI from parts, mirroring HttpUri.cs:23-69.
func rebuild(scheme, host string, port portOption, path, query, fragment string) URI {
	var b strings.Builder
	if strings.TrimSpace(scheme) != "" {
		b.WriteString(scheme)
		b.WriteByte(':')
	}
	if strings.TrimSpace(host) != "" {
		b.WriteString("//")
		b.WriteString(host)
		if port.set {
			fmt.Fprintf(&b, ":%d", port.v)
		}
	}
	if strings.TrimSpace(path) != "" {
		if strings.TrimSpace(host) != "" || strings.HasPrefix(path, "/") {
			b.WriteByte('/')
		}
		b.WriteString(strings.TrimLeft(path, "/"))
	}
	if strings.TrimSpace(query) != "" {
		b.WriteByte('?')
		b.WriteString(query)
	}
	if strings.TrimSpace(fragment) != "" {
		b.WriteByte('#')
		b.WriteString(fragment)
	}
	out, err := ParseURI(b.String())
	if err != nil {
		// Construction-time guarantee: rebuild only ever combines parts
		// already extracted from a parsed URI, so a parse failure here
		// indicates a programmer bug. Surface it loudly.
		panic(err)
	}
	return out
}

type portOption struct {
	v   int
	set bool
}

func portOpt(u URI) portOption {
	if v, ok := u.Port(); ok {
		return portOption{v: v, set: true}
	}
	return portOption{}
}

// Equal reports whether two URIs have the same full text. Mirrors
// HttpUri.Equals(HttpUri) at HttpUri.cs:243-251 — string compare, not
// normalised parse compare.
func (u URI) Equal(other URI) bool { return u.full == other.full }

// ErrEmptyURI is returned by helpers that demand a non-empty URI.
var ErrEmptyURI = errors.New("httpclient: empty URI")
