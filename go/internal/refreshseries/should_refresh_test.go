package refreshseries_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/refreshseries"
	"sonarr.tv/sonarr/internal/series"
)

func ptrTime(t time.Time) *time.Time { return &t }

var shouldNow = time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

type stubEpisodeLookup struct {
	eps []episodes.Episode
	err error
}

func (s *stubEpisodeLookup) AllBySeriesID(_ context.Context, _ int) ([]episodes.Episode, error) {
	return s.eps, s.err
}

func TestShouldRefresh_NeverSynced_Refreshes(t *testing.T) {
	t.Parallel()
	s := &series.Series{ID: 1, LastInfoSync: nil}
	if !refreshseries.ShouldRefresh(t.Context(), s, &stubEpisodeLookup{}, shouldNow, nil) {
		t.Errorf("never-synced row should refresh")
	}
}

func TestShouldRefresh_Over30DaysOld_Refreshes(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		ID:           1,
		LastInfoSync: ptrTime(shouldNow.AddDate(0, 0, -31)),
	}
	if !refreshseries.ShouldRefresh(t.Context(), s, &stubEpisodeLookup{}, shouldNow, nil) {
		t.Errorf("stale-31d row should refresh")
	}
}

func TestShouldRefresh_AiredTBA_Refreshes(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusEnded, // even ended series refresh when TBA episode aired
		LastInfoSync: ptrTime(shouldNow.Add(-1 * time.Hour)),
	}
	ep := &stubEpisodeLookup{
		eps: []episodes.Episode{
			{SeasonNumber: 1, AirDateUtc: ptrTime(shouldNow.Add(-24 * time.Hour)), Title: "TBA"},
		},
	}
	if !refreshseries.ShouldRefresh(t.Context(), s, ep, shouldNow, nil) {
		t.Errorf("aired TBA-titled episode should force refresh")
	}
}

func TestShouldRefresh_AiredTBA_FutureEpisode_DoesNotTrigger(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusEnded,
		LastInfoSync: ptrTime(shouldNow.Add(-1 * time.Hour)), // within 6h window → skip
	}
	ep := &stubEpisodeLookup{
		eps: []episodes.Episode{
			// Future-air episode with TBA title — doesn't satisfy "aired"
			{SeasonNumber: 1, AirDateUtc: ptrTime(shouldNow.Add(24 * time.Hour)), Title: "TBA"},
		},
	}
	if refreshseries.ShouldRefresh(t.Context(), s, ep, shouldNow, nil) {
		t.Errorf("unaired TBA episode should not trigger TBA arm")
	}
}

func TestShouldRefresh_AiredTBA_Season0_Ignored(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusEnded,
		LastInfoSync: ptrTime(shouldNow.Add(-1 * time.Hour)),
	}
	ep := &stubEpisodeLookup{
		eps: []episodes.Episode{
			// Specials (season 0) with TBA: C# `:36` excludes season 0
			{SeasonNumber: 0, AirDateUtc: ptrTime(shouldNow.Add(-24 * time.Hour)), Title: "TBA"},
		},
	}
	if refreshseries.ShouldRefresh(t.Context(), s, ep, shouldNow, nil) {
		t.Errorf("season-0 TBA should not trigger TBA arm")
	}
}

func TestShouldRefresh_Recent_NotEnded_Refreshes(t *testing.T) {
	t.Parallel()
	// Recent enough to pass the 6h gate BUT status is not ended → refresh.
	// Actually wait — the C# code is: if recent < 6h → skip. We need to be
	// AT LEAST 6h old to evaluate the not-ended branch.
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusContinuing,
		LastInfoSync: ptrTime(shouldNow.Add(-7 * time.Hour)), // >6h, <30d
	}
	if !refreshseries.ShouldRefresh(t.Context(), s, &stubEpisodeLookup{}, shouldNow, nil) {
		t.Errorf("not-ended series should refresh after 6h staleness")
	}
}

func TestShouldRefresh_Within6h_NotEnded_Skips(t *testing.T) {
	t.Parallel()
	// Within 6h gate — predicate skips even for not-ended series.
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusContinuing,
		LastInfoSync: ptrTime(shouldNow.Add(-1 * time.Hour)),
	}
	if refreshseries.ShouldRefresh(t.Context(), s, &stubEpisodeLookup{}, shouldNow, nil) {
		t.Errorf("recent (<6h) sync should skip refresh")
	}
}

func TestShouldRefresh_Ended_RecentLastEpisode_Refreshes(t *testing.T) {
	t.Parallel()
	// Ended series, last episode aired within 30d → refresh
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusEnded,
		LastInfoSync: ptrTime(shouldNow.Add(-7 * time.Hour)),
	}
	ep := &stubEpisodeLookup{
		eps: []episodes.Episode{
			{SeasonNumber: 1, AirDateUtc: ptrTime(shouldNow.AddDate(0, 0, -15))},
		},
	}
	if !refreshseries.ShouldRefresh(t.Context(), s, ep, shouldNow, nil) {
		t.Errorf("ended series with recent last episode should refresh")
	}
}

func TestShouldRefresh_EndedLongAgo_Skips(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusEnded,
		LastInfoSync: ptrTime(shouldNow.Add(-7 * time.Hour)),
	}
	ep := &stubEpisodeLookup{
		eps: []episodes.Episode{
			{SeasonNumber: 1, AirDateUtc: ptrTime(shouldNow.AddDate(0, 0, -365))},
		},
	}
	if refreshseries.ShouldRefresh(t.Context(), s, ep, shouldNow, nil) {
		t.Errorf("ended series with old last episode should skip")
	}
}

func TestShouldRefresh_EpisodeLookupError_DefaultsRefresh(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusContinuing,
		LastInfoSync: ptrTime(shouldNow.Add(-1 * time.Hour)), // would normally skip
	}
	ep := &stubEpisodeLookup{err: errors.New("db down")}
	if !refreshseries.ShouldRefresh(t.Context(), s, ep, shouldNow, nil) {
		t.Errorf("episode lookup error should default to refresh=true")
	}
}

func TestShouldRefresh_NilSeries_DefaultsRefresh(t *testing.T) {
	t.Parallel()
	if !refreshseries.ShouldRefresh(t.Context(), nil, &stubEpisodeLookup{}, shouldNow, nil) {
		t.Errorf("nil series should default to refresh=true")
	}
}

func TestShouldRefresh_NilEpisodeLookup_DefaultsRefreshTrue(t *testing.T) {
	t.Parallel()
	// Recent sync + not-ended would normally evaluate the predicate,
	// but with nil lookup we skip the per-episode checks and fall to
	// the C# catch-all `return true` posture.
	s := &series.Series{
		ID:           1,
		Status:       series.SeriesStatusContinuing,
		LastInfoSync: ptrTime(shouldNow.Add(-1 * time.Hour)),
	}
	if !refreshseries.ShouldRefresh(t.Context(), s, nil, shouldNow, nil) {
		t.Errorf("nil episode lookup should default to refresh=true")
	}
}
