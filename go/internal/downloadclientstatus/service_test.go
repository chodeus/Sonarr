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

func TestService_FreshFailureSeedsRow(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	if err := svc.RecordFailure(t.Context(), 7, 0); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 1 || all[0].EscalationLevel != 1 {
		t.Errorf("expected len=1 level=1, got %+v", all)
	}
}

func TestService_RecordSuccessClearsDisabled(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	_ = svc.RecordFailure(t.Context(), 7, 0)
	_ = svc.RecordSuccess(t.Context(), 7)
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 0 {
		t.Errorf("expected unblocked, got %d", len(all))
	}
}

func TestService_OnProviderDeletedRemovesRow(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	_ = svc.RecordFailure(t.Context(), 7, 0)
	if err := svc.OnProviderDeleted(t.Context(), 7); err != nil {
		t.Fatalf("OnProviderDeleted: %v", err)
	}
	all, _ := svc.GetBlockedProviders(t.Context())
	if len(all) != 0 {
		t.Errorf("expected len=0 after delete, got %d", len(all))
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
