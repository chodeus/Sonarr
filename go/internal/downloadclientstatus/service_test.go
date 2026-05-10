package downloadclientstatus_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/downloadclientstatus"
	"sonarr.tv/sonarr/internal/messaging"
)

// Service tests for downloadclientstatus mirror the
// notificationstatus suite — same shared logic, different per-type
// repo + event. The light coverage here pins that the wiring is
// correct; the deep behavioural tests live on the pure-logic side
// in providerstatus/logic_test.go.

var refNow = time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) *downloadclientstatus.Service {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := downloadclientstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := downloadclientstatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetClock(func() time.Time { return refNow })
	return svc
}

// newServiceWithRepo returns the Service alongside its underlying
// Repo so tests that need to inspect the row directly (not via the
// disabled-filter projection) can do so.
func newServiceWithRepo(t *testing.T) (*downloadclientstatus.Service, *downloadclientstatus.Repo) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := downloadclientstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := downloadclientstatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetClock(func() time.Time { return refNow })
	return svc, repo
}

// TestService_FreshFailureSeedsRow_NoDisable verifies the C# override
// at `Download/DownloadClientStatusService.cs:18` —
// MinimumTimeSinceInitialFailure=5min — keeps a fresh failure in
// the grace window (no DisabledTill, level=1 only).
func TestService_FreshFailureSeedsRow_NoDisable(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceWithRepo(t)
	if err := svc.RecordFailure(t.Context(), 7, 0); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	// The row is seeded at level=1 but the 5-min grace window means
	// DisabledTill stays nil, so GetBlockedProviders filters it out.
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 0 {
		t.Errorf("GetBlockedProviders: got %d blocked, want 0 (5-min grace window suppresses DisabledTill on first failure)", len(all))
	}
	row, err := repo.GetByProviderID(t.Context(), 7)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if row.EscalationLevel != 1 {
		t.Errorf("EscalationLevel = %d, want 1", row.EscalationLevel)
	}
	if row.DisabledTill != nil {
		t.Errorf("DisabledTill = %v, want nil (in grace window)", row.DisabledTill)
	}
}

// TestService_FreshFailureWithMinimumBackOff_DisablesPastGrace
// verifies that an explicit minimumBackOff bypasses the grace window
// — matches the C# `ApplyFailure(minimumBackOff > 0)` arm.
func TestService_FreshFailureWithMinimumBackOff_DisablesPastGrace(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	if err := svc.RecordFailure(t.Context(), 7, time.Hour); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 1 {
		t.Fatalf("expected 1 blocked, got %d", len(all))
	}
}

func TestService_RecordSuccessClearsDisabled(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	_ = svc.RecordFailure(t.Context(), 7, time.Hour) // bypass grace via minimumBackOff
	_ = svc.RecordSuccess(t.Context(), 7)
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 0 {
		t.Errorf("expected unblocked, got %d", len(all))
	}
}

func TestService_OnProviderDeletedRemovesRow(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceWithRepo(t)
	_ = svc.RecordFailure(t.Context(), 7, 0)
	if err := svc.OnProviderDeleted(t.Context(), 7); err != nil {
		t.Fatalf("OnProviderDeleted: %v", err)
	}
	if _, err := repo.GetByProviderID(t.Context(), 7); err == nil {
		t.Errorf("row still present after OnProviderDeleted")
	}
}

// TestService_MaxEscalationLevelOverride verifies the C# override at
// `Download/DownloadClientStatusService.cs:19` — MaxEscalationLevel=5.
// Repeated escalating failures should cap at level 5 regardless of how
// many times RecordFailure is invoked.
func TestService_MaxEscalationLevelOverride(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceWithRepo(t)

	// Seed the row past the initial-failure grace window so each
	// subsequent RecordFailure actually escalates.
	pastFailure := refNow.Add(-1 * time.Hour)
	if err := repo.Upsert(t.Context(), &downloadclientstatus.Status{
		ProviderID:      7,
		InitialFailure:  &pastFailure,
		EscalationLevel: 1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Many failures — more than enough to push past the C# override
	// cap of 5.
	for i := 0; i < 20; i++ {
		if err := svc.RecordFailure(t.Context(), 7, 0); err != nil {
			t.Fatalf("RecordFailure[%d]: %v", i, err)
		}
	}

	row, err := repo.GetByProviderID(t.Context(), 7)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if row.EscalationLevel != downloadclientstatus.MaxEscalationLevel {
		t.Errorf("EscalationLevel = %d, want %d (MaxEscalationLevel cap)", row.EscalationLevel, downloadclientstatus.MaxEscalationLevel)
	}
}

func TestService_PublishesEvent(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := downloadclientstatus.NewRepo(db)
	bus := messaging.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var got atomic.Int32
	messaging.Subscribe(bus, "dcs.test",
		func(_ context.Context, _ downloadclientstatus.StatusChangedEvent) error {
			got.Add(1)
			return nil
		})

	svc, _ := downloadclientstatus.NewService(repo, bus, refNow.Add(-1*time.Hour))
	svc.SetClock(func() time.Time { return refNow })
	_ = svc.RecordFailure(t.Context(), 7, 0)
	_ = svc.RecordSuccess(t.Context(), 7) // level 1→0 publishes

	bus.BeginShutdown()
	bus.Wait()
	if got.Load() != 2 {
		t.Errorf("event count: got %d want 2", got.Load())
	}
}

func TestService_NonPositiveProviderIDIgnored(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	if err := svc.RecordFailure(t.Context(), 0, 0); err != nil {
		t.Errorf("RecordFailure(0): %v", err)
	}
	if err := svc.RecordSuccess(t.Context(), -1); err != nil {
		t.Errorf("RecordSuccess(-1): %v", err)
	}
	if err := svc.RecordConnectionFailure(t.Context(), 0); err != nil {
		t.Errorf("RecordConnectionFailure(0): %v", err)
	}
}
