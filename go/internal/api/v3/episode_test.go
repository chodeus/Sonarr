package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"sonarr.tv/sonarr/internal/episodes"
)

type fakeEpisodeBySeries struct {
	rows []episodes.Episode
	err  error
	got  int
}

func (f *fakeEpisodeBySeries) GetEpisodesBySeries(_ context.Context, id int) ([]episodes.Episode, error) {
	f.got = id
	return f.rows, f.err
}

func TestEpisodeListHandler_EmptyForKnownSeries(t *testing.T) {
	t.Parallel()
	lister := &fakeEpisodeBySeries{}
	rr := httptest.NewRecorder()
	EpisodeListHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
	if lister.got != 42 {
		t.Errorf("seriesId not forwarded: got %d", lister.got)
	}
}

func TestEpisodeListHandler_MissingSeriesId400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestEpisodeListHandler_PopulatedShape(t *testing.T) {
	t.Parallel()
	tvdb := 12345
	lister := &fakeEpisodeBySeries{rows: []episodes.Episode{
		{
			ID: 1, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1,
			Title: "Pilot", TvdbID: &tvdb, Runtime: 60, Monitored: true,
		},
	}}
	rr := httptest.NewRecorder()
	EpisodeListHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=42", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`, `"seriesId":42`, `"tvdbId":12345`,
		`"seasonNumber":1`, `"episodeNumber":1`,
		`"title":"Pilot"`, `"runtime":60`,
		`"monitored":true`, `"hasFile":false`,
	} {
		if !contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestEpisodeListHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=1", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestEpisodeListHandler_InvalidSeriesId400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// strconv reference to silence unused-import nags when tests change.
var _ = strconv.Itoa
