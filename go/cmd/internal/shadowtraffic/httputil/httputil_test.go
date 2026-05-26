package httputil

import "testing"

func TestSingleJoin(t *testing.T) {
	cases := []struct {
		name, base, req, want string
	}{
		{"empty base", "", "/api/v3", "/api/v3"},
		{"slash base", "/", "/api/v3", "/api/v3"},
		{"prefix no trailing slash", "/prefix", "/api/v3", "/prefix/api/v3"},
		{"prefix trailing slash + req leading slash", "/prefix/", "/api/v3", "/prefix/api/v3"},
		{"prefix trailing slash + req no leading slash", "/prefix/", "api/v3", "/prefix/api/v3"},
		{"empty req", "/prefix", "", "/prefix"},
		{"both empty", "", "", ""},
		{"slash base + empty req", "/", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SingleJoin(tc.base, tc.req); got != tc.want {
				t.Errorf("SingleJoin(%q, %q) = %q, want %q", tc.base, tc.req, got, tc.want)
			}
		})
	}
}

func TestIsHopHeader(t *testing.T) {
	hop := []string{
		"Connection",
		"connection",
		"CONNECTION",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
	}
	for _, h := range hop {
		t.Run("hop/"+h, func(t *testing.T) {
			if !IsHopHeader(h) {
				t.Errorf("IsHopHeader(%q) = false, want true", h)
			}
		})
	}

	notHop := []string{
		"Content-Type",
		"Content-Length",
		"X-Api-Key",
		"Authorization",
		"X-Custom",
		"Host",
		"",
		"X-Connection-Hint", // not exactly "Connection"
	}
	for _, h := range notHop {
		t.Run("not-hop/"+h, func(t *testing.T) {
			if IsHopHeader(h) {
				t.Errorf("IsHopHeader(%q) = true, want false", h)
			}
		})
	}
}
