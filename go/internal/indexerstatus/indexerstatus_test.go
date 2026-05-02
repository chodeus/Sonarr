package indexerstatus_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/indexerstatus"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := indexerstatus.NewRepo(db)
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

	// Realistic-shape ReleaseInfo JSON. We don't validate the shape — the
	// repo treats it as opaque text — but exercising a non-trivial body
	// catches naive escaping bugs.
	const releaseJSON = `{"title":"Foo.S01E01","publishDate":"2026-05-01T12:00:00Z","size":1234567}`

	full := &indexerstatus.Status{
		ProviderID:             7,
		InitialFailure:         &now,
		MostRecentFailure:      &now,
		EscalationLevel:        3,
		DisabledTill:           &tomorrow,
		LastRssSyncReleaseInfo: releaseJSON,
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	clean := &indexerstatus.Status{ProviderID: 8}
	if _, err := repo.Insert(t.Context(), clean); err != nil {
		t.Fatalf("Insert clean: %v", err)
	}

	got, err := repo.GetByProviderID(t.Context(), 7)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if got.LastRssSyncReleaseInfo != releaseJSON {
		t.Errorf("LastRssSyncReleaseInfo round-trip:\n got: %s\nwant: %s", got.LastRssSyncReleaseInfo, releaseJSON)
	}
	if !got.IsDisabled(now) {
		t.Errorf("IsDisabled at now: false")
	}

	// Empty-string LastRssSyncReleaseInfo persists as NULL → empty on read.
	got2, err := repo.GetByProviderID(t.Context(), 8)
	if err != nil {
		t.Fatalf("GetByProviderID(8): %v", err)
	}
	if got2.LastRssSyncReleaseInfo != "" {
		t.Errorf("clean LastRssSyncReleaseInfo: %q, want empty", got2.LastRssSyncReleaseInfo)
	}

	if _, err := repo.Insert(t.Context(), &indexerstatus.Status{ProviderID: 7}); err == nil {
		t.Errorf("Insert duplicate ProviderId: expected error")
	}

	// Update — clear LastRssSyncReleaseInfo back to NULL.
	got.LastRssSyncReleaseInfo = ""
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.LastRssSyncReleaseInfo != "" {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Update(t.Context(), &indexerstatus.Status{ID: 99999, ProviderID: 1}); !errors.Is(err, indexerstatus.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); !errors.Is(err, indexerstatus.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, indexerstatus.ErrNotFound) {
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
