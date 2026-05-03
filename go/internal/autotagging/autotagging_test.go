package autotagging_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := autotagging.NewRepo(db)
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

	const specsJSON = `[{"type":"GenreSpecification","body":{"name":"anime","negate":false,"required":true,"value":["Animation"]}},{"type":"YearSpecification","body":{"name":"2020s","negate":false,"required":false,"min":2020,"max":2029}}]`
	want := &autotagging.AutoTag{
		Name:                    "anime-2020s",
		Specifications:          specsJSON,
		RemoveTagsAutomatically: true,
		Tags:                    []int{42, 7},
	}
	if _, err := repo.Insert(t.Context(), want); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Specifications != specsJSON {
		t.Errorf("Specifications round-trip:\n got %q\nwant %q", got.Specifications, specsJSON)
	}
	if !got.RemoveTagsAutomatically {
		t.Errorf("RemoveTagsAutomatically: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != 42 {
		t.Errorf("Tags round-trip: %v", got.Tags)
	}

	// Nil Tags coerces to [] (NOT NULL constraint).
	empty := &autotagging.AutoTag{Name: "empty", Specifications: "", Tags: nil}
	if _, err := repo.Insert(t.Context(), empty); err != nil {
		t.Fatalf("Insert empty: %v", err)
	}
	gotEmpty, err := repo.Get(t.Context(), empty.ID)
	if err != nil {
		t.Fatalf("Get empty: %v", err)
	}
	if gotEmpty.Specifications != "[]" {
		t.Errorf("empty Specifications coercion: %q", gotEmpty.Specifications)
	}
	if len(gotEmpty.Tags) != 0 {
		t.Errorf("empty Tags coercion: %v", gotEmpty.Tags)
	}

	byName, err := repo.FindByName(t.Context(), "anime-2020s")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if byName.ID != want.ID {
		t.Errorf("FindByName id: %d", byName.ID)
	}
	if _, err := repo.FindByName(t.Context(), "missing"); !errors.Is(err, autotagging.ErrNotFound) {
		t.Errorf("FindByName(missing): %v", err)
	}

	got.Name = "anime-renamed"
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Name != "anime-renamed" {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Delete(t.Context(), want.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), want.ID); !errors.Is(err, autotagging.ErrNotFound) {
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
