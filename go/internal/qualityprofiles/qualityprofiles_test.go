package qualityprofiles_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := qualityprofiles.NewRepo(db)
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

	const itemsJSON = `[{"quality":{"id":1,"name":"SDTV"},"allowed":true},{"quality":{"id":4,"name":"HDTV-720p"},"allowed":true}]`
	const formatItemsJSON = `[{"format":1,"score":100},{"format":2,"score":-50}]`

	full := &qualityprofiles.Profile{
		Name:                  "HD-720p",
		Cutoff:                4,
		Items:                 itemsJSON,
		UpgradeAllowed:        true,
		FormatItems:           formatItemsJSON,
		MinFormatScore:        0,
		CutoffFormatScore:     50,
		MinUpgradeFormatScore: 1,
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	// Sparse — just NOT-NULL minimums; opaque JSON columns left empty.
	sparse := &qualityprofiles.Profile{
		Name:   "Any",
		Cutoff: 1,
		// Items required NOT NULL — caller must set; but our default
		// kicks in via Bind which writes "[]" when empty.
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	got, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Items != itemsJSON {
		t.Errorf("Items round-trip:\n got: %s\nwant: %s", got.Items, itemsJSON)
	}
	if got.FormatItems != formatItemsJSON {
		t.Errorf("FormatItems round-trip:\n got: %s\nwant: %s", got.FormatItems, formatItemsJSON)
	}
	if !got.UpgradeAllowed {
		t.Errorf("UpgradeAllowed: false, want true")
	}
	if got.CutoffFormatScore != 50 {
		t.Errorf("CutoffFormatScore: %d", got.CutoffFormatScore)
	}

	// GetByName resolves the unique key.
	byName, err := repo.GetByName(t.Context(), "HD-720p")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if byName.ID != full.ID {
		t.Errorf("GetByName: %d, want %d", byName.ID, full.ID)
	}
	if _, err := repo.GetByName(t.Context(), "no-such"); !errors.Is(err, qualityprofiles.ErrNotFound) {
		t.Errorf("GetByName(missing): %v", err)
	}

	// UNIQUE on Name.
	if _, err := repo.Insert(t.Context(), &qualityprofiles.Profile{Name: "HD-720p", Cutoff: 1}); err == nil {
		t.Errorf("Insert duplicate Name: expected error")
	}

	// Update — toggle UpgradeAllowed, change Items.
	got.UpgradeAllowed = false
	got.Items = `[{"quality":{"id":7,"name":"WEBDL-1080p"},"allowed":true}]`
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.UpgradeAllowed || post.Items != got.Items {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Update(t.Context(), &qualityprofiles.Profile{ID: 99999, Name: "x", Cutoff: 1}); !errors.Is(err, qualityprofiles.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
