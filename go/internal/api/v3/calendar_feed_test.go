package v3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

type fakeCalendarFeedSeries struct {
	rows []series.Series
	err  error
}

func (f *fakeCalendarFeedSeries) GetAllSeries(_ context.Context) ([]series.Series, error) {
	return f.rows, f.err
}

type fakeCalendarEpisodeLister struct {
	rows []episodes.Episode
	err  error
}

func (f *fakeCalendarEpisodeLister) EpisodesBetweenDates(_ context.Context, _, _ time.Time, _, _ bool) ([]episodes.Episode, error) {
	return f.rows, f.err
}

func airDate(s string) *time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return &t
}

func TestCalendarFeedHandler_EmptyMinimalCalendar(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CalendarFeedHandler(&fakeCalendarEpisodeLister{}, &fakeCalendarFeedSeries{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/feed/v3/calendar/Sonarr.ics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/calendar; charset=utf-8" {
		t.Errorf("Content-Type: %q", got)
	}
	body := rr.Body.String()
	required := []string{
		"BEGIN:VCALENDAR\r\n",
		"PRODID:-//sonarr.tv//Sonarr//EN\r\n",
		"VERSION:2.0\r\n",
		"NAME:Sonarr TV Schedule\r\n",
		"X-WR-CALNAME:Sonarr TV Schedule\r\n",
		"END:VCALENDAR\r\n",
	}
	for _, r := range required {
		if !strings.Contains(body, r) {
			t.Errorf("missing %q in body: %s", r, body)
		}
	}
}

func TestCalendarFeedHandler_EmitsVEventPerEpisode(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	episodeFileID := 99
	srSvc := &fakeCalendarFeedSeries{rows: []series.Series{
		{ID: 1, Title: "My Show", Network: "HBO", Runtime: 60, SeriesType: 0},
	}}
	epSvc := &fakeCalendarEpisodeLister{rows: []episodes.Episode{
		{
			ID:            10,
			SeriesID:      1,
			SeasonNumber:  2,
			EpisodeNumber: 3,
			Title:         "The Title",
			Overview:      "Plot summary.",
			AirDateUtc:    airDate("2026-05-18T12:00:00Z"),
			EpisodeFileID: &episodeFileID,
		},
	}}
	CalendarFeedHandler(epSvc, srSvc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/feed/v3/calendar/Sonarr.ics", nil))
	body := rr.Body.String()
	want := []string{
		"BEGIN:VEVENT\r\n",
		"UID:NzbDrone_episode_10\r\n",
		"STATUS:CONFIRMED\r\n",
		"DESCRIPTION:Plot summary.\r\n",
		"CATEGORIES:HBO\r\n",
		"DTSTART:20260518T120000Z\r\n",
		"DTEND:20260518T130000Z\r\n", // +60 minutes
		"SUMMARY:My Show - 2x03 - The Title\r\n",
		"END:VEVENT\r\n",
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in body:\n%s", w, body)
		}
	}
}

func TestCalendarFeedHandler_TentativeWhenNoFile(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	srSvc := &fakeCalendarFeedSeries{rows: []series.Series{{ID: 1, Title: "Show", Runtime: 30}}}
	epSvc := &fakeCalendarEpisodeLister{rows: []episodes.Episode{
		{ID: 1, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", AirDateUtc: airDate("2026-05-18T12:00:00Z")},
	}}
	CalendarFeedHandler(epSvc, srSvc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/feed/v3/calendar/Sonarr.ics", nil))
	if !strings.Contains(rr.Body.String(), "STATUS:TENTATIVE\r\n") {
		t.Errorf("expected TENTATIVE; body=%s", rr.Body.String())
	}
}

func TestCalendarFeedHandler_DailySeriesSkipsSxxEyy(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	srSvc := &fakeCalendarFeedSeries{rows: []series.Series{
		{ID: 1, Title: "Daily Show", Runtime: 30, SeriesType: seriesTypeDaily},
	}}
	epSvc := &fakeCalendarEpisodeLister{rows: []episodes.Episode{
		{ID: 1, SeriesID: 1, SeasonNumber: 2026, EpisodeNumber: 138, Title: "Monday", AirDateUtc: airDate("2026-05-18T12:00:00Z")},
	}}
	CalendarFeedHandler(epSvc, srSvc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/feed/v3/calendar/Sonarr.ics", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "SUMMARY:Daily Show - Monday\r\n") {
		t.Errorf("daily summary: %s", body)
	}
	if strings.Contains(body, "2026x") {
		t.Errorf("daily series should not include SxxEyy form: %s", body)
	}
}

func TestCalendarFeedHandler_PremieresOnly(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	srSvc := &fakeCalendarFeedSeries{rows: []series.Series{{ID: 1, Title: "Show", Runtime: 30}}}
	epSvc := &fakeCalendarEpisodeLister{rows: []episodes.Episode{
		{ID: 1, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", AirDateUtc: airDate("2026-05-18T12:00:00Z")},
		{ID: 2, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2, Title: "Two", AirDateUtc: airDate("2026-05-19T12:00:00Z")},
		{ID: 3, SeriesID: 1, SeasonNumber: 0, EpisodeNumber: 1, Title: "Special", AirDateUtc: airDate("2026-05-17T12:00:00Z")},
	}}
	CalendarFeedHandler(epSvc, srSvc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/feed/v3/calendar/Sonarr.ics?premieresOnly=true", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "UID:NzbDrone_episode_1\r\n") {
		t.Errorf("Pilot should remain: %s", body)
	}
	if strings.Contains(body, "UID:NzbDrone_episode_2\r\n") {
		t.Errorf("S01E02 should be filtered out (premieresOnly): %s", body)
	}
	if strings.Contains(body, "UID:NzbDrone_episode_3\r\n") {
		t.Errorf("S00E01 should be filtered out (specials): %s", body)
	}
}

func TestCalendarFeedHandler_AsAllDayUsesDate(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	srSvc := &fakeCalendarFeedSeries{rows: []series.Series{{ID: 1, Title: "Show", Runtime: 30}}}
	epSvc := &fakeCalendarEpisodeLister{rows: []episodes.Episode{
		{ID: 1, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", AirDateUtc: airDate("2026-05-18T12:00:00Z")},
	}}
	CalendarFeedHandler(epSvc, srSvc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/feed/v3/calendar/Sonarr.ics?asAllDay=true", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "DTSTART;VALUE=DATE:20260518\r\n") {
		t.Errorf("asAllDay should emit DATE form; body=%s", body)
	}
	if strings.Contains(body, "DTEND:") {
		t.Errorf("asAllDay should not emit DTEND; body=%s", body)
	}
}

func TestEscapeICalText(t *testing.T) {
	t.Parallel()
	got := escapeICalText("a,b; c\nd\\e")
	want := `a\,b\; c\nd\\e`
	if got != want {
		t.Errorf("escape: got %q want %q", got, want)
	}
}
