package episodes_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// runWantedMissingSuite exercises `Repo.WantedMissing` against both
// engines. The fixture seeds two series (one monitored, one not) and
// four episodes spanning every branch the predicate cares about:
//
//   - aired + no file + monitored series → IN
//   - aired + no file + unmonitored series → OUT (when monitoredOnly)
//   - aired + has file → OUT (file linkage)
//   - future air date → OUT (cutoff not reached)
//
// Mirrors `EpisodeRepositoryFixture.EpisodesWithoutFiles` cases in
// `src/NzbDrone.Core.Test/Tv/EpisodeRepositoryTests/`.
func runWantedMissingSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	episodesRepo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("episodes.NewRepo: %v", err)
	}

	monitoredSeries := &series.Series{TvdbID: 1001, Title: "Monitored", CleanTitle: "monitored", Monitored: true, Runtime: 30}
	if _, err := seriesRepo.Insert(t.Context(), monitoredSeries); err != nil {
		t.Fatalf("insert monitored series: %v", err)
	}
	unmonitoredSeries := &series.Series{TvdbID: 1002, Title: "Unmonitored", CleanTitle: "unmonitored", Monitored: false, Runtime: 30}
	if _, err := seriesRepo.Insert(t.Context(), unmonitoredSeries); err != nil {
		t.Fatalf("insert unmonitored series: %v", err)
	}

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)
	future := now.Add(48 * time.Hour)

	want := &episodes.Episode{
		SeriesID: monitoredSeries.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "Want", Monitored: true, AirDateUtc: &past, Runtime: 30,
	}
	notInUnmonitored := &episodes.Episode{
		SeriesID: unmonitoredSeries.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "UnmonitoredSeries", Monitored: true, AirDateUtc: &past, Runtime: 30,
	}
	hasFile := &episodes.Episode{
		SeriesID: monitoredSeries.ID, SeasonNumber: 1, EpisodeNumber: 2,
		Title: "HasFile", Monitored: true, AirDateUtc: &past, Runtime: 30,
		EpisodeFileID: ptrInt(7),
	}
	notAired := &episodes.Episode{
		SeriesID: monitoredSeries.ID, SeasonNumber: 1, EpisodeNumber: 3,
		Title: "Future", Monitored: true, AirDateUtc: &future, Runtime: 30,
	}
	for _, e := range []*episodes.Episode{want, notInUnmonitored, hasFile, notAired} {
		if _, err := episodesRepo.Insert(t.Context(), e); err != nil {
			t.Fatalf("insert episode %q: %v", e.Title, err)
		}
	}

	got, err := episodesRepo.WantedMissing(t.Context(), now, true)
	if err != nil {
		t.Fatalf("WantedMissing(monitoredOnly=true): %v", err)
	}
	if len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("monitoredOnly=true: got %+v, want [%q]", got, want.Title)
	}

	// monitoredOnly=false widens the gate — the unmonitored-series row joins
	// the set, but the has-file and not-aired rows still drop out.
	got, err = episodesRepo.WantedMissing(t.Context(), now, false)
	if err != nil {
		t.Fatalf("WantedMissing(monitoredOnly=false): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("monitoredOnly=false: got %d rows, want 2: %+v", len(got), got)
	}
}

func TestWantedMissing_SQLite(t *testing.T) {
	t.Parallel()
	runWantedMissingSuite(t, datastoretest.OpenSQLite(t))
}

func TestWantedMissing_Postgres(t *testing.T) {
	runWantedMissingSuite(t, datastoretest.OpenPostgres(t))
}
