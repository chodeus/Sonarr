package series_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// now is the fixed reference timestamp every test refers to. All
// AirDateUtc / LastInfoSync values in the cases below are derived
// from this so the rules' "30 days ago" / "6 hours ago" cutoffs are
// crisp.
var now = time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func TestShouldRefresh_NilSeries_ReturnsTrue(t *testing.T) {
	t.Parallel()
	if !series.ShouldRefresh(nil, nil, now) {
		t.Error("ShouldRefresh(nil) = false, want true (pathological-input safety)")
	}
}

func TestShouldRefresh_LastInfoSyncOlderThan30Days(t *testing.T) {
	t.Parallel()
	old := now.AddDate(0, 0, -31)
	s := &series.Series{LastInfoSync: &old, Status: series.SeriesStatusEnded}
	if !series.ShouldRefresh(s, nil, now) {
		t.Error("LastInfoSync 31 days old → should refresh")
	}
}

func TestShouldRefresh_LastInfoSyncNil_TreatedAsVeryOld(t *testing.T) {
	t.Parallel()
	s := &series.Series{Status: series.SeriesStatusEnded}
	if !series.ShouldRefresh(s, nil, now) {
		t.Error("nil LastInfoSync → should refresh (zero-time before any real timestamp)")
	}
}

func TestShouldRefresh_AiredEpisodeWithTBATitle(t *testing.T) {
	t.Parallel()
	last := now.Add(-1 * time.Hour) // recent → would skip on time alone
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusContinuing}
	eps := []episodes.Episode{
		{SeasonNumber: 1, AirDateUtc: ptr(now.Add(-24 * time.Hour)), Title: "TBA"},
	}
	if !series.ShouldRefresh(s, eps, now) {
		t.Error("aired episode with Title=TBA → should refresh (rule 2 wins over rule 3)")
	}
}

func TestShouldRefresh_TBATitleCaseSensitive(t *testing.T) {
	t.Parallel()
	last := now.Add(-1 * time.Hour)
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusContinuing}
	// Lowercase "tba" should NOT trigger rule 2 (C# uses Ordinal compare).
	eps := []episodes.Episode{
		{SeasonNumber: 1, AirDateUtc: ptr(now.Add(-24 * time.Hour)), Title: "tba"},
	}
	if series.ShouldRefresh(s, eps, now) {
		t.Error("Title \"tba\" (lowercase) should NOT match TBA (case-sensitive)")
	}
}

func TestShouldRefresh_TBASkipsSpecialsSeason(t *testing.T) {
	t.Parallel()
	last := now.Add(-1 * time.Hour)
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusContinuing}
	// SeasonNumber=0 (specials) is excluded from rule 2.
	eps := []episodes.Episode{
		{SeasonNumber: 0, AirDateUtc: ptr(now.Add(-24 * time.Hour)), Title: "TBA"},
	}
	if series.ShouldRefresh(s, eps, now) {
		t.Error("TBA in season 0 (specials) should be ignored by rule 2")
	}
}

func TestShouldRefresh_TBAUnAiredIgnored(t *testing.T) {
	t.Parallel()
	last := now.Add(-1 * time.Hour)
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusContinuing}
	// Future AirDate → episode hasn't aired → not eligible for rule 2.
	eps := []episodes.Episode{
		{SeasonNumber: 1, AirDateUtc: ptr(now.Add(48 * time.Hour)), Title: "TBA"},
	}
	if series.ShouldRefresh(s, eps, now) {
		t.Error("TBA on un-aired episode should be ignored")
	}
}

func TestShouldRefresh_RecentlySyncedSkips(t *testing.T) {
	t.Parallel()
	last := now.Add(-30 * time.Minute) // < 6 hours ago
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusContinuing}
	if series.ShouldRefresh(s, nil, now) {
		t.Error("LastInfoSync 30 min ago → should NOT refresh (rule 3)")
	}
}

func TestShouldRefresh_NotEndedAndStaleEnoughToBypassRule3(t *testing.T) {
	t.Parallel()
	last := now.Add(-12 * time.Hour) // > 6 hours and < 30 days
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusContinuing}
	if !series.ShouldRefresh(s, nil, now) {
		t.Error("Continuing series 12h stale → should refresh (rule 4)")
	}
}

func TestShouldRefresh_EndedRecentEpisode(t *testing.T) {
	t.Parallel()
	last := now.Add(-12 * time.Hour)
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusEnded}
	eps := []episodes.Episode{
		{SeasonNumber: 1, AirDateUtc: ptr(now.AddDate(0, 0, -10))}, // 10 days ago
	}
	if !series.ShouldRefresh(s, eps, now) {
		t.Error("Ended series with episode 10 days ago → should refresh (rule 5)")
	}
}

func TestShouldRefresh_EndedLongAgo(t *testing.T) {
	t.Parallel()
	last := now.Add(-12 * time.Hour)
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusEnded}
	eps := []episodes.Episode{
		{SeasonNumber: 1, AirDateUtc: ptr(now.AddDate(0, 0, -60))}, // 60 days ago
	}
	if series.ShouldRefresh(s, eps, now) {
		t.Error("Ended series with last episode 60 days ago → should NOT refresh (rule 6)")
	}
}

func TestShouldRefresh_EndedNoEpisodes(t *testing.T) {
	t.Parallel()
	last := now.Add(-12 * time.Hour)
	s := &series.Series{LastInfoSync: &last, Status: series.SeriesStatusEnded}
	if series.ShouldRefresh(s, nil, now) {
		t.Error("Ended series with no episodes → should NOT refresh (rule 6 fallthrough)")
	}
}
