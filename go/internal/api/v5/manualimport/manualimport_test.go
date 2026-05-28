package manualimport

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// fakeSeriesLookup implements SeriesLookup.
type fakeSeriesLookup struct{}

func (fakeSeriesLookup) GetSeries(_ context.Context, _ int) (*series.Series, error) {
	return nil, series.ErrNotFound
}

// fakeEpisodeLookup implements EpisodeLookup.
type fakeEpisodeLookup struct{}

func (fakeEpisodeLookup) GetEpisode(_ context.Context, _ int) (*episodes.Episode, error) {
	return nil, episodes.ErrNotFound
}

// ----- GET /manualimport — empty-corpus stub -----

func TestGetHandler_EmptyCorpus_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	// v3 stub emits `[]` for empty-corpus; path-alias preserves that
	// at the v5 surface.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/manualimport", nil)
	GetHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "[]" {
		t.Errorf("body: got %q want `[]`", body)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
}

func TestGetHandler_InvalidSeriesID_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/manualimport?seriesId=abc", nil)
	GetHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- POST /manualimport — body validation passthrough -----

func TestReprocessHandler_EmptyBody_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/manualimport", bytes.NewReader([]byte(`[]`)))
	ReprocessHandler(fakeSeriesLookup{}, fakeEpisodeLookup{}, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (empty items)", rr.Code)
	}
}

func TestReprocessHandler_MalformedBody_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/manualimport", bytes.NewReader([]byte(`{not json`)))
	ReprocessHandler(fakeSeriesLookup{}, fakeEpisodeLookup{}, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (malformed body)", rr.Code)
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if GetHandler == nil || ReprocessHandler == nil {
		t.Error("handler aliases must be non-nil")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ ReprocessItem = apiv3.ManualImportReprocessItem{}
	var _ apiv3.ManualImportReprocessItem = ReprocessItem{}
	var _ SeriesLookup = (apiv3.ManualImportSeriesLookup)(nil)
	var _ EpisodeLookup = (apiv3.ManualImportEpisodeLookup)(nil)
}
