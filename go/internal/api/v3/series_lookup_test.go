package v3

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSeriesLookupHandler_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesLookupHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series/lookup?term=The+Show", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestSeriesLookupHandler_MissingTerm_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesLookupHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series/lookup", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
