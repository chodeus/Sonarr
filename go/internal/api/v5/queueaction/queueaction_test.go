package queueaction_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/queueaction"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/series"
)

type fakePending struct {
	rows map[int]*pendingreleases.Release
}

func (f *fakePending) Get(_ context.Context, id int) (*pendingreleases.Release, error) {
	if r, ok := f.rows[id]; ok {
		return r, nil
	}
	return nil, pendingreleases.ErrNotFound
}

type fakeSeries struct{}

func (fakeSeries) GetSeries(_ context.Context, _ int) (*series.Series, error) {
	return nil, series.ErrNotFound
}

type fakeDispatcher struct {
	mu    sync.Mutex
	count int
}

func (f *fakeDispatcher) ProcessDecisions(_ context.Context, _ []decisionengine.Decision) *download.ProcessedDecisions {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	return &download.ProcessedDecisions{}
}
func (f *fakeDispatcher) dispatchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

func validPending(id int) *pendingreleases.Release {
	return &pendingreleases.Release{
		ID:                id,
		ReleaseInfo:       `{}`,
		ParsedEpisodeInfo: `{}`,
	}
}

func TestGrabHandler_Success_RewritesTo204(t *testing.T) {
	t.Parallel()
	pending := &fakePending{rows: map[int]*pendingreleases.Release{7: validPending(7)}}
	dispatcher := &fakeDispatcher{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/queue/grab/7", nil)
	req.SetPathValue("id", "7")
	queueaction.GrabHandler(pending, fakeSeries{}, dispatcher).ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body must be empty for 204: %q", rr.Body.String())
	}
	if dispatcher.dispatchCount() != 1 {
		t.Errorf("expected 1 dispatch, got %d", dispatcher.dispatchCount())
	}
}

func TestGrabHandler_NotFound_PassesThrough(t *testing.T) {
	t.Parallel()
	pending := &fakePending{rows: map[int]*pendingreleases.Release{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/queue/grab/999", nil)
	req.SetPathValue("id", "999")
	queueaction.GrabHandler(pending, fakeSeries{}, &fakeDispatcher{}).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (wrapper must NOT swallow)", rr.Code)
	}
}

func TestGrabHandler_InvalidID_PassesThrough400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/queue/grab/abc", nil)
	req.SetPathValue("id", "abc")
	queueaction.GrabHandler(&fakePending{}, fakeSeries{}, &fakeDispatcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestGrabBulkHandler_Success_RewritesTo204(t *testing.T) {
	t.Parallel()
	pending := &fakePending{rows: map[int]*pendingreleases.Release{
		1: validPending(1), 2: validPending(2),
	}}
	dispatcher := &fakeDispatcher{}
	body := []byte(`{"ids":[1,2]}`)
	rr := httptest.NewRecorder()
	queueaction.GrabBulkHandler(pending, fakeSeries{}, dispatcher).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/queue/grab/bulk", bytes.NewReader(body)))

	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if dispatcher.dispatchCount() != 2 {
		t.Errorf("expected 2 dispatches, got %d", dispatcher.dispatchCount())
	}
}

func TestGrabBulkHandler_FirstMissing_AbortsBatch_404(t *testing.T) {
	t.Parallel()
	pending := &fakePending{rows: map[int]*pendingreleases.Release{
		1: validPending(1), // 999 absent
	}}
	dispatcher := &fakeDispatcher{}
	body := []byte(`{"ids":[1,999,2]}`)
	rr := httptest.NewRecorder()
	queueaction.GrabBulkHandler(pending, fakeSeries{}, dispatcher).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/queue/grab/bulk", bytes.NewReader(body)))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (wrapper must NOT swallow)", rr.Code)
	}
	// First id (1) DID dispatch before 999 aborted; matches v3 semantics.
	if dispatcher.dispatchCount() != 1 {
		t.Errorf("first id should dispatch before abort, got %d dispatches", dispatcher.dispatchCount())
	}
}

func TestGrabBulkHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queueaction.GrabBulkHandler(&fakePending{}, fakeSeries{}, &fakeDispatcher{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/queue/grab/bulk", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestHandlerConstructors_ReturnNonNil(t *testing.T) {
	t.Parallel()
	if queueaction.GrabHandler(&fakePending{}, fakeSeries{}, &fakeDispatcher{}) == nil {
		t.Errorf("GrabHandler returned nil")
	}
	if queueaction.GrabBulkHandler(&fakePending{}, fakeSeries{}, &fakeDispatcher{}) == nil {
		t.Errorf("GrabBulkHandler returned nil")
	}
}
