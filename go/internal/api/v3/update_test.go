package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeUpdateLister struct {
	rows []UpdateResource
	err  error
}

func (f *fakeUpdateLister) RecentUpdates(context.Context) ([]UpdateResource, error) {
	return f.rows, f.err
}

func TestUpdateHandler_EmptyList(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	UpdateHandler(&fakeUpdateLister{}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/update", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body should be []: %q", rr.Body.String())
	}
}

func TestUpdateHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	UpdateHandler(&fakeUpdateLister{err: errors.New("boom")}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/update", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
