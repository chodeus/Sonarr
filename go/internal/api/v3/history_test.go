package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeHistoryLister struct {
	records []HistoryResource
	total   int
	err     error
}

func (f *fakeHistoryLister) PagedHistory(context.Context, PagingRequest) ([]HistoryResource, int, error) {
	return f.records, f.total, f.err
}

func TestHistoryHandler_EmptyDefaults(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistoryHandler(&fakeHistoryLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got PagingResource[HistoryResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Page != 1 || got.PageSize != 10 || got.SortKey != "date" ||
		got.SortDirection != "descending" || got.TotalRecords != 0 {
		t.Errorf("envelope: %+v", got)
	}
	if body := rr.Body.String(); !contains(body, `"records":[]`) {
		t.Errorf(`body missing "records":[]: %s`, body)
	}
}

func TestHistoryHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistoryHandler(&fakeHistoryLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
