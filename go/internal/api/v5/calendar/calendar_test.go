package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// fakeEpisodeLister implements EpisodeLister.
type fakeEpisodeLister struct {
	rows []episodes.Episode
}

func (f *fakeEpisodeLister) EpisodesBetweenDates(_ context.Context, _, _ time.Time, _, _ bool) ([]episodes.Episode, error) {
	out := make([]episodes.Episode, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

// fakeSeriesLister implements FeedSeriesLister.
type fakeSeriesLister struct {
	rows []series.Series
}

func (f *fakeSeriesLister) GetAllSeries(_ context.Context) ([]series.Series, error) {
	out := make([]series.Series, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

// fakeEpisodeGetter implements EpisodeByIDGetter.
type fakeEpisodeGetter struct {
	found *episodes.Episode
}

func (f *fakeEpisodeGetter) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	if f.found != nil && f.found.ID == id {
		cp := *f.found
		return &cp, nil
	}
	return nil, episodes.ErrNotFound
}

// ----- GET /api/v5/calendar — path-alias empty-corpus shape -----

func TestListHandler_EmptyCorpus_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/calendar", nil)
	ListHandler(&fakeEpisodeLister{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	// Empty rows → `[]` per v3 handler's nil-normalisation; pin the
	// canonical empty-corpus shape.
	if body := rr.Body.String(); body != "[]" {
		t.Errorf("body should be `[]`; got %q", body)
	}
}

func TestListHandler_StatusAndContentTypeMatchV3(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/calendar", nil)
	ListHandler(&fakeEpisodeLister{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
}

// ----- GET /api/v5/calendar/{id} — inherited ByID -----

func TestByIDHandler_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/calendar/999", nil)
	req.SetPathValue("id", "999")
	ByIDHandler(&fakeEpisodeGetter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestByIDHandler_Found_Returns200(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/calendar/7", nil)
	req.SetPathValue("id", "7")
	ByIDHandler(&fakeEpisodeGetter{found: &episodes.Episode{ID: 7, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 3}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	var got Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("id: got %d want 7", got.ID)
	}
}

// ----- GET /feed/v5/calendar/sonarr.ics — iCal feed -----

func TestFeedHandler_EmptyCorpus_Returns200CalendarHeader(t *testing.T) {
	t.Parallel()
	// Empty episode list still emits a valid VCALENDAR envelope per
	// Ical.Net (just no VEVENTs). v3 handler shape is identical.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feed/v5/calendar/sonarr.ics", nil)
	FeedHandler(&fakeEpisodeLister{}, &fakeSeriesLister{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK && rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 200 or 204", rr.Code)
	}
	if rr.Code == http.StatusOK {
		body := rr.Body.String()
		// Pin the iCal envelope shape — both v3 and v5 emit a valid
		// VCALENDAR even for empty episode lists.
		if !strings.Contains(body, "BEGIN:VCALENDAR") {
			t.Errorf("body should contain VCALENDAR envelope; got %q", body[:min(200, len(body))])
		}
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/calendar") {
			t.Errorf("content-type: got %q want text/calendar", ct)
		}
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if ByIDHandler == nil || FeedHandler == nil {
		t.Error("handler aliases must be non-nil")
	}
	// ListHandler is now a func (J418 includeSubresources translation).
	if ListHandler(nil) == nil {
		t.Error("ListHandler must construct a handler")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	// Pure-alias slice: any drift between v5 calendar types and v3
	// types would fail to compile.
	var _ Resource = apiv3.EpisodeResource{}
	var _ EpisodeLister = (apiv3.CalendarEpisodeLister)(nil)
	var _ FeedSeriesLister = (apiv3.CalendarFeedSeriesLister)(nil)
	var _ EpisodeByIDGetter = (apiv3.EpisodeByIDGetter)(nil)
}
