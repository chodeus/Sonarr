package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// ---------- 5.I21: /since + /series tests ----------

type fakeHistoryRangeReader struct {
	sinceRows  []history.Entry
	seriesRows []history.Entry
	gotEvent   *history.EventType
	gotSeries  int
	err        error
}

func (f *fakeHistoryRangeReader) Since(_ context.Context, _ time.Time, ev *history.EventType) ([]history.Entry, error) {
	f.gotEvent = ev
	if f.err != nil {
		return nil, f.err
	}
	if ev != nil {
		return filterByEventTypeFake(f.sinceRows, *ev), nil
	}
	return f.sinceRows, nil
}

func (f *fakeHistoryRangeReader) GetBySeries(_ context.Context, seriesID int, ev *history.EventType) ([]history.Entry, error) {
	f.gotSeries = seriesID
	f.gotEvent = ev
	if f.err != nil {
		return nil, f.err
	}
	if ev != nil {
		return filterByEventTypeFake(f.seriesRows, *ev), nil
	}
	return f.seriesRows, nil
}

func filterByEventTypeFake(rows []history.Entry, et history.EventType) []history.Entry {
	out := make([]history.Entry, 0, len(rows))
	for _, e := range rows {
		if e.EventType != nil && history.EventType(*e.EventType) == et {
			out = append(out, e)
		}
	}
	return out
}

func TestHistorySinceHandler_MissingDate_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySinceHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistorySinceHandler_InvalidDate_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySinceHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=not-a-date", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// J296 wire-shape pin: a populated row must emit
// `customFormats:[]` (not `null`) and `customFormatScore:0` —
// matches the C# Newtonsoft default for a non-null
// `List<CustomFormatResource>` initialised via
// `customFormats.ToResource(false)`.
func TestHistorySinceHandler_EmitsCustomFormatsAndScore(t *testing.T) {
	t.Parallel()
	one := 1
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 7, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), EventType: &one},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !contains(body, `"customFormats":[]`) {
		t.Errorf("body must contain `\"customFormats\":[]`; got %s", body)
	}
	if !contains(body, `"customFormatScore":0`) {
		t.Errorf("body must contain `\"customFormatScore\":0`; got %s", body)
	}
	// Decode through to confirm the struct fields too.
	var got []HistoryResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len: %d", len(got))
	}
	if got[0].CustomFormats == nil {
		t.Errorf("CustomFormats must be non-nil (empty slice, not nil)")
	}
	if len(got[0].CustomFormats) != 0 {
		t.Errorf("CustomFormats must be empty at deferred state; got %d", len(got[0].CustomFormats))
	}
	if got[0].CustomFormatScore != 0 {
		t.Errorf("CustomFormatScore must be 0 at deferred state; got %d", got[0].CustomFormatScore)
	}
}

func TestHistorySinceHandler_Success(t *testing.T) {
	t.Parallel()
	one := 1
	two := 4
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), EventType: &one},
		{ID: 2, SeriesID: 11, SourceTitle: "Show.S01E02", Date: time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC), EventType: &two},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []HistoryResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len: %d", len(got))
	}
	if got[0].EventType != "Grabbed" || got[1].EventType != "DownloadFailed" {
		t.Errorf("eventType names: %q %q", got[0].EventType, got[1].EventType)
	}
	if svc.gotEvent != nil {
		t.Errorf("unfiltered call should pass nil eventType; got %+v", svc.gotEvent)
	}
}

func TestHistorySinceHandler_FilterByEventType(t *testing.T) {
	t.Parallel()
	one := 1
	two := 4
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 1, SourceTitle: "grab", Date: time.Now().UTC(), EventType: &one},
		{ID: 2, SourceTitle: "fail", Date: time.Now().UTC(), EventType: &two},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z&eventType=4", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if svc.gotEvent == nil || *svc.gotEvent != history.EventTypeDownloadFailed {
		t.Errorf("expected DownloadFailed filter passed through; got %+v", svc.gotEvent)
	}
}

func TestHistorySinceHandler_BadEventType_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySinceHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z&eventType=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistorySeriesHandler_MissingSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySeriesHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/series", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistorySeriesHandler_Success(t *testing.T) {
	t.Parallel()
	one := 1
	svc := &fakeHistoryRangeReader{seriesRows: []history.Entry{
		{ID: 1, SeriesID: 42, SourceTitle: "Show.S01E01", Date: time.Now().UTC(), EventType: &one},
	}}
	rr := httptest.NewRecorder()
	HistorySeriesHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/series?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if svc.gotSeries != 42 {
		t.Errorf("seriesId not forwarded: got %d", svc.gotSeries)
	}
}

func TestHistorySeriesHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySeriesHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/series?seriesId=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
