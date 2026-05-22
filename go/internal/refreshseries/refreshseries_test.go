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
	"sonarr.tv/sonarr/internal/mediafiles"
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
	stored       *series.Series
	all          []series.Series // optional — feeds GetAllSeries when set
	allErr       error
	getErr       error
	updated      *series.Series // captures the row passed to UpdateSeries
	updateErr    error
	tagsCalled   int   // counts UpdateTags invocations
	tagsCalledOn []int // captures the series IDs UpdateTags was called for
	tagsChanged  bool
	tagsErr      error
}

func (s *stubStore) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	// When `all` is populated (sweep tests) — return the matching row
	// by ID so RefreshSeriesInfo's per-id GetSeries call inside
	// RefreshAll finds the seed shape. Otherwise default to `stored`.
	if len(s.all) > 0 {
		for i := range s.all {
			if s.all[i].ID == id {
				cp := s.all[i]
				return &cp, nil
			}
		}
	}
	if s.stored != nil {
		cp := *s.stored
		return &cp, nil
	}
	return nil, errors.New("stubStore: no series configured")
}

func (s *stubStore) GetAllSeries(_ context.Context) ([]series.Series, error) {
	if s.allErr != nil {
		return nil, s.allErr
	}
	return s.all, nil
}

func (s *stubStore) UpdateSeries(_ context.Context, ns *series.Series, _ series.UpdateOptions) (*series.Series, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	cp := *ns
	s.updated = &cp
	return &cp, nil
}

func (s *stubStore) UpdateTags(_ context.Context, row *series.Series) (bool, error) {
	s.tagsCalled++
	if row != nil {
		s.tagsCalledOn = append(s.tagsCalledOn, row.ID)
	}
	return s.tagsChanged, s.tagsErr
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

	got, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
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
	got, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
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
	got, _ := svc.RefreshSeriesInfo(t.Context(), 42, false)

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
	got, _ := svc.RefreshSeriesInfo(t.Context(), 42, false)

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
	got, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
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
	_, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
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
	_, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
	if err == nil {
		t.Fatal("want error on nil merged")
	}
}

func TestRefresh_AdaptsToErrorOnly(t *testing.T) {
	t.Parallel()
	info := &stubProvider{merged: &series.Series{TvdbID: 100, Seasons: "[]"}}
	store := &stubStore{stored: &series.Series{TvdbID: 100, Seasons: "[]"}}
	svc := mustNewService(t, info, store)
	if err := svc.Refresh(t.Context(), 42, false); err != nil {
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
	_, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
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

	_, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
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
	_, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
	if !errors.Is(err, metadatasource.ErrSeriesNotFound) {
		t.Fatalf("err: want wrapped ErrSeriesNotFound, got %v", err)
	}
	if store.updated == nil || store.updated.Status != series.SeriesStatusDeleted {
		t.Errorf("nil-bus path should still persist Deleted status; got %+v", store.updated)
	}
}

func TestRefreshAll_IteratesInSortTitleOrder(t *testing.T) {
	t.Parallel()
	// Provider returns the same merged shape for every TVDB id so we
	// just need to observe the order GetSeries is called.
	var seenIDs []int
	info := &stubProvider{
		merged: &series.Series{Seasons: "[]"},
	}
	store := &stubStore{
		all: []series.Series{
			{ID: 1, TvdbID: 100, SortTitle: "zulu show"},
			{ID: 2, TvdbID: 200, SortTitle: "alpha show"},
			{ID: 3, TvdbID: 300, SortTitle: "mike show"},
		},
	}
	now := func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) }
	svc, err := refreshseries.NewService(info, &orderCapturingStore{stubStore: store, ids: &seenIDs}, nil, now, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.RefreshAll(t.Context(), false); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	want := []int{2, 3, 1} // alpha, mike, zulu
	if len(seenIDs) != len(want) {
		t.Fatalf("seen IDs: got %v, want %v", seenIDs, want)
	}
	for i, id := range want {
		if seenIDs[i] != id {
			t.Errorf("call[%d]: got id=%d, want %d", i, seenIDs[i], id)
		}
	}
}

func TestRefreshAll_PerRowErrorAbsorbed(t *testing.T) {
	t.Parallel()
	// Force GetSeriesInfo to fail mid-sweep — sweep must continue.
	info := &errorOnTvdbProvider{badTvdb: 200}
	store := &stubStore{
		all: []series.Series{
			{ID: 1, TvdbID: 100, SortTitle: "a"},
			{ID: 2, TvdbID: 200, SortTitle: "b"},
			{ID: 3, TvdbID: 300, SortTitle: "c"},
		},
	}
	now := func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) }
	svc, _ := refreshseries.NewService(info, store, nil, now, nil)
	if err := svc.RefreshAll(t.Context(), false); err != nil {
		t.Errorf("RefreshAll: per-row error should be absorbed, got %v", err)
	}
	if info.callCount != 3 {
		t.Errorf("provider call count: got %d, want 3 (sweep should continue past failed row)", info.callCount)
	}
}

func TestRefreshAll_GetAllSeriesError_Propagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	store := &stubStore{allErr: boom}
	svc, _ := refreshseries.NewService(&stubProvider{}, store, nil, nil, nil)
	err := svc.RefreshAll(t.Context(), false)
	if !errors.Is(err, boom) {
		t.Fatalf("RefreshAll: want wrapped boom, got %v", err)
	}
}

func TestRefreshAll_CtxCancelMidSweep_PropagatesCancel(t *testing.T) {
	t.Parallel()
	info := &stubProvider{merged: &series.Series{Seasons: "[]"}}
	store := &stubStore{
		all: []series.Series{
			{ID: 1, TvdbID: 100, SortTitle: "a"},
			{ID: 2, TvdbID: 200, SortTitle: "b"},
		},
	}
	svc, _ := refreshseries.NewService(info, store, nil, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // pre-cancel
	if err := svc.RefreshAll(ctx, false); !errors.Is(err, context.Canceled) {
		t.Errorf("RefreshAll: want context.Canceled, got %v", err)
	}
}

// J196 — RefreshSeriesInfo calls UpdateTags after successful persist.
func TestRefreshSeriesInfo_CallsUpdateTags(t *testing.T) {
	t.Parallel()
	info := &stubProvider{merged: &series.Series{TvdbID: 100, Seasons: "[]"}}
	store := &stubStore{
		stored: &series.Series{ID: 42, TvdbID: 100, Seasons: "[]"},
	}
	svc := mustNewService(t, info, store)
	if _, err := svc.RefreshSeriesInfo(t.Context(), 42, false); err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}
	if store.tagsCalled != 1 {
		t.Errorf("UpdateTags call count: got %d, want 1", store.tagsCalled)
	}
	if len(store.tagsCalledOn) != 1 || store.tagsCalledOn[0] != 42 {
		t.Errorf("UpdateTags called for: got %v, want [42]", store.tagsCalledOn)
	}
}

// J196 — RefreshSeriesInfo absorbs UpdateTags errors with a warn log.
func TestRefreshSeriesInfo_UpdateTagsError_Absorbed(t *testing.T) {
	t.Parallel()
	info := &stubProvider{merged: &series.Series{TvdbID: 100, Seasons: "[]"}}
	store := &stubStore{
		stored:  &series.Series{ID: 42, TvdbID: 100, Seasons: "[]"},
		tagsErr: errors.New("autotag down"),
	}
	svc := mustNewService(t, info, store)
	if _, err := svc.RefreshSeriesInfo(t.Context(), 42, false); err != nil {
		t.Errorf("RefreshSeriesInfo: UpdateTags error should be absorbed, got %v", err)
	}
}

// J196 — RefreshAll calls UpdateTags for skipped rows too (C# :277-279).
func TestRefreshAll_CallsUpdateTagsOnSkippedRows(t *testing.T) {
	t.Parallel()
	info := &stubProvider{merged: &series.Series{TvdbID: 100, Seasons: "[]"}}
	now := func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) }
	// Row recently synced + status ended + no recent episodes → skip.
	store := &stubStore{
		all: []series.Series{
			{
				ID:           1,
				TvdbID:       100,
				SortTitle:    "alpha",
				Status:       series.SeriesStatusEnded,
				LastInfoSync: ptrTime(now().Add(-1 * time.Hour)),
			},
		},
	}
	svc, _ := refreshseries.NewService(info, store, nil, now, nil)
	svc.SetEpisodeLookup(&stubEpisodeLookup{ /* no episodes — ended-no-recent skips */ })
	if err := svc.RefreshAll(t.Context(), false); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if store.tagsCalled != 1 {
		t.Errorf("UpdateTags call count (skipped row): got %d, want 1 (skipped rows still get UpdateTags)", store.tagsCalled)
	}
}

// orderCapturingStore wraps stubStore to record the IDs that pass
// through GetSeries — used by TestRefreshAll_IteratesInSortTitleOrder.
type orderCapturingStore struct {
	*stubStore
	ids *[]int
}

func (o *orderCapturingStore) GetSeries(ctx context.Context, id int) (*series.Series, error) {
	*o.ids = append(*o.ids, id)
	return o.stubStore.GetSeries(ctx, id)
}

// errorOnTvdbProvider is a stub provider that returns an error for
// one specific TvdbID and the default merged shape for everything
// else. Tracks total invocations so the sweep can be observed to
// continue past failed rows.
type errorOnTvdbProvider struct {
	badTvdb   int
	callCount int
}

func (e *errorOnTvdbProvider) GetSeriesInfo(_ context.Context, tvdbID int) (*series.Series, []episodes.Episode, error) {
	e.callCount++
	if tvdbID == e.badTvdb {
		return nil, nil, errors.New("upstream blew up")
	}
	return &series.Series{TvdbID: tvdbID, Seasons: "[]"}, nil, nil
}

func TestRefreshSeriesInfo_GenericError_NoSoftDelete(t *testing.T) {
	t.Parallel()
	info := &stubProvider{err: errors.New("not a not-found")}
	store := &stubStore{
		stored: &series.Series{ID: 42, TvdbID: 100, Status: series.SeriesStatusContinuing},
	}
	svc := mustNewService(t, info, store)
	_, err := svc.RefreshSeriesInfo(t.Context(), 42, false)
	if err == nil {
		t.Fatal("expected error propagation")
	}
	// Generic error (not ErrSeriesNotFound) must NOT trigger the
	// soft-delete arm — only TheTVDB 404 marks the row deleted.
	if store.updated != nil {
		t.Errorf("UpdateSeries should not be called for generic errors; got %+v", store.updated)
	}
}

// ---- J197 — DiskScanService.Rescan integration tests ----

// stubRescanner captures Scan calls and lets a test pin a scan-side
// error to assert the warn-and-absorb stance.
type stubRescanner struct {
	mu    sync.Mutex
	calls []int
	err   error
}

func (r *stubRescanner) Scan(_ context.Context, ser *series.Series) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ser != nil {
		r.calls = append(r.calls, ser.ID)
	}
	return r.err
}

func (r *stubRescanner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// stubRescanCfg pins the RescanAfterRefresh enum string.
type stubRescanCfg struct{ mode string }

func (s stubRescanCfg) RescanAfterRefresh() string { return s.mode }

// j197Stubs returns a service wired with happy-path provider+store
// stubs. The rescan tests only need to observe the post-refresh tail
// (scanner + bus subscribers), so the provider and store handles are
// internal.
func j197Stubs(t *testing.T) *refreshseries.Service {
	t.Helper()
	info := &stubProvider{
		merged: &series.Series{
			TvdbID:    100,
			Title:     "skyhook-title",
			TitleSlug: "skyhook-slug",
			Status:    1,
			Seasons:   `[{"seasonNumber":1,"monitored":true}]`,
		},
	}
	store := &stubStore{
		stored: &series.Series{
			ID:      42,
			TvdbID:  100,
			Title:   "old-title",
			Status:  series.SeriesStatusContinuing,
			Seasons: `[{"seasonNumber":1,"monitored":true}]`,
		},
	}
	svc := mustNewService(t, info, store)
	return svc
}

func TestRescanSeries_AlwaysMode_CallsScan(t *testing.T) {
	t.Parallel()
	svc := j197Stubs(t)
	scanner := &stubRescanner{}
	svc.SetRescanner(scanner, stubRescanCfg{mode: "Always"})

	if _, err := svc.RefreshSeriesInfo(t.Context(), 42, false); err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}
	if scanner.callCount() != 1 || scanner.calls[0] != 42 {
		t.Errorf("expected one Scan call for series 42; got %+v", scanner.calls)
	}
}

func TestRescanSeries_NeverMode_PublishesSkipEvent(t *testing.T) {
	t.Parallel()
	info := &stubProvider{
		merged: &series.Series{
			TvdbID: 100, Title: "skyhook-title", TitleSlug: "skyhook-slug", Status: 1,
			Seasons: `[{"seasonNumber":1,"monitored":true}]`,
		},
	}
	store := &stubStore{
		stored: &series.Series{
			ID: 42, TvdbID: 100, Title: "old-title", Status: series.SeriesStatusContinuing,
			Seasons: `[{"seasonNumber":1,"monitored":true}]`,
		},
	}
	bus := messaging.New(slog.Default())
	defer bus.BeginShutdown()

	var mu sync.Mutex
	var got []mediafiles.SeriesScanSkippedEvent
	messaging.Subscribe(bus, "test.J197Never", func(_ context.Context, ev mediafiles.SeriesScanSkippedEvent) error {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		return nil
	})
	svc := mustNewServiceWithBus(t, info, store, bus)
	scanner := &stubRescanner{}
	svc.SetRescanner(scanner, stubRescanCfg{mode: "Never"})

	if _, err := svc.RefreshSeriesInfo(t.Context(), 42, false); err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}
	if scanner.callCount() != 0 {
		t.Errorf("Never mode must skip Scan; got calls=%+v", scanner.calls)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected one SeriesScanSkippedEvent; got %d", len(got))
	}
	if got[0].Reason != mediafiles.SeriesScanSkippedReasonNeverRescanAfterRefresh {
		t.Errorf("Never reason mismatch; got %s", got[0].Reason)
	}
	if got[0].Series == nil || got[0].Series.ID != 42 {
		t.Errorf("event series mismatch; got %+v", got[0].Series)
	}
}

func TestRescanSeries_AfterManualMode_ScheduledTrigger_PublishesSkip(t *testing.T) {
	t.Parallel()
	info := &stubProvider{
		merged: &series.Series{
			TvdbID: 100, Title: "skyhook-title", TitleSlug: "skyhook-slug", Status: 1,
			Seasons: `[{"seasonNumber":1,"monitored":true}]`,
		},
	}
	store := &stubStore{
		stored: &series.Series{
			ID: 42, TvdbID: 100, Title: "old-title", Status: series.SeriesStatusContinuing,
			Seasons: `[{"seasonNumber":1,"monitored":true}]`,
		},
	}
	bus := messaging.New(slog.Default())
	defer bus.BeginShutdown()

	var mu sync.Mutex
	var got []mediafiles.SeriesScanSkippedEvent
	messaging.Subscribe(bus, "test.J197AfterManual", func(_ context.Context, ev mediafiles.SeriesScanSkippedEvent) error {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		return nil
	})
	svc := mustNewServiceWithBus(t, info, store, bus)
	scanner := &stubRescanner{}
	svc.SetRescanner(scanner, stubRescanCfg{mode: "AfterManual"})

	// RefreshSeriesInfo passes isManual=false today — AfterManual +
	// !isManual → skip per the rescanSeries gate.
	if _, err := svc.RefreshSeriesInfo(t.Context(), 42, false); err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}
	if scanner.callCount() != 0 {
		t.Errorf("AfterManual+scheduled must skip Scan; got calls=%+v", scanner.calls)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected one SeriesScanSkippedEvent; got %d", len(got))
	}
	if got[0].Reason != mediafiles.SeriesScanSkippedReasonRescanAfterManualRefreshOnly {
		t.Errorf("AfterManual reason mismatch; got %s", got[0].Reason)
	}
}

func TestRescanSeries_ScanError_Absorbed(t *testing.T) {
	t.Parallel()
	svc := j197Stubs(t)
	scanner := &stubRescanner{err: errors.New("scan blew up")}
	svc.SetRescanner(scanner, stubRescanCfg{mode: "Always"})

	if _, err := svc.RefreshSeriesInfo(t.Context(), 42, false); err != nil {
		t.Errorf("scan error must not abort refresh; got %v", err)
	}
	if scanner.callCount() != 1 {
		t.Errorf("expected one Scan call; got %+v", scanner.calls)
	}
}

func TestRescanSeries_NilRescanner_NoPanicNoScan(t *testing.T) {
	t.Parallel()
	svc := j197Stubs(t)
	// Deliberately do NOT call SetRescanner; refresh must still run.
	if _, err := svc.RefreshSeriesInfo(t.Context(), 42, false); err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}
}

// emptyEpisodeLookup satisfies refreshseries.EpisodeLookup with an
// empty episode list — used so ShouldRefresh runs against the
// supplied predicate rather than short-circuiting on nil-lookup.
type emptyEpisodeLookup struct{}

func (emptyEpisodeLookup) AllBySeriesID(_ context.Context, _ int) ([]episodes.Episode, error) {
	return nil, nil
}

func TestRefreshAll_SkippedRow_StillCallsRescan(t *testing.T) {
	t.Parallel()
	// Pin now and build a row that ShouldRefresh classifies as skip:
	// LastInfoSync 30 min ago (>now-6h gate fails the <30d-stale arm)
	// AND Status=Ended with no recent episodes — the
	// "ended long ago → skip" branch fires.
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-30 * time.Minute)
	row := series.Series{
		ID:           42,
		TvdbID:       100,
		Title:        "fresh-row",
		Status:       series.SeriesStatusEnded,
		LastInfoSync: &recent,
		Seasons:      `[{"seasonNumber":1,"monitored":true}]`,
	}
	store := &stubStore{all: []series.Series{row}}
	info := &stubProvider{}

	svc, err := refreshseries.NewService(info, store, nil, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetEpisodeLookup(emptyEpisodeLookup{})

	scanner := &stubRescanner{}
	svc.SetRescanner(scanner, stubRescanCfg{mode: "Always"})

	if err := svc.RefreshAll(t.Context(), false); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if info.gotTvdbID != 0 {
		t.Errorf("expected ShouldRefresh skip; got SkyHook GetSeriesInfo call tvdbId=%d", info.gotTvdbID)
	}
	if scanner.callCount() != 1 || scanner.calls[0] != 42 {
		t.Errorf("expected one Scan call for skipped row 42; got %+v", scanner.calls)
	}
}

// ---- J198 — isManual gating tests ----

func TestRefreshAll_Manual_BypassesShouldRefresh(t *testing.T) {
	t.Parallel()
	// Row classified as skip by ShouldRefresh (recent LastInfoSync,
	// Ended status, no recent episodes — same shape as the J197 sweep
	// test). Without isManual the row should skip; with isManual the
	// predicate is bypassed and the row refreshes.
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-30 * time.Minute)
	row := series.Series{
		ID:           42,
		TvdbID:       100,
		Title:        "fresh-row",
		Status:       series.SeriesStatusEnded,
		LastInfoSync: &recent,
		Seasons:      `[{"seasonNumber":1,"monitored":true}]`,
	}
	store := &stubStore{all: []series.Series{row}}
	// SkyHook provider returns a valid merged row so the per-id refresh
	// reaches the persist path.
	info := &stubProvider{
		merged: &series.Series{
			TvdbID:  100,
			Title:   "refreshed-row",
			Seasons: `[{"seasonNumber":1,"monitored":true}]`,
			Status:  1,
		},
	}

	svc, err := refreshseries.NewService(info, store, nil, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetEpisodeLookup(emptyEpisodeLookup{})

	// Manual trigger bypasses the gate; SkyHook GetSeriesInfo must fire.
	if err := svc.RefreshAll(t.Context(), true /* isManual */); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if info.gotTvdbID != 100 {
		t.Errorf("expected manual-trigger bypass; SkyHook GetSeriesInfo not called (gotTvdbID=%d)", info.gotTvdbID)
	}
}

func TestRescanSeries_AfterManualMode_ManualTrigger_CallsScan(t *testing.T) {
	t.Parallel()
	// AfterManual + isManual=true → Scan should fire (not skip).
	svc := j197Stubs(t)
	scanner := &stubRescanner{}
	svc.SetRescanner(scanner, stubRescanCfg{mode: "AfterManual"})

	if _, err := svc.RefreshSeriesInfo(t.Context(), 42, true /* isManual */); err != nil {
		t.Fatalf("RefreshSeriesInfo: %v", err)
	}
	if scanner.callCount() != 1 || scanner.calls[0] != 42 {
		t.Errorf("AfterManual+manual must call Scan; got %+v", scanner.calls)
	}
}
