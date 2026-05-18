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

// ---------- 5.I18: /queue/details tests ----------

type fakeQueueDetailsLister struct {
	rows         []QueueResource
	err          error
	gotSeriesID  *int
	gotEpisodes  []int
	rememberArgs bool
}

func (f *fakeQueueDetailsLister) QueueDetails(_ context.Context, seriesID *int, episodeIDs []int) ([]QueueResource, error) {
	if f.rememberArgs {
		f.gotSeriesID = seriesID
		f.gotEpisodes = append([]int(nil), episodeIDs...)
	}
	return f.rows, f.err
}

func TestQueueDetailsHandler_EmptyArrayShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueDetailsHandler(&fakeQueueDetailsLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestQueueDetailsHandler_SeriesIDParsed(t *testing.T) {
	t.Parallel()
	l := &fakeQueueDetailsLister{rememberArgs: true}
	rr := httptest.NewRecorder()
	QueueDetailsHandler(l).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if l.gotSeriesID == nil || *l.gotSeriesID != 42 {
		t.Errorf("seriesId: %+v", l.gotSeriesID)
	}
}

func TestQueueDetailsHandler_EpisodeIDsParsed(t *testing.T) {
	t.Parallel()
	l := &fakeQueueDetailsLister{rememberArgs: true}
	rr := httptest.NewRecorder()
	QueueDetailsHandler(l).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details?episodeIds=1&episodeIds=2&episodeIds=3", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(l.gotEpisodes) != 3 || l.gotEpisodes[0] != 1 || l.gotEpisodes[2] != 3 {
		t.Errorf("episodeIds: %+v", l.gotEpisodes)
	}
}

func TestQueueDetailsHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueDetailsHandler(&fakeQueueDetailsLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details?seriesId=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestQueueDetailsHandler_InvalidEpisodeID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueDetailsHandler(&fakeQueueDetailsLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details?episodeIds=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestQueueDetailsHandler_ListerError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueDetailsHandler(&fakeQueueDetailsLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestQueueDetailsHandler_RecordsPassthrough(t *testing.T) {
	t.Parallel()
	seriesID := 7
	l := &fakeQueueDetailsLister{rows: []QueueResource{{ID: 1, SeriesID: &seriesID, Title: "demo"}}}
	rr := httptest.NewRecorder()
	QueueDetailsHandler(l).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"title":"demo"`) {
		t.Errorf("body should include title; got %s", rr.Body.String())
	}
}
