package notificationstatus_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/notificationstatus"
	"sonarr.tv/sonarr/internal/providerstatus"
)

// refNow is the pinned clock for deterministic service tests; far
// past any plausible runtimeStart so the startup-grace window has
// lapsed by default.
var refNow = time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

func newServiceLongAgo(t *testing.T) *notificationstatus.Service {
	t.Helper()
	svc, _ := newServiceWithRepo(t)
	return svc
}

// newServiceWithRepo returns the Service alongside its underlying
// Repo so tests that need to seed rows past the 5-minute grace window
// (or inspect the row directly, not via the disabled-filter
// projection) can do so.
func newServiceWithRepo(t *testing.T) (*notificationstatus.Service, *notificationstatus.Repo) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := notificationstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := notificationstatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetClock(func() time.Time { return refNow })
	return svc, repo
}

// TestService_FreshFailureSeedsRow_NoDisable verifies the C# override
// at `Notifications/NotificationStatusService.cs:18` —
// MinimumTimeSinceInitialFailure=5min — keeps a fresh failure in the
// grace window (no DisabledTill, level=1 only). Mirrors the
// downloadclientstatus precedent at
// downloadclientstatus/service_test.go.
func TestService_FreshFailureSeedsRow_NoDisable(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceWithRepo(t)
	if err := svc.RecordFailure(t.Context(), 42, 0); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 0 {
		t.Errorf("GetBlockedProviders: got %d blocked, want 0 (5-min grace window suppresses DisabledTill on first failure)", len(all))
	}
	row, err := repo.GetByProviderID(t.Context(), 42)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if row.EscalationLevel != 1 {
		t.Errorf("EscalationLevel = %d, want 1", row.EscalationLevel)
	}
	if row.InitialFailure == nil || !row.InitialFailure.Equal(refNow) {
		t.Errorf("InitialFailure: %v, want %v", row.InitialFailure, refNow)
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
	svc := newServiceLongAgo(t)
	if err := svc.RecordFailure(t.Context(), 42, time.Hour); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 1 {
		t.Fatalf("expected 1 blocked, got %d", len(all))
	}
}

// Two consecutive failures past the grace window escalate level 1→2.
// Seeds the row via the repo so InitialFailure is outside the
// MinTimeSinceInitialFailure=5min window.
func TestService_RecordFailure_Escalates(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceWithRepo(t)
	pastFailure := refNow.Add(-1 * time.Hour)
	if err := repo.Upsert(t.Context(), &notificationstatus.Status{
		ProviderID:      42,
		InitialFailure:  &pastFailure,
		EscalationLevel: 1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = svc.RecordFailure(t.Context(), 42, 0)
	row, err := repo.GetByProviderID(t.Context(), 42)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if row.EscalationLevel != 2 {
		t.Errorf("EscalationLevel: %d want 2", row.EscalationLevel)
	}
}

// RecordSuccess after escalating drops level back down. Uses a
// minimumBackOff to bypass the grace window on the first failure so
// the level/DisabledTill pair is meaningfully set.
func TestService_RecordSuccess_DeEscalates(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	_ = svc.RecordFailure(t.Context(), 42, time.Hour) // bypass grace
	if err := svc.RecordSuccess(t.Context(), 42); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 0 {
		t.Errorf("post-Success blocked: %d", len(all))
	}
}

// TestService_MaxEscalationLevelOverride verifies the C# override at
// `Notifications/NotificationStatusService.cs:19` — MaxEscalationLevel=5.
// Repeated escalating failures cap at level 5 regardless of how many
// times RecordFailure is invoked.
func TestService_MaxEscalationLevelOverride(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceWithRepo(t)

	// Seed past the initial-failure grace window so each subsequent
	// RecordFailure actually escalates.
	pastFailure := refNow.Add(-1 * time.Hour)
	if err := repo.Upsert(t.Context(), &notificationstatus.Status{
		ProviderID:      42,
		InitialFailure:  &pastFailure,
		EscalationLevel: 1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 0; i < 20; i++ {
		if err := svc.RecordFailure(t.Context(), 42, 0); err != nil {
			t.Fatalf("RecordFailure[%d]: %v", i, err)
		}
	}

	row, err := repo.GetByProviderID(t.Context(), 42)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if row.EscalationLevel != notificationstatus.MaxEscalationLevel {
		t.Errorf("EscalationLevel = %d, want %d (MaxEscalationLevel cap)", row.EscalationLevel, notificationstatus.MaxEscalationLevel)
	}
}

// RecordSuccess on a clean provider is a no-op (no row is inserted).
func TestService_RecordSuccess_FreshIsNoop(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	if err := svc.RecordSuccess(t.Context(), 42); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 0 {
		t.Errorf("expected no rows after RecordSuccess on fresh provider")
	}
}

// providerID <= 0 is silently ignored (matches C# `:62-65` early
// return).
func TestService_NonPositiveProviderIDIgnored(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	for _, id := range []int{0, -1, -99} {
		if err := svc.RecordFailure(t.Context(), id, 0); err != nil {
			t.Errorf("RecordFailure(%d): %v", id, err)
		}
		if err := svc.RecordSuccess(t.Context(), id); err != nil {
			t.Errorf("RecordSuccess(%d): %v", id, err)
		}
		if err := svc.RecordConnectionFailure(t.Context(), id); err != nil {
			t.Errorf("RecordConnectionFailure(%d): %v", id, err)
		}
	}
}

// RecordConnectionFailure does NOT escalate the level (escalate=false).
// Seeds via the repo to land past the 5-min grace window so DisabledTill
// is set on the back-off side, then RecordConnectionFailure twice and
// asserts the level stayed at 1.
func TestService_RecordConnectionFailure(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceWithRepo(t)
	pastFailure := refNow.Add(-1 * time.Hour)
	if err := repo.Upsert(t.Context(), &notificationstatus.Status{
		ProviderID:      42,
		InitialFailure:  &pastFailure,
		EscalationLevel: 1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = svc.RecordConnectionFailure(t.Context(), 42) // no escalate
	_ = svc.RecordConnectionFailure(t.Context(), 42) // no escalate
	row, err := repo.GetByProviderID(t.Context(), 42)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if row.EscalationLevel != 1 {
		t.Errorf("EscalationLevel: %d want 1 (connection failures don't escalate)", row.EscalationLevel)
	}
}

// RecordFailure with a minimum back-off seeks the level upward.
func TestService_RecordFailure_MinimumBackOff(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	_ = svc.RecordFailure(t.Context(), 42, 25*time.Minute)
	all, _ := svc.GetBlockedProviders(t.Context())
	// First failure forces escalate=false but the minimumBackOff
	// loop bumps level up to a 30min tier (level 4).
	if all[0].EscalationLevel != 4 {
		t.Errorf("EscalationLevel: %d want 4 (25min→30min tier)", all[0].EscalationLevel)
	}
}

// OnProviderDeleted removes the status row.
func TestService_OnProviderDeleted(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	_ = svc.RecordFailure(t.Context(), 42, 0)
	if err := svc.OnProviderDeleted(t.Context(), 42); err != nil {
		t.Fatalf("OnProviderDeleted: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 0 {
		t.Errorf("expected no rows after delete, got %d", len(all))
	}
}

// Bus event publication: every successful mutation publishes a
// StatusChangedEvent. RecordSuccess on a fresh provider does NOT
// publish (mutate returns false).
func TestService_PublishesStatusChangedEvent(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := notificationstatus.NewRepo(db)
	bus := messaging.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var changed atomic.Int32
	messaging.Subscribe(bus, "ns.test",
		func(_ context.Context, _ notificationstatus.StatusChangedEvent) error {
			changed.Add(1)
			return nil
		})

	svc, _ := notificationstatus.NewService(repo, bus, refNow.Add(-1*time.Hour))
	svc.SetClock(func() time.Time { return refNow })

	_ = svc.RecordFailure(t.Context(), 42, 0)        // publishes
	_ = svc.RecordConnectionFailure(t.Context(), 42) // publishes
	_ = svc.RecordSuccess(t.Context(), 99)           // does NOT publish (no row)
	_ = svc.RecordSuccess(t.Context(), 42)           // publishes (level 1→0)

	bus.BeginShutdown()
	bus.Wait()

	if got := changed.Load(); got != 3 {
		t.Errorf("StatusChangedEvent count: got %d want 3", got)
	}
}

// Tunables override propagates into the back-off arithmetic.
func TestService_TunablesOverride(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := notificationstatus.NewRepo(db)
	svc, _ := notificationstatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
	svc.SetClock(func() time.Time { return refNow })

	tun := providerstatus.DefaultTunables(refNow.Add(-1 * time.Hour))
	tun.MaxEscalationLevel = 2
	svc.SetTunables(tun)

	for i := 0; i < 5; i++ {
		_ = svc.RecordFailure(t.Context(), 42, 0)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if all[0].EscalationLevel != 2 {
		t.Errorf("EscalationLevel: %d want 2 (capped by tunable)", all[0].EscalationLevel)
	}
}
