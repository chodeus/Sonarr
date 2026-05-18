package v3

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRenameHandler_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestRenameHandler_MissingSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRenameHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRenameHandler_WithSeasonNumber(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=42&seasonNumber=1", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRenameBulkHandler_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameBulkHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename/bulk?seriesIds=1&seriesIds=2", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestRenameBulkHandler_MissingSeriesIDs_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameBulkHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename/bulk", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRenameBulkHandler_NegativeSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameBulkHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename/bulk?seriesIds=0", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
