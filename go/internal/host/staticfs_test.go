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

// ---- J206 — SPA fallback exclusion tests ----

func TestStaticHandlerFS_J206_APIRoutesGet404OnMiss(t *testing.T) {
	t.Parallel()
	h := staticHandlerFS(fakeBundleFS())
	cases := []string{
		"/api/v3/series",       // typical V3 endpoint
		"/api/v3/seasonpass",   // unimplemented route — should 404 not HTML
		"/api/v5/auth",         // unimplemented V5 endpoint
		"/signalr/messages",    // SignalR hub miss
		"/signalr/negotiate",   // SignalR negotiate miss
		"/content/missing.css", // missing /content asset
		"/mediacover/1/poster.jpg",
		"/login/oauth-callback",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
			if rr.Code != http.StatusNotFound {
				t.Errorf("%s: status=%d, want 404 (got body %q)", p, rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "SPA-FS") {
				t.Errorf("%s: leaked SPA HTML", p)
			}
		})
	}
}

func TestStaticHandlerFS_J206_AssetExtensionGet404OnMiss(t *testing.T) {
	t.Parallel()
	h := staticHandlerFS(fakeBundleFS())
	// Missing file paths with a `.` (asset-extension heuristic) should
	// 404 rather than SPA-fallback. C# uses the same `Contains('.')`
	// rule at IndexHtmlMapper.cs:41.
	cases := []string{
		"/favicon.ico",
		"/missing-image.png",
		"/manifest.json",
		"/static/missing.js",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
			if rr.Code != http.StatusNotFound {
				t.Errorf("%s: status=%d, want 404", p, rr.Code)
			}
		})
	}
}

func TestStaticHandlerFS_J206_ExistingDottedFileStillServed(t *testing.T) {
	t.Parallel()
	// Critical guard: an existing file with `.` in its path must STILL
	// be served. The J206 exclusion only applies on the miss path; the
	// fs.Stat success branch wins for actually-present assets.
	h := staticHandlerFS(fakeBundleFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("existing /static/app.js: status=%d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "console.log") {
		t.Errorf("existing /static/app.js: body lost — got %q", rr.Body.String())
	}
}

func TestStaticHandlerFS_J206_SPARouteStillFallsBack(t *testing.T) {
	t.Parallel()
	// React Router client-side routes like /series/42 must STILL get
	// the SPA index.html so the bundle can take over. The exclusion
	// list only excludes API/asset prefixes — bare extensionless paths
	// remain SPA-eligible.
	// Note: /calendar maps to an actual directory with index.html in
	// the fake bundle FS, so it serves CALENDAR-FS via the dir-index
	// branch — a separate path from the SPA fallback. Excluded here.
	h := staticHandlerFS(fakeBundleFS())
	cases := []string{
		"/series/42",
		"/wanted/missing",
		"/system/tasks",
		"/settings/general",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
			if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "SPA-FS") {
				t.Errorf("%s: status=%d body=%q — should SPA-fallback", p, rr.Code, rr.Body.String())
			}
		})
	}
}
