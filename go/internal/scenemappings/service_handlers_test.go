package scenemappings_test

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/scenemappings"
)

// TestHandleSeriesAddedOrImported_FetchesOnceAfterStartup — the first
// SeriesAdded/Imported triggers an on-demand UpdateMappings; subsequent
// ones no-op (the C# `_updatedAfterStartup` gate).
func TestHandleSeriesAddedOrImported_FetchesOnceAfterStartup(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{mappings: []scenemappings.Mapping{{MappingID: "a", Title: "Alpha", SearchTerm: "alpha"}}}
	svc := makeSvc(t, newFakeStore(), []scenemappings.Provider{prov}, nil)

	if err := svc.HandleSeriesAddedOrImported(t.Context()); err != nil {
		t.Fatalf("first HandleSeriesAddedOrImported: %v", err)
	}
	if prov.calls != 1 {
		t.Fatalf("after first add: provider calls = %d, want 1", prov.calls)
	}
	// Second + third should be no-ops (already updated after startup).
	_ = svc.HandleSeriesAddedOrImported(t.Context())
	_ = svc.HandleSeriesAddedOrImported(t.Context())
	if prov.calls != 1 {
		t.Errorf("after repeat adds: provider calls = %d, want 1 (once-after-startup gate)", prov.calls)
	}
}

// TestHandleSeriesRefreshStarting_NonManualIsNoop — a scheduled / per-id
// background refresh never triggers a provider fetch.
func TestHandleSeriesRefreshStarting_NonManualIsNoop(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{}
	svc := makeSvc(t, newFakeStore(), []scenemappings.Provider{prov}, nil)
	if err := svc.HandleSeriesRefreshStarting(t.Context(), false); err != nil {
		t.Fatalf("HandleSeriesRefreshStarting(false): %v", err)
	}
	if prov.calls != 0 {
		t.Errorf("non-manual refresh triggered %d provider calls, want 0", prov.calls)
	}
}

// TestHandleSeriesRefreshStarting_ManualFetchesWhenNotUpdated — a manual
// refresh on a never-updated service fetches.
func TestHandleSeriesRefreshStarting_ManualFetchesWhenNotUpdated(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{}
	svc := makeSvc(t, newFakeStore(), []scenemappings.Provider{prov}, nil)
	if err := svc.HandleSeriesRefreshStarting(t.Context(), true); err != nil {
		t.Fatalf("HandleSeriesRefreshStarting(true): %v", err)
	}
	if prov.calls != 1 {
		t.Errorf("manual refresh (not-yet-updated) triggered %d provider calls, want 1", prov.calls)
	}
}

// TestHandleSeriesRefreshStarting_ManualRespectsStaleness — once updated,
// a manual refresh re-fetches only when the cache is older than 1 minute.
func TestHandleSeriesRefreshStarting_ManualRespectsStaleness(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{}

	var mu sync.Mutex
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clk }
	advance := func(d time.Duration) { mu.Lock(); clk = clk.Add(d); mu.Unlock() }

	svc := scenemappings.NewService(newFakeStore(), slog.Default()).
		WithWriter(newFakeStore()).
		WithProviders(prov).
		WithClock(now)

	// First manual refresh fetches (not-yet-updated) and stamps lastRefresh=T0.
	if err := svc.HandleSeriesRefreshStarting(t.Context(), true); err != nil {
		t.Fatalf("first manual refresh: %v", err)
	}
	if prov.calls != 1 {
		t.Fatalf("calls after first manual refresh = %d, want 1", prov.calls)
	}

	// Same instant: cache is fresh + updatedAfterStartup → no re-fetch.
	advance(30 * time.Second)
	_ = svc.HandleSeriesRefreshStarting(t.Context(), true)
	if prov.calls != 1 {
		t.Errorf("calls after fresh-cache manual refresh = %d, want 1 (within 1min, no re-fetch)", prov.calls)
	}

	// Past the 1-minute window → re-fetch.
	advance(2 * time.Minute)
	if err := svc.HandleSeriesRefreshStarting(t.Context(), true); err != nil {
		t.Fatalf("stale manual refresh: %v", err)
	}
	if prov.calls != 2 {
		t.Errorf("calls after stale manual refresh = %d, want 2", prov.calls)
	}
}
