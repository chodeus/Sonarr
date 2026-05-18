package host

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/httperr"
)

func TestInjectLogger_StashesLoggerInRequestContext(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := httperr.LoggerFromRequest(r)
		if got != logger {
			t.Errorf("inner handler must see injected logger")
		}
	})
	h := injectLogger(logger, inner)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestInjectLogger_NilLoggerLeavesContextClean(t *testing.T) {
	t.Parallel()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := httperr.LoggerFromRequest(r); got != nil {
			t.Errorf("nil-logger injection must NOT populate context, got %v", got)
		}
	})
	h := injectLogger(nil, inner)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestInjectLogger_NilNextReturnsNil(t *testing.T) {
	t.Parallel()
	// Defensive: passing a nil next handler returns nil rather
	// than panicking at request time.
	if got := injectLogger(nil, nil); got != nil {
		t.Errorf("nil next must produce nil, got %v", got)
	}
}

// TestInjectLogger_EndToEnd500Logs is the integration pin for 5.A5:
// a handler calling httperr.WriteErr(..., nil) on a 500 path emits
// a log line via the context-injected logger. This is the contract
// the cross-cutting 5.A4 sweep depends on for actual observability.
func TestInjectLogger_EndToEnd500Logs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httperr.WriteErr(w, r, http.StatusInternalServerError, errInjectedTest, nil)
	})
	wrapped := injectLogger(logger, handler)
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v3/series", nil))
	if !strings.Contains(buf.String(), "injected-end-to-end") {
		t.Errorf("end-to-end 500 must log via context logger: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "POST") {
		t.Errorf("log must include method: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "/api/v3/series") {
		t.Errorf("log must include path: %q", buf.String())
	}
}

var errInjectedTest = injectedError("injected-end-to-end")

type injectedError string

func (e injectedError) Error() string { return string(e) }
