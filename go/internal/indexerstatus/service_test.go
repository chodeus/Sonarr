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

// GetLastRssSyncReleaseInfo on a fresh service (no rows) returns
// "" + nil — mirrors C# `GetLastRssSyncReleaseInfo` collapsing the
// "no row" case to a null ReleaseInfo (which the Go port collapses
// further to an empty string sentinel).
func TestService_GetLastRssSyncReleaseInfo_AbsentReturnsEmpty(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	got, err := svc.GetLastRssSyncReleaseInfo(t.Context(), 7)
	if err != nil {
		t.Fatalf("GetLastRssSyncReleaseInfo: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// UpdateRssSyncStatus on a provider with no prior row creates the
// row and sets the column. Mirrors C# `GetProviderStatus(id)`
// fabricating a fresh `IndexerStatus { LastRssSyncReleaseInfo =
// releaseInfo }` then upserting.
func TestService_UpdateRssSyncStatus_InsertsWhenAbsent(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	payload := `{"guid":"BTN-12245","publishDate":"2026-05-01T00:00:00Z"}`
	if err := svc.UpdateRssSyncStatus(t.Context(), 11, payload); err != nil {
		t.Fatalf("UpdateRssSyncStatus: %v", err)
	}
	got, err := svc.GetLastRssSyncReleaseInfo(t.Context(), 11)
	if err != nil {
		t.Fatalf("GetLastRssSyncReleaseInfo: %v", err)
	}
	if got != payload {
		t.Errorf("LastRssSyncReleaseInfo round-trip:\n got: %q\nwant: %q", got, payload)
	}
}

// UpdateRssSyncStatus on a row with existing back-off state must
// preserve InitialFailure / MostRecentFailure / EscalationLevel /
// DisabledTill — RSS-sync writes are side-data and never touch the
// escalation columns. Mirrors C# `IndexerStatusService.
// UpdateRssSyncStatus` which sets only the one column.
func TestService_UpdateRssSyncStatus_PreservesEscalation(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	if err := svc.RecordFailure(t.Context(), 13, 0); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	blocked, _ := svc.GetBlockedProviders(t.Context())
	if len(blocked) != 1 {
		t.Fatalf("len(blocked) = %d, want 1", len(blocked))
	}
	wantLevel := blocked[0].EscalationLevel
	wantInitial := blocked[0].InitialFailure
	wantDisabled := blocked[0].DisabledTill

	if err := svc.UpdateRssSyncStatus(t.Context(), 13, `{"guid":"BTN-9"}`); err != nil {
		t.Fatalf("UpdateRssSyncStatus: %v", err)
	}

	post, _ := svc.GetBlockedProviders(t.Context())
	if len(post) != 1 {
		t.Fatalf("len(post-blocked) = %d, want 1", len(post))
	}
	if post[0].EscalationLevel != wantLevel {
		t.Errorf("EscalationLevel: got %d, want %d", post[0].EscalationLevel, wantLevel)
	}
	if post[0].InitialFailure == nil || wantInitial == nil || !post[0].InitialFailure.Equal(*wantInitial) {
		t.Errorf("InitialFailure: got %v, want %v", post[0].InitialFailure, wantInitial)
	}
	if post[0].DisabledTill == nil || wantDisabled == nil || !post[0].DisabledTill.Equal(*wantDisabled) {
		t.Errorf("DisabledTill: got %v, want %v", post[0].DisabledTill, wantDisabled)
	}
	if post[0].LastRssSyncReleaseInfo != `{"guid":"BTN-9"}` {
		t.Errorf("LastRssSyncReleaseInfo: got %q", post[0].LastRssSyncReleaseInfo)
	}
}

// Empty payload clears the column back to NULL — round-trips as "".
// Confirms the `nullableString` mapper semantics survive the
// Service-level update path.
func TestService_UpdateRssSyncStatus_EmptyClearsColumn(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	if err := svc.UpdateRssSyncStatus(t.Context(), 5, `{"guid":"BTN-1"}`); err != nil {
		t.Fatalf("UpdateRssSyncStatus seed: %v", err)
	}
	if err := svc.UpdateRssSyncStatus(t.Context(), 5, ""); err != nil {
		t.Fatalf("UpdateRssSyncStatus clear: %v", err)
	}
	got, _ := svc.GetLastRssSyncReleaseInfo(t.Context(), 5)
	if got != "" {
		t.Errorf("after-clear: got %q, want empty", got)
	}
}

// Invalid providerID (0 or negative) is a no-op on both read and
// write — matches the existing ProviderID <= 0 guards on
// RecordSuccess / RecordFailure / RecordConnectionFailure.
func TestService_RssSyncStatus_NonPositiveProviderIDIsNoOp(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	if err := svc.UpdateRssSyncStatus(t.Context(), 0, `{"guid":"BTN-1"}`); err != nil {
		t.Errorf("UpdateRssSyncStatus(0): %v", err)
	}
	if err := svc.UpdateRssSyncStatus(t.Context(), -1, `{"guid":"BTN-1"}`); err != nil {
		t.Errorf("UpdateRssSyncStatus(-1): %v", err)
	}
	if got, err := svc.GetLastRssSyncReleaseInfo(t.Context(), 0); err != nil || got != "" {
		t.Errorf("GetLastRssSyncReleaseInfo(0): got=%q err=%v", got, err)
	}
	if got, err := svc.GetLastRssSyncReleaseInfo(t.Context(), -1); err != nil || got != "" {
		t.Errorf("GetLastRssSyncReleaseInfo(-1): got=%q err=%v", got, err)
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
