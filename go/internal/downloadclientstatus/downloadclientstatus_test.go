package downloadclientstatus_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/downloadclientstatus"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := downloadclientstatus.NewRepo(db)
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
	full := &downloadclientstatus.Status{
		ProviderID:        7,
		InitialFailure:    &now,
		MostRecentFailure: &now,
		EscalationLevel:   3,
		DisabledTill:      &tomorrow,
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	clean := &downloadclientstatus.Status{ProviderID: 8}
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
	if got.InitialFailure == nil || !got.InitialFailure.Equal(now) {
		t.Errorf("InitialFailure: %v", got.InitialFailure)
	}
	if got.DisabledTill == nil || !got.DisabledTill.Equal(tomorrow) {
		t.Errorf("DisabledTill: %v", got.DisabledTill)
	}

	if !got.IsDisabled(now) {
		t.Errorf("IsDisabled(%v) = false, want true", now)
	}
	if got.IsDisabled(tomorrow.Add(time.Hour)) {
		t.Errorf("IsDisabled past window = true")
	}

	got2, err := repo.GetByProviderID(t.Context(), 8)
	if err != nil {
		t.Fatalf("GetByProviderID(8): %v", err)
	}
	if got2.InitialFailure != nil || got2.MostRecentFailure != nil || got2.DisabledTill != nil {
		t.Errorf("expected all-nil times: %+v", got2)
	}

	if _, err := repo.Insert(t.Context(), &downloadclientstatus.Status{ProviderID: 7}); err == nil {
		t.Errorf("Insert duplicate ProviderId: expected error")
	}

	got.EscalationLevel = 4
	got.DisabledTill = nil // de-escalation: explicit clear
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.EscalationLevel != 4 || post.DisabledTill != nil {
		t.Errorf("post-Update: %+v", post)
	}

	if err := repo.Update(t.Context(), &downloadclientstatus.Status{ID: 99999, ProviderID: 1}); !errors.Is(err, downloadclientstatus.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); !errors.Is(err, downloadclientstatus.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, downloadclientstatus.ErrNotFound) {
		t.Errorf("Get(missing): %v", err)
	}

	// Upsert insert + reuse-Id-for-existing + update.
	up := &downloadclientstatus.Status{ProviderID: 21, EscalationLevel: 1}
	if err := repo.Upsert(t.Context(), up); err != nil {
		t.Fatalf("Upsert(new): %v", err)
	}
	firstID := up.ID
	up2 := &downloadclientstatus.Status{ProviderID: 21, EscalationLevel: 4}
	if err := repo.Upsert(t.Context(), up2); err != nil {
		t.Fatalf("Upsert(existing, Id=0): %v", err)
	}
	if up2.ID != firstID {
		t.Errorf("Upsert reuse Id: got %d want %d", up2.ID, firstID)
	}

	if err := repo.DeleteByProviderID(t.Context(), 21); err != nil {
		t.Fatalf("DeleteByProviderID: %v", err)
	}
	if _, err := repo.GetByProviderID(t.Context(), 21); !errors.Is(err, downloadclientstatus.ErrNotFound) {
		t.Errorf("post-DeleteByProviderID Get: %v", err)
	}
	if err := repo.DeleteByProviderID(t.Context(), 99999); err != nil {
		t.Errorf("DeleteByProviderID(missing): %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
