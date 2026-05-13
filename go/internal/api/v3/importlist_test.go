package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
)

type fakeImportListLister struct {
	rows []importlists.ImportList
	err  error
}

func (f *fakeImportListLister) All(context.Context) ([]importlists.ImportList, error) {
	return f.rows, f.err
}

func TestImportListHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListHandler(&fakeImportListLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlist", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestImportListHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListHandler(&fakeImportListLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlist", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
