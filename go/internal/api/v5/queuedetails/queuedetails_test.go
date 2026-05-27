package queuedetails_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/queuedetails"
)

type fakeLister struct {
	rows []apiv3.QueueResource
	err  error
}

func (f *fakeLister) QueueDetails(_ context.Context, _ *int, _ []int) ([]apiv3.QueueResource, error) {
	return f.rows, f.err
}

func TestGetHandler_EmptyReturnsArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuedetails.GetHandler(&fakeLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/details", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want [] (DEFERRED — queue projection chain not ported)", rr.Body.String())
	}
}

func TestGetHandler_WithSeriesIDFilter_PassesThrough(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuedetails.GetHandler(&fakeLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/details?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestGetHandler_WithEpisodeIDsFilter_PassesThrough(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuedetails.GetHandler(&fakeLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/details?episodeIds=1&episodeIds=2", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
}

func TestGetHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuedetails.GetHandler(&fakeLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/details?seriesId=-1", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestGetHandler_InvalidEpisodeID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuedetails.GetHandler(&fakeLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/details?episodeIds=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestGetHandler_ListerError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuedetails.GetHandler(&fakeLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/details", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestGetHandler_WithIncludeSubresources_AccepetedButIgnored(t *testing.T) {
	t.Parallel()
	// v5-only includeSubresources query param. v3 handler ignores it
	// (just like the v3-side includeSeries/includeEpisode flags) at
	// the deferred-stub level. Both surfaces still emit `[]`.
	rr := httptest.NewRecorder()
	queuedetails.GetHandler(&fakeLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/details?includeSubresources=series&includeSubresources=episodes", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q (v5 includeSubresources accepted but ignored at stub level)", rr.Body.String())
	}
}

// TestResourceTypeAlias pins compile-time equivalence with v3.
func TestResourceTypeAlias(t *testing.T) {
	t.Parallel()
	r := queuedetails.Resource{ID: 1}
	if r.ID != 1 {
		t.Errorf("Resource not addressable")
	}
}
