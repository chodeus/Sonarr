package namingconfig_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/namingconfig"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := namingconfig.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// HEAD-state seed: NamingConfig table is empty after migrations.
	// The C# NamingConfigService inserts the Default() row lazily on
	// first read; the Go side will follow the same pattern at the
	// service layer. The repo itself doesn't auto-seed.
	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("All initial: got %d, want 0", len(all))
	}

	// Insert the canonical default. Round-trip exercises every column
	// including the nullable-bool RenameEpisodes and the five nullable
	// format strings.
	d := namingconfig.Default()
	if _, err := repo.Insert(t.Context(), d); err != nil {
		t.Fatalf("Insert Default(): %v", err)
	}
	if d.ID == 0 {
		t.Fatalf("Insert: ID not populated")
	}
	got, err := repo.Get(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MultiEpisodeStyle != 5 || got.ColonReplacementFormat != 4 {
		t.Errorf("enum int round-trip: %+v", got)
	}
	if got.RenameEpisodes {
		t.Errorf("RenameEpisodes default: true, want false")
	}
	if !got.ReplaceIllegalCharacters {
		t.Errorf("ReplaceIllegalCharacters default: false, want true")
	}
	if got.SpecialsFolderFormat != "Specials" {
		t.Errorf("SpecialsFolderFormat: %q", got.SpecialsFolderFormat)
	}
	if got.CustomColonReplacementFormat != "" {
		t.Errorf("CustomColonReplacementFormat: %q", got.CustomColonReplacementFormat)
	}

	// Toggle RenameEpisodes and clear two format strings — exercises the
	// nullable-bool and nullable-string columns.
	got.RenameEpisodes = true
	got.AnimeEpisodeFormat = ""
	got.SpecialsFolderFormat = ""
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update toggles: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if !post.RenameEpisodes {
		t.Errorf("RenameEpisodes round-trip: false")
	}
	if post.AnimeEpisodeFormat != "" || post.SpecialsFolderFormat != "" {
		t.Errorf("nullable string clear: %+v", post)
	}

	// Insert a second config — the table doesn't enforce singleton at the
	// SQL layer (singleton-ness is application-level), so two rows is
	// allowed by the schema.
	other := namingconfig.Default()
	other.SeriesFolderFormat = "{Series Title} ({Year})"
	if _, err := repo.Insert(t.Context(), other); err != nil {
		t.Fatalf("Insert other: %v", err)
	}
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("All: got %d, want 2", len(all))
	}

	if err := repo.Update(t.Context(), &namingconfig.Config{ID: 99999}); !errors.Is(err, namingconfig.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), other.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), other.ID); !errors.Is(err, namingconfig.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, namingconfig.ErrNotFound) {
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
