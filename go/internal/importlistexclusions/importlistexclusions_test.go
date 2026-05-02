package importlistexclusions_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/importlistexclusions"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := importlistexclusions.NewRepo(db)
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

	zorro := &importlistexclusions.Exclusion{TvdbID: 12345, Title: "Zorro"}
	if _, err := repo.Insert(t.Context(), zorro); err != nil {
		t.Fatalf("Insert zorro: %v", err)
	}
	alpha := &importlistexclusions.Exclusion{TvdbID: 67890, Title: "Alpha"}
	if _, err := repo.Insert(t.Context(), alpha); err != nil {
		t.Fatalf("Insert alpha: %v", err)
	}

	got, err := repo.Get(t.Context(), zorro.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TvdbID != 12345 || got.Title != "Zorro" {
		t.Errorf("Get round-trip: %+v", got)
	}

	byTvdb, err := repo.GetByTvdbID(t.Context(), 67890)
	if err != nil {
		t.Fatalf("GetByTvdbID: %v", err)
	}
	if byTvdb.ID != alpha.ID {
		t.Errorf("GetByTvdbID: got %d, want %d", byTvdb.ID, alpha.ID)
	}
	if _, err := repo.GetByTvdbID(t.Context(), 999999); !errors.Is(err, importlistexclusions.ErrNotFound) {
		t.Errorf("GetByTvdbID(missing): %v", err)
	}

	// All ordered by Title.
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 || all[0].Title != "Alpha" || all[1].Title != "Zorro" {
		t.Errorf("All ordering: %+v", all)
	}

	// UNIQUE on TvdbId.
	if _, err := repo.Insert(t.Context(), &importlistexclusions.Exclusion{TvdbID: 12345, Title: "Different"}); err == nil {
		t.Errorf("Insert duplicate TvdbId: expected error, got nil")
	}

	got.Title = "Zorro (renamed)"
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Title != "Zorro (renamed)" {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Update(t.Context(), &importlistexclusions.Exclusion{ID: 99999, TvdbID: 1, Title: "x"}); !errors.Is(err, importlistexclusions.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	if err := repo.Delete(t.Context(), zorro.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), zorro.ID); !errors.Is(err, importlistexclusions.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, importlistexclusions.ErrNotFound) {
		t.Errorf("Get(missing): %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
