package rootfolders_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/rootfolders"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := rootfolders.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	rf := &rootfolders.RootFolder{Path: "/tv"}
	if _, err := repo.Insert(t.Context(), rf); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if rf.ID == 0 {
		t.Errorf("Insert: ID not populated")
	}

	// Get by ID + by Path.
	got, err := repo.Get(t.Context(), rf.ID)
	if err != nil || got.Path != "/tv" {
		t.Errorf("Get: %v err %v", got, err)
	}
	gotByPath, err := repo.GetByPath(t.Context(), "/tv")
	if err != nil || gotByPath.ID != rf.ID {
		t.Errorf("GetByPath: %v err %v", gotByPath, err)
	}
	if _, err := repo.Get(t.Context(), 999); !errors.Is(err, rootfolders.ErrNotFound) {
		t.Errorf("Get(missing): %v", err)
	}
	if _, err := repo.GetByPath(t.Context(), "/no"); !errors.Is(err, rootfolders.ErrNotFound) {
		t.Errorf("GetByPath(missing): %v", err)
	}

	// Duplicate path is rejected by IX_RootFolders_Path UNIQUE constraint.
	if _, err := repo.Insert(t.Context(), &rootfolders.RootFolder{Path: "/tv"}); err == nil {
		t.Error("Insert duplicate Path succeeded; want UNIQUE error")
	}

	// All.
	if all, err := repo.All(t.Context()); err != nil || len(all) != 1 {
		t.Errorf("All: len=%d err=%v", len(all), err)
	}

	// Delete.
	if err := repo.Delete(t.Context(), rf.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), rf.ID); !errors.Is(err, rootfolders.ErrNotFound) {
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
