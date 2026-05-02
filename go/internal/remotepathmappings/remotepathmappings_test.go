package remotepathmappings_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	rpm "sonarr.tv/sonarr/internal/remotepathmappings"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := rpm.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	m := &rpm.Mapping{Host: "qbit.local", RemotePath: "/downloads", LocalPath: "/mnt/downloads"}
	if _, err := repo.Insert(t.Context(), m); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if m.ID == 0 {
		t.Errorf("Insert: ID not populated")
	}

	// Get round-trips all four fields.
	got, err := repo.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Host != "qbit.local" || got.RemotePath != "/downloads" || got.LocalPath != "/mnt/downloads" {
		t.Errorf("Get: %+v", got)
	}

	// Update changes everything.
	m.Host = "transmission.local"
	m.RemotePath = "/dls"
	m.LocalPath = "/mnt/dls"
	if err := repo.Update(t.Context(), m); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = repo.Get(t.Context(), m.ID)
	if got.Host != "transmission.local" || got.RemotePath != "/dls" || got.LocalPath != "/mnt/dls" {
		t.Errorf("post-Update: %+v", got)
	}
	if err := repo.Update(t.Context(), &rpm.Mapping{ID: 9999}); !errors.Is(err, rpm.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	// All.
	if all, err := repo.All(t.Context()); err != nil || len(all) != 1 {
		t.Errorf("All: %v len=%d", err, len(all))
	}

	// Delete.
	if err := repo.Delete(t.Context(), m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), m.ID); !errors.Is(err, rpm.ErrNotFound) {
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
