package customformats_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := customformats.NewRepo(db)
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

	// Realistic envelope: a polymorphic list of {type, body} pairs as
	// described in analysis/embedded-docs/catalogue.md.
	const specsJSON = `[{"type":"ReleaseTitleSpecification","body":{"name":"x265","negate":false,"required":false,"value":"x265"}},{"type":"SizeSpecification","body":{"name":"<5GB","negate":false,"required":false,"min":0,"max":5}}]`
	want := &customformats.CustomFormat{
		Name:                            "x265 small",
		Specifications:                  specsJSON,
		IncludeCustomFormatWhenRenaming: true,
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
	if !got.IncludeCustomFormatWhenRenaming {
		t.Errorf("flag round-trip: %+v", got)
	}

	// Empty Specifications coerces to "[]" on Bind so Get returns "[]".
	empty := &customformats.CustomFormat{Name: "empty", Specifications: ""}
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

	byName, err := repo.FindByName(t.Context(), "x265 small")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if byName.ID != want.ID {
		t.Errorf("FindByName id: %d", byName.ID)
	}
	if _, err := repo.FindByName(t.Context(), "missing"); !errors.Is(err, customformats.ErrNotFound) {
		t.Errorf("FindByName(missing): %v", err)
	}

	got.Name = "x265 small (renamed)"
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Name != "x265 small (renamed)" {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Delete(t.Context(), want.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), want.ID); !errors.Is(err, customformats.ErrNotFound) {
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
