package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeQueuePagedLister struct {
	records []QueueResource
	total   int
	err     error
	gotReq  PagingRequest
}

func (f *fakeQueuePagedLister) PagedQueue(_ context.Context, req PagingRequest) ([]QueueResource, int, error) {
	f.gotReq = req
	return f.records, f.total, f.err
}

func TestQueueHandler_DefaultEnvelope(t *testing.T) {
	t.Parallel()
	lister := &fakeQueuePagedLister{}
	rr := httptest.NewRecorder()
	QueueHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
	if lister.gotReq.SortKey != "timeleft" {
		t.Errorf("default sortKey: got %q want %q", lister.gotReq.SortKey, "timeleft")
	}
	var got PagingResource[QueueResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SortKey != "timeleft" || got.SortDirection != "descending" || got.TotalRecords != 0 {
		t.Errorf("envelope: %+v", got)
	}
	if !strings.Contains(rr.Body.String(), `"records":[]`) {
		t.Errorf(`expected "records":[] on empty body: %s`, rr.Body.String())
	}
}

func TestQueueHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueHandler(&fakeQueuePagedLister{err: errors.New("boom")}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestQueueHandler_RecordsPassthrough(t *testing.T) {
	t.Parallel()
	// Populated records still flow through the envelope. Exercises
	// the records-non-nil branch so future per-record mapping work
	// has a regression net.
	seriesID := 42
	lister := &fakeQueuePagedLister{
		records: []QueueResource{{ID: 1, SeriesID: &seriesID, Title: "demo", Status: "downloading", Protocol: "torrent"}},
		total:   1,
	}
	rr := httptest.NewRecorder()
	QueueHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue", nil))
	var got PagingResource[QueueResource]
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.TotalRecords != 1 || len(got.Records) != 1 || got.Records[0].Title != "demo" {
		t.Errorf("records passthrough: %+v", got)
	}
}
