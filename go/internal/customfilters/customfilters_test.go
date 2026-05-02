package customfilters_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/customfilters"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := customfilters.NewRepo(db)
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

	const filtersJSON = `[{"key":"monitored","value":"true","type":"equal"}]`
	f := &customfilters.Filter{
		Type:    "series",
		Label:   "Monitored",
		Filters: filtersJSON,
	}
	if _, err := repo.Insert(t.Context(), f); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if f.ID == 0 {
		t.Fatalf("ID not populated")
	}
	got, err := repo.Get(t.Context(), f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Type != "series" || got.Label != "Monitored" || got.Filters != filtersJSON {
		t.Errorf("round-trip: %+v", got)
	}

	got.Label = "Monitored (renamed)"
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Label != "Monitored (renamed)" {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Update(t.Context(), &customfilters.Filter{ID: 99999, Type: "x", Label: "x", Filters: "[]"}); !errors.Is(err, customfilters.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), f.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), f.ID); !errors.Is(err, customfilters.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, customfilters.ErrNotFound) {
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
