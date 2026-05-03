package indexers_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/indexers"
)

func ptrStr(s string) *string { return &s }
func ptrBool(b bool) *bool    { return &b }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := indexers.NewRepo(db)
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

	const settingsBody = `{"baseUrl":"https://example.com/api","apiKey":"secret"}`
	want := &indexers.Indexer{
		Name:                                "Test Newznab",
		Implementation:                      "Newznab",
		Settings:                            ptrStr(settingsBody),
		ConfigContract:                      ptrStr("NewznabSettings"),
		EnableRss:                           ptrBool(true),
		EnableAutomaticSearch:               ptrBool(true),
		EnableInteractiveSearch:             true,
		Priority:                            25,
		Tags:                                []int{1, 7},
		DownloadClientID:                    0,
		SeasonSearchMaximumSingleEpisodeAge: 0,
	}
	if _, err := repo.Insert(t.Context(), want); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if want.ID == 0 {
		t.Fatalf("ID not populated")
	}

	got, err := repo.Get(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != want.Name || got.Implementation != "Newznab" {
		t.Errorf("scalar mismatch: %+v", got)
	}
	if got.Settings == nil || *got.Settings != settingsBody {
		t.Errorf("Settings round-trip: %v", got.Settings)
	}
	if got.ConfigContract == nil || *got.ConfigContract != "NewznabSettings" {
		t.Errorf("ConfigContract round-trip: %v", got.ConfigContract)
	}
	if got.EnableRss == nil || !*got.EnableRss {
		t.Errorf("EnableRss: %v", got.EnableRss)
	}
	if len(got.Tags) != 2 || got.Tags[0] != 1 || got.Tags[1] != 7 {
		t.Errorf("Tags round-trip: %v", got.Tags)
	}

	// Nullable round-trip: null Settings/ConfigContract/Tags/EnableRss survive.
	null := &indexers.Indexer{
		Name:                    "Bare",
		Implementation:          "TorrentRssIndexer",
		EnableInteractiveSearch: false,
		Priority:                25,
	}
	if _, err := repo.Insert(t.Context(), null); err != nil {
		t.Fatalf("Insert null: %v", err)
	}
	gotNull, err := repo.Get(t.Context(), null.ID)
	if err != nil {
		t.Fatalf("Get null: %v", err)
	}
	if gotNull.Settings != nil || gotNull.ConfigContract != nil ||
		gotNull.EnableRss != nil || gotNull.EnableAutomaticSearch != nil ||
		gotNull.Tags != nil {
		t.Errorf("null round-trip failed: %+v", gotNull)
	}

	// FindByName matches what we just stored.
	byName, err := repo.FindByName(t.Context(), "Test Newznab")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if byName.ID != want.ID {
		t.Errorf("FindByName: id %d, want %d", byName.ID, want.ID)
	}
	if _, err := repo.FindByName(t.Context(), "missing"); !errors.Is(err, indexers.ErrNotFound) {
		t.Errorf("FindByName(missing): %v", err)
	}

	// Update mutates one row.
	got.Priority = 50
	got.Implementation = "Torznab"
	got.ConfigContract = ptrStr("TorznabSettings")
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.Priority != 50 || post.Implementation != "Torznab" {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Delete(t.Context(), want.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), want.ID); !errors.Is(err, indexers.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, indexers.ErrNotFound) {
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
