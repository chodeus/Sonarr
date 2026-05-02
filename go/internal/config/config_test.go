package config_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := config.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// Empty.
	if all, err := repo.All(t.Context()); err != nil || len(all) != 0 {
		t.Errorf("All on empty: %v / len=%d", err, len(all))
	}

	// First Set inserts.
	e1, err := repo.Set(t.Context(), "logLevel", "info")
	if err != nil {
		t.Fatalf("Set logLevel=info: %v", err)
	}
	if e1.ID == 0 {
		t.Errorf("Set: ID not populated")
	}

	// Second Set on the same key updates (no new row).
	e2, err := repo.Set(t.Context(), "logLevel", "debug")
	if err != nil {
		t.Fatalf("Set logLevel=debug: %v", err)
	}
	if e2.ID != e1.ID {
		t.Errorf("Set upsert: new ID %d, want preserved %d", e2.ID, e1.ID)
	}
	if got, _ := repo.GetByKey(t.Context(), "logLevel"); got.Value != "debug" {
		t.Errorf("Value after upsert = %q, want debug", got.Value)
	}

	// A different key creates a new row.
	if _, err := repo.Set(t.Context(), "uiTheme", "dark"); err != nil {
		t.Fatalf("Set uiTheme: %v", err)
	}

	// Get by ID round-trips.
	got, err := repo.Get(t.Context(), e1.ID)
	if err != nil || got.Key != "logLevel" {
		t.Errorf("Get(%d): got %v err %v, want Key=logLevel", e1.ID, got, err)
	}
	if _, err := repo.Get(t.Context(), 99_999); !errors.Is(err, config.ErrNotFound) {
		t.Errorf("Get(missing): err = %v, want ErrNotFound", err)
	}

	// GetByKey.
	if got, err := repo.GetByKey(t.Context(), "uiTheme"); err != nil || got.Value != "dark" {
		t.Errorf("GetByKey: %v err %v", got, err)
	}
	if _, err := repo.GetByKey(t.Context(), "missing"); !errors.Is(err, config.ErrNotFound) {
		t.Errorf("GetByKey(missing): err = %v", err)
	}

	// All ordered by Key.
	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 || all[0].Key != "logLevel" || all[1].Key != "uiTheme" {
		t.Errorf("All ordering: %v", all)
	}

	// Delete by key.
	if err := repo.Delete(t.Context(), "uiTheme"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), "uiTheme"); !errors.Is(err, config.ErrNotFound) {
		t.Errorf("Delete idempotent: err = %v, want ErrNotFound", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}

func TestRepo_RejectsNilDB(t *testing.T) {
	t.Parallel()
	if _, err := config.NewRepo(nil); err == nil {
		t.Error("NewRepo accepted nil DB; want error")
	}
}
