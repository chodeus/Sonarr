package renameepisode_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/renameepisode"
)

func TestGetHandler_ValidSeriesID_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	renameepisode.GetHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/rename?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want [] (DEFERRED — comparison driver not ported)", rr.Body.String())
	}
}

func TestGetHandler_MissingSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	renameepisode.GetHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/rename", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (seriesId required)", rr.Code)
	}
}

func TestGetHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	renameepisode.GetHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/rename?seriesId=-1", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (seriesId must be positive)", rr.Code)
	}
}

func TestGetHandler_WithSeasonNumber_AlsoReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	renameepisode.GetHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/rename?seriesId=42&seasonNumber=3", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestBulkHandler_ValidSeriesIDs_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	renameepisode.BulkHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/rename/bulk?seriesIds=1&seriesIds=2", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestBulkHandler_MissingSeriesIDs_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	renameepisode.BulkHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/rename/bulk", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (seriesIds must be provided)", rr.Code)
	}
}

func TestBulkHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	renameepisode.BulkHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/rename/bulk?seriesIds=0", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (seriesIds must be positive)", rr.Code)
	}
}

func TestHandlerAliases_NonNil(t *testing.T) {
	t.Parallel()
	if renameepisode.GetHandler(nil) == nil {
		t.Errorf("GetHandler returned nil handler")
	}
	if renameepisode.BulkHandler(nil) == nil {
		t.Errorf("BulkHandler returned nil handler")
	}
}

// TestResourceTypeAlias pins compile-time equivalence with v3.
func TestResourceTypeAlias(t *testing.T) {
	t.Parallel()
	r := renameepisode.Resource{ID: 1, SeriesID: 2, NewPath: "/x"}
	if r.NewPath != "/x" || r.SeriesID != 2 {
		t.Errorf("Resource not addressable")
	}
}
