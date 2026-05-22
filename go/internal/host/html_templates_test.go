package host

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// htmlHandler returns an http.Handler that emits the supplied body
// with Content-Type: text/html. Used in tests as the inner handler.
func htmlHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
}

func TestInjectHTMLTemplates_ReplacesURLBase(t *testing.T) {
	t.Parallel()
	inner := htmlHandler(`<script>window.Sonarr = { urlBase: '__URL_BASE__' };</script>`)
	wrapped := injectHTMLTemplates(inner, "/sonarr", "auto")

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	body, _ := io.ReadAll(rr.Result().Body)
	if strings.Contains(string(body), "__URL_BASE__") {
		t.Errorf("__URL_BASE__ not replaced: %s", body)
	}
	if !strings.Contains(string(body), "urlBase: '/sonarr'") {
		t.Errorf("urlBase not set to /sonarr: %s", body)
	}
}

func TestInjectHTMLTemplates_ReplacesTheme(t *testing.T) {
	t.Parallel()
	inner := htmlHandler(`<script>var theme = '_THEME_';</script>`)
	wrapped := injectHTMLTemplates(inner, "", "dark")

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	body, _ := io.ReadAll(rr.Result().Body)
	if strings.Contains(string(body), "_THEME_") {
		t.Errorf("_THEME_ not replaced: %s", body)
	}
	if !strings.Contains(string(body), "var theme = 'dark';") {
		t.Errorf("theme not set to dark: %s", body)
	}
}

func TestInjectHTMLTemplates_StripsMiniProfilerPlaceholder(t *testing.T) {
	t.Parallel()
	inner := htmlHandler(`<head><title>Sonarr</title>__MINI_PROFILER__</head>`)
	wrapped := injectHTMLTemplates(inner, "", "auto")

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	body, _ := io.ReadAll(rr.Result().Body)
	if strings.Contains(string(body), "__MINI_PROFILER__") {
		t.Errorf("__MINI_PROFILER__ not stripped: %s", body)
	}
}

func TestInjectHTMLTemplates_NonHTMLPassthrough(t *testing.T) {
	t.Parallel()
	// JS bundle with a string that LOOKS like a placeholder must not
	// be rewritten — Content-Type guards the substitution path.
	jsBody := `console.log("__URL_BASE__ stays as the literal");`
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(jsBody))
	})
	wrapped := injectHTMLTemplates(inner, "/sonarr", "dark")

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/index.js", nil))

	body, _ := io.ReadAll(rr.Result().Body)
	if string(body) != jsBody {
		t.Errorf("JS body modified: got %q, want %q", body, jsBody)
	}
}

func TestInjectHTMLTemplates_EmptyURLBase_ReplacesWithEmpty(t *testing.T) {
	t.Parallel()
	// Default install: urlBase="" → __URL_BASE__ becomes "" so the
	// React app's window.Sonarr.urlBase === "" and downstream API
	// paths are absolute (/api/v3/...).
	inner := htmlHandler(`urlBase: '__URL_BASE__'`)
	wrapped := injectHTMLTemplates(inner, "", "auto")

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	body, _ := io.ReadAll(rr.Result().Body)
	if !strings.Contains(string(body), "urlBase: ''") {
		t.Errorf("urlBase not set to empty: %s", body)
	}
}

func TestInjectHTMLTemplates_AllThreeAtOnce(t *testing.T) {
	t.Parallel()
	// Composite test against an HTML body containing all three
	// placeholders — verifies no replacement interferes with another
	// and the order doesn't matter.
	body := `<script>
window.Sonarr = { urlBase: '__URL_BASE__' };
var theme = '_THEME_';
__MINI_PROFILER__
</script>`
	wrapped := injectHTMLTemplates(htmlHandler(body), "/sonarr", "dark")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	got := rr.Body.String()
	for _, placeholder := range []string{"__URL_BASE__", "_THEME_", "__MINI_PROFILER__"} {
		if strings.Contains(got, placeholder) {
			t.Errorf("placeholder %s remains: %s", placeholder, got)
		}
	}
	if !strings.Contains(got, "/sonarr") || !strings.Contains(got, "dark") {
		t.Errorf("expected /sonarr + dark in output: %s", got)
	}
}
