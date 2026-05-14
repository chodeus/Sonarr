package host

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeBundleFS builds a tiny in-memory frontend tree via
// testing/fstest.MapFS — fs.FS for the in-tree static handler.
//
//	root/index.html
//	root/static/app.js
//	root/calendar/index.html (for the dir-index test)
func fakeBundleFS() fs.FS {
	return fstest.MapFS{
		"index.html":          &fstest.MapFile{Data: []byte("<html>SPA-FS</html>")},
		"static/app.js":       &fstest.MapFile{Data: []byte("console.log('fs');")},
		"calendar/index.html": &fstest.MapFile{Data: []byte("CALENDAR-FS")},
	}
}

func TestStaticHandlerFS_ServesExistingFile(t *testing.T) {
	t.Parallel()
	h := staticHandlerFS(fakeBundleFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "console.log") {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestStaticHandlerFS_FallsBackToIndexForUnknownPath(t *testing.T) {
	t.Parallel()
	h := staticHandlerFS(fakeBundleFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/series/42", nil))
	if !strings.Contains(rr.Body.String(), "SPA-FS") {
		t.Errorf("SPA fallback failed: %q", rr.Body.String())
	}
}

func TestStaticHandlerFS_RootServesIndex(t *testing.T) {
	t.Parallel()
	h := staticHandlerFS(fakeBundleFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rr.Body.String(), "SPA-FS") {
		t.Errorf("root index: %q", rr.Body.String())
	}
}

func TestStaticHandlerFS_DirectoryServesItsIndex(t *testing.T) {
	t.Parallel()
	h := staticHandlerFS(fakeBundleFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/calendar", nil))
	if !strings.Contains(rr.Body.String(), "CALENDAR-FS") {
		t.Errorf("dir index: %q", rr.Body.String())
	}
}

func TestStaticHandlerFS_MissingIndexReturns404(t *testing.T) {
	t.Parallel()
	// Empty MapFS — SPA fallback can't find index.html.
	h := staticHandlerFS(fstest.MapFS{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing index.html should 404; got %d", rr.Code)
	}
}

// Note on traversal defense: net/http's URL parser cleans `/..`
// paths to `/` before the handler observes them, so a normal
// httptest request can't actually exercise the fs.ValidPath
// branch — the guard exists for defense-in-depth against any
// future path-routing changes upstream. We rely on the disk-variant
// traversal test (TestStaticHandler_TraversalAttackFallsBackToIndex)
// + the fs.ValidPath stdlib contract for the security guarantee.

func TestServer_WithStaticFS_RoutesAroundAPI(t *testing.T) {
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
		WithStaticFS(fakeBundleFS()),
	)
	mux := srv.Handler()

	// API route — must hit the API handler, NOT the SPA index.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if rr.Body.String() != "API" {
		t.Errorf("API route shadowed by static FS: %q", rr.Body.String())
	}

	// Unknown path — SPA fallback from the embedded FS.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/series", nil))
	if !strings.Contains(rr.Body.String(), "SPA-FS") {
		t.Errorf("SPA fallback (FS): %q", rr.Body.String())
	}
}

func TestServer_WithStaticDir_WinsOverStaticFS(t *testing.T) {
	t.Parallel()
	// When both are set, disk wins (the development-iteration
	// path takes priority over the embedded production fallback).
	root := staticBundle(t) // disk: emits "SPA"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(
		Config{}, logger,
		okConfigProbe{}, staticAPIKey("test-key"),
		WithStaticDir(root),
		WithStaticFS(fakeBundleFS()), // emits "SPA-FS"
	)
	mux := srv.Handler()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "SPA") || strings.Contains(body, "SPA-FS") {
		t.Errorf("disk should win over FS; got %q", body)
	}
}
