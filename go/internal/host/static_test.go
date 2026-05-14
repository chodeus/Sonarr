package host

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// staticBundle builds a tiny fake frontend tree in a tmp dir:
//
//	root/index.html  ← SPA shell
//	root/static/app.js
//
// Returned root path is t.TempDir() — auto-cleaned.
func staticBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>SPA</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "static", "app.js"), []byte("console.log('hi');"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStaticHandler_ServesExistingFile(t *testing.T) {
	t.Parallel()
	root := staticBundle(t)
	h, err := staticHandler(root)
	if err != nil {
		t.Fatalf("staticHandler: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "console.log") {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestStaticHandler_FallsBackToIndexForUnknownPath(t *testing.T) {
	t.Parallel()
	root := staticBundle(t)
	h, _ := staticHandler(root)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/series/42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "SPA") {
		t.Errorf("expected SPA shell for /series/42, got: %q", rr.Body.String())
	}
}

func TestStaticHandler_RootServesIndex(t *testing.T) {
	t.Parallel()
	root := staticBundle(t)
	h, _ := staticHandler(root)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rr.Body.String(), "SPA") {
		t.Errorf("root should serve index.html: %q", rr.Body.String())
	}
}

func TestStaticHandler_DirectoryServesItsIndex(t *testing.T) {
	t.Parallel()
	root := staticBundle(t)
	// Add a subdir with its own index.html.
	subdir := filepath.Join(root, "calendar")
	_ = os.MkdirAll(subdir, 0o755)
	_ = os.WriteFile(filepath.Join(subdir, "index.html"), []byte("CALENDAR"), 0o644)

	h, _ := staticHandler(root)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/calendar", nil))
	if !strings.Contains(rr.Body.String(), "CALENDAR") {
		t.Errorf("dir index: %q", rr.Body.String())
	}
}

func TestStaticHandler_TraversalAttackFallsBackToIndex(t *testing.T) {
	t.Parallel()
	root := staticBundle(t)
	// Sibling-of-root file that the traversal would reach if the
	// guard fails.
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	_ = os.WriteFile(secret, []byte("SHOULD-NEVER-LEAK"), 0o600)
	t.Cleanup(func() { _ = os.Remove(secret) })

	h, _ := staticHandler(root)
	rr := httptest.NewRecorder()
	// Path-traversal — http.Request resolves "/../" to "/" before
	// the handler sees it, so this exercises both the request-line
	// cleaning AND our defense.
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/../secret.txt", nil))
	if strings.Contains(rr.Body.String(), "SHOULD-NEVER-LEAK") {
		t.Errorf("path traversal leaked secret: %q", rr.Body.String())
	}
}

func TestStaticHandler_MissingIndexReturns404(t *testing.T) {
	t.Parallel()
	// Empty dir → SPA fallback can't find index.html → 404.
	root := t.TempDir()
	h, _ := staticHandler(root)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing index.html should 404, got %d", rr.Code)
	}
}

func TestServer_WithStaticDir_DoesNotShadowAPIRoutes(t *testing.T) {
	t.Parallel()
	root := staticBundle(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(
		Config{},
		logger,
		okConfigProbe{},
		staticAPIKey("test-key"),
		WithRoutes(Route{
			Method: "GET", Path: "/api/v3/series", Anonymous: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`API`))
			}),
		}),
		WithStaticDir(root),
	)
	mux := srv.Handler()

	// API route — must hit the API handler, NOT the SPA index.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if rr.Body.String() != "API" {
		t.Errorf("API route shadowed by static: got %q", rr.Body.String())
	}

	// Unknown path — falls back to SPA index.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/series", nil))
	if !strings.Contains(rr.Body.String(), "SPA") {
		t.Errorf("SPA fallback failed for /series: got %q", rr.Body.String())
	}

	// /ping — must still hit the ping handler.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("/ping status: %d", rr.Code)
	}
}

type okConfigProbe struct{}

func (okConfigProbe) All(_ context.Context) error { return nil }
