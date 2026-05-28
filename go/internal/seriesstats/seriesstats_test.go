package seriesstats

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
)

func tp(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func fid(n int) *int { return &n }

// now anchor for all tests.
var now = mustTime("2026-05-28T00:00:00Z")

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestCompute_Counts exercises the per-season + per-series counting
// CASE semantics: total / available / episode / file / monitored.
func TestCompute_Counts_J368(t *testing.T) {
	eps := []episodes.Episode{
		// Season 1: 4 episodes.
		// e1: aired, monitored, has file.
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: tp("2020-01-01T00:00:00Z"), AirDate: "2020-01-01", EpisodeFileID: fid(10)},
		// e2: aired, monitored, no file.
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: tp("2020-02-01T00:00:00Z"), AirDate: "2020-02-01"},
		// e3: future, monitored, no file (counts toward NextAiring + monitored, NOT episodeCount).
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: tp("2030-01-01T00:00:00Z"), AirDate: "2030-01-01"},
		// e4: aired, UNmonitored, no file (not episodeCount, not monitored).
		{SeriesID: 1, SeasonNumber: 1, Monitored: false, AirDateUtc: tp("2020-03-01T00:00:00Z"), AirDate: "2020-03-01"},
		// Season 0 (specials): 1 episode with a file.
		{SeriesID: 1, SeasonNumber: 0, Monitored: false, AirDate: "2019-12-01", EpisodeFileID: fid(20)},
	}
	files := []episodefiles.File{
		{SeriesID: 1, SeasonNumber: 1, Size: 1000, ReleaseGroup: "GRP", ReleaseType: 1, Quality: `{"quality":7,"revision":{"version":1}}`},
		{SeriesID: 1, SeasonNumber: 0, Size: 500, ReleaseGroup: "GRP", ReleaseType: 2, Quality: `{"quality":3,"revision":{"version":1}}`},
	}
	st := Compute(eps, files, now)

	if st.SeriesID != 1 {
		t.Errorf("seriesID: %d", st.SeriesID)
	}
	// Series totals: total=5, file=2, monitored=3 (e1,e2,e3).
	if st.TotalEpisodeCount != 5 {
		t.Errorf("totalEpisodeCount: %d want 5", st.TotalEpisodeCount)
	}
	if st.EpisodeFileCount != 2 {
		t.Errorf("episodeFileCount: %d want 2", st.EpisodeFileCount)
	}
	if st.MonitoredEpisodeCount != 3 {
		t.Errorf("monitoredEpisodeCount: %d want 3", st.MonitoredEpisodeCount)
	}
	// EpisodeCount: e1 (mon+aired+file), e2 (mon+aired), s0 file (hasFile). e3 future-unaired, e4 unmonitored → out. = 3.
	if st.EpisodeCount != 3 {
		t.Errorf("episodeCount: %d want 3", st.EpisodeCount)
	}
	if st.SizeOnDisk != 1500 {
		t.Errorf("sizeOnDisk: %d want 1500", st.SizeOnDisk)
	}

	// Two seasons, sorted 0 then 1.
	if len(st.SeasonStatistics) != 2 || st.SeasonStatistics[0].SeasonNumber != 0 || st.SeasonStatistics[1].SeasonNumber != 1 {
		t.Fatalf("seasons: %+v", st.SeasonStatistics)
	}
	s1 := st.SeasonStatistics[1]
	if s1.TotalEpisodeCount != 4 || s1.EpisodeFileCount != 1 || s1.MonitoredEpisodeCount != 3 || s1.EpisodeCount != 2 {
		t.Errorf("season1 counts: %+v", s1)
	}
	if s1.SizeOnDisk != 1000 {
		t.Errorf("season1 size: %d", s1.SizeOnDisk)
	}
}

// TestCompute_Airing pins NextAiring (min future monitored),
// PreviousAiring (max past monitored), and LastAired (max AirDate).
func TestCompute_Airing_J368(t *testing.T) {
	eps := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: tp("2020-01-01T00:00:00Z"), AirDate: "2020-01-01"},
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: tp("2020-06-01T00:00:00Z"), AirDate: "2020-06-01"},
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: tp("2030-01-01T00:00:00Z"), AirDate: "2030-01-01"},
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: tp("2031-01-01T00:00:00Z"), AirDate: "2031-01-01"},
		// Unmonitored future — must NOT influence NextAiring.
		{SeriesID: 1, SeasonNumber: 1, Monitored: false, AirDateUtc: tp("2027-01-01T00:00:00Z"), AirDate: "2027-01-01"},
	}
	st := Compute(eps, nil, now)
	if st.NextAiring == nil || !st.NextAiring.Equal(*tp("2030-01-01T00:00:00Z")) {
		t.Errorf("nextAiring: %v want 2030-01-01", st.NextAiring)
	}
	if st.PreviousAiring == nil || !st.PreviousAiring.Equal(*tp("2020-06-01T00:00:00Z")) {
		t.Errorf("previousAiring: %v want 2020-06-01", st.PreviousAiring)
	}
	// LastAired = max AirDate over all = 2031-01-01.
	if st.LastAired == nil || !st.LastAired.Equal(mustTime("2031-01-01T00:00:00Z")) {
		t.Errorf("lastAired: %v want 2031-01-01", st.LastAired)
	}
}

// TestCompute_LastAiredExcludesSeason0 — series LastAired ignores
// specials (SeasonNumber 0), matching the C# `SeasonNumber > 0` filter.
func TestCompute_LastAiredExcludesSeason0_J368(t *testing.T) {
	eps := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 0, Monitored: true, AirDate: "2040-01-01"}, // special — ignored for series LastAired
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDate: "2020-01-01"},
	}
	st := Compute(eps, nil, now)
	if st.LastAired == nil || !st.LastAired.Equal(mustTime("2020-01-01T00:00:00Z")) {
		t.Errorf("series lastAired must exclude season 0: %v want 2020-01-01", st.LastAired)
	}
	// But the season-0 SeasonStatistics still records its own LastAired.
	if st.SeasonStatistics[0].SeasonNumber != 0 || st.SeasonStatistics[0].LastAired == nil {
		t.Errorf("season0 stat should still carry lastAired: %+v", st.SeasonStatistics[0])
	}
}

// TestCompute_FileAggregation — distinct release groups (sorted),
// release types, and quality ids unioned across seasons.
func TestCompute_FileAggregation_J368(t *testing.T) {
	files := []episodefiles.File{
		{SeriesID: 1, SeasonNumber: 1, Size: 10, ReleaseGroup: "ZGRP", ReleaseType: 1, Quality: `{"quality":7}`},
		{SeriesID: 1, SeasonNumber: 1, Size: 20, ReleaseGroup: "AGRP", ReleaseType: 1, Quality: `{"quality":3}`},
		{SeriesID: 1, SeasonNumber: 2, Size: 30, ReleaseGroup: "AGRP", ReleaseType: 2, Quality: `{"quality":7}`},
		{SeriesID: 1, SeasonNumber: 2, Size: 5, ReleaseGroup: "", ReleaseType: 0, Quality: ``}, // empty group/quality skipped
	}
	eps := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, EpisodeFileID: fid(1)},
		{SeriesID: 1, SeasonNumber: 2, Monitored: true, EpisodeFileID: fid(2)},
	}
	st := Compute(eps, files, now)
	if got := st.ReleaseGroups; len(got) != 2 || got[0] != "AGRP" || got[1] != "ZGRP" {
		t.Errorf("series releaseGroups: %v want [AGRP ZGRP]", got)
	}
	if got := st.ReleaseTypes; len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Errorf("series releaseTypes: %v want [0 1 2]", got)
	}
	if got := st.EpisodeFileQualities; len(got) != 2 || got[0] != 3 || got[1] != 7 {
		t.Errorf("series qualities: %v want [3 7]", got)
	}
	if st.SizeOnDisk != 65 {
		t.Errorf("sizeOnDisk: %d want 65", st.SizeOnDisk)
	}
}

// TestCompute_Empty — no episodes/files → zero stats, empty slices.
func TestCompute_Empty_J368(t *testing.T) {
	st := Compute(nil, nil, now)
	if st.TotalEpisodeCount != 0 || st.EpisodeCount != 0 || st.SizeOnDisk != 0 {
		t.Errorf("non-zero on empty: %+v", st)
	}
	if st.NextAiring != nil || st.PreviousAiring != nil || st.LastAired != nil {
		t.Errorf("airing must be nil on empty: %+v", st)
	}
	if len(st.SeasonStatistics) != 0 {
		t.Errorf("seasons must be empty: %+v", st.SeasonStatistics)
	}
	if st.ReleaseGroups == nil || st.ReleaseTypes == nil || st.EpisodeFileQualities == nil {
		t.Errorf("series slices must be non-nil: %+v", st)
	}
}

// TestCompute_NilAirDateUtc — a nil AirDateUtc episode with a file is
// available + counts (hasFile), but does not influence airing windows.
func TestCompute_NilAirDateUtc_J368(t *testing.T) {
	eps := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: nil, EpisodeFileID: fid(1)},
		{SeriesID: 1, SeasonNumber: 1, Monitored: true, AirDateUtc: nil}, // no file, no airdate → only monitored++
	}
	st := Compute(eps, nil, now)
	if st.EpisodeFileCount != 1 || st.EpisodeCount != 1 || st.MonitoredEpisodeCount != 2 || st.TotalEpisodeCount != 2 {
		t.Errorf("counts: %+v", st)
	}
	if st.NextAiring != nil || st.PreviousAiring != nil {
		t.Errorf("nil airDateUtc must not set airing: %+v", st)
	}
}
