package downloadclients_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/downloadclients"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := downloadclients.NewRepo(db)
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

	const settingsBody = `{"host":"localhost","port":8080}`
	want := &downloadclients.DownloadClient{
		Enable:                   true,
		Name:                     "Sab",
		Implementation:           "Sabnzbd",
		Settings:                 settingsBody,
		ConfigContract:           "SabnzbdSettings",
		Priority:                 1,
		RemoveCompletedDownloads: true,
		RemoveFailedDownloads:    true,
		Tags:                     []int{2, 5},
	}
	if _, err := repo.Insert(t.Context(), want); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Settings != settingsBody || got.ConfigContract != "SabnzbdSettings" {
		t.Errorf("Settings/ConfigContract round-trip: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != 2 {
		t.Errorf("Tags round-trip: %v", got.Tags)
	}

	// Pair-key validation: Vuze and Transmission share TransmissionSettings.
	// Both rows must persist as distinct entries — no constraint folds them.
	transmission := &downloadclients.DownloadClient{
		Enable: true, Name: "Trans", Implementation: "Transmission",
		Settings: "{}", ConfigContract: "TransmissionSettings", Priority: 2,
		RemoveCompletedDownloads: true, RemoveFailedDownloads: true,
	}
	vuze := &downloadclients.DownloadClient{
		Enable: true, Name: "Vuze", Implementation: "Vuze",
		Settings: "{}", ConfigContract: "TransmissionSettings", Priority: 3,
		RemoveCompletedDownloads: true, RemoveFailedDownloads: true,
	}
	if _, err := repo.Insert(t.Context(), transmission); err != nil {
		t.Fatalf("Insert Transmission: %v", err)
	}
	if _, err := repo.Insert(t.Context(), vuze); err != nil {
		t.Fatalf("Insert Vuze: %v", err)
	}
	rows, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("All count: %d, want 3", len(rows))
	}
	// All() ordered by Priority — Sab (1), Transmission (2), Vuze (3).
	if rows[0].Implementation != "Sabnzbd" ||
		rows[1].Implementation != "Transmission" ||
		rows[2].Implementation != "Vuze" {
		t.Errorf("ordering: %+v", []string{rows[0].Implementation, rows[1].Implementation, rows[2].Implementation})
	}

	byName, err := repo.FindByName(t.Context(), "Vuze")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if byName.ID != vuze.ID {
		t.Errorf("FindByName: id %d, want %d", byName.ID, vuze.ID)
	}

	got.Priority = 99
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Priority != 99 {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Delete(t.Context(), want.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), want.ID); !errors.Is(err, downloadclients.ErrNotFound) {
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
