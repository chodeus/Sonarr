package host

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDisableCacheHeaders_AppliesFourHeaders(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	disableCacheHeaders(inner).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))

	checks := []struct{ header, want string }{
		{"Cache-Control", "no-cache, no-store"},
		{"Pragma", "no-cache"},
		{"Expires", "-1"},
		{"Vary", "Accept-Encoding"},
	}
	for _, c := range checks {
		if got := rr.Header().Get(c.header); got != c.want {
			t.Errorf("header %q: got %q want %q", c.header, got, c.want)
		}
	}
}

func TestDisableCacheHeaders_StripsLastModified(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Last-Modified", "Wed, 20 May 2026 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	disableCacheHeaders(inner).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if got := rr.Header().Get("Last-Modified"); got != "" {
		t.Errorf("Last-Modified should be stripped; got %q", got)
	}
}

func TestShouldSkipCacheHeaders(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		{"/api/v3/series", false},
		{"/api/v3/mediacover/1/poster.jpg", true},
		{"/api/v3/MediaCover/1/poster.jpg", true},
		{"/ping", false},
		{"/api/v3/system/status", false},
	}
	for _, tc := range cases {
		if got := shouldSkipCacheHeaders(tc.path); got != tc.want {
			t.Errorf("shouldSkipCacheHeaders(%q): got %v want %v", tc.path, got, tc.want)
		}
	}
}
