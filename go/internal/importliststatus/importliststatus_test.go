package importliststatus_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/importliststatus"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := importliststatus.NewRepo(db)
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

	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	tomorrow := now.Add(24 * time.Hour)
	full := &importliststatus.Status{
		ProviderID:                   7,
		InitialFailure:               &now,
		MostRecentFailure:            &now,
		EscalationLevel:              3,
		DisabledTill:                 &tomorrow,
		LastInfoSync:                 &now,
		HasRemovedItemSinceLastClean: true,
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	clean := &importliststatus.Status{ProviderID: 8}
	if _, err := repo.Insert(t.Context(), clean); err != nil {
		t.Fatalf("Insert clean: %v", err)
	}

	got, err := repo.GetByProviderID(t.Context(), 7)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if got.EscalationLevel != 3 {
		t.Errorf("EscalationLevel: %d", got.EscalationLevel)
	}
	if got.LastInfoSync == nil || !got.LastInfoSync.Equal(now) {
		t.Errorf("LastInfoSync: %v", got.LastInfoSync)
	}
	if !got.HasRemovedItemSinceLastClean {
		t.Errorf("HasRemovedItemSinceLastClean: false, want true")
	}
	if !got.IsDisabled(now) {
		t.Errorf("IsDisabled at now: false")
	}

	got2, err := repo.GetByProviderID(t.Context(), 8)
	if err != nil {
		t.Fatalf("GetByProviderID(8): %v", err)
	}
	if got2.LastInfoSync != nil || got2.HasRemovedItemSinceLastClean {
		t.Errorf("clean defaults: %+v", got2)
	}

	if _, err := repo.Insert(t.Context(), &importliststatus.Status{ProviderID: 7}); err == nil {
		t.Errorf("Insert duplicate ProviderId: expected error")
	}

	got.HasRemovedItemSinceLastClean = false
	got.LastInfoSync = nil
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.HasRemovedItemSinceLastClean || post.LastInfoSync != nil {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Update(t.Context(), &importliststatus.Status{ID: 99999, ProviderID: 1}); !errors.Is(err, importliststatus.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); !errors.Is(err, importliststatus.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, importliststatus.ErrNotFound) {
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
