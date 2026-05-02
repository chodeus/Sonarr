package notificationstatus_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/notificationstatus"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := notificationstatus.NewRepo(db)
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

	// Insert with ALL nullable times set.
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	tomorrow := now.Add(24 * time.Hour)
	full := &notificationstatus.Status{
		ProviderID:        7,
		InitialFailure:    &now,
		MostRecentFailure: &now,
		EscalationLevel:   3,
		DisabledTill:      &tomorrow,
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	// Insert with all-NULL times — fresh provider, no failures yet.
	clean := &notificationstatus.Status{ProviderID: 8, EscalationLevel: 0}
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

	// IsDisabled — at "now" we're still inside the DisabledTill window.
	if !got.IsDisabled(now) {
		t.Errorf("IsDisabled(%v) = false, want true", now)
	}
	// At "after tomorrow" we should be re-enabled.
	dayAfter := tomorrow.Add(time.Hour)
	if got.IsDisabled(dayAfter) {
		t.Errorf("IsDisabled(%v) = true, want false", dayAfter)
	}

	// Verify NULL round-trip.
	got2, err := repo.GetByProviderID(t.Context(), 8)
	if err != nil {
		t.Fatalf("GetByProviderID(8): %v", err)
	}
	if got2.InitialFailure != nil || got2.MostRecentFailure != nil || got2.DisabledTill != nil {
		t.Errorf("expected all-nil times, got %+v", got2)
	}
	if got2.IsDisabled(now) {
		t.Errorf("clean status IsDisabled = true")
	}

	// UNIQUE on ProviderId.
	if _, err := repo.Insert(t.Context(), &notificationstatus.Status{ProviderID: 7}); err == nil {
		t.Errorf("Insert duplicate ProviderId: expected error, got nil")
	}

	// Update escalates the level + sets DisabledTill.
	got.EscalationLevel = 4
	soon := now.Add(15 * time.Minute)
	got.DisabledTill = &soon
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if post.EscalationLevel != 4 || post.DisabledTill == nil || !post.DisabledTill.Equal(soon) {
		t.Errorf("post-Update: %+v", post)
	}

	// Update on missing.
	if err := repo.Update(t.Context(), &notificationstatus.Status{ID: 99999, ProviderID: 1}); !errors.Is(err, notificationstatus.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); !errors.Is(err, notificationstatus.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, notificationstatus.ErrNotFound) {
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
