package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

type fakeSeriesSearcher struct {
	results []*series.Series
	err     error
	gotTerm string
}

func (f *fakeSeriesSearcher) SearchForNewSeries(_ context.Context, term string) ([]*series.Series, error) {
	f.gotTerm = term
	return f.results, f.err
}

// nil searcher → empty list (pre-G6 stub posture preserved).
func TestSeriesLookupHandler_NilSearcher_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesLookupHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series/lookup?term=The+Show", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "[]" {
		t.Errorf("status=%d body=%q, want 200 []", rr.Code, rr.Body.String())
	}
}

func TestSeriesLookupHandler_MissingTerm_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesLookupHandler(&fakeSeriesSearcher{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series/lookup", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d, want 400", rr.Code)
	}
}

// J426 (G6): results are projected to SeriesResource; the term is passed through.
func TestSeriesLookupHandler_ReturnsResults_J426(t *testing.T) {
	t.Parallel()
	f := &fakeSeriesSearcher{results: []*series.Series{
		{ID: 0, Title: "The Show", TvdbID: 12345, Year: 2020},
		nil, // nil entries skipped
		{ID: 0, Title: "The Show (US)", TvdbID: 6789},
	}}
	rr := httptest.NewRecorder()
	SeriesLookupHandler(f).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series/lookup?term=the+show", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if f.gotTerm != "the show" {
		t.Errorf("term passed = %q, want 'the show'", f.gotTerm)
	}
	var got []SeriesResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (nil skipped)", len(got))
	}
	if got[0].Title != "The Show" || got[0].TvdbID != 12345 {
		t.Errorf("result[0] = %+v", got[0])
	}
}

// Search error → 500.
func TestSeriesLookupHandler_SearchError_500_J426(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesLookupHandler(&fakeSeriesSearcher{err: errors.New("skyhook down")}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series/lookup?term=x", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d, want 500", rr.Code)
	}
}
