package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// ---- Stubs ----

type ssStubSeriesLookup struct {
	rows map[int]*series.Series
	err  error
}

func (s *ssStubSeriesLookup) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows[id], nil
}

type ssStubEpisodesLister struct {
	rows []episodes.Episode
	err  error
}

func (s *ssStubEpisodesLister) GetEpisodesBySeries(_ context.Context, _ int) ([]episodes.Episode, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]episodes.Episode, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

type ssStubEpisodeSearcher struct {
	calls    []int
	decision int
	err      error
}

func (s *ssStubEpisodeSearcher) EpisodeSearch(_ context.Context, episodeID int) (int, error) {
	s.calls = append(s.calls, episodeID)
	if s.err != nil {
		return 0, s.err
	}
	return s.decision, nil
}

type ssStubSeasonSearcher struct {
	calls []struct {
		seriesID, seasonNumber int
		missingOnly            bool
	}
	decision int
	err      error
}

func (s *ssStubSeasonSearcher) SeasonSearch(_ context.Context, seriesID, seasonNumber int, missingOnly bool) (int, error) {
	s.calls = append(s.calls, struct {
		seriesID, seasonNumber int
		missingOnly            bool
	}{seriesID, seasonNumber, missingOnly})
	if s.err != nil {
		return 0, s.err
	}
	return s.decision, nil
}

func ptrTime(t time.Time) *time.Time { return &t }
func ptrInt(v int) *int              { return &v }

func mustSeasonsJSON(t *testing.T, rows []map[string]any) string {
	t.Helper()
	buf, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(buf)
}

func mustNewSeriesSearch(t *testing.T, sl *ssStubSeriesLookup, el *ssStubEpisodesLister, es *ssStubEpisodeSearcher, ss *ssStubSeasonSearcher) *handlers.SeriesSearch {
	t.Helper()
	h := handlers.NewSeriesSearch(sl, el, es, ss, nil)
	h.SetClock(func() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) })
	return h
}

// ---- Tests ----

func TestSeriesSearch_NilDeps_StubExecutes(t *testing.T) {
	t.Parallel()
	h := handlers.NewSeriesSearch(nil, nil, nil, nil, nil)
	if err := h.Execute(t.Context(), `{"seriesId":42}`); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestSeriesSearch_EmptyBody_Errors(t *testing.T) {
	t.Parallel()
	h := mustNewSeriesSearch(t, &ssStubSeriesLookup{}, &ssStubEpisodesLister{}, &ssStubEpisodeSearcher{}, &ssStubSeasonSearcher{})
	if err := h.Execute(t.Context(), ""); err == nil {
		t.Fatal("empty body: want error")
	}
}

func TestSeriesSearch_MalformedBody_Errors(t *testing.T) {
	t.Parallel()
	h := mustNewSeriesSearch(t, &ssStubSeriesLookup{}, &ssStubEpisodesLister{}, &ssStubEpisodeSearcher{}, &ssStubSeasonSearcher{})
	if err := h.Execute(t.Context(), `{bad`); err == nil {
		t.Fatal("malformed body: want error")
	}
}

func TestSeriesSearch_SeriesIdMissingOrZero_Errors(t *testing.T) {
	t.Parallel()
	h := mustNewSeriesSearch(t, &ssStubSeriesLookup{}, &ssStubEpisodesLister{}, &ssStubEpisodeSearcher{}, &ssStubSeasonSearcher{})
	if err := h.Execute(t.Context(), `{"seriesId":0}`); err == nil {
		t.Fatal("seriesId=0: want error")
	}
	if err := h.Execute(t.Context(), `{}`); err == nil {
		t.Fatal("seriesId missing: want error")
	}
}

func TestSeriesSearch_SeriesNotFound_Errors(t *testing.T) {
	t.Parallel()
	sl := &ssStubSeriesLookup{rows: map[int]*series.Series{}} // empty — id=42 missing
	h := mustNewSeriesSearch(t, sl, &ssStubEpisodesLister{}, &ssStubEpisodeSearcher{}, &ssStubSeasonSearcher{})
	if err := h.Execute(t.Context(), `{"seriesId":42}`); err == nil {
		t.Fatal("series not found: want error")
	}
}

// No seasons monitored → per-episode mode (C# :42-56).
func TestSeriesSearch_NoSeasonsMonitored_PerEpisodeMode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{
		ID: 42, Title: "Test",
		Seasons: mustSeasonsJSON(t, []map[string]any{
			{"seasonNumber": 1, "monitored": false},
			{"seasonNumber": 2, "monitored": false},
		}),
	}
	sl := &ssStubSeriesLookup{rows: map[int]*series.Series{42: ser}}
	el := &ssStubEpisodesLister{rows: []episodes.Episode{
		// Eligible: Monitored + no file + aired.
		{ID: 1, SeasonNumber: 1, Monitored: true, EpisodeFileID: nil, AirDateUtc: ptrTime(now.AddDate(0, 0, -10))},
		// Skipped: not monitored.
		{ID: 2, SeasonNumber: 1, Monitored: false, EpisodeFileID: nil, AirDateUtc: ptrTime(now.AddDate(0, 0, -10))},
		// Skipped: has file.
		{ID: 3, SeasonNumber: 1, Monitored: true, EpisodeFileID: ptrInt(100), AirDateUtc: ptrTime(now.AddDate(0, 0, -10))},
		// Skipped: future air date.
		{ID: 4, SeasonNumber: 1, Monitored: true, EpisodeFileID: nil, AirDateUtc: ptrTime(now.AddDate(0, 0, 5))},
		// Skipped: nil air date.
		{ID: 5, SeasonNumber: 1, Monitored: true, EpisodeFileID: nil, AirDateUtc: nil},
	}}
	es := &ssStubEpisodeSearcher{decision: 3}
	ss := &ssStubSeasonSearcher{}
	h := mustNewSeriesSearch(t, sl, el, es, ss)
	if err := h.Execute(t.Context(), `{"seriesId":42}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(es.calls) != 1 || es.calls[0] != 1 {
		t.Errorf("per-episode dispatch: got calls=%v, want [1]", es.calls)
	}
	if len(ss.calls) != 0 {
		t.Errorf("per-season must NOT fire when no seasons monitored; got %d", len(ss.calls))
	}
}

// At least one season monitored → per-season mode, skipping unmonitored seasons (C# :58-72).
func TestSeriesSearch_AnySeasonMonitored_PerSeasonMode(t *testing.T) {
	t.Parallel()
	ser := &series.Series{
		ID: 42, Title: "Test",
		Seasons: mustSeasonsJSON(t, []map[string]any{
			{"seasonNumber": 2, "monitored": true}, // out of order — verify sort
			{"seasonNumber": 1, "monitored": true},
			{"seasonNumber": 0, "monitored": false}, // unmonitored — skipped
		}),
	}
	sl := &ssStubSeriesLookup{rows: map[int]*series.Series{42: ser}}
	es := &ssStubEpisodeSearcher{}
	ss := &ssStubSeasonSearcher{decision: 2}
	h := mustNewSeriesSearch(t, sl, &ssStubEpisodesLister{}, es, ss)
	if err := h.Execute(t.Context(), `{"seriesId":42}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(es.calls) != 0 {
		t.Errorf("per-episode must NOT fire when seasons monitored; got %d", len(es.calls))
	}
	if len(ss.calls) != 2 {
		t.Fatalf("per-season dispatch: got %d calls, want 2 (season 1 + season 2)", len(ss.calls))
	}
	// Verify ASC ordering.
	if ss.calls[0].seasonNumber != 1 || ss.calls[1].seasonNumber != 2 {
		t.Errorf("per-season order: got %d,%d; want 1,2", ss.calls[0].seasonNumber, ss.calls[1].seasonNumber)
	}
}

func TestSeriesSearch_PerEpisodeSearchError_AbsorbedAndContinues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: 42, Seasons: ""} // empty → no-seasons → per-episode mode
	sl := &ssStubSeriesLookup{rows: map[int]*series.Series{42: ser}}
	el := &ssStubEpisodesLister{rows: []episodes.Episode{
		{ID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: ptrTime(now.AddDate(0, 0, -1))},
		{ID: 2, SeasonNumber: 1, Monitored: true, AirDateUtc: ptrTime(now.AddDate(0, 0, -1))},
	}}
	es := &ssStubEpisodeSearcher{err: errors.New("indexer down")}
	h := mustNewSeriesSearch(t, sl, el, es, &ssStubSeasonSearcher{})
	if err := h.Execute(t.Context(), `{"seriesId":42}`); err != nil {
		t.Fatalf("per-episode errors must be absorbed: %v", err)
	}
	if len(es.calls) != 2 {
		t.Errorf("both episodes should have been attempted despite errors; got %d calls", len(es.calls))
	}
}

func TestSeriesSearch_PerSeasonSearchError_AbsorbedAndContinues(t *testing.T) {
	t.Parallel()
	ser := &series.Series{
		ID: 42,
		Seasons: mustSeasonsJSON(t, []map[string]any{
			{"seasonNumber": 1, "monitored": true},
			{"seasonNumber": 2, "monitored": true},
		}),
	}
	sl := &ssStubSeriesLookup{rows: map[int]*series.Series{42: ser}}
	ss := &ssStubSeasonSearcher{err: errors.New("indexer down")}
	h := mustNewSeriesSearch(t, sl, &ssStubEpisodesLister{}, &ssStubEpisodeSearcher{}, ss)
	if err := h.Execute(t.Context(), `{"seriesId":42}`); err != nil {
		t.Fatalf("per-season errors must be absorbed: %v", err)
	}
	if len(ss.calls) != 2 {
		t.Errorf("both seasons should have been attempted despite errors; got %d calls", len(ss.calls))
	}
}

func TestSeriesSearch_EmptySeasons_FallsThroughToPerEpisode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: 42, Seasons: ""}
	sl := &ssStubSeriesLookup{rows: map[int]*series.Series{42: ser}}
	el := &ssStubEpisodesLister{rows: []episodes.Episode{
		{ID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: ptrTime(now.AddDate(0, 0, -5))},
	}}
	es := &ssStubEpisodeSearcher{decision: 1}
	ss := &ssStubSeasonSearcher{}
	h := mustNewSeriesSearch(t, sl, el, es, ss)
	if err := h.Execute(t.Context(), `{"seriesId":42}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(es.calls) != 1 || len(ss.calls) != 0 {
		t.Errorf("empty seasons → per-episode mode; got episode=%d season=%d", len(es.calls), len(ss.calls))
	}
}
