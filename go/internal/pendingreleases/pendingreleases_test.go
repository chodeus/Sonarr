package pendingreleases_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := pendingreleases.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("All initial: got %d, want 0", len(all))
	}

	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)

	r1 := &pendingreleases.Release{
		SeriesID:          50,
		Title:             "Foo.S01E01.WEBRip.RARBG",
		Added:             now,
		ParsedEpisodeInfo: `{"seriesTitle":"Foo","seasonNumber":1,"episodeNumbers":[1]}`,
		ReleaseInfo:       `{"title":"Foo.S01E01.WEBRip.RARBG","size":1234567}`,
		Reason:            1, // Delay
		AdditionalInfo:    `{"seriesMatchType":1,"releaseSource":2}`,
	}
	if _, err := repo.Insert(t.Context(), r1); err != nil {
		t.Fatalf("Insert r1: %v", err)
	}

	r2 := &pendingreleases.Release{
		SeriesID: 50,
		Title:    "Foo.S01E02",
		Added:    now,
		Reason:   0,
	}
	if _, err := repo.Insert(t.Context(), r2); err != nil {
		t.Fatalf("Insert r2: %v", err)
	}

	r3 := &pendingreleases.Release{
		SeriesID: 99,
		Title:    "Bar.S01E01",
		Added:    now,
		Reason:   1,
	}
	if _, err := repo.Insert(t.Context(), r3); err != nil {
		t.Fatalf("Insert r3: %v", err)
	}

	got, err := repo.Get(t.Context(), r1.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AdditionalInfo != r1.AdditionalInfo {
		t.Errorf("AdditionalInfo: %s", got.AdditionalInfo)
	}
	if got.ParsedEpisodeInfo != r1.ParsedEpisodeInfo {
		t.Errorf("ParsedEpisodeInfo: %s", got.ParsedEpisodeInfo)
	}

	gotR2, err := repo.Get(t.Context(), r2.ID)
	if err != nil {
		t.Fatalf("Get r2: %v", err)
	}
	if gotR2.AdditionalInfo != "" {
		t.Errorf("r2 AdditionalInfo: %q, want empty", gotR2.AdditionalInfo)
	}
	if gotR2.ParsedEpisodeInfo != "{}" {
		t.Errorf("r2 ParsedEpisodeInfo: %q, want default '{}'", gotR2.ParsedEpisodeInfo)
	}

	bySeries50, err := repo.AllBySeriesID(t.Context(), 50)
	if err != nil {
		t.Fatalf("AllBySeriesID: %v", err)
	}
	if len(bySeries50) != 2 {
		t.Errorf("series 50: got %d, want 2", len(bySeries50))
	}

	if err := repo.Update(t.Context(), &pendingreleases.Release{ID: 99999, SeriesID: 1, Title: "x", Added: now}); !errors.Is(err, pendingreleases.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), r1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), r1.ID); !errors.Is(err, pendingreleases.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
