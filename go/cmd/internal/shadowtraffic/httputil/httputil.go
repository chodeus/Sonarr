// Package httputil holds tiny HTTP helpers shared across the
// cmd/shadow-traffic-{diff,replay,record} binaries.
//
// These helpers were originally copy-pasted across all three binaries
// (each cmd kept its own singleJoin + isHopHeader). J243 consolidated
// them here. The package lives under cmd/internal/ (rather than the
// natural-looking cmd/shadowtraffic/internal/) so Go's internal-import
// rule permits use from any sibling cmd/* binary — cmd/shadowtraffic/
// has no binaries of its own.
package httputil

import "net/http"

// SingleJoin joins a base URL path with the request path, collapsing
// the duplicated leading slash that would otherwise result from
// concatenation when base ends with "/" and request starts with "/".
//
// Examples:
//
//	SingleJoin("", "/api/v3")        → "/api/v3"
//	SingleJoin("/", "/api/v3")       → "/api/v3"
//	SingleJoin("/prefix", "/api/v3") → "/prefix/api/v3"
//	SingleJoin("/prefix/", "/api/v3")→ "/prefix/api/v3"
//	SingleJoin("/prefix/", "api/v3") → "/prefix/api/v3"
func SingleJoin(base, req string) string {
	if base == "" || base == "/" {
		return req
	}
	if base[len(base)-1] == '/' && len(req) > 0 && req[0] == '/' {
		return base + req[1:]
	}
	return base + req
}

// IsHopHeader reports whether h is one of the hop-by-hop header names
// defined by RFC 7230 §6.1 — these MUST NOT be forwarded by a proxy.
// The check is case-insensitive (CanonicalHeaderKey is applied first).
func IsHopHeader(h string) bool {
	switch http.CanonicalHeaderKey(h) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailers", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}
