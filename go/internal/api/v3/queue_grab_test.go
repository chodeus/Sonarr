package v3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/series"
)

// fakePendingLookup is a narrow PendingReleaseLookup test double.
type fakePendingLookup struct {
	byID   map[int]*pendingreleases.Release
	getErr error
}

func (f *fakePendingLookup) Get(_ context.Context, id int) (*pendingreleases.Release, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if row, ok := f.byID[id]; ok {
		return row, nil
	}
	return nil, pendingreleases.ErrNotFound
}

// fakeSeriesLookup satisfies QueueGrabSeriesLookup.
type fakeSeriesLookup struct {
	byID map[int]*series.Series
}

func (f *fakeSeriesLookup) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if row, ok := f.byID[id]; ok {
		return row, nil
	}
	return nil, series.ErrNotFound
}

// fakeDispatcher captures the dispatched decisions for inspection.
type fakeDispatcher struct {
	calls [][]decisionengine.Decision
}

func (f *fakeDispatcher) ProcessDecisions(_ context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions {
	cp := make([]decisionengine.Decision, len(decisions))
	copy(cp, decisions)
	f.calls = append(f.calls, cp)
	return download.NewProcessedDecisions()
}

// helper to build a valid persisted pending release row for the tests.
func makePendingRow(id, seriesID int) *pendingreleases.Release {
	relInfo := parser.ReleaseInfo{
		Title:       "Show.S01E01.Title.720p",
		DownloadURL: "https://example.com/release.torrent",
		Indexer:     "TestIndexer",
		IndexerID:   42,
	}
	relJSON, _ := json.Marshal(relInfo)
	parsedInfo := parser.ParsedEpisodeInfo{
		SeasonNumber:   1,
		EpisodeNumbers: []int{1},
		ReleaseTitle:   "Show.S01E01.Title.720p",
	}
	parsedJSON, _ := json.Marshal(parsedInfo)
	return &pendingreleases.Release{
		ID:                id,
		SeriesID:          seriesID,
		Title:             "Show.S01E01.Title.720p",
		ReleaseInfo:       string(relJSON),
		ParsedEpisodeInfo: string(parsedJSON),
	}
}

// ----- POST /api/v3/queue/grab/{id} — single -----

func TestQueueGrabHandler_HappyPath(t *testing.T) {
	t.Parallel()
	row := makePendingRow(7, 11)
	pending := &fakePendingLookup{byID: map[int]*pendingreleases.Release{7: row}}
	seriesLookup := &fakeSeriesLookup{byID: map[int]*series.Series{11: {ID: 11, Title: "My Show"}}}
	dispatcher := &fakeDispatcher{}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/7", nil)
	req.SetPathValue("id", "7")
	QueueGrabHandler(pending, seriesLookup, dispatcher).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "{}" {
		t.Errorf("body: got %q want {}", rr.Body.String())
	}
	if len(dispatcher.calls) != 1 || len(dispatcher.calls[0]) != 1 {
		t.Fatalf("dispatcher: got %d calls", len(dispatcher.calls))
	}
	sub, ok := dispatcher.calls[0][0].Subject.(*decisionengine.Subject)
	if !ok || sub == nil {
		t.Fatalf("dispatched decision has no *Subject")
	}
	rel, ok := sub.Release.(*parser.ReleaseInfo)
	if !ok || rel == nil {
		t.Fatalf("Subject.Release not *parser.ReleaseInfo")
	}
	if rel.Title != "Show.S01E01.Title.720p" {
		t.Errorf("Subject.Release.Title: got %q", rel.Title)
	}
	if rel.DownloadURL != "https://example.com/release.torrent" {
		t.Errorf("Subject.Release.DownloadURL: got %q", rel.DownloadURL)
	}
	if sub.Series == nil || sub.Series.Title != "My Show" {
		t.Errorf("Subject.Series not populated: got %+v", sub.Series)
	}
	if !sub.DownloadAllowed {
		t.Errorf("Subject.DownloadAllowed must be true to pass isQualified gate")
	}
}

func TestQueueGrabHandler_NotFound404(t *testing.T) {
	t.Parallel()
	pending := &fakePendingLookup{} // empty — Get returns ErrNotFound
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/99", nil)
	req.SetPathValue("id", "99")
	QueueGrabHandler(pending, &fakeSeriesLookup{}, &fakeDispatcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestQueueGrabHandler_InvalidID400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/abc", nil)
	req.SetPathValue("id", "abc")
	QueueGrabHandler(&fakePendingLookup{}, &fakeSeriesLookup{}, &fakeDispatcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestQueueGrabHandler_PendingGetErr500(t *testing.T) {
	t.Parallel()
	pending := &fakePendingLookup{getErr: errors.New("db down")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/5", nil)
	req.SetPathValue("id", "5")
	QueueGrabHandler(pending, &fakeSeriesLookup{}, &fakeDispatcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestQueueGrabHandler_NilSeriesLookupTolerated(t *testing.T) {
	t.Parallel()
	// SeriesID=0 OR seriesLookup=nil should not block dispatch. Some
	// pending rows may have been added before SeriesID was set; the
	// grab still dispatches with Subject.Series=nil (the dispatcher
	// tolerates nil Series for qBittorrent's AddSeriesTags case).
	row := makePendingRow(3, 0) // SeriesID=0
	pending := &fakePendingLookup{byID: map[int]*pendingreleases.Release{3: row}}
	dispatcher := &fakeDispatcher{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/3", nil)
	req.SetPathValue("id", "3")
	QueueGrabHandler(pending, &fakeSeriesLookup{}, dispatcher).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if len(dispatcher.calls) != 1 {
		t.Errorf("dispatcher: got %d calls want 1", len(dispatcher.calls))
	}
}

// ----- POST /api/v3/queue/grab/bulk -----

func TestQueueGrabBulkHandler_HappyPath(t *testing.T) {
	t.Parallel()
	pending := &fakePendingLookup{byID: map[int]*pendingreleases.Release{
		1: makePendingRow(1, 11),
		2: makePendingRow(2, 11),
	}}
	seriesLookup := &fakeSeriesLookup{byID: map[int]*series.Series{11: {ID: 11, Title: "X"}}}
	dispatcher := &fakeDispatcher{}

	body := bytes.NewBufferString(`{"ids":[1,2]}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/bulk", body)
	QueueGrabBulkHandler(pending, seriesLookup, dispatcher).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "{}" {
		t.Errorf("body: got %q want {}", rr.Body.String())
	}
	if len(dispatcher.calls) != 2 {
		t.Errorf("dispatcher: got %d calls want 2", len(dispatcher.calls))
	}
}

func TestQueueGrabBulkHandler_FirstMissing404ShortCircuits(t *testing.T) {
	t.Parallel()
	// id=99 doesn't exist; id=2 does. Bulk must short-circuit on the
	// first NotFound — matches C# `QueueActionController.Grab(bulk)`
	// at `:38-55` which throws NotFoundException on the first miss.
	pending := &fakePendingLookup{byID: map[int]*pendingreleases.Release{
		2: makePendingRow(2, 11),
	}}
	dispatcher := &fakeDispatcher{}
	body := bytes.NewBufferString(`{"ids":[99,2]}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/bulk", body)
	QueueGrabBulkHandler(pending, &fakeSeriesLookup{}, dispatcher).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (short-circuit on first miss)", rr.Code)
	}
	if len(dispatcher.calls) != 0 {
		t.Errorf("dispatcher: got %d calls want 0 (first miss must short-circuit before dispatch)", len(dispatcher.calls))
	}
}

func TestQueueGrabBulkHandler_MalformedBody400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/bulk", strings.NewReader(`not json`))
	QueueGrabBulkHandler(&fakePendingLookup{}, &fakeSeriesLookup{}, &fakeDispatcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestQueueGrabBulkHandler_EmptyIds200NoDispatch(t *testing.T) {
	t.Parallel()
	// C# iterates the ids slice — empty slice = no iteration = empty
	// {} response. The Go-port matches.
	dispatcher := &fakeDispatcher{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/queue/grab/bulk", strings.NewReader(`{"ids":[]}`))
	QueueGrabBulkHandler(&fakePendingLookup{}, &fakeSeriesLookup{}, dispatcher).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if len(dispatcher.calls) != 0 {
		t.Errorf("dispatcher: got %d calls want 0", len(dispatcher.calls))
	}
}
