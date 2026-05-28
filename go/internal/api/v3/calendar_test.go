package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

type fakeCalendarLister struct {
	rows                  []episodes.Episode
	err                   error
	gotStart, gotEnd      time.Time
	gotUnmon, gotSpecials bool
}

func (f *fakeCalendarLister) EpisodesBetweenDates(_ context.Context, start, end time.Time, unmon, specials bool) ([]episodes.Episode, error) {
	f.gotStart, f.gotEnd, f.gotUnmon, f.gotSpecials = start, end, unmon, specials
	return f.rows, f.err
}

func TestCalendarHandler_DateRangePassthrough(t *testing.T) {
	t.Parallel()
	lister := &fakeCalendarLister{}
	rr := httptest.NewRecorder()
	CalendarHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/calendar?start=2026-01-01&end=2026-12-31", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
	if !lister.gotStart.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("start: got %v want 2026-01-01", lister.gotStart)
	}
	if !lister.gotEnd.Equal(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("end: got %v want 2026-12-31", lister.gotEnd)
	}
	if !lister.gotSpecials {
		t.Error("includeSpecials should always be true (C# call site)")
	}
}

func TestCalendarHandler_DefaultsToTodayAndTwoDaysAhead(t *testing.T) {
	t.Parallel()
	lister := &fakeCalendarLister{}
	rr := httptest.NewRecorder()
	CalendarHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/calendar", nil))
	delta := lister.gotEnd.Sub(lister.gotStart)
	if delta != 48*time.Hour {
		t.Errorf("default range should be 48h: got %v", delta)
	}
}

func TestCalendarHandler_UnmonitoredFlag(t *testing.T) {
	t.Parallel()
	lister := &fakeCalendarLister{}
	rr := httptest.NewRecorder()
	CalendarHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/calendar?unmonitored=true", nil))
	if !lister.gotUnmon {
		t.Error("unmonitored=true not forwarded")
	}
}

func TestCalendarHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CalendarHandler(&fakeCalendarLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/calendar", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// fakeCalendarListerWithEmbeds implements CalendarEpisodeLister plus the
// EpisodeSeriesLookup + EpisodeFileLookupForEpisode optional extensions
// — exercises the J376 include branches.
type fakeCalendarListerWithEmbeds struct {
	rows   []episodes.Episode
	series map[int]*series.Series
	files  map[int]*episodefiles.File
}

func (f *fakeCalendarListerWithEmbeds) EpisodesBetweenDates(_ context.Context, _, _ time.Time, _, _ bool) ([]episodes.Episode, error) {
	return f.rows, nil
}
func (f *fakeCalendarListerWithEmbeds) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if s, ok := f.series[id]; ok {
		return s, nil
	}
	return nil, series.ErrNotFound
}
func (f *fakeCalendarListerWithEmbeds) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if x, ok := f.files[id]; ok {
		return x, nil
	}
	return nil, episodefiles.ErrNotFound
}

// TestCalendarHandler_IncludeBranches_J376 pins the includeSeries /
// includeEpisodeFile / includeEpisodeImages embeds: absent without the
// params, all present (series/episodeFile/images) when set true and the
// lister implements the optional extensions.
func TestCalendarHandler_IncludeBranches_J376(t *testing.T) {
	t.Parallel()
	efid := 9
	row := episodes.Episode{
		ID: 5, SeriesID: 7, SeasonNumber: 1, EpisodeNumber: 1, EpisodeFileID: &efid,
		Images: `[{"coverType":4,"url":"/s","remoteUrl":"https://r/s.jpg"}]`,
	}
	lister := &fakeCalendarListerWithEmbeds{
		rows:   []episodes.Episode{row},
		series: map[int]*series.Series{7: {ID: 7, Title: "Breaking Bad"}},
		files:  map[int]*episodefiles.File{9: {ID: 9, SeriesID: 7, RelativePath: "a.mkv"}},
	}

	// No include params → no embeds.
	rr := httptest.NewRecorder()
	CalendarHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/calendar", nil))
	if b := rr.Body.String(); strings.Contains(b, `"series"`) || strings.Contains(b, `"episodeFile"`) || strings.Contains(b, `"images"`) {
		t.Errorf("no embeds expected without include params; got %s", b)
	}

	// All three → all embeds present.
	rr2 := httptest.NewRecorder()
	CalendarHandler(lister).ServeHTTP(rr2, httptest.NewRequest(http.MethodGet,
		"/api/v3/calendar?includeSeries=true&includeEpisodeFile=true&includeEpisodeImages=true", nil))
	var got []EpisodeResource
	if err := json.Unmarshal(rr2.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row; got %d", len(got))
	}
	if got[0].Series == nil || got[0].Series.ID != 7 || got[0].Series.Title != "Breaking Bad" {
		t.Errorf("series embed: %+v", got[0].Series)
	}
	if got[0].EpisodeFile == nil || got[0].EpisodeFile.ID != 9 {
		t.Errorf("episodeFile embed: %+v", got[0].EpisodeFile)
	}
	if got[0].Images == nil || len(*got[0].Images) != 1 {
		t.Errorf("images embed: %+v", got[0].Images)
	}
}
