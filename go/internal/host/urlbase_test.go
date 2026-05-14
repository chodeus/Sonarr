package host

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormaliseURLBase(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", ""},
		{"/", ""},
		{"//", ""},
		{"sonarr", "/sonarr"},
		{"/sonarr", "/sonarr"},
		{"/sonarr/", "/sonarr"},
		{"sonarr/", "/sonarr"},
		{"a/b/c", "/a/b/c"},
	}
	for _, c := range cases {
		if got := normaliseURLBase(c.in); got != c.want {
			t.Errorf("normaliseURLBase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestServer_URLBase_RoutesUnderPrefix(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(
		Config{}, logger,
		okConfigProbe{}, staticAPIKey("test-key"),
		WithRoutes(Route{
			Method: "GET", Path: "/api/v3/series", Anonymous: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`API`))
			}),
		}),
		WithURLBase("/sonarr"),
	)
	mux := srv.Handler()

	// In-prefix → handler hit.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/sonarr/api/v3/series", nil))
	if rr.Body.String() != "API" {
		t.Errorf("in-prefix: got %q", rr.Body.String())
	}

	// /ping in-prefix → ping hit.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/sonarr/ping", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("in-prefix /sonarr/ping: status %d", rr.Code)
	}

	// Out-of-prefix → 404.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("out-of-prefix /api/v3/series should 404; got %d body=%q", rr.Code, rr.Body.String())
	}

	// Sibling-path-without-slash that LOOKS like the prefix but
	// isn't (e.g. /sonarrextra) → 404.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/sonarrextra/api/v3/series", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("/sonarrextra/... should 404; got %d", rr.Code)
	}
}

func TestServer_URLBase_BarePrefixServesRoot(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := staticBundle(t)
	srv := New(
		Config{}, logger,
		okConfigProbe{}, staticAPIKey("test-key"),
		WithStaticDir(root),
		WithURLBase("/sonarr"),
	)
	mux := srv.Handler()

	// /sonarr (no trailing slash) → SPA root index.html.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/sonarr", nil))
	if !strings.Contains(rr.Body.String(), "SPA") {
		t.Errorf("bare prefix should serve index.html; got %q", rr.Body.String())
	}

	// /sonarr/ → also SPA root.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/sonarr/", nil))
	if !strings.Contains(rr.Body.String(), "SPA") {
		t.Errorf("trailing-slash prefix should serve index.html; got %q", rr.Body.String())
	}

	// /sonarr/anything → SPA fallback.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/sonarr/series/42", nil))
	if !strings.Contains(rr.Body.String(), "SPA") {
		t.Errorf("SPA fallback under prefix: %q", rr.Body.String())
	}

	// /sonarr/static/app.js → real asset.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/sonarr/static/app.js", nil))
	if !strings.Contains(rr.Body.String(), "console.log") {
		t.Errorf("static asset under prefix: %q", rr.Body.String())
	}
}

func TestServer_URLBase_Empty_NoMiddleware(t *testing.T) {
	t.Parallel()
	// Empty URLBase → no wrapping → behaves identically to a server
	// constructed without the option.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(
		Config{}, logger,
		okConfigProbe{}, staticAPIKey("test-key"),
		WithURLBase(""),
		WithRoutes(Route{
			Method: "GET", Path: "/api/v3/series", Anonymous: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`API`))
			}),
		}),
	)
	mux := srv.Handler()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if rr.Body.String() != "API" {
		t.Errorf("empty URLBase should route at root: got %q", rr.Body.String())
	}
}
