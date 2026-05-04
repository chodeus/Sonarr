package indexerstatus_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/indexerstatus"
	"sonarr.tv/sonarr/internal/messaging"
)

// Service tests mirror the notificationstatus suite — wiring
// smoke + the indexer-only LastRssSyncReleaseInfo round-trip.

var refNow = time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) *indexerstatus.Service {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := indexerstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := indexerstatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
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
	repo, _ := indexerstatus.NewRepo(db)
	bus := messaging.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var got atomic.Int32
	messaging.Subscribe(bus, "is.test",
		func(_ context.Context, _ indexerstatus.StatusChangedEvent) error {
			got.Add(1)
			return nil
		})

	svc, _ := indexerstatus.NewService(repo, bus, refNow.Add(-1*time.Hour))
	svc.SetClock(func() time.Time { return refNow })
	_ = svc.RecordFailure(t.Context(), 7, 0)
	_ = svc.RecordSuccess(t.Context(), 7)

	bus.BeginShutdown()
	bus.Wait()
	if got.Load() != 2 {
		t.Errorf("event count: got %d want 2", got.Load())
	}
}

// Pre-existing rows with LastRssSyncReleaseInfo set should
// round-trip through the Service path untouched — the column
// doesn't participate in the back-off logic.
func TestService_LastRssSyncReleaseInfoSurvives(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := indexerstatus.NewRepo(db)
	row := &indexerstatus.Status{
		ProviderID:             42,
		EscalationLevel:        0,
		LastRssSyncReleaseInfo: `{"title":"S01E01"}`,
	}
	if _, err := repo.Insert(t.Context(), row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	svc, _ := indexerstatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
	svc.SetClock(func() time.Time { return refNow })

	_ = svc.RecordFailure(t.Context(), 42, 0)

	got, err := repo.GetByProviderID(t.Context(), 42)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if got.LastRssSyncReleaseInfo != `{"title":"S01E01"}` {
		t.Errorf("LastRssSyncReleaseInfo round-trip: got %q", got.LastRssSyncReleaseInfo)
	}
	if got.EscalationLevel != 1 {
		t.Errorf("EscalationLevel: got %d want 1", got.EscalationLevel)
	}
}
