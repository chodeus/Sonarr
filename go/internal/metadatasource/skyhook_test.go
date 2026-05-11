package metadatasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/series"
)

// fakeDailySeries lets each test scope opt a TVDB id in or out of
// the SeriesType=Daily override. The C# tree wires a real
// IDailySeriesService backed by the DailySeries persistence table;
// the Go-port tests use the narrow interface directly.
type fakeDailySeries struct {
	dailyIDs map[int]bool
}

func (f *fakeDailySeries) IsDailySeries(tvdbID int) bool {
	return f.dailyIDs[tvdbID]
}

// fixtureShow returns a populated ShowResource that exercises every
// nullable + sub-object branch in MapSeries. Used by the
// GetSeriesInfo + standalone-mapSeries test cases below.
func fixtureShow() ShowResource {
	tvRage, tvMaze, tmdb, runtime := 18164, 169, 1396, 47
	return ShowResource{
		TvdbID:           81189,
		Title:            "Breaking Bad",
		Overview:         "A high school chemistry teacher diagnosed with cancer turns to cooking meth.",
		Slug:             "breaking-bad",
		FirstAired:       "2008-01-20",
		LastAired:        "2013-09-29",
		TvRageID:         &tvRage,
		TvMazeID:         &tvMaze,
		TmdbID:           &tmdb,
		MalIDs:           []int{9876, 9877},
		AniListIDs:       []int{55555},
		Status:           "ended",
		Runtime:          &runtime,
		TimeOfDay:        &TimeOfDayResource{Hours: 22, Minutes: 5},
		Network:          "AMC",
		ImdbID:           "tt0903747",
		OriginalLanguage: "eng",
		OriginalCountry:  "usa",
		Actors: []ActorResource{
			{Name: "Bryan Cranston", Character: "Walter White", Image: "https://image/wc.jpg"},
			{Name: "Anna Gunn", Character: "Skyler White", Image: ""}, // exercise no-image actor
		},
		Genres:        []string{"Drama", "Thriller"},
		ContentRating: "tv-ma",
		Rating:        &RatingResource{Count: 12345, Value: 9.4},
		Images: []ImageResource{
			{CoverType: "poster", URL: "https://image/poster.jpg"},
			{CoverType: "fanart", URL: "https://image/fanart.jpg"},
			{CoverType: "weird", URL: "https://image/weird.jpg"}, // unknown maps to Unknown
		},
		Seasons: []SeasonResource{
			{SeasonNumber: 0, Images: []ImageResource{}},                                                   // specials unmonitored
			{SeasonNumber: 1, Images: []ImageResource{{CoverType: "poster", URL: "https://image/s1.jpg"}}}, // monitored
		},
		Episodes: []EpisodeResource{
			{
				TvdbID:                349231,
				SeasonNumber:          1,
				EpisodeNumber:         1,
				AbsoluteEpisodeNumber: intPtr(1),
				Title:                 "Pilot",
				AirDate:               "2008-01-20",
				AirDateUtc:            timePtr(time.Date(2008, 1, 21, 3, 0, 0, 0, time.UTC)),
				Runtime:               58,
				FinaleType:            "",
				Rating:                &RatingResource{Count: 100, Value: 8.7},
				Overview:              "Walt cooks his first batch.",
				Image:                 "https://image/ep1.jpg",
			},
		},
	}
}

func intPtr(v int) *int              { return &v }
func timePtr(t time.Time) *time.Time { return &t }

// fixtureHandler returns an http.HandlerFunc that serves `show` at
// `/shows/en/<TvdbID>/` and 404s elsewhere. The path discipline
// matches the C# request-builder template.
func fixtureHandler(t *testing.T, show ShowResource) http.HandlerFunc {
	t.Helper()
	expectedPath := fmt.Sprintf("/shows/en/%d/", show.TvdbID)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept header = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(show)
	}
}

func newProxyForServer(t *testing.T, srv *httptest.Server, daily DailySeriesService) *Proxy {
	t.Helper()
	p, err := NewProxy(srv.Client(), srv.URL, daily, nil, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return p
}

// TestProxy_GetSeriesInfo_HappyPath round-trips the full fixture
// through GetSeriesInfo and asserts every series.Series + first
// episodes.Episode field maps per C# `SkyHookProxy.cs:43-71`.
func TestProxy_GetSeriesInfo_HappyPath(t *testing.T) {
	show := fixtureShow()
	srv := httptest.NewServer(fixtureHandler(t, show))
	defer srv.Close()

	daily := &fakeDailySeries{dailyIDs: map[int]bool{}}
	p := newProxyForServer(t, srv, daily)

	s, eps, err := p.GetSeriesInfo(context.Background(), show.TvdbID)
	if err != nil {
		t.Fatalf("GetSeriesInfo: %v", err)
	}

	if s.TvdbID != 81189 || s.Title != "Breaking Bad" || s.TvRageID != 18164 ||
		s.TvMazeID != 169 || s.TmdbID != 1396 || s.ImdbID != "tt0903747" ||
		s.Network != "AMC" || s.OriginalCountry != "usa" || s.TitleSlug != "breaking-bad" {
		t.Errorf("series scalar fields wrong: %+v", s)
	}
	if s.Runtime != 47 {
		t.Errorf("Runtime = %d, want 47", s.Runtime)
	}
	if s.Year == nil || *s.Year != 2008 {
		t.Errorf("Year = %v, want *2008", s.Year)
	}
	if s.FirstAired == nil || s.FirstAired.Year() != 2008 ||
		s.FirstAired.Month() != time.January || s.FirstAired.Day() != 20 {
		t.Errorf("FirstAired = %v, want 2008-01-20 UTC", s.FirstAired)
	}
	if s.LastAired == nil || s.LastAired.Year() != 2013 {
		t.Errorf("LastAired = %v, want 2013-09-29 UTC", s.LastAired)
	}
	if s.AirTime != "22:05" {
		t.Errorf("AirTime = %q, want 22:05", s.AirTime)
	}
	if s.Status != series.SeriesStatusEnded {
		t.Errorf("Status = %d, want %d (Ended)", s.Status, series.SeriesStatusEnded)
	}
	if s.SeriesType != series.SeriesTypeStandard {
		t.Errorf("SeriesType = %d, want %d (Standard) — not a daily series here",
			s.SeriesType, series.SeriesTypeStandard)
	}
	if s.Certification != "TV-MA" {
		t.Errorf("Certification = %q, want TV-MA (uppercased per C# `:246`)", s.Certification)
	}
	if s.OriginalLanguage != language.English.ID {
		t.Errorf("OriginalLanguage = %d, want %d (English from eng ISO)",
			s.OriginalLanguage, language.English.ID)
	}
	if !s.Monitored {
		t.Errorf("Monitored = false, want true (C# `:257` default)")
	}
	if got, want := s.MalIDs, []int{9876, 9877}; !equalIntSlice(got, want) {
		t.Errorf("MalIDs = %v, want %v", got, want)
	}
	if got, want := s.AniListIDs, []int{55555}; !equalIntSlice(got, want) {
		t.Errorf("AniListIDs = %v, want %v", got, want)
	}

	// Embedded-JSON columns — verify by decoding what mapSeries wrote.
	var ratings ratingsPersist
	if err := json.Unmarshal([]byte(s.Ratings), &ratings); err != nil {
		t.Fatalf("Ratings JSON unmarshal: %v", err)
	}
	if ratings.Votes != 12345 || ratings.Value != 9.4 {
		t.Errorf("Ratings = %+v, want {Votes:12345 Value:9.4}", ratings)
	}

	var genres []string
	if err := json.Unmarshal([]byte(s.Genres), &genres); err != nil {
		t.Fatalf("Genres JSON unmarshal: %v", err)
	}
	if len(genres) != 2 || genres[0] != "Drama" || genres[1] != "Thriller" {
		t.Errorf("Genres = %v", genres)
	}

	var actors []actorPersist
	if err := json.Unmarshal([]byte(s.Actors), &actors); err != nil {
		t.Fatalf("Actors JSON unmarshal: %v", err)
	}
	if len(actors) != 2 {
		t.Fatalf("Actors length = %d, want 2", len(actors))
	}
	if actors[0].Name != "Bryan Cranston" || actors[0].Character != "Walter White" {
		t.Errorf("actors[0] = %+v", actors[0])
	}
	if len(actors[0].Images) != 1 || actors[0].Images[0].CoverType != mediacover.CoverTypeHeadshot {
		t.Errorf("actors[0].Images = %+v", actors[0].Images)
	}
	if len(actors[1].Images) != 0 {
		t.Errorf("actors[1].Images = %+v, want empty (no upstream image)", actors[1].Images)
	}

	var seasons []seasonPersist
	if err := json.Unmarshal([]byte(s.Seasons), &seasons); err != nil {
		t.Fatalf("Seasons JSON unmarshal: %v", err)
	}
	if len(seasons) != 2 {
		t.Fatalf("Seasons length = %d, want 2", len(seasons))
	}
	if seasons[0].SeasonNumber != 0 || seasons[0].Monitored {
		t.Errorf("season 0 = %+v, want SeasonNumber=0 Monitored=false (specials per C# `:316`)", seasons[0])
	}
	if seasons[1].SeasonNumber != 1 || !seasons[1].Monitored {
		t.Errorf("season 1 = %+v, want SeasonNumber=1 Monitored=true", seasons[1])
	}

	var covers []mediacover.Cover
	if err := json.Unmarshal([]byte(s.Images), &covers); err != nil {
		t.Fatalf("Images JSON unmarshal: %v", err)
	}
	if len(covers) != 3 {
		t.Fatalf("Images length = %d, want 3", len(covers))
	}
	if covers[0].CoverType != mediacover.CoverTypePoster ||
		covers[1].CoverType != mediacover.CoverTypeFanart ||
		covers[2].CoverType != mediacover.CoverTypeUnknown {
		t.Errorf("covers = %+v (want poster, fanart, unknown)", covers)
	}

	// Episode pass.
	if len(eps) != 1 {
		t.Fatalf("episodes length = %d, want 1", len(eps))
	}
	ep := eps[0]
	if ep.TvdbID == nil || *ep.TvdbID != 349231 {
		t.Errorf("ep.TvdbID = %v, want *349231", ep.TvdbID)
	}
	if ep.SeasonNumber != 1 || ep.EpisodeNumber != 1 || ep.Title != "Pilot" {
		t.Errorf("ep IDs = %+v", ep)
	}
	if ep.AirDate != "2008-01-20" {
		t.Errorf("ep.AirDate = %q, want 2008-01-20", ep.AirDate)
	}
	if ep.AirDateUtc == nil || ep.AirDateUtc.Year() != 2008 {
		t.Errorf("ep.AirDateUtc = %v", ep.AirDateUtc)
	}
	if ep.Runtime != 58 {
		t.Errorf("ep.Runtime = %d, want 58", ep.Runtime)
	}
	if ep.AbsoluteEpisodeNumber == nil || *ep.AbsoluteEpisodeNumber != 1 {
		t.Errorf("ep.AbsoluteEpisodeNumber = %v", ep.AbsoluteEpisodeNumber)
	}
	var epCovers []mediacover.Cover
	if err := json.Unmarshal([]byte(ep.Images), &epCovers); err != nil {
		t.Fatalf("ep.Images JSON unmarshal: %v", err)
	}
	if len(epCovers) != 1 || epCovers[0].CoverType != mediacover.CoverTypeScreenshot {
		t.Errorf("ep.Images = %+v, want [Screenshot]", epCovers)
	}
}

// TestProxy_GetSeriesInfo_DailySeriesFlagsType verifies the
// `SeriesType = Daily` override fires when DailySeriesService says so.
func TestProxy_GetSeriesInfo_DailySeriesFlagsType(t *testing.T) {
	show := fixtureShow()
	srv := httptest.NewServer(fixtureHandler(t, show))
	defer srv.Close()

	daily := &fakeDailySeries{dailyIDs: map[int]bool{81189: true}}
	p := newProxyForServer(t, srv, daily)

	s, _, err := p.GetSeriesInfo(context.Background(), show.TvdbID)
	if err != nil {
		t.Fatalf("GetSeriesInfo: %v", err)
	}
	if s.SeriesType != series.SeriesTypeDaily {
		t.Errorf("SeriesType = %d, want %d (Daily)", s.SeriesType, series.SeriesTypeDaily)
	}
}

// TestProxy_GetSeriesInfo_NilDailyServiceTreatsAsStandard exercises
// the nil-tolerant DailySeriesService path.
func TestProxy_GetSeriesInfo_NilDailyServiceTreatsAsStandard(t *testing.T) {
	show := fixtureShow()
	srv := httptest.NewServer(fixtureHandler(t, show))
	defer srv.Close()

	p, err := NewProxy(srv.Client(), srv.URL, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	s, _, err := p.GetSeriesInfo(context.Background(), show.TvdbID)
	if err != nil {
		t.Fatalf("GetSeriesInfo: %v", err)
	}
	if s.SeriesType != series.SeriesTypeStandard {
		t.Errorf("SeriesType = %d, want %d (Standard)", s.SeriesType, series.SeriesTypeStandard)
	}
}

// TestProxy_GetSeriesInfo_NotFoundMapsToSentinel exercises the 404
// branch — must wrap [ErrSeriesNotFound] so the Phase-5 API layer
// can map to HTTP 404.
func TestProxy_GetSeriesInfo_NotFoundMapsToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := newProxyForServer(t, srv, nil)
	_, _, err := p.GetSeriesInfo(context.Background(), 999)
	if err == nil {
		t.Fatal("err = nil, want ErrSeriesNotFound")
	}
	if !errors.Is(err, ErrSeriesNotFound) {
		t.Errorf("err = %v, want wraps ErrSeriesNotFound", err)
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("err.Error() = %q, want tvdbId 999 in message", err.Error())
	}
}

// TestProxy_GetSeriesInfo_ServerErrorMapsToServiceUnavailable
// exercises the 5xx branch — must wrap [ErrServiceUnavailable] so
// the Phase-5 API layer can map to HTTP 503.
func TestProxy_GetSeriesInfo_ServerErrorMapsToServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newProxyForServer(t, srv, nil)
	_, _, err := p.GetSeriesInfo(context.Background(), 81189)
	if err == nil {
		t.Fatal("err = nil, want ErrServiceUnavailable")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want wraps ErrServiceUnavailable", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err.Error() = %q, want status 500 in message", err.Error())
	}
}

// TestProxy_GetSeriesInfo_DecodeErrorMapsToServiceUnavailable
// exercises the JSON-decode-failure branch.
func TestProxy_GetSeriesInfo_DecodeErrorMapsToServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"this is not valid JSON`))
	}))
	defer srv.Close()

	p := newProxyForServer(t, srv, nil)
	_, _, err := p.GetSeriesInfo(context.Background(), 81189)
	if err == nil {
		t.Fatal("err = nil, want decode error")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want wraps ErrServiceUnavailable", err)
	}
}

// TestProxy_GetSeriesInfo_ContextCancellationPropagates verifies
// ctx.Err() propagates verbatim (no sentinel wrapping). The Phase-5
// API layer can then distinguish "user cancelled" from "SkyHook
// unreachable".
func TestProxy_GetSeriesInfo_ContextCancellationPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second) // longer than ctx deadline
	}))
	defer srv.Close()

	p := newProxyForServer(t, srv, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := p.GetSeriesInfo(ctx, 81189)
	if err == nil {
		t.Fatal("err = nil, want ctx deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

// TestNewProxy_RejectsNil + EmptyBaseURL covers the constructor guards.
func TestNewProxy_RejectsNil(t *testing.T) {
	cases := []struct {
		name   string
		client *http.Client
		base   string
	}{
		{"nil_client", nil, "https://skyhook.sonarr.tv/v1/tvdb"},
		{"empty_base", &http.Client{}, ""},
		{"whitespace_base", &http.Client{}, "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewProxy(tc.client, tc.base, nil, nil, nil); err == nil {
				t.Errorf("NewProxy(%v, %q) = no err, want guard rejection", tc.client, tc.base)
			}
		})
	}
}

// TestMapSeriesStatus covers the three documented branches plus the
// case-insensitive "default branches to Continuing" fall-through.
func TestMapSeriesStatus(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"ended", series.SeriesStatusEnded},
		{"ENDED", series.SeriesStatusEnded},
		{"upcoming", series.SeriesStatusUpcoming},
		{"Upcoming", series.SeriesStatusUpcoming},
		{"continuing", series.SeriesStatusContinuing},
		{"airing", series.SeriesStatusContinuing}, // default branch
		{"", series.SeriesStatusContinuing},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := mapSeriesStatus(tc.in); got != tc.want {
				t.Errorf("mapSeriesStatus(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestMapCoverType covers each named-mapping + the unknown fallback.
func TestMapCoverType(t *testing.T) {
	cases := []struct {
		in   string
		want mediacover.CoverType
	}{
		{"poster", mediacover.CoverTypePoster},
		{"POSTER", mediacover.CoverTypePoster},
		{"banner", mediacover.CoverTypeBanner},
		{"fanart", mediacover.CoverTypeFanart},
		{"clearlogo", mediacover.CoverTypeClearlogo},
		{"unknown", mediacover.CoverTypeUnknown},
		{"weird-tag", mediacover.CoverTypeUnknown},
		{"", mediacover.CoverTypeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := mapCoverType(tc.in); got != tc.want {
				t.Errorf("mapCoverType(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestMapOriginalLanguage covers the three documented branches.
func TestMapOriginalLanguage(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want language.Language
	}{
		{"empty_falls_to_english", "", language.English},
		{"whitespace_falls_to_english", "   ", language.English},
		{"three_letter_eng_to_english", "eng", language.English},
		{"three_letter_spa_to_spanish", "spa", language.Spanish},
		{"three_letter_unknown_falls_to_english", "xyz", language.English},
		{"upper_case_is_lowered", "ENG", language.English},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapOriginalLanguage(tc.in); got != tc.want {
				t.Errorf("mapOriginalLanguage(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestMapRatings covers the nil-input path (zero Ratings) + happy path.
func TestMapRatings(t *testing.T) {
	if got := mapRatings(nil); got.Votes != 0 || got.Value != 0.0 {
		t.Errorf("mapRatings(nil) = %+v, want zero", got)
	}
	got := mapRatings(&RatingResource{Count: 100, Value: 7.5})
	if got.Votes != 100 || got.Value != 7.5 {
		t.Errorf("mapRatings({100, 7.5}) = %+v", got)
	}
}

// TestParseSkyHookDate covers the formatting branches.
func TestParseSkyHookDate(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantOK   bool
		wantYear int
	}{
		{"happy", "2008-01-20", true, 2008},
		{"empty", "", false, 0},
		{"whitespace", "   ", false, 0},
		{"unparseable", "not-a-date", false, 0},
		{"wrong_format", "01/20/2008", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := parseSkyHookDate(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && d.Year() != tc.wantYear {
				t.Errorf("year = %d, want %d", d.Year(), tc.wantYear)
			}
		})
	}
}

// TestMapSeries_OmittedNullables covers the "all-pointers-nil" path —
// a minimal ShowResource where TvRageID/TvMazeID/TmdbID/Runtime/
// TimeOfDay/Rating/FirstAired/LastAired/Genres/Actors/Seasons/Images/
// ContentRating are all absent. The map pass should produce a
// well-formed series.Series with all the corresponding fields at
// their Go zero values (and the embedded-JSON columns at "[]" / "{}").
func TestMapSeries_OmittedNullables(t *testing.T) {
	show := &ShowResource{
		TvdbID: 42,
		Title:  "Minimal",
	}
	s, err := mapSeries(show, nil)
	if err != nil {
		t.Fatalf("mapSeries: %v", err)
	}
	if s.TvdbID != 42 || s.Title != "Minimal" {
		t.Errorf("scalar mismatch: %+v", s)
	}
	if s.TvRageID != 0 || s.TvMazeID != 0 || s.TmdbID != 0 {
		t.Errorf("ID ints not zero: %+v", s)
	}
	if s.FirstAired != nil || s.LastAired != nil {
		t.Errorf("dates = %v / %v, want both nil", s.FirstAired, s.LastAired)
	}
	if s.Year != nil {
		t.Errorf("Year = %v, want nil (no FirstAired so Year stays nil)", s.Year)
	}
	if s.AirTime != "" {
		t.Errorf("AirTime = %q, want empty (no TimeOfDay)", s.AirTime)
	}
	if s.Certification != "" {
		t.Errorf("Certification = %q, want empty (no ContentRating)", s.Certification)
	}
	if s.OriginalLanguage != language.English.ID {
		t.Errorf("OriginalLanguage = %d, want %d (English fallback for empty code)",
			s.OriginalLanguage, language.English.ID)
	}
	// Embedded-JSON columns should marshal to non-nil "[]" or "{}":
	if s.Ratings == "" || s.Genres == "" || s.Actors == "" || s.Seasons == "" || s.Images == "" {
		t.Errorf("embedded-JSON columns must not be empty strings: ratings=%q genres=%q actors=%q seasons=%q images=%q",
			s.Ratings, s.Genres, s.Actors, s.Seasons, s.Images)
	}
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
