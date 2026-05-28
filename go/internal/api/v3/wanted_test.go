package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeWantedLister struct {
	records []EpisodeResource
	total   int
	err     error
	got     WantedScope
}

func (f *fakeWantedLister) PagedWanted(_ context.Context, _ PagingRequest, scope WantedScope, _, _ bool) ([]EpisodeResource, int, error) {
	f.got = scope
	return f.records, f.total, f.err
}

func TestWantedHandler_MissingDefaults(t *testing.T) {
	t.Parallel()
	lister := &fakeWantedLister{}
	rr := httptest.NewRecorder()
	WantedHandler(lister, WantedMissing).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/wanted/missing", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if lister.got != WantedMissing {
		t.Errorf("scope: got %v want WantedMissing", lister.got)
	}
	var got PagingResource[EpisodeResource]
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.SortKey != "episodes.airDateUtc" || got.SortDirection != "descending" || got.TotalRecords != 0 {
		t.Errorf("envelope: %+v", got)
	}
	if !contains(rr.Body.String(), `"records":[]`) {
		t.Errorf(`body should contain "records":[]: %s`, rr.Body.String())
	}
}

func TestWantedHandler_CutoffScopePassthrough(t *testing.T) {
	t.Parallel()
	lister := &fakeWantedLister{}
	rr := httptest.NewRecorder()
	WantedHandler(lister, WantedCutoff).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/wanted/cutoff", nil))
	if lister.got != WantedCutoff {
		t.Errorf("scope: got %v want WantedCutoff", lister.got)
	}
}

func TestWantedHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	WantedHandler(&fakeWantedLister{err: errors.New("boom")}, WantedMissing).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/wanted/missing", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
