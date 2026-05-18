package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/history"
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

// ---------- 5.E4: POST history/failed/{id} ----------

type fakeFailedDownloadMarker struct {
	calledID int
	err      error
}

func (f *fakeFailedDownloadMarker) MarkAsFailed(_ context.Context, id int) error {
	f.calledID = id
	return f.err
}

func TestHistoryMarkAsFailedHandler_Success(t *testing.T) {
	t.Parallel()
	m := &fakeFailedDownloadMarker{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/history/failed/42", nil)
	req.SetPathValue("id", "42")
	HistoryMarkAsFailedHandler(m).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if m.calledID != 42 {
		t.Errorf("expected MarkAsFailed(42); got %d", m.calledID)
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body should be empty object; got %q", rr.Body.String())
	}
}

func TestHistoryMarkAsFailedHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	m := &fakeFailedDownloadMarker{err: history.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/history/failed/999", nil)
	req.SetPathValue("id", "999")
	HistoryMarkAsFailedHandler(m).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistoryMarkAsFailedHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/history/failed/abc", nil)
	req.SetPathValue("id", "abc")
	HistoryMarkAsFailedHandler(&fakeFailedDownloadMarker{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistoryMarkAsFailedHandler_ServiceError_500(t *testing.T) {
	t.Parallel()
	m := &fakeFailedDownloadMarker{err: errors.New("boom")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/history/failed/1", nil)
	req.SetPathValue("id", "1")
	HistoryMarkAsFailedHandler(m).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}
