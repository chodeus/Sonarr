package episoderefreshed_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episoderefreshed"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// ---- Stubs ----

type stubSeriesLookup struct {
	rows map[int]*series.Series
	err  error
}

func (s *stubSeriesLookup) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows[id], nil
}

type stubEpisodeLookup struct {
	rows map[int]*episodes.Episode
	errs map[int]error
}

func (s *stubEpisodeLookup) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	if err, ok := s.errs[id]; ok {
		return nil, err
	}
	return s.rows[id], nil
}

type stubSearcher struct {
	mu    sync.Mutex
	calls [][]int
	err   error
}

func (s *stubSearcher) SearchEpisodes(_ context.Context, ids []int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]int, len(ids))
	copy(cp, ids)
	s.calls = append(s.calls, cp)
	return s.err
}

func (s *stubSearcher) lastCall() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return nil
	}
	return s.calls[len(s.calls)-1]
}

func (s *stubSearcher) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// fixed-clock helper.
func at(t time.Time) func() time.Time { return func() time.Time { return t } }

// ptrTime / ptrInt small helpers (other packages duplicate these).
func ptrTime(t time.Time) *time.Time { return &t }
func ptrInt(v int) *int              { return &v }

const seriesID = 42

func mustNewSvc(t *testing.T, seriesRow *series.Series, episodeRows map[int]*episodes.Episode, now time.Time) (*episoderefreshed.Service, *stubSearcher) {
	t.Helper()
	sl := &stubSeriesLookup{rows: map[int]*series.Series{seriesID: seriesRow}}
	el := &stubEpisodeLookup{rows: episodeRows}
	searcher := &stubSearcher{}
	svc, err := episoderefreshed.NewService(nil, sl, el, searcher, at(now), nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, searcher
}

// ---- Handle tests ----

func TestHandle_SkipsWhenAddOptionsSet(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true, AddOptions: `{"monitor":"all"}`}
	svc, searcher := mustNewSvc(t, ser, nil, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -1)), Monitored: true},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Search drains nothing because Handle skipped.
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("AddOptions present: searcher should NOT have been called; got %d calls", searcher.callCount())
	}
}

func TestHandle_SkipsWhenSeriesNotMonitored(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: false}
	svc, searcher := mustNewSvc(t, ser, nil, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -1)), Monitored: true},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("series not monitored: searcher should NOT have been called; got %d calls", searcher.callCount())
	}
}

func TestHandle_AddedRecentMonitored_Buffered(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	eps := map[int]*episodes.Episode{
		1: {ID: 1, EpisodeFileID: nil}, // missing file → search dispatched
	}
	svc, searcher := mustNewSvc(t, ser, eps, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -3)), Monitored: true}, // 3 days ago, in window
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 1 {
		t.Fatalf("recent+monitored Added: searcher call count=%d, want 1", searcher.callCount())
	}
	got := searcher.lastCall()
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("dispatched IDs=%v, want [1]", got)
	}
}

func TestHandle_AddedFutureBeyondOneDay_Skipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	svc, searcher := mustNewSvc(t, ser, nil, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, 5)), Monitored: true}, // 5 days in future
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("future episode: searcher should NOT have been called; got %d calls", searcher.callCount())
	}
}

func TestHandle_AddedTooOld_Skipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	svc, searcher := mustNewSvc(t, ser, nil, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -20)), Monitored: true}, // 20 days ago
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("too-old episode: searcher should NOT have been called; got %d calls", searcher.callCount())
	}
}

func TestHandle_AddedUnmonitored_Skipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	svc, searcher := mustNewSvc(t, ser, nil, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -1)), Monitored: false}, // unmonitored episode
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("unmonitored episode: searcher should NOT have been called; got %d calls", searcher.callCount())
	}
}

func TestHandle_UpdatedWithAbsAdded_Buffered(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	eps := map[int]*episodes.Episode{
		5: {ID: 5, EpisodeFileID: nil},
	}
	svc, searcher := mustNewSvc(t, ser, eps, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Updated: []episodes.Episode{
			{ID: 5, AirDateUtc: ptrTime(now.AddDate(0, 0, -2)), Monitored: true, AbsoluteEpisodeNumberAdded: true},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 1 {
		t.Fatalf("Updated+abs-added: searcher call count=%d, want 1", searcher.callCount())
	}
	got := searcher.lastCall()
	if len(got) != 1 || got[0] != 5 {
		t.Errorf("dispatched IDs=%v, want [5]", got)
	}
}

func TestHandle_UpdatedWithoutAbsAdded_Skipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	svc, searcher := mustNewSvc(t, ser, nil, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Updated: []episodes.Episode{
			{ID: 5, AirDateUtc: ptrTime(now.AddDate(0, 0, -2)), Monitored: true, AbsoluteEpisodeNumberAdded: false},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("Updated w/o flag: searcher should NOT have been called; got %d calls", searcher.callCount())
	}
}

func TestHandle_DistinctAcrossAddedAndUpdated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	eps := map[int]*episodes.Episode{
		1: {ID: 1},
		2: {ID: 2},
	}
	svc, searcher := mustNewSvc(t, ser, eps, now)

	// Edge case: same ID appears in both Added and Updated (unusual but
	// possible if Updated re-emits a row that's also in Added). Distinct()
	// should collapse to a single entry.
	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -3)), Monitored: true},
			{ID: 2, AirDateUtc: ptrTime(now.AddDate(0, 0, -1)), Monitored: true},
		},
		Updated: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -3)), Monitored: true, AbsoluteEpisodeNumberAdded: true},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 1 {
		t.Fatalf("distinct: searcher call count=%d, want 1", searcher.callCount())
	}
	got := searcher.lastCall()
	if len(got) != 2 {
		t.Errorf("dispatched IDs=%v, want exactly 2 unique entries (1, 2)", got)
	}
}

// ---- Search tests ----

func TestSearch_NoCachedEntry_NoOp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	svc, searcher := mustNewSvc(t, ser, nil, now)

	// Never call Handle → no cache entry. Search should be a quiet no-op.
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("no cached entry: searcher should NOT have been called; got %d", searcher.callCount())
	}
}

func TestSearch_AllCachedHaveFiles_NoDispatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	// Both episodes have non-nil EpisodeFileID > 0 → HasFile=true → skip dispatch.
	eps := map[int]*episodes.Episode{
		1: {ID: 1, EpisodeFileID: ptrInt(100)},
		2: {ID: 2, EpisodeFileID: ptrInt(101)},
	}
	svc, searcher := mustNewSvc(t, ser, eps, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -3)), Monitored: true},
			{ID: 2, AirDateUtc: ptrTime(now.AddDate(0, 0, -1)), Monitored: true},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("all have files: searcher should NOT have been called; got %d", searcher.callCount())
	}
}

func TestSearch_DrainsCacheEvenWhenNoDispatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	eps := map[int]*episodes.Episode{
		1: {ID: 1, EpisodeFileID: ptrInt(100)}, // already has file
	}
	svc, searcher := mustNewSvc(t, ser, eps, now)

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -3)), Monitored: true},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	// Second Search should be a clean no-op — cache drained even though
	// no dispatch fired.
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if searcher.callCount() != 0 {
		t.Errorf("searcher unexpectedly invoked; got %d", searcher.callCount())
	}
}

func TestSearch_NilSearcher_DrainsAndSkipsDispatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	eps := map[int]*episodes.Episode{
		1: {ID: 1, EpisodeFileID: nil},
	}
	sl := &stubSeriesLookup{rows: map[int]*series.Series{seriesID: ser}}
	el := &stubEpisodeLookup{rows: eps}
	svc, err := episoderefreshed.NewService(nil, sl, el, nil /* searcher nil */, at(now), nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -3)), Monitored: true},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v (nil searcher should be tolerated)", err)
	}
}

func TestSearch_EpisodeLookupError_AbsorbsAndContinues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ser := &series.Series{ID: seriesID, Monitored: true}
	eps := map[int]*episodes.Episode{
		2: {ID: 2, EpisodeFileID: nil},
	}
	sl := &stubSeriesLookup{rows: map[int]*series.Series{seriesID: ser}}
	el := &stubEpisodeLookup{rows: eps, errs: map[int]error{1: errors.New("DB down")}}
	searcher := &stubSearcher{}
	svc, err := episoderefreshed.NewService(nil, sl, el, searcher, at(now), nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	ev := episodes.EpisodeInfoRefreshedEvent{
		SeriesID: seriesID,
		Added: []episodes.Episode{
			{ID: 1, AirDateUtc: ptrTime(now.AddDate(0, 0, -3)), Monitored: true},
			{ID: 2, AirDateUtc: ptrTime(now.AddDate(0, 0, -1)), Monitored: true},
		},
	}
	if err := svc.Handle(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := svc.Search(t.Context(), seriesID); err != nil {
		t.Fatalf("Search: %v (lookup error should be absorbed)", err)
	}
	// ID 1 failed to load → skipped; ID 2 dispatched.
	if searcher.callCount() != 1 {
		t.Fatalf("searcher call count=%d, want 1", searcher.callCount())
	}
	got := searcher.lastCall()
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("dispatched IDs=%v, want [2]", got)
	}
}

// ---- Constructor validation ----

func TestNewService_NilSeriesLookup_Errors(t *testing.T) {
	t.Parallel()
	_, err := episoderefreshed.NewService(nil, nil, &stubEpisodeLookup{}, nil, nil, nil)
	if err == nil {
		t.Fatal("NewService(nil seriesLookup): want error, got nil")
	}
}

func TestNewService_NilEpisodeLookup_Errors(t *testing.T) {
	t.Parallel()
	_, err := episoderefreshed.NewService(nil, &stubSeriesLookup{}, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("NewService(nil episodeLookup): want error, got nil")
	}
}
