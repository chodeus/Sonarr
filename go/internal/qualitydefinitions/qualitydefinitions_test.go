package qualitydefinitions_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := qualitydefinitions.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// HEAD-state seed: QualityDefinitions table is empty until the
	// service layer populates it on first run from
	// Quality.DefaultQualityDefinitions. The migrations themselves
	// don't seed rows.
	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("All initial: got %d rows, want 0", len(all))
	}

	// Insert two rows in non-Quality-ordered insertion order to verify
	// All sorts by Quality.
	hd := &qualitydefinitions.Definition{Quality: 4, Title: "HDTV-720p"}
	if _, err := repo.Insert(t.Context(), hd); err != nil {
		t.Fatalf("Insert hd: %v", err)
	}
	sd := &qualitydefinitions.Definition{Quality: 1, Title: "SDTV"}
	if _, err := repo.Insert(t.Context(), sd); err != nil {
		t.Fatalf("Insert sd: %v", err)
	}
	if hd.ID == 0 || sd.ID == 0 {
		t.Fatalf("ID not populated: hd=%d sd=%d", hd.ID, sd.ID)
	}

	// Get and GetByQuality round-trip.
	got, err := repo.Get(t.Context(), hd.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Quality != 4 || got.Title != "HDTV-720p" {
		t.Errorf("Get: %+v", got)
	}
	byQ, err := repo.GetByQuality(t.Context(), 1)
	if err != nil {
		t.Fatalf("GetByQuality: %v", err)
	}
	if byQ.ID != sd.ID {
		t.Errorf("GetByQuality: got id %d, want %d", byQ.ID, sd.ID)
	}
	if _, err := repo.GetByQuality(t.Context(), 9999); !errors.Is(err, qualitydefinitions.ErrNotFound) {
		t.Errorf("GetByQuality(missing): %v", err)
	}

	// All ordered by Quality ascending: SDTV (1) before HDTV-720p (4).
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 || all[0].Quality != 1 || all[1].Quality != 4 {
		t.Errorf("All ordering: %+v", all)
	}

	// UNIQUE on Quality.
	if _, err := repo.Insert(t.Context(), &qualitydefinitions.Definition{Quality: 1, Title: "SD-Different"}); err == nil {
		t.Errorf("Insert duplicate Quality: expected error, got nil")
	}
	// UNIQUE on Title.
	if _, err := repo.Insert(t.Context(), &qualitydefinitions.Definition{Quality: 99, Title: "SDTV"}); err == nil {
		t.Errorf("Insert duplicate Title: expected error, got nil")
	}

	// Update Title.
	got.Title = "HDTV 720p (renamed)"
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Title != "HDTV 720p (renamed)" {
		t.Errorf("post-Update title: %q", post.Title)
	}

	// Update on missing ID.
	if err := repo.Update(t.Context(), &qualitydefinitions.Definition{ID: 99999, Quality: 50, Title: "x"}); !errors.Is(err, qualitydefinitions.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	// Delete.
	if err := repo.Delete(t.Context(), hd.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), hd.ID); !errors.Is(err, qualitydefinitions.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, qualitydefinitions.ErrNotFound) {
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
