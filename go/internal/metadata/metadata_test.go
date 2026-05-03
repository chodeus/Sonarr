package metadata_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/metadata"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := metadata.NewRepo(db)
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

	const settingsBody = `{"seriesMetadata":true,"episodeMetadata":true}`
	want := &metadata.Metadata{
		Enable:         true,
		Name:           "Kodi",
		Implementation: "XbmcMetadata",
		Settings:       settingsBody,
		ConfigContract: "XbmcMetadataSettings",
	}
	if _, err := repo.Insert(t.Context(), want); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Settings != settingsBody {
		t.Errorf("Settings round-trip: %q", got.Settings)
	}
	if got.ConfigContract != "XbmcMetadataSettings" {
		t.Errorf("ConfigContract: %q", got.ConfigContract)
	}

	byName, err := repo.FindByName(t.Context(), "Kodi")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if byName.ID != want.ID {
		t.Errorf("FindByName id: %d", byName.ID)
	}

	got.Enable = false
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Enable {
		t.Errorf("post-Update Enable still true: %+v", post)
	}

	if err := repo.Delete(t.Context(), want.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), want.ID); !errors.Is(err, metadata.ErrNotFound) {
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
