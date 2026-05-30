package importliststatus_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/importliststatus"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/providerstatus"
)

// Service tests for importliststatus mirror the
// notificationstatus suite — same shared logic, different per-type
// repo + event. Light wiring coverage; deep behavioural tests live
// on the pure-logic side in providerstatus/logic_test.go.

var refNow = time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) *importliststatus.Service {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := importliststatus.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := importliststatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
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
	repo, _ := importliststatus.NewRepo(db)
	bus := messaging.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var got atomic.Int32
	messaging.Subscribe(bus, "ils.test",
		func(_ context.Context, _ importliststatus.StatusChangedEvent) error {
			got.Add(1)
			return nil
		})

	svc, _ := importliststatus.NewService(repo, bus, refNow.Add(-1*time.Hour))
	svc.SetClock(func() time.Time { return refNow })
	_ = svc.RecordFailure(t.Context(), 7, 0)
	_ = svc.RecordSuccess(t.Context(), 7)

	bus.BeginShutdown()
	bus.Wait()
	if got.Load() != 2 {
		t.Errorf("event count: got %d want 2", got.Load())
	}
}

// Pre-existing rows with LastInfoSync / HasRemovedItemSinceLastClean
// set should round-trip through the Service path untouched —
// neither column participates in the back-off logic.
func TestService_TypeSpecificColumnsSurvive(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := importliststatus.NewRepo(db)
	pre := refNow.Add(-2 * time.Hour)
	row := &importliststatus.Status{
		ProviderID:                   42,
		EscalationLevel:              0,
		LastInfoSync:                 &pre,
		HasRemovedItemSinceLastClean: true,
	}
	if _, err := repo.Insert(t.Context(), row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	svc, _ := importliststatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
	svc.SetClock(func() time.Time { return refNow })

	_ = svc.RecordFailure(t.Context(), 42, 0) // mutates back-off, must preserve the rest

	got, err := repo.GetByProviderID(t.Context(), 42)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if got.LastInfoSync == nil || !got.LastInfoSync.Equal(pre) {
		t.Errorf("LastInfoSync round-trip: got %v want %v", got.LastInfoSync, pre)
	}
	if !got.HasRemovedItemSinceLastClean {
		t.Errorf("HasRemovedItemSinceLastClean round-trip: got false want true")
	}
	if got.EscalationLevel != 1 {
		t.Errorf("EscalationLevel: got %d want 1", got.EscalationLevel)
	}
}

// C# `ImportListStatusService` (ImportLists/ImportListStatusService.cs:18-58)
// has no constructor body overrides — unlike NotificationStatusService /
// DownloadClientStatusService it does NOT cap MaxEscalationLevel at 5 or
// shift MinTimeSinceInitialFailure off zero. This test pins that parity by
// escalating beyond level 5 and observing the level ride up to the shared
// providerstatus default (len(BackOffPeriods)-1 = 9). If a future slice
// quietly adds a tunable override here, this test breaks.
func TestService_NoTunablesOverride_MatchesCSharpParity(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, _ := importliststatus.NewRepo(db)
	svc, _ := importliststatus.NewService(repo, nil, refNow.Add(-1*time.Hour))
	svc.SetClock(func() time.Time { return refNow })

	for i := 0; i < providerstatus.MaxEscalationLevel+5; i++ {
		_ = svc.RecordFailure(t.Context(), 7, 0)
	}
	got, err := repo.GetByProviderID(t.Context(), 7)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if got.EscalationLevel != providerstatus.MaxEscalationLevel {
		t.Errorf("EscalationLevel: got %d want %d (default ceiling — no C# override)",
			got.EscalationLevel, providerstatus.MaxEscalationLevel)
	}
}

// --- I4c clean-library pending-clean bookkeeping (J499) ---

func TestService_UpdateListSyncStatus_SetsLastInfoSyncAndFlag(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ctx := t.Context()

	// First sync removed nothing.
	if err := svc.UpdateListSyncStatus(ctx, 3, false); err != nil {
		t.Fatalf("UpdateListSyncStatus: %v", err)
	}
	st, err := svc.GetListStatus(ctx, 3)
	if err != nil {
		t.Fatalf("GetListStatus: %v", err)
	}
	if st.LastInfoSync == nil || !st.LastInfoSync.Equal(refNow) {
		t.Errorf("LastInfoSync: got %v, want %v", st.LastInfoSync, refNow)
	}
	if st.HasRemovedItemSinceLastClean {
		t.Errorf("flag should be false after a no-removal sync")
	}

	// Second sync removed an item — flag OR-accumulates to true.
	if err := svc.UpdateListSyncStatus(ctx, 3, true); err != nil {
		t.Fatalf("UpdateListSyncStatus 2: %v", err)
	}
	st, _ = svc.GetListStatus(ctx, 3)
	if !st.HasRemovedItemSinceLastClean {
		t.Errorf("flag should be true after a removal sync")
	}

	// Third sync removed nothing — flag STAYS true (sticky until cleaned).
	if err := svc.UpdateListSyncStatus(ctx, 3, false); err != nil {
		t.Fatalf("UpdateListSyncStatus 3: %v", err)
	}
	st, _ = svc.GetListStatus(ctx, 3)
	if !st.HasRemovedItemSinceLastClean {
		t.Errorf("flag must remain true until MarkListsAsCleaned")
	}
}

func TestService_GetListStatus_DefaultsWhenAbsent(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	st, err := svc.GetListStatus(t.Context(), 42)
	if err != nil {
		t.Fatalf("GetListStatus: %v", err)
	}
	if st == nil || st.ProviderID != 42 || st.LastInfoSync != nil || st.HasRemovedItemSinceLastClean {
		t.Errorf("absent status should default: %+v", st)
	}
}

func TestService_AllListsSuccessfulWithAPendingClean(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ctx := t.Context()

	// Empty id set: vacuously all-successful but nothing removed → false.
	if ok, err := svc.AllListsSuccessfulWithAPendingClean(ctx, nil); err != nil || ok {
		t.Errorf("empty: got (%v,%v), want (false,nil)", ok, err)
	}

	// List 1 never synced → false.
	if ok, _ := svc.AllListsSuccessfulWithAPendingClean(ctx, []int{1}); ok {
		t.Errorf("never-synced list should gate to false")
	}

	// Both synced, list 1 removed an item → pending clean → true.
	if err := svc.UpdateListSyncStatus(ctx, 1, true); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if err := svc.UpdateListSyncStatus(ctx, 2, false); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if ok, err := svc.AllListsSuccessfulWithAPendingClean(ctx, []int{1, 2}); err != nil || !ok {
		t.Errorf("both synced + one pending clean: got (%v,%v), want (true,nil)", ok, err)
	}

	// List 2 now in a back-off window (last sync failed) → false even
	// though list 1 has a pending clean.
	if err := svc.RecordFailure(ctx, 2, 0); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if ok, _ := svc.AllListsSuccessfulWithAPendingClean(ctx, []int{1, 2}); ok {
		t.Errorf("a disabled list must gate to false")
	}
}

func TestService_MarkListsAsCleaned_ClearsFlag(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ctx := t.Context()

	if err := svc.UpdateListSyncStatus(ctx, 1, true); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if err := svc.UpdateListSyncStatus(ctx, 2, true); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if ok, _ := svc.AllListsSuccessfulWithAPendingClean(ctx, []int{1, 2}); !ok {
		t.Fatalf("precondition: should have a pending clean")
	}

	if err := svc.MarkListsAsCleaned(ctx); err != nil {
		t.Fatalf("MarkListsAsCleaned: %v", err)
	}
	for _, id := range []int{1, 2} {
		st, _ := svc.GetListStatus(ctx, id)
		if st.HasRemovedItemSinceLastClean {
			t.Errorf("list %d flag not cleared", id)
		}
		// LastInfoSync must survive the clean.
		if st.LastInfoSync == nil {
			t.Errorf("list %d LastInfoSync wiped by clean", id)
		}
	}
	// No pending clean remains.
	if ok, _ := svc.AllListsSuccessfulWithAPendingClean(ctx, []int{1, 2}); ok {
		t.Errorf("after clean there should be no pending clean")
	}
}
