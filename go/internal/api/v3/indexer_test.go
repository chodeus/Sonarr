package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/indexers"
)

type fakeIndexerLister struct {
	rows []indexers.Indexer
	err  error
}

func (f *fakeIndexerLister) All(context.Context) ([]indexers.Indexer, error) {
	return f.rows, f.err
}

func TestIndexerHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerHandler(&fakeIndexerLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/indexer", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestIndexerHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerHandler(&fakeIndexerLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/indexer", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
