package refreshseries_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/metadatasource"
	"sonarr.tv/sonarr/internal/refreshseries"
	"sonarr.tv/sonarr/internal/series"
)

type stubProvider struct {
	gotTvdbID int
	merged    *series.Series
	eps       []episodes.Episode
	err       error
}

func (s *stubProvider) GetSeriesInfo(_ context.Context, tvdbID int) (*series.Series, []episodes.Episode, error) {
	s.gotTvdbID = tvdbID
	return s.merged, s.eps, s.err
}

type stubStore struct {
	stored    *series.Series
	getErr    error
	updated   *series.Series // captures the row passed to UpdateSeries
	updateErr error
}

func (s *stubStore) GetSeries(_ context.Context, _ int) (*series.Series, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	// Return a deep-ish copy so the service can mutate without
	// corrupting the test fixture.
	cp := *s.stored
	return &cp, nil
}

func (s *stubStore) UpdateSeries(_ context.Context, ns *series.Series, _ series.UpdateOptions) (*series.Series, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	cp := *ns
	s.updated = &cp
	return &cp, nil
}

func mustNewService(t *testing.T, info *stubProvider, store *stubStore) *refreshseries.Service {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) }
	svc, err := refreshseries.NewService(info, store, nil, now, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestRefreshSeriesInfo_HappyPath_CopiesFields(t *testing.T) {
	t.Parallel()
	info := &stubProvider{
		merged: &series.Series{
			TvdbID:    100,
			Title:     "skyhook-title",
			Year:      2024,
			TitleSlug: "skyhook-slug",
			TvRageID:  9,
			TvMazeID:  8,
			TmdbID:    7,
			ImdbID:    "tt-skyhook",
			Network:   "Disney+",
			Overview:  "skyhook-overview",
			Status:    1,
			Seasons:   `[{"seasonNumber":1,"monitored":true}]`,
		},
	}
	store := &stubStore{
		stored: &series.Series{
			ID:        42,
			TvdbID:    100,
			Title:     "stale-title",
			TitleSlug: "stale-slug",
			Network:   "stale-network",
			Overview:  "stale-overview",
			Status:    0,
			// Existing season — same SeasonNumber but Monitored=false:
			// the refresh should preserve this user choice.
			Seasons: `[{"seasonNumber":1,"monitored":false}]`,
		},
	}
	svc := mustNewService(t, info, store)

	got, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}
	if got.Title != "skyhook-title" {
		t.Errorf("Title: got %q", got.Title)
	}
	if got.TitleSlug != "skyhook-slug" {
		t.Errorf("TitleSlug: got %q", got.TitleSlug)
	}
	if got.ImdbID != "tt-skyhook" {
		t.Errorf("ImdbID: got %q", got.ImdbID)
	}
	if got.Network != "Disney+" {
		t.Errorf("Network: got %q", got.Network)
	}
	if got.Overview != "skyhook-overview" {
		t.Errorf("Overview: got %q", got.Overview)
	}
	if got.LastInfoSync == nil {
		t.Fatal("LastInfoSync: want non-nil")
	}

	// Existing-season monitored=false must be preserved.
	var seasons []map[string]any
	if err := json.Unmarshal([]byte(got.Seasons), &seasons); err != nil {
		t.Fatalf("decode merged seasons: %v", err)
	}
	if len(seasons) != 1 {
		t.Fatalf("seasons: got %d, want 1", len(seasons))
	}
	if mon, _ := seasons[0]["monitored"].(bool); mon {
		t.Errorf("monitored: want false (preserved), got true")
	}

	// UpdateSeries was called with the refreshed row.
	if store.updated == nil {
		t.Fatal("UpdateSeries not called")
	}
	if store.updated.Title != "skyhook-title" {
		t.Errorf("UpdateSeries: persisted Title %q", store.updated.Title)
	}
}

func TestRefreshSeriesInfo_NewSeason_MonitorNewItemsAll(t *testing.T) {
	t.Parallel()
	info := &stubProvider{
		merged: &series.Series{
			TvdbID:  100,
			Seasons: `[{"seasonNumber":1},{"seasonNumber":2}]`, // S2 is brand new
		},
	}
	store := &stubStore{
		stored: &series.Series{
			TvdbID:          100,
			MonitorNewItems: 0, // All → new seasons should be monitored
			Seasons:         `[{"seasonNumber":1,"monitored":true}]`,
		},
	}
	svc := mustNewService(t, info, store)
	got, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}

	var seasons []map[string]any
	_ = json.Unmarshal([]byte(got.Seasons), &seasons)
	if len(seasons) != 2 {
		t.Fatalf("seasons: got %d, want 2", len(seasons))
	}
	if mon, _ := seasons[1]["monitored"].(bool); !mon {
		t.Errorf("season 2 monitored: want true (MonitorNewItems=All), got false")
	}
}

func TestRefreshSeriesInfo_NewSeason_MonitorNewItemsNone(t *testing.T) {
	t.Parallel()
	info := &stubProvider{
		merged: &series.Series{
			TvdbID:  100,
			Seasons: `[{"seasonNumber":2}]`,
		},
	}
	store := &stubStore{
		stored: &series.Series{
			TvdbID:          100,
			MonitorNewItems: 1, // None
			Seasons:         `[]`,
		},
	}
	svc := mustNewService(t, info, store)
	got, _ := svc.RefreshSeriesInfo(t.Context(), 42)

	var seasons []map[string]any
	_ = json.Unmarshal([]byte(got.Seasons), &seasons)
	if mon, _ := seasons[0]["monitored"].(bool); mon {
		t.Errorf("season 2 monitored: want false (MonitorNewItems=None), got true")
	}
}

func TestRefreshSeriesInfo_NewSeasonZero_AlwaysUnmonitored(t *testing.T) {
	t.Parallel()
	info := &stubProvider{
		merged: &series.Series{
			TvdbID:  100,
			Seasons: `[{"seasonNumber":0},{"seasonNumber":1}]`,
		},
	}
	store := &stubStore{
		stored: &series.Series{
			TvdbID:          100,
			MonitorNewItems: 0, // All — but season 0 still defaults unmonitored
			Seasons:         `[]`,
		},
	}
	svc := mustNewService(t, info, store)
	got, _ := svc.RefreshSeriesInfo(t.Context(), 42)

	var seasons []map[string]any
	_ = json.Unmarshal([]byte(got.Seasons), &seasons)
	s0Mon, _ := seasons[0]["monitored"].(bool)
	s1Mon, _ := seasons[1]["monitored"].(bool)
	if s0Mon {
		t.Errorf("season 0: want false, got true")
	}
	if !s1Mon {
		t.Errorf("season 1: want true (MonitorNewItems=All), got false")
	}
}

func TestRefreshSeriesInfo_TvdbIdReplaced_WarnsAndTakesNew(t *testing.T) {
	t.Parallel()
	info := &stubProvider{
		merged: &series.Series{
			TvdbID:  999, // Different from the stored 100 — upstream dedup
			Title:   "new-title",
			Seasons: `[{"seasonNumber":1}]`,
		},
	}
	store := &stubStore{
		stored: &series.Series{TvdbID: 100, Title: "old-title", Seasons: "[]"},
	}
	svc := mustNewService(t, info, store)
	got, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}
	if got.TvdbID != 999 {
		t.Errorf("TvdbID: got %d, want 999 (upstream replacement)", got.TvdbID)
	}
}

func TestRefreshSeriesInfo_GetSeriesInfoError_Propagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("skyhook down")
	info := &stubProvider{err: boom}
	store := &stubStore{stored: &series.Series{TvdbID: 100}}
	svc := mustNewService(t, info, store)
	_, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if !errors.Is(err, boom) {
		t.Fatalf("err: got %v, want wrapped boom", err)
	}
	if store.updated != nil {
		t.Errorf("UpdateSeries should not have been called on info error")
	}
}

func TestRefreshSeriesInfo_NilMerged_Errors(t *testing.T) {
	t.Parallel()
	info := &stubProvider{merged: nil}
	store := &stubStore{stored: &series.Series{TvdbID: 100}}
	svc := mustNewService(t, info, store)
	_, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if err == nil {
		t.Fatal("want error on nil merged")
	}
}

func TestRefresh_AdaptsToErrorOnly(t *testing.T) {
	t.Parallel()
	info := &stubProvider{merged: &series.Series{TvdbID: 100, Seasons: "[]"}}
	store := &stubStore{stored: &series.Series{TvdbID: 100, Seasons: "[]"}}
	svc := mustNewService(t, info, store)
	if err := svc.Refresh(t.Context(), 42); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
}

func TestNewService_RejectsNilInfo(t *testing.T) {
	t.Parallel()
	_, err := refreshseries.NewService(nil, &stubStore{}, nil, nil, nil)
	if err == nil {
		t.Fatal("want error for nil info")
	}
}

func TestNewService_RejectsNilStore(t *testing.T) {
	t.Parallel()
	_, err := refreshseries.NewService(&stubProvider{}, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("want error for nil store")
	}
}

// mustNewServiceWithBus is like mustNewService but takes an explicit
// bus so the J192 soft-delete arm can be observed via subscription.
func mustNewServiceWithBus(t *testing.T, info *stubProvider, store *stubStore, bus *messaging.Bus) *refreshseries.Service {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) }
	svc, err := refreshseries.NewService(info, store, bus, now, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestRefreshSeriesInfo_SeriesNotFound_FlipsStatusToDeleted(t *testing.T) {
	t.Parallel()
	info := &stubProvider{err: metadatasource.NewSeriesNotFoundError(100)}
	store := &stubStore{
		stored: &series.Series{
			ID:     42,
			TvdbID: 100,
			Title:  "Cancelled Show",
			Status: series.SeriesStatusContinuing, // not yet deleted
		},
	}
	bus := messaging.New(slog.Default())
	defer bus.BeginShutdown()

	var mu sync.Mutex
	var got []series.SeriesUpdatedEvent
	messaging.Subscribe(bus, "test.SoftDelete", func(_ context.Context, ev series.SeriesUpdatedEvent) error {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		return nil
	})

	svc := mustNewServiceWithBus(t, info, store, bus)
	_, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if !errors.Is(err, metadatasource.ErrSeriesNotFound) {
		t.Fatalf("err: want wrapped ErrSeriesNotFound, got %v", err)
	}

	if store.updated == nil {
		t.Fatal("UpdateSeries not called on soft-delete arm")
	}
	if store.updated.Status != series.SeriesStatusDeleted {
		t.Errorf("Status: got %d, want SeriesStatusDeleted (%d)", store.updated.Status, series.SeriesStatusDeleted)
	}

	// Wait for the sync handler to complete; Publish runs sync
	// subscribers inline so this is non-blocking by the time
	// RefreshSeriesInfo returned. Keep the lock-pop pattern for
	// the race detector.
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("SeriesUpdatedEvent: got %d events, want 1", len(got))
	}
	if got[0].Series == nil || got[0].Series.Status != series.SeriesStatusDeleted {
		t.Errorf("event series.Status: not flagged deleted: %+v", got[0].Series)
	}
}

func TestRefreshSeriesInfo_SeriesNotFound_AlreadyDeleted_NoOp(t *testing.T) {
	t.Parallel()
	info := &stubProvider{err: metadatasource.NewSeriesNotFoundError(100)}
	store := &stubStore{
		stored: &series.Series{
			ID:     42,
			TvdbID: 100,
			Status: series.SeriesStatusDeleted, // already deleted — no-op
		},
	}
	svc := mustNewService(t, info, store)

	_, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if !errors.Is(err, metadatasource.ErrSeriesNotFound) {
		t.Fatalf("err: want wrapped ErrSeriesNotFound, got %v", err)
	}
	if store.updated != nil {
		t.Errorf("UpdateSeries should not be called when already deleted; got %+v", store.updated)
	}
}

func TestRefreshSeriesInfo_SeriesNotFound_NilBus_SkipsPublish(t *testing.T) {
	t.Parallel()
	info := &stubProvider{err: metadatasource.NewSeriesNotFoundError(100)}
	store := &stubStore{
		stored: &series.Series{ID: 42, TvdbID: 100, Status: series.SeriesStatusContinuing},
	}
	// nil bus — should still persist the status flip silently.
	svc := mustNewService(t, info, store)
	_, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if !errors.Is(err, metadatasource.ErrSeriesNotFound) {
		t.Fatalf("err: want wrapped ErrSeriesNotFound, got %v", err)
	}
	if store.updated == nil || store.updated.Status != series.SeriesStatusDeleted {
		t.Errorf("nil-bus path should still persist Deleted status; got %+v", store.updated)
	}
}

func TestRefreshSeriesInfo_GenericError_NoSoftDelete(t *testing.T) {
	t.Parallel()
	info := &stubProvider{err: errors.New("not a not-found")}
	store := &stubStore{
		stored: &series.Series{ID: 42, TvdbID: 100, Status: series.SeriesStatusContinuing},
	}
	svc := mustNewService(t, info, store)
	_, err := svc.RefreshSeriesInfo(t.Context(), 42)
	if err == nil {
		t.Fatal("expected error propagation")
	}
	// Generic error (not ErrSeriesNotFound) must NOT trigger the
	// soft-delete arm — only TheTVDB 404 marks the row deleted.
	if store.updated != nil {
		t.Errorf("UpdateSeries should not be called for generic errors; got %+v", store.updated)
	}
}
