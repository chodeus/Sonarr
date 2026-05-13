package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
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
