package episodefiles_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
)

func runQueriesSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	r, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

	// Seed: 2 files in series 1, 1 file in series 2, 1 file with NULL
	// MediaInfo (the empty string in our model maps to NULL via
	// nullableStringEmpty in the existing mapper).
	seeds := []episodefiles.File{
		{SeriesID: 1, SeasonNumber: 1, RelativePath: "s1.mkv", Quality: "{}", Size: 100, DateAdded: now, MediaInfo: `{"v":1}`, Languages: []int{1}},
		{SeriesID: 1, SeasonNumber: 2, RelativePath: "s2.mkv", Quality: "{}", Size: 200, DateAdded: now, MediaInfo: `{"v":2}`, Languages: []int{1}},
		{SeriesID: 1, SeasonNumber: 1, RelativePath: "s1.mkv", Quality: "{}", Size: 110, DateAdded: now, Languages: []int{1}}, // duplicate relpath, no MediaInfo
		{SeriesID: 2, SeasonNumber: 1, RelativePath: "x.mkv", Quality: "{}", Size: 300, DateAdded: now, MediaInfo: `{"v":3}`, Languages: []int{1}},
	}
	for i := range seeds {
		if _, err := r.Insert(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("Insert seed %d: %v", i, err)
		}
	}

	// AllBySeriesIDs.
	all, err := r.AllBySeriesIDs(t.Context(), []int{1, 2})
	if err != nil {
		t.Fatalf("AllBySeriesIDs: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("AllBySeriesIDs: got %d, want 4", len(all))
	}
	if empty, err := r.AllBySeriesIDs(t.Context(), nil); err != nil || len(empty) != 0 {
		t.Errorf("AllBySeriesIDs nil: %v / len=%d", err, len(empty))
	}

	// AllBySeason: series 1 season 1 → 2 rows.
	bySeason, err := r.AllBySeason(t.Context(), 1, 1)
	if err != nil {
		t.Fatalf("AllBySeason: %v", err)
	}
	if len(bySeason) != 2 {
		t.Errorf("AllBySeason(1,1): got %d, want 2", len(bySeason))
	}

	// GetMany.
	pick, err := r.GetMany(t.Context(), []int{seeds[0].ID, seeds[3].ID})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(pick) != 2 {
		t.Errorf("GetMany: got %d, want 2", len(pick))
	}
	if empty, err := r.GetMany(t.Context(), nil); err != nil || len(empty) != 0 {
		t.Errorf("GetMany nil: %v / len=%d", err, len(empty))
	}

	// AllWithoutMediaInfo: only seeds[2] has empty MediaInfo.
	noInfo, err := r.AllWithoutMediaInfo(t.Context())
	if err != nil {
		t.Fatalf("AllWithoutMediaInfo: %v", err)
	}
	if len(noInfo) != 1 {
		t.Errorf("AllWithoutMediaInfo: got %d, want 1", len(noInfo))
	}
	if len(noInfo) == 1 && noInfo[0].ID != seeds[2].ID {
		t.Errorf("AllWithoutMediaInfo: got id=%d, want %d", noInfo[0].ID, seeds[2].ID)
	}

	// AllWithRelativePath: series 1 / "s1.mkv" → 2 rows (seeds[0]+[2]).
	dups, err := r.AllWithRelativePath(t.Context(), 1, "s1.mkv")
	if err != nil {
		t.Fatalf("AllWithRelativePath: %v", err)
	}
	if len(dups) != 2 {
		t.Errorf("AllWithRelativePath: got %d, want 2", len(dups))
	}

	// DeleteForSeries: drops 3 rows (series 1's three files).
	n, err := r.DeleteForSeries(t.Context(), []int{1})
	if err != nil {
		t.Fatalf("DeleteForSeries: %v", err)
	}
	if n != 3 {
		t.Errorf("DeleteForSeries(1): rows=%d, want 3", n)
	}
	if remaining, err := r.AllBySeriesIDs(t.Context(), []int{1, 2}); err != nil || len(remaining) != 1 {
		t.Errorf("AllBySeriesIDs after delete: rem=%d err=%v", len(remaining), err)
	}
	// Empty input is a no-op.
	if n, err := r.DeleteForSeries(t.Context(), nil); err != nil || n != 0 {
		t.Errorf("DeleteForSeries nil: rows=%d err=%v", n, err)
	}
}

func TestQueries_SQLite(t *testing.T) {
	t.Parallel()
	runQueriesSuite(t, datastoretest.OpenSQLite(t))
}

func TestQueries_Postgres(t *testing.T) {
	runQueriesSuite(t, datastoretest.OpenPostgres(t))
}
