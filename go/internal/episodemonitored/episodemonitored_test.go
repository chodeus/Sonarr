package episodemonitored_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodemonitored"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// ---- Stubs ----

type stubEpisodes struct {
	rows    []episodes.Episode
	getErr  error
	updated []episodes.Episode
	updErr  error
}

func (s *stubEpisodes) GetEpisodesBySeries(_ context.Context, _ int) ([]episodes.Episode, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	out := make([]episodes.Episode, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

func (s *stubEpisodes) UpdateMany(_ context.Context, list []*episodes.Episode) error {
	if s.updErr != nil {
		return s.updErr
	}
	for _, e := range list {
		s.updated = append(s.updated, *e)
	}
	return nil
}

type stubSeriesStore struct {
	persisted *series.Series
	err       error
}

func (s *stubSeriesStore) UpdateSeries(_ context.Context, ns *series.Series, _ series.UpdateOptions) (*series.Series, error) {
	if s.err != nil {
		return nil, s.err
	}
	cp := *ns
	s.persisted = &cp
	return &cp, nil
}

func ptrTime(t time.Time) *time.Time { return &t }
func ptrInt(v int) *int              { return &v }

func mustNewSvc(t *testing.T, eps *stubEpisodes, ss *stubSeriesStore, now time.Time) *episodemonitored.Service {
	t.Helper()
	svc, err := episodemonitored.NewService(eps, ss, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func makeSeriesWith(seasons []map[string]any, status int) *series.Series {
	raw, _ := json.Marshal(seasons)
	return &series.Series{ID: 42, Title: "Test", Status: status, Seasons: string(raw)}
}

func monitoredCount(eps []episodes.Episode) int {
	n := 0
	for _, e := range eps {
		if e.Monitored {
			n++
		}
	}
	return n
}

// ---- Constructor + early exits ----

func TestNewService_NilEpisodes_Errors(t *testing.T) {
	t.Parallel()
	if _, err := episodemonitored.NewService(nil, &stubSeriesStore{}, nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestNewService_NilSeries_Errors(t *testing.T) {
	t.Parallel()
	if _, err := episodemonitored.NewService(&stubEpisodes{}, nil, nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestSet_NilSeries_ReturnsError(t *testing.T) {
	t.Parallel()
	svc := mustNewSvc(t, &stubEpisodes{}, &stubSeriesStore{}, time.Now())
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestSet_NilOptions_JustPersistsSeries(t *testing.T) {
	t.Parallel()
	eps := &stubEpisodes{}
	ss := &stubSeriesStore{}
	svc := mustNewSvc(t, eps, ss, time.Now())
	ser := &series.Series{ID: 42, Title: "Test"}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if ss.persisted == nil || ss.persisted.ID != 42 {
		t.Errorf("UpdateSeries should fire with nil options")
	}
	if len(eps.updated) != 0 {
		t.Errorf("UpdateMany must not fire on nil options")
	}
}

func TestSet_Unknown_LogsAndNoOps(t *testing.T) {
	t.Parallel()
	eps := &stubEpisodes{}
	ss := &stubSeriesStore{}
	svc := mustNewSvc(t, eps, ss, time.Now())
	ser := &series.Series{ID: 42}
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorUnknown}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if ss.persisted != nil {
		t.Errorf("Unknown must not persist (legacy path deferred)")
	}
}

func TestSet_Skip_NoOps(t *testing.T) {
	t.Parallel()
	eps := &stubEpisodes{}
	ss := &stubSeriesStore{}
	svc := mustNewSvc(t, eps, ss, time.Now())
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorSkip}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), &series.Series{ID: 42}, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if ss.persisted != nil || len(eps.updated) != 0 {
		t.Errorf("Skip must not persist or update")
	}
}

// ---- Monitor branches ----

func newScenario(t *testing.T, now time.Time, rows []episodes.Episode) (*episodemonitored.Service, *stubEpisodes, *stubSeriesStore) {
	t.Helper()
	eps := &stubEpisodes{rows: rows}
	ss := &stubSeriesStore{}
	return mustNewSvc(t, eps, ss, now), eps, ss
}

func TestSet_All_MonitorsAllExceptSeason0(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 0, "monitored": false},
		{"seasonNumber": 1, "monitored": false},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 0, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 3, SeasonNumber: 1, EpisodeNumber: 2},
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorAll}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	// Season 0 ep stays unmonitored; the other two flip to monitored.
	got := monitoredCount(eps.updated)
	if got != 2 {
		t.Errorf("monitored count: got %d, want 2 (season 0 excluded)", got)
	}
	if eps.updated[0].Monitored {
		t.Errorf("season 0 episode must not be monitored")
	}
}

func TestSet_Future_KeepsFutureAndNoAirDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{{"seasonNumber": 1}}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -10))}, // past → unmonitored
		{ID: 2, SeasonNumber: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, 10))},  // future → monitored
		{ID: 3, SeasonNumber: 1, AirDateUtc: nil},                             // unknown → monitored
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorFuture}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if eps.updated[0].Monitored {
		t.Errorf("past episode must NOT be monitored")
	}
	if !eps.updated[1].Monitored {
		t.Errorf("future episode must be monitored")
	}
	if !eps.updated[2].Monitored {
		t.Errorf("nil-AirDate episode must be monitored")
	}
}

func TestSet_Missing_MonitorsOnlyMissingFiles(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{{"seasonNumber": 1}}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeFileID: ptrInt(100)}, // has file
		{ID: 2, SeasonNumber: 1, EpisodeFileID: nil},         // missing
		{ID: 3, SeasonNumber: 1, EpisodeFileID: ptrInt(0)},   // zero-pointee = missing
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorMissing}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if eps.updated[0].Monitored {
		t.Errorf("ID=1 has file — must NOT be monitored under Missing")
	}
	if !eps.updated[1].Monitored {
		t.Errorf("ID=2 has nil file — must be monitored under Missing")
	}
	if !eps.updated[2].Monitored {
		t.Errorf("ID=3 has zero-pointee file — must be monitored under Missing")
	}
}

func TestSet_Existing_InverseOfMissing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{{"seasonNumber": 1}}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeFileID: ptrInt(100)},
		{ID: 2, SeasonNumber: 1, EpisodeFileID: nil},
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorExisting}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !eps.updated[0].Monitored {
		t.Errorf("ID=1 has file — must be monitored under Existing")
	}
	if eps.updated[1].Monitored {
		t.Errorf("ID=2 has no file — must NOT be monitored under Existing")
	}
}

func TestSet_Pilot_OnlyMonitorsS1E1AndUnmonitorsFirstSeason(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 1, "monitored": true},
		{"seasonNumber": 2, "monitored": true},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 2},
		{ID: 3, SeasonNumber: 2, EpisodeNumber: 1},
	}
	svc, eps, ss := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorPilot}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !eps.updated[0].Monitored {
		t.Errorf("S1E1 pilot must be monitored")
	}
	if eps.updated[1].Monitored || eps.updated[2].Monitored {
		t.Errorf("non-pilot episodes must not be monitored")
	}
	// Pilot cascade: first season explicitly unmonitored (C# :153-157).
	var persistedSeasons []map[string]any
	_ = json.Unmarshal([]byte(ss.persisted.Seasons), &persistedSeasons)
	if persistedSeasons[0]["monitored"].(bool) {
		t.Errorf("season 1 must be unmonitored under Pilot (don't monitor whole season for just the pilot)")
	}
}

func TestSet_FirstSeason_MonitorsAllOfSeason1(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 1},
		{"seasonNumber": 2},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 2},
		{ID: 3, SeasonNumber: 2, EpisodeNumber: 1},
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorFirstSeason}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !eps.updated[0].Monitored || !eps.updated[1].Monitored {
		t.Errorf("season 1 episodes must be monitored")
	}
	if eps.updated[2].Monitored {
		t.Errorf("season 2 episode must NOT be monitored")
	}
}

func TestSet_LastSeason_MonitorsAllOfHighestSeason(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 1},
		{"seasonNumber": 2},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 2, EpisodeNumber: 1},
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorLastSeason}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if eps.updated[0].Monitored {
		t.Errorf("season 1 must NOT be monitored under LastSeason")
	}
	if !eps.updated[1].Monitored {
		t.Errorf("season 2 (last) must be monitored")
	}
}

func TestSet_Recent_MonitorsPast90DaysFutureAndNoAirDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{{"seasonNumber": 1}}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -30))},  // within 90d → monitored
		{ID: 2, SeasonNumber: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -120))}, // beyond 90d → unmonitored
		{ID: 3, SeasonNumber: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, 10))},   // future → monitored
		{ID: 4, SeasonNumber: 1, AirDateUtc: nil},                              // unknown → monitored
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorRecent}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !eps.updated[0].Monitored {
		t.Errorf("within 90d episode must be monitored")
	}
	if eps.updated[1].Monitored {
		t.Errorf("beyond 90d episode must NOT be monitored")
	}
	if !eps.updated[2].Monitored {
		t.Errorf("future episode must be monitored")
	}
	if !eps.updated[3].Monitored {
		t.Errorf("nil-AirDate episode must be monitored")
	}
}

func TestSet_MonitorSpecials_OnlyTogglesSeason0(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 0, "monitored": false},
		{"seasonNumber": 1, "monitored": true},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 0, Monitored: false},
		{ID: 2, SeasonNumber: 1, Monitored: true}, // must stay true
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorMonitorSpecials}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !eps.updated[0].Monitored {
		t.Errorf("season 0 episode must become monitored")
	}
	if !eps.updated[1].Monitored {
		t.Errorf("season 1 episode (already monitored) must stay monitored")
	}
}

func TestSet_UnmonitorSpecials_OnlyTogglesSeason0(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 0, "monitored": true},
		{"seasonNumber": 1, "monitored": true},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 0, Monitored: true},
		{ID: 2, SeasonNumber: 1, Monitored: true},
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorUnmonitorSpecials}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if eps.updated[0].Monitored {
		t.Errorf("season 0 episode must become unmonitored")
	}
	if !eps.updated[1].Monitored {
		t.Errorf("season 1 episode (already monitored) must stay monitored")
	}
}

func TestSet_None_UnmonitorsEverything(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 0, "monitored": true},
		{"seasonNumber": 1, "monitored": true},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 0, Monitored: true},
		{ID: 2, SeasonNumber: 1, Monitored: true},
	}
	svc, eps, _ := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorNone}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	if eps.updated[0].Monitored || eps.updated[1].Monitored {
		t.Errorf("None must unmonitor every episode regardless of season")
	}
}

// ---- Season cascade ----

func TestSet_All_ContinuingSeries_LastSeasonAlwaysMonitored(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 1, "monitored": false},
		{"seasonNumber": 2, "monitored": false},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 2, EpisodeNumber: 1},
	}
	svc, _, ss := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorAll}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	var s []map[string]any
	_ = json.Unmarshal([]byte(ss.persisted.Seasons), &s)
	if !s[1]["monitored"].(bool) {
		t.Errorf("All + last season must be true in cascade")
	}
}

func TestSet_Future_ContinuingSeries_LastSeasonMonitored(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 1, "monitored": false},
		{"seasonNumber": 2, "monitored": false},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -30))},
		{ID: 2, SeasonNumber: 2, EpisodeNumber: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, 30))},
	}
	svc, _, ss := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorFuture}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	var s []map[string]any
	_ = json.Unmarshal([]byte(ss.persisted.Seasons), &s)
	if !s[1]["monitored"].(bool) {
		t.Errorf("Future + continuing series → last season must be monitored")
	}
}

func TestSet_Future_EndedSeries_LastSeasonNotForced(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 1, "monitored": false},
	}, series.SeriesStatusEnded)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -30))}, // past → unmonitored
	}
	svc, _, ss := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorFuture}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	var s []map[string]any
	_ = json.Unmarshal([]byte(ss.persisted.Seasons), &s)
	if s[0]["monitored"].(bool) {
		t.Errorf("Future + ended series → last season must NOT be forced monitored (no episodes monitored either)")
	}
}

func TestSet_SeasonInheritsAnyMonitoredEpisode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := makeSeriesWith([]map[string]any{
		{"seasonNumber": 1, "monitored": false},
	}, series.SeriesStatusContinuing)
	rows := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeFileID: nil}, // missing → monitored under Missing
	}
	svc, _, ss := newScenario(t, now, rows)
	opts := &episodemonitored.MonitoringOptions{Monitor: series.MonitorMissing}
	if err := svc.SetEpisodeMonitoredStatus(t.Context(), ser, opts); err != nil {
		t.Fatalf("err: %v", err)
	}
	var s []map[string]any
	_ = json.Unmarshal([]byte(ss.persisted.Seasons), &s)
	if !s[0]["monitored"].(bool) {
		t.Errorf("season with any monitored episode → season Monitored=true cascade")
	}
}
