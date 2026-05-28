package episodes_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// runWantedCutoffUnmetSuite exercises Repo.WantedCutoffUnmet against
// both engines. Fixture: one monitored series on profile 1, one
// unmonitored series on profile 1. Profile 1's below-cutoff set is
// {2}. Episode files carry quality id 2 (below cutoff) or 5 (at/above).
//
//   - file quality 2 + season 1 + monitored series → IN
//   - file quality 5 + season 1 → OUT (not below cutoff)
//   - file quality 2 + season 0 (special) → OUT (includeSpecials=false)
//   - no file → OUT (cutoff applies only to episodes that HAVE a file)
//   - file quality 2 + unmonitored series → OUT when monitoredOnly
func runWantedCutoffUnmetSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	episodesRepo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("episodes.NewRepo: %v", err)
	}
	filesRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("episodefiles.NewRepo: %v", err)
	}

	monitored := &series.Series{TvdbID: 2001, Title: "Mon", CleanTitle: "mon", Monitored: true, Runtime: 30, QualityProfileID: 1}
	if _, err := seriesRepo.Insert(t.Context(), monitored); err != nil {
		t.Fatalf("insert monitored: %v", err)
	}
	unmonitored := &series.Series{TvdbID: 2002, Title: "Unmon", CleanTitle: "unmon", Monitored: false, Runtime: 30, QualityProfileID: 1}
	if _, err := seriesRepo.Insert(t.Context(), unmonitored); err != nil {
		t.Fatalf("insert unmonitored: %v", err)
	}

	// Quality JSON in the exact quality.Model serialisation shape.
	const qBelow = `{"quality":2,"revision":{"version":1,"real":0,"isRepack":false}}`
	const qAbove = `{"quality":5,"revision":{"version":1,"real":0,"isRepack":false}}`

	mkFile := func(seriesID int, q string) int {
		f, err := filesRepo.Insert(t.Context(), &episodefiles.File{
			SeriesID: seriesID, Quality: q, Size: 100, DateAdded: time.Now().UTC(), SeasonNumber: 1,
		})
		if err != nil {
			t.Fatalf("insert file: %v", err)
		}
		return f.ID
	}

	fBelowMon := mkFile(monitored.ID, qBelow)
	fAboveMon := mkFile(monitored.ID, qAbove)
	fBelowSpecial := mkFile(monitored.ID, qBelow)
	fBelowUnmon := mkFile(unmonitored.ID, qBelow)

	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := &episodes.Episode{SeriesID: monitored.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Want", Monitored: true, AirDateUtc: &past, EpisodeFileID: &fBelowMon}
	above := &episodes.Episode{SeriesID: monitored.ID, SeasonNumber: 1, EpisodeNumber: 2, Title: "Above", Monitored: true, AirDateUtc: &past, EpisodeFileID: &fAboveMon}
	special := &episodes.Episode{SeriesID: monitored.ID, SeasonNumber: 0, EpisodeNumber: 1, Title: "Special", Monitored: true, AirDateUtc: &past, EpisodeFileID: &fBelowSpecial}
	nofile := &episodes.Episode{SeriesID: monitored.ID, SeasonNumber: 1, EpisodeNumber: 3, Title: "NoFile", Monitored: true, AirDateUtc: &past}
	unmon := &episodes.Episode{SeriesID: unmonitored.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Unmon", Monitored: true, AirDateUtc: &past, EpisodeFileID: &fBelowUnmon}
	for _, e := range []*episodes.Episode{want, above, special, nofile, unmon} {
		if _, err := episodesRepo.Insert(t.Context(), e); err != nil {
			t.Fatalf("insert episode %q: %v", e.Title, err)
		}
	}

	below := map[int][]int{1: {2}}

	// monitoredOnly=true → only the monitored-series below-cutoff aired row.
	got, err := episodesRepo.WantedCutoffUnmet(t.Context(), below, true)
	if err != nil {
		t.Fatalf("WantedCutoffUnmet(true): %v", err)
	}
	if len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("monitoredOnly=true: got %+v want [%q]", got, want.Title)
	}

	// monitoredOnly=false → the unmonitored-series row joins (still
	// excludes above-cutoff / special / no-file).
	got, err = episodesRepo.WantedCutoffUnmet(t.Context(), below, false)
	if err != nil {
		t.Fatalf("WantedCutoffUnmet(false): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("monitoredOnly=false: got %d rows want 2: %+v", len(got), got)
	}

	// Empty below-cutoff map → empty result (short-circuit).
	got, err = episodesRepo.WantedCutoffUnmet(t.Context(), map[int][]int{}, true)
	if err != nil {
		t.Fatalf("WantedCutoffUnmet(empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty below-cutoff: got %d rows want 0", len(got))
	}
}

func TestWantedCutoffUnmet_SQLite(t *testing.T) {
	t.Parallel()
	runWantedCutoffUnmetSuite(t, datastoretest.OpenSQLite(t))
}

func TestWantedCutoffUnmet_Postgres(t *testing.T) {
	runWantedCutoffUnmetSuite(t, datastoretest.OpenPostgres(t))
}
