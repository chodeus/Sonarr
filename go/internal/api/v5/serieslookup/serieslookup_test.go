package serieslookup_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/serieslookup"
)

func TestGetHandler_WithTerm_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	serieslookup.GetHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/series/lookup?term=Breaking+Bad", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want [] (DEFERRED — search-proxy integration not ported)", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: %q", ct)
	}
}

func TestGetHandler_MissingTerm_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	serieslookup.GetHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/series/lookup", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (term required)", rr.Code)
	}
}

func TestGetHandler_EmptyTerm_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	serieslookup.GetHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/series/lookup?term=", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (empty term rejected)", rr.Code)
	}
}

func TestHandlerAlias_NonNil(t *testing.T) {
	t.Parallel()
	if serieslookup.GetHandler == nil {
		t.Errorf("GetHandler nil")
	}
}
