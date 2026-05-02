package extrafiles_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/extrafiles"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := extrafiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	f := &extrafiles.ExtraFile{
		SeriesID:      50,
		SeasonNumber:  1,
		EpisodeFileID: 100,
		RelativePath:  "Season 1/Foo.S01E01.txt",
		Extension:     ".txt",
		Added:         now,
		LastUpdated:   now,
	}
	if _, err := repo.Insert(t.Context(), f); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(t.Context(), f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Added.Equal(now) || !got.LastUpdated.Equal(now) {
		t.Errorf("times: %+v", got)
	}
	if got.RelativePath != f.RelativePath {
		t.Errorf("RelativePath: %q", got.RelativePath)
	}
	if err := repo.Delete(t.Context(), f.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(t.Context(), f.ID); !errors.Is(err, extrafiles.ErrNotFound) {
		t.Errorf("Get(deleted): %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
