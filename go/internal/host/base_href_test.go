package host

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInjectBaseHref_EmptyURLBaseIsPassThrough(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><base href="/"></head></html>`))
	})
	rr := httptest.NewRecorder()
	injectBaseHref(inner, "").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rr.Body.String(), `<base href="/">`) {
		t.Errorf("empty urlBase should pass through unchanged; body=%q", rr.Body.String())
	}
}

func TestInjectBaseHref_RewritesExistingTag(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><base href="/">other</head></html>`))
	})
	rr := httptest.NewRecorder()
	injectBaseHref(inner, "/sonarr").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/sonarr/", nil))
	want := `<base href="/sonarr/">`
	if !strings.Contains(rr.Body.String(), want) {
		t.Errorf("rewrite missing; body=%q", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `<base href="/">`) {
		t.Errorf("old base href should be replaced; body=%q", rr.Body.String())
	}
}

func TestInjectBaseHref_InjectsWhenAbsent(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Sonarr</title></head><body></body></html>`))
	})
	rr := httptest.NewRecorder()
	injectBaseHref(inner, "sonarr").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rr.Body.String(), `<head><base href="/sonarr/">`) {
		t.Errorf("base href should be injected immediately after <head>; body=%q", rr.Body.String())
	}
}

func TestInjectBaseHref_PrependsWhenNoHead(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<body>no head</body>`))
	})
	rr := httptest.NewRecorder()
	injectBaseHref(inner, "/sonarr/").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.HasPrefix(rr.Body.String(), `<base href="/sonarr/">`) {
		t.Errorf("base href should be prepended; body=%q", rr.Body.String())
	}
}

func TestInjectBaseHref_NormalisesURLBase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		urlBase string
		want    string
	}{
		{"sonarr", `<base href="/sonarr/">`},
		{"/sonarr", `<base href="/sonarr/">`},
		{"/sonarr/", `<base href="/sonarr/">`},
		{"////sonarr////", `<base href="/sonarr/">`},
		{"app/sonarr", `<base href="/app/sonarr/">`},
	}
	for _, tc := range cases {
		t.Run(tc.urlBase, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<head></head>`))
			})
			rr := httptest.NewRecorder()
			injectBaseHref(inner, tc.urlBase).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
			if !strings.Contains(rr.Body.String(), tc.want) {
				t.Errorf("urlBase %q: want %q in body; got %q", tc.urlBase, tc.want, rr.Body.String())
			}
		})
	}
}

func TestInjectBaseHref_NonHTMLIsPassthrough(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(`console.log("<base href=ignore-me>");`))
	})
	rr := httptest.NewRecorder()
	injectBaseHref(inner, "/sonarr").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if !strings.Contains(rr.Body.String(), `ignore-me`) {
		t.Errorf("JS body should pass through unchanged; got %q", rr.Body.String())
	}
}

func TestInjectBaseHref_StripsStaleContentLength(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", "5") // intentionally wrong post-rewrite
		_, _ = w.Write([]byte(`<head></head>`))
	})
	rr := httptest.NewRecorder()
	injectBaseHref(inner, "/sonarr").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Header().Get("Content-Length") != "" {
		t.Errorf("stale Content-Length should be stripped; got %q", rr.Header().Get("Content-Length"))
	}
}

func TestInjectBaseHref_RootURLBaseIsPassthrough(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<head></head>`))
	})
	rr := httptest.NewRecorder()
	// Just "/" → empty URL base after trim → no injection.
	injectBaseHref(inner, "/").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(rr.Body.String(), `<base`) {
		t.Errorf(`"/" should not inject; got %q`, rr.Body.String())
	}
}

func TestIsIndexPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/index.html", true},
		{"/sonarr/index.html", true},
		{"/sonarr", false},
		{"/series/123", false},
		{"/static/app.js", false},
	}
	for _, tc := range cases {
		if got := IsIndexPath(tc.path); got != tc.want {
			t.Errorf("IsIndexPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
