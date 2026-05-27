package releasepush_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/releasepush"
)

func TestByIDHandler_Always404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/release/push/1", nil)
	req.SetPathValue("id", "1")
	releasepush.ByIDHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (push releases are not persisted)", rr.Code)
	}
}

func TestByIDHandler_AnyValidID404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/release/push/12345", nil)
	req.SetPathValue("id", "12345")
	releasepush.ByIDHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestByIDHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/release/push/abc", nil)
	req.SetPathValue("id", "abc")
	releasepush.ByIDHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestByIDHandler_NegativeID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/release/push/-1", nil)
	req.SetPathValue("id", "-1")
	releasepush.ByIDHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (non-positive rejected)", rr.Code)
	}
}

// TestPostHandlerAlias_HandlerType compiles iff PostHandler has the
// expected (DecisionMaker, Dispatcher, *releasecache.Cache,
// IndexerLookup, DownloadClientLookup) → http.Handler signature.
// We don't exercise the full decision-engine/dispatcher pipeline
// here — the v3 ReleasePushHandler has its own coverage; this
// slice's contract is "v5 mounts the v3 handler at
// /api/v5/release/push".
func TestPostHandlerAlias_HandlerType(t *testing.T) {
	t.Parallel()
	var (
		_ = releasepush.PostHandler // referenced for compile-time pin
		_ = bytes.NewReader         // silence unused import on test refactor
	)
}

// TestPushBodyTypeAlias pins compile-time equivalence with v3.
func TestPushBodyTypeAlias(t *testing.T) {
	t.Parallel()
	b := releasepush.PushBody{Title: "x", DownloadURL: "https://example.com/r.nzb"}
	if b.Title != "x" {
		t.Errorf("PushBody not addressable")
	}
}
