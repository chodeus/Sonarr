package episodes_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// runQueriesSuite exercises the read helpers added in Phase 2.61. Runs
// against both engines via the SQLite/Postgres wrappers below; mirrors
// the runRepoSuite shape established for the base CRUD test.
func runQueriesSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// Seed Series rows first so SeriesId references in episodes resolve.
	// Repo.Insert ignores the struct's Id field — the schema autoincrements
	// — so capture the assigned IDs and use them downstream. Series 100
	// (TvdbId=100) is monitored; series 200 has no Series row so its
	// episode is excluded by the Series.Monitored EXISTS clause.
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	s100 := &series.Series{
		TvdbID: 100, TvRageID: 0, Title: "Show 100", CleanTitle: "show 100",
		TitleSlug: "show-100", Status: 1, Path: "/x/show100", Monitored: true,
		Images: "[]", Seasons: "[]",
	}
	if _, err := seriesRepo.Insert(t.Context(), s100); err != nil {
		t.Fatalf("series Insert 100: %v", err)
	}

	airApr := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	airMay1 := time.Date(2026, 5, 1, 4, 0, 0, 0, time.UTC)
	airMay2 := time.Date(2026, 5, 2, 4, 0, 0, 0, time.UTC)
	airMay8 := time.Date(2026, 5, 8, 4, 0, 0, 0, time.UTC)
	airJun := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Seeded fixture covers the major shapes the queries discriminate on.
	seeds := []episodes.Episode{
		{ // S1E1: monitored, has file 1, scene-mapped (1,1), abs=1.
			SeriesID: s100.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot",
			AbsoluteEpisodeNumber:      ptrInt(1),
			SceneSeasonNumber:          ptrInt(1),
			SceneEpisodeNumber:         ptrInt(1),
			SceneAbsoluteEpisodeNumber: ptrInt(1),
			Monitored:                  true,
			AirDateUtc:                 ptrTime(airMay1),
			AirDate:                    "2026-05-01",
		},
		{ // S1E2: monitored, scene-mapped (1,2).
			SeriesID: s100.ID, SeasonNumber: 1, EpisodeNumber: 2, Title: "Two",
			AbsoluteEpisodeNumber:      ptrInt(2),
			SceneSeasonNumber:          ptrInt(1),
			SceneEpisodeNumber:         ptrInt(2),
			SceneAbsoluteEpisodeNumber: ptrInt(2),
			Monitored:                  true,
			AirDateUtc:                 ptrTime(airMay1),
			AirDate:                    "2026-05-01",
		},
		{ // S1E3: monitored, no scene mapping.
			SeriesID: s100.ID, SeasonNumber: 1, EpisodeNumber: 3, Title: "Three",
			Monitored:  true,
			AirDateUtc: ptrTime(airMay8),
			AirDate:    "2026-05-08",
		},
		{ // S2E1: unmonitored.
			SeriesID: s100.ID, SeasonNumber: 2, EpisodeNumber: 1, Title: "Two-One",
			Monitored:  false,
			AirDateUtc: ptrTime(airMay2),
			AirDate:    "2026-05-02",
		},
		{ // Special (S0E1): monitored, same air date as an S1 episode.
			SeriesID: s100.ID, SeasonNumber: 0, EpisodeNumber: 1, Title: "Special",
			Monitored:  true,
			AirDateUtc: ptrTime(airMay1),
			AirDate:    "2026-05-01",
		},
		{ // Unseeded series id — exists in Episodes table but no Series row,
			// so the Series.Monitored EXISTS clause should exclude it.
			SeriesID: s100.ID + 1000, SeasonNumber: 1, EpisodeNumber: 1, Title: "Other",
			Monitored:  true,
			AirDateUtc: ptrTime(airMay1),
			AirDate:    "2026-05-01",
		},
		{ // Outside any reasonable window — used as a negative filter target.
			SeriesID: s100.ID, SeasonNumber: 3, EpisodeNumber: 1, Title: "Far Past",
			Monitored: true, AirDateUtc: ptrTime(airApr), AirDate: "2026-04-01",
		},
		{
			SeriesID: s100.ID, SeasonNumber: 3, EpisodeNumber: 2, Title: "Far Future",
			Monitored: true, AirDateUtc: ptrTime(airJun), AirDate: "2026-06-01",
		},
	}
	for i := range seeds {
		if _, err := repo.Insert(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("Insert seed %d: %v", i, err)
		}
	}

	// Find: S1E2 of series 100.
	if got, err := repo.Find(t.Context(), s100.ID, 1, 2); err != nil {
		t.Fatalf("Find: %v", err)
	} else if got.Title != "Two" {
		t.Errorf("Find: got %q want %q", got.Title, "Two")
	}
	if _, err := repo.Find(t.Context(), s100.ID, 99, 99); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("Find missing: got %v, want ErrNotFound", err)
	}

	// FindByAbsolute.
	if got, err := repo.FindByAbsolute(t.Context(), s100.ID, 2); err != nil {
		t.Fatalf("FindByAbsolute: %v", err)
	} else if got.EpisodeNumber != 2 {
		t.Errorf("FindByAbsolute: got E%d want E2", got.EpisodeNumber)
	}
	if _, err := repo.FindByAbsolute(t.Context(), s100.ID, 999); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("FindByAbsolute missing: %v", err)
	}

	// FindByAirDate: 2026-05-01 has 3 series-100 rows (S1E1, S1E2, S0E1).
	got, err := repo.FindByAirDate(t.Context(), s100.ID, "2026-05-01")
	if err != nil {
		t.Fatalf("FindByAirDate: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("FindByAirDate(100,2026-05-01): got %d, want 3", len(got))
	}

	// AllBySeriesIDs: both series, expect every row across both.
	all, err := repo.AllBySeriesIDs(t.Context(), []int{s100.ID, s100.ID + 1000})
	if err != nil {
		t.Fatalf("AllBySeriesIDs: %v", err)
	}
	if len(all) != len(seeds) {
		t.Errorf("AllBySeriesIDs: got %d, want %d", len(all), len(seeds))
	}
	if empty, err := repo.AllBySeriesIDs(t.Context(), nil); err != nil {
		t.Errorf("AllBySeriesIDs nil: %v", err)
	} else if len(empty) != 0 {
		t.Errorf("AllBySeriesIDs nil: got %d, want 0", len(empty))
	}

	// AllBySceneSeason: scene season 1 of series 100 = S1E1+S1E2.
	scene, err := repo.AllBySceneSeason(t.Context(), s100.ID, 1)
	if err != nil {
		t.Fatalf("AllBySceneSeason: %v", err)
	}
	if len(scene) != 2 {
		t.Errorf("AllBySceneSeason: got %d, want 2", len(scene))
	}

	// FindBySceneNumbering(season,episode): hits S1E1.
	bySE, err := repo.FindBySceneNumbering(t.Context(), s100.ID, 1, 1)
	if err != nil {
		t.Fatalf("FindBySceneNumbering: %v", err)
	}
	if len(bySE) != 1 || bySE[0].EpisodeNumber != 1 {
		t.Errorf("FindBySceneNumbering: got %v", bySE)
	}

	// FindBySceneAbsolute: abs=2 hits S1E2 only.
	byAbs, err := repo.FindBySceneAbsolute(t.Context(), s100.ID, 2)
	if err != nil {
		t.Fatalf("FindBySceneAbsolute: %v", err)
	}
	if len(byAbs) != 1 || byAbs[0].EpisodeNumber != 2 {
		t.Errorf("FindBySceneAbsolute: got %v", byAbs)
	}

	// EpisodesBetweenDates: window [2026-05-01, 2026-05-08].
	// includeUnmonitored=true, includeSpecials=true → S1E1, S1E2, S2E1, S0E1, S1E3 (series 100)
	//                                                + 200/S1E1.
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 23, 59, 59, 0, time.UTC)
	full, err := repo.EpisodesBetweenDates(t.Context(), start, end, true, true)
	if err != nil {
		t.Fatalf("EpisodesBetweenDates full: %v", err)
	}
	if len(full) != 6 {
		t.Errorf("EpisodesBetweenDates(unmon=t,spec=t): got %d, want 6", len(full))
	}

	// includeSpecials=false → drops S0E1 → 5.
	noSpec, err := repo.EpisodesBetweenDates(t.Context(), start, end, true, false)
	if err != nil {
		t.Fatalf("EpisodesBetweenDates noSpec: %v", err)
	}
	if len(noSpec) != 5 {
		t.Errorf("EpisodesBetweenDates(unmon=t,spec=f): got %d, want 5", len(noSpec))
	}

	// includeUnmonitored=false: requires Series.Monitored=true via the
	// EXISTS subquery. The s100 row was seeded above; only its
	// monitored rows survive (S2E1 dropped for Monitored=false; the
	// orphan series row dropped for missing Series row entirely).
	monOnly, err := repo.EpisodesBetweenDates(t.Context(), start, end, false, true)
	if err != nil {
		t.Fatalf("EpisodesBetweenDates monitoredOnly: %v", err)
	}
	// Drops S2E1 (Monitored=false) and 200/S1E1 (no Series row for 200).
	if len(monOnly) != 4 {
		t.Errorf("EpisodesBetweenDates(unmon=f,spec=t): got %d, want 4", len(monOnly))
	}

	// Now link S1E1 + S1E2 to a real EpisodeFile so EpisodesWithFiles
	// has something to JOIN against.
	efRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("episodefiles.NewRepo: %v", err)
	}
	ef := &episodefiles.File{
		SeriesID: s100.ID, Quality: "{}", Size: 1024, DateAdded: time.Now().UTC(),
		SeasonNumber: 1, RelativePath: "x.mkv", Languages: []int{1},
	}
	if _, err := efRepo.Insert(t.Context(), ef); err != nil {
		t.Fatalf("episodefiles Insert: %v", err)
	}
	for i := 0; i < 2; i++ {
		seeds[i].EpisodeFileID = ptrInt(ef.ID)
		if err := repo.Update(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("link episode %d: %v", i, err)
		}
	}
	withFiles, err := repo.EpisodesWithFiles(t.Context(), s100.ID)
	if err != nil {
		t.Fatalf("EpisodesWithFiles: %v", err)
	}
	if len(withFiles) != 2 {
		t.Errorf("EpisodesWithFiles: got %d, want 2", len(withFiles))
	}

	// AllByFileID: file ef.ID maps to S1E1+S1E2 (multi-episode file).
	files, err := repo.AllByFileID(t.Context(), ef.ID)
	if err != nil {
		t.Fatalf("AllByFileID: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("AllByFileID(%d): got %d, want 2", ef.ID, len(files))
	}

	// GetMany: pull two specific rows by ID.
	pickIDs := []int{seeds[0].ID, seeds[2].ID}
	picked, err := repo.GetMany(t.Context(), pickIDs)
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(picked) != 2 {
		t.Errorf("GetMany: got %d, want 2", len(picked))
	}
	if empty, err := repo.GetMany(t.Context(), nil); err != nil {
		t.Errorf("GetMany nil: %v", err)
	} else if len(empty) != 0 {
		t.Errorf("GetMany nil: got %d, want 0", len(empty))
	}
}

func TestQueries_SQLite(t *testing.T) {
	t.Parallel()
	runQueriesSuite(t, datastoretest.OpenSQLite(t))
}

func TestQueries_Postgres(t *testing.T) {
	runQueriesSuite(t, datastoretest.OpenPostgres(t))
}
