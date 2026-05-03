package importlists_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/importlists"
)

func ptrStr(s string) *string { return &s }
func ptrBool(b bool) *bool    { return &b }

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := importlists.NewRepo(db)
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

	const settingsBody = `{"username":"alice","authToken":"x"}`
	want := &importlists.ImportList{
		Name:                     "trakt-popular",
		Implementation:           "TraktPopularImport",
		Settings:                 ptrStr(settingsBody),
		ConfigContract:           ptrStr("TraktPopularSettings"),
		EnableAutomaticAdd:       ptrBool(true),
		RootFolderPath:           "/tv",
		ShouldMonitor:            1,
		QualityProfileID:         1,
		Tags:                     []int{4},
		SeriesType:               0,
		SeasonFolder:             true,
		SearchForMissingEpisodes: true,
		MonitorNewItems:          0,
	}
	if _, err := repo.Insert(t.Context(), want); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Settings == nil || *got.Settings != settingsBody {
		t.Errorf("Settings round-trip: %v", got.Settings)
	}
	if got.ConfigContract == nil || *got.ConfigContract != "TraktPopularSettings" {
		t.Errorf("ConfigContract round-trip: %v", got.ConfigContract)
	}
	if got.EnableAutomaticAdd == nil || !*got.EnableAutomaticAdd {
		t.Errorf("EnableAutomaticAdd: %v", got.EnableAutomaticAdd)
	}
	if got.RootFolderPath != "/tv" || !got.SeasonFolder {
		t.Errorf("scalar: %+v", got)
	}

	byName, err := repo.FindByName(t.Context(), "trakt-popular")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if byName.ID != want.ID {
		t.Errorf("FindByName id: %d", byName.ID)
	}
	if _, err := repo.FindByName(t.Context(), "missing"); !errors.Is(err, importlists.ErrNotFound) {
		t.Errorf("FindByName(missing): %v", err)
	}

	got.RootFolderPath = "/anime"
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.RootFolderPath != "/anime" {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Delete(t.Context(), want.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), want.ID); !errors.Is(err, importlists.ErrNotFound) {
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
