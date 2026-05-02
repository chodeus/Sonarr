package releaseprofiles_test

import (
	"errors"
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/releaseprofiles"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := releaseprofiles.NewRepo(db)
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

	// Full row exercising every JSON column + bool + named profile.
	full := &releaseprofiles.Profile{
		Name:               "REPACK only",
		Enabled:            true,
		Required:           []string{"REPACK", "PROPER"},
		Ignored:            []string{"PREAIR"},
		Tags:               []int{1, 2, 3},
		ExcludedTags:       []int{99},
		IndexerIDs:         []int{10, 20},
		AirDateRestriction: true,
		AirDateGracePeriod: 14,
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	// Sparse — empty slices and unnamed.
	sparse := &releaseprofiles.Profile{
		Enabled: false,
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	got, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got.Required, []string{"REPACK", "PROPER"}) {
		t.Errorf("Required: %v", got.Required)
	}
	if !reflect.DeepEqual(got.Tags, []int{1, 2, 3}) {
		t.Errorf("Tags: %v", got.Tags)
	}
	if !reflect.DeepEqual(got.IndexerIDs, []int{10, 20}) {
		t.Errorf("IndexerIDs: %v", got.IndexerIDs)
	}
	if got.Name != "REPACK only" || !got.AirDateRestriction || got.AirDateGracePeriod != 14 {
		t.Errorf("scalar columns: %+v", got)
	}

	gotSparse, err := repo.Get(t.Context(), sparse.ID)
	if err != nil {
		t.Fatalf("Get sparse: %v", err)
	}
	if gotSparse.Name != "" {
		t.Errorf("sparse Name: %q, want empty", gotSparse.Name)
	}
	// Empty-slice round-trip — JSON "[]" should unmarshal to empty (non-nil).
	if gotSparse.Required == nil || len(gotSparse.Required) != 0 {
		t.Errorf("sparse Required: %v, want empty slice", gotSparse.Required)
	}
	if gotSparse.Tags == nil || len(gotSparse.Tags) != 0 {
		t.Errorf("sparse Tags: %v, want empty slice", gotSparse.Tags)
	}

	// Update — adjust scalars + change one slice.
	got.Required = []string{"REPACK"}
	got.AirDateGracePeriod = 30
	got.Name = "" // clear the nullable name
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if !reflect.DeepEqual(post.Required, []string{"REPACK"}) || post.AirDateGracePeriod != 30 || post.Name != "" {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Update(t.Context(), &releaseprofiles.Profile{ID: 99999}); !errors.Is(err, releaseprofiles.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); !errors.Is(err, releaseprofiles.ErrNotFound) {
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
