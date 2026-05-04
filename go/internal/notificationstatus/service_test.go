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
	return svc
}

// First failure on a fresh provider seeds InitialFailure +
// MostRecentFailure, sets level=1, and disables for 60s (Periods[1]).
func TestService_RecordFailure_FreshProvider(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	if err := svc.RecordFailure(t.Context(), 42, 0); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 1 {
		t.Fatalf("GetBlockedProviders: len=%d want 1", len(all))
	}
	got := all[0]
	if got.ProviderID != 42 || got.EscalationLevel != 1 {
		t.Errorf("first failure: %+v", got)
	}
	if got.InitialFailure == nil || !got.InitialFailure.Equal(refNow) {
		t.Errorf("InitialFailure: %v", got.InitialFailure)
	}
	if got.DisabledTill == nil || !got.DisabledTill.Equal(refNow.Add(60*time.Second)) {
		t.Errorf("DisabledTill: %v", got.DisabledTill)
	}
}

// Two consecutive failures escalate level 1→2.
func TestService_RecordFailure_Escalates(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	_ = svc.RecordFailure(t.Context(), 42, 0)
	_ = svc.RecordFailure(t.Context(), 42, 0)
	all, _ := svc.GetBlockedProviders(t.Context())
	if all[0].EscalationLevel != 2 {
		t.Errorf("EscalationLevel: %d want 2", all[0].EscalationLevel)
	}
}

// RecordSuccess after escalating drops level back down.
func TestService_RecordSuccess_DeEscalates(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	_ = svc.RecordFailure(t.Context(), 42, 0)
	_ = svc.RecordFailure(t.Context(), 42, 0)
	if err := svc.RecordSuccess(t.Context(), 42); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	// Level 1 → DisabledTill cleared by ApplySuccess → not blocked anymore.
	if len(all) != 0 {
		t.Errorf("post-Success blocked: %d", len(all))
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
func TestService_RecordConnectionFailure(t *testing.T) {
	t.Parallel()
	svc := newServiceLongAgo(t)
	_ = svc.RecordFailure(t.Context(), 42, 0)        // seed
	_ = svc.RecordConnectionFailure(t.Context(), 42) // no escalate
	_ = svc.RecordConnectionFailure(t.Context(), 42) // no escalate
	all, _ := svc.GetBlockedProviders(t.Context())
	if all[0].EscalationLevel != 1 {
		t.Errorf("EscalationLevel: %d want 1 (connection failures don't escalate)", all[0].EscalationLevel)
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
