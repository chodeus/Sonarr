package notifications_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/notifications"
)

func ptrStr(s string) *string { return &s }
func ptrBool(b bool) *bool    { return &b }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := notifications.NewRepo(db)
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

	const settingsBody = `{"webhookUrl":"https://discord.example/abc"}`
	want := &notifications.Notification{
		Name:                          "discord-main",
		OnGrab:                        true,
		OnDownload:                    true,
		Settings:                      settingsBody,
		Implementation:                "Discord",
		ConfigContract:                ptrStr("DiscordSettings"),
		OnUpgrade:                     ptrBool(true),
		Tags:                          []int{3},
		OnRename:                      false,
		OnEpisodeFileDeleteForUpgrade: true,
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
	if got.ConfigContract == nil || *got.ConfigContract != "DiscordSettings" {
		t.Errorf("ConfigContract round-trip: %v", got.ConfigContract)
	}
	if got.OnUpgrade == nil || !*got.OnUpgrade {
		t.Errorf("OnUpgrade round-trip: %v", got.OnUpgrade)
	}
	if len(got.Tags) != 1 || got.Tags[0] != 3 {
		t.Errorf("Tags round-trip: %v", got.Tags)
	}
	if !got.OnEpisodeFileDeleteForUpgrade {
		t.Errorf("OnEpisodeFileDeleteForUpgrade round-trip: %v", got)
	}

	// Null nullable fields persist as null.
	null := &notifications.Notification{
		Name:           "minimal",
		OnGrab:         false,
		OnDownload:     false,
		Settings:       "{}",
		Implementation: "Webhook",
	}
	if _, err := repo.Insert(t.Context(), null); err != nil {
		t.Fatalf("Insert null: %v", err)
	}
	gotNull, err := repo.Get(t.Context(), null.ID)
	if err != nil {
		t.Fatalf("Get null: %v", err)
	}
	if gotNull.ConfigContract != nil || gotNull.OnUpgrade != nil || gotNull.Tags != nil {
		t.Errorf("nullable round-trip: %+v", gotNull)
	}

	byName, err := repo.FindByName(t.Context(), "discord-main")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if byName.ID != want.ID {
		t.Errorf("FindByName: id %d", byName.ID)
	}

	got.OnRename = true
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if !post.OnRename {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Delete(t.Context(), want.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), want.ID); !errors.Is(err, notifications.ErrNotFound) {
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
