package episodes_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// Compile-time bridge: `*episodes.Service` satisfies
// `series.EpisodeMonitorCascade`. Pins the bridge point used by the
// SeriesService UpdateSeries season-monitored cascade in 2.64 — the
// host wires `episodesSvc` into `seriesSvc.SetEpisodeMonitorCascade`
// without source coupling.
var _ series.EpisodeMonitorCascade = (*episodes.Service)(nil)

func newServiceForTest(t *testing.T, db *datastore.DB) (*episodes.Service, *episodes.Repo) {
	t.Helper()
	r, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := episodes.NewService(r, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, r
}

func TestService_GettersAndFinders(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, r := newServiceForTest(t, db)

	// Seed.
	air := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seeds := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", Monitored: true, AirDate: "2026-05-01", AirDateUtc: ptrTime(air), AbsoluteEpisodeNumber: ptrInt(1), SceneSeasonNumber: ptrInt(1), SceneEpisodeNumber: ptrInt(1), SceneAbsoluteEpisodeNumber: ptrInt(1)},
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2, Title: "Two", Monitored: true},
	}
	for i := range seeds {
		if _, err := r.Insert(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("Insert seed %d: %v", i, err)
		}
	}

	// GetEpisode.
	if got, err := svc.GetEpisode(t.Context(), seeds[0].ID); err != nil || got.Title != "Pilot" {
		t.Errorf("GetEpisode: %v / %+v", err, got)
	}
	// GetEpisodes (multi-id).
	if got, err := svc.GetEpisodes(t.Context(), []int{seeds[0].ID, seeds[1].ID}); err != nil || len(got) != 2 {
		t.Errorf("GetEpisodes: len=%d err=%v", len(got), err)
	}
	// FindEpisode.
	if got, err := svc.FindEpisode(t.Context(), 1, 1, 2); err != nil || got.Title != "Two" {
		t.Errorf("FindEpisode: %v / %+v", err, got)
	}
	if _, err := svc.FindEpisode(t.Context(), 1, 99, 99); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("FindEpisode missing: %v", err)
	}
	// FindEpisodeByAbsolute.
	if got, err := svc.FindEpisodeByAbsolute(t.Context(), 1, 1); err != nil || got.EpisodeNumber != 1 {
		t.Errorf("FindEpisodeByAbsolute: %v / %+v", err, got)
	}
	// Scene-numbering finders.
	if got, err := svc.FindEpisodesBySceneNumbering(t.Context(), 1, 1, 1); err != nil || len(got) != 1 {
		t.Errorf("FindEpisodesBySceneNumbering: %v / %v", err, got)
	}
	if got, err := svc.FindEpisodesBySceneAbsolute(t.Context(), 1, 1); err != nil || len(got) != 1 {
		t.Errorf("FindEpisodesBySceneAbsolute: %v / %v", err, got)
	}
	// Series-scoped getters.
	if got, err := svc.GetEpisodesBySeries(t.Context(), 1); err != nil || len(got) != 2 {
		t.Errorf("GetEpisodesBySeries: %v / %d", err, len(got))
	}
	if got, err := svc.GetEpisodesBySeason(t.Context(), 1, 1); err != nil || len(got) != 2 {
		t.Errorf("GetEpisodesBySeason: %v / %d", err, len(got))
	}
	if got, err := svc.GetEpisodesBySceneSeason(t.Context(), 1, 1); err != nil || len(got) != 1 {
		t.Errorf("GetEpisodesBySceneSeason: %v / %d", err, len(got))
	}
	if got, err := svc.GetEpisodesByMultipleSeries(t.Context(), []int{1}); err != nil || len(got) != 2 {
		t.Errorf("GetEpisodesByMultipleSeries: %v / %d", err, len(got))
	}
}

func TestService_FindEpisodeByTitle(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, r := newServiceForTest(t, db)

	// Three season-1 episodes with distinct titles. The release title
	// embeds episode 2's title — FindEpisodeByTitle should pick it.
	seeds := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Title: "Heart of Glass"},
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2, Title: "The Dark Side of the Moon"},
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 3, Title: "Empty Title Test"},
	}
	for i := range seeds {
		if _, err := r.Insert(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("Insert seed %d: %v", i, err)
		}
	}
	got, err := svc.FindEpisodeByTitle(t.Context(), 1, 1, "Show.S01E02.The.Dark.Side.of.the.Moon.HDTV.x264")
	if err != nil {
		t.Fatalf("FindEpisodeByTitle: %v", err)
	}
	if got.EpisodeNumber != 2 {
		t.Errorf("FindEpisodeByTitle: got E%d, want E2", got.EpisodeNumber)
	}
	// No match → ErrNotFound.
	if _, err := svc.FindEpisodeByTitle(t.Context(), 1, 1, "Nothing.Matches.Here.S01E99"); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("FindEpisodeByTitle no match: %v", err)
	}
	// Empty season → ErrNotFound.
	if _, err := svc.FindEpisodeByTitle(t.Context(), 99, 99, "anything"); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("FindEpisodeByTitle empty: %v", err)
	}
}

func TestService_FindEpisodeByAirDate(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, r := newServiceForTest(t, db)

	// Three episodes share air date: 1 special (S0E1), 2 regular (S1E1+S2E1).
	seeds := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 0, EpisodeNumber: 1, AirDate: "2026-05-01"},
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2026-05-01"},
		{SeriesID: 1, SeasonNumber: 2, EpisodeNumber: 1, AirDate: "2026-05-01"},
	}
	for i := range seeds {
		if _, err := r.Insert(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("Insert seed %d: %v", i, err)
		}
	}

	// 0 hits → ErrNotFound.
	if _, err := svc.FindEpisodeByAirDate(t.Context(), 1, "1900-01-01", nil); !errors.Is(err, episodes.ErrNotFound) {
		t.Errorf("FindEpisodeByAirDate 0 hits: %v", err)
	}

	// 3 hits, no part: 2 regular remain → ErrMultipleEpisodesSameAirDate.
	if _, err := svc.FindEpisodeByAirDate(t.Context(), 1, "2026-05-01", nil); !errors.Is(err, episodes.ErrMultipleEpisodesSameAirDate) {
		t.Errorf("FindEpisodeByAirDate ambiguous: %v", err)
	}

	// part=1 → first regular by (season,episode) = S1E1.
	one := 1
	got, err := svc.FindEpisodeByAirDate(t.Context(), 1, "2026-05-01", &one)
	if err != nil {
		t.Fatalf("FindEpisodeByAirDate part=1: %v", err)
	}
	if got.SeasonNumber != 1 || got.EpisodeNumber != 1 {
		t.Errorf("FindEpisodeByAirDate part=1: got S%dE%d", got.SeasonNumber, got.EpisodeNumber)
	}

	// part=2 → S2E1.
	two := 2
	got, err = svc.FindEpisodeByAirDate(t.Context(), 1, "2026-05-01", &two)
	if err != nil {
		t.Fatalf("FindEpisodeByAirDate part=2: %v", err)
	}
	if got.SeasonNumber != 2 {
		t.Errorf("FindEpisodeByAirDate part=2: got S%d", got.SeasonNumber)
	}

	// part=3 (out of range) → ambiguous.
	three := 3
	if _, err := svc.FindEpisodeByAirDate(t.Context(), 1, "2026-05-01", &three); !errors.Is(err, episodes.ErrMultipleEpisodesSameAirDate) {
		t.Errorf("FindEpisodeByAirDate part=3: %v", err)
	}

	// Single regular + special, no part → returns the regular.
	if err := r.Delete(t.Context(), seeds[2].ID); err != nil {
		t.Fatalf("delete S2E1: %v", err)
	}
	if got, err := svc.FindEpisodeByAirDate(t.Context(), 1, "2026-05-01", nil); err != nil || got.SeasonNumber != 1 {
		t.Errorf("FindEpisodeByAirDate single-regular+special: got %v err %v", got, err)
	}
}

func TestService_Mutations(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, r := newServiceForTest(t, db)

	seeds := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Monitored: false},
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2, Monitored: false},
	}
	for i := range seeds {
		if _, err := r.Insert(t.Context(), &seeds[i]); err != nil {
			t.Fatalf("Insert seed %d: %v", i, err)
		}
	}

	// SetEpisodeMonitored: load + flip.
	if err := svc.SetEpisodeMonitored(t.Context(), seeds[0].ID, true); err != nil {
		t.Fatalf("SetEpisodeMonitored: %v", err)
	}
	if got, err := r.Get(t.Context(), seeds[0].ID); err != nil || !got.Monitored {
		t.Errorf("after SetEpisodeMonitored: %+v", got)
	}

	// SetMonitored bulk.
	if err := svc.SetMonitored(t.Context(), []int{seeds[0].ID, seeds[1].ID}, false); err != nil {
		t.Fatalf("SetMonitored: %v", err)
	}

	// SetEpisodeMonitoredBySeason returns count.
	n, err := svc.SetEpisodeMonitoredBySeason(t.Context(), 1, 1, true)
	if err != nil {
		t.Fatalf("SetEpisodeMonitoredBySeason: %v", err)
	}
	if n != 2 {
		t.Errorf("SetEpisodeMonitoredBySeason: rows=%d, want 2", n)
	}

	// UpdateEpisode + UpdateEpisodes + UpdateMany aliases.
	seeds[0].Title = "After Update"
	if err := svc.UpdateEpisode(t.Context(), &seeds[0]); err != nil {
		t.Fatalf("UpdateEpisode: %v", err)
	}
	if err := svc.UpdateEpisodes(t.Context(), []*episodes.Episode{&seeds[0]}); err != nil {
		t.Fatalf("UpdateEpisodes: %v", err)
	}
	if err := svc.UpdateMany(t.Context(), []*episodes.Episode{&seeds[0]}); err != nil {
		t.Fatalf("UpdateMany: %v", err)
	}

	// UpdateLastSearchTime.
	when := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	stamp := []episodes.Episode{{ID: seeds[0].ID, LastSearchTime: ptrTime(when)}}
	if err := svc.UpdateLastSearchTime(t.Context(), stamp); err != nil {
		t.Fatalf("UpdateLastSearchTime: %v", err)
	}

	// SetFileID + ClearFileID via service. Cache bookkeeping is covered
	// in TestService_MissingFromDiskCache.
	if err := svc.SetFileID(t.Context(), &seeds[0], 42); err != nil {
		t.Fatalf("SetFileID: %v", err)
	}
	if err := svc.ClearFileID(t.Context(), &seeds[0], false); err != nil {
		t.Fatalf("ClearFileID: %v", err)
	}

	// InsertMany + DeleteMany.
	more := []*episodes.Episode{
		{SeriesID: 2, SeasonNumber: 1, EpisodeNumber: 1, Title: "M-1"},
		{SeriesID: 2, SeasonNumber: 1, EpisodeNumber: 2, Title: "M-2"},
	}
	if err := svc.InsertMany(t.Context(), more); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}
	delList := []episodes.Episode{{ID: more[0].ID}, {ID: more[1].ID}}
	if err := svc.DeleteMany(t.Context(), delList); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}

	// EpisodesWithFiles + GetEpisodesByFileID + EpisodesBetweenDates
	// are direct repo pass-throughs — covered by query tests.
	if _, err := svc.EpisodesWithFiles(t.Context(), 1); err != nil {
		t.Errorf("EpisodesWithFiles: %v", err)
	}
	if _, err := svc.GetEpisodesByFileID(t.Context(), 1); err != nil {
		t.Errorf("GetEpisodesByFileID: %v", err)
	}
	if _, err := svc.EpisodesBetweenDates(t.Context(), time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour), true, true); err != nil {
		t.Errorf("EpisodesBetweenDates: %v", err)
	}
}

func TestService_MissingFromDiskCache(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, _ := newServiceForTest(t, db)

	// Empty drain returns nil for an unknown series.
	if got := svc.DrainMissingFromDisk(99); got != nil {
		t.Errorf("DrainMissingFromDisk(unknown): %v, want nil", got)
	}

	// Remember a few episodes for two series.
	svc.RememberMissingFromDisk(1, 100)
	svc.RememberMissingFromDisk(1, 101)
	svc.RememberMissingFromDisk(2, 200)

	got := svc.DrainMissingFromDisk(1)
	if len(got) != 2 || got[0] != 100 || got[1] != 101 {
		t.Errorf("DrainMissingFromDisk(1): got %v, want [100 101]", got)
	}
	// Second drain is empty (entry was deleted).
	if got := svc.DrainMissingFromDisk(1); got != nil {
		t.Errorf("DrainMissingFromDisk(1) again: %v, want nil", got)
	}
	// Series 2 untouched.
	got = svc.DrainMissingFromDisk(2)
	if len(got) != 1 || got[0] != 200 {
		t.Errorf("DrainMissingFromDisk(2): got %v, want [200]", got)
	}

	// SetFileID drains the relevant episode from the cache.
	r, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	ep := &episodes.Episode{SeriesID: 7, SeasonNumber: 1, EpisodeNumber: 1}
	if _, err := r.Insert(t.Context(), ep); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	svc.RememberMissingFromDisk(7, ep.ID)
	if err := svc.SetFileID(t.Context(), ep, 5); err != nil {
		t.Fatalf("SetFileID: %v", err)
	}
	if got := svc.DrainMissingFromDisk(7); got != nil {
		t.Errorf("DrainMissingFromDisk(7) after SetFileID: %v, want nil", got)
	}
}

func TestService_DeleteBySeriesID(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, r := newServiceForTest(t, db)

	// Insert 3 episodes for series 50, 1 for series 51.
	for _, ep := range []episodes.Episode{
		{SeriesID: 50, SeasonNumber: 1, EpisodeNumber: 1},
		{SeriesID: 50, SeasonNumber: 1, EpisodeNumber: 2},
		{SeriesID: 50, SeasonNumber: 2, EpisodeNumber: 1},
		{SeriesID: 51, SeasonNumber: 1, EpisodeNumber: 1},
	} {
		ep := ep
		if _, err := r.Insert(t.Context(), &ep); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	// Seed the missingFromDiskCache for the soon-to-be-deleted series
	// + an unrelated series. The cascade must evict series 50 but
	// preserve series 51.
	svc.RememberMissingFromDisk(50, 1001)
	svc.RememberMissingFromDisk(50, 1002)
	svc.RememberMissingFromDisk(51, 1003)

	deleted, err := svc.DeleteBySeriesID(t.Context(), 50)
	if err != nil {
		t.Fatalf("DeleteBySeriesID: %v", err)
	}
	if deleted != 3 {
		t.Errorf("rows deleted: got %d, want 3", deleted)
	}

	// Series 50 has zero episodes; series 51 still has 1.
	left, _ := r.AllBySeriesID(t.Context(), 50)
	if len(left) != 0 {
		t.Errorf("series 50 should have 0 episodes; got %d", len(left))
	}
	left51, _ := r.AllBySeriesID(t.Context(), 51)
	if len(left51) != 1 {
		t.Errorf("series 51 should still have 1 episode; got %d", len(left51))
	}

	// missingFromDiskCache: series 50 evicted, series 51 preserved.
	if got := svc.DrainMissingFromDisk(50); got != nil {
		t.Errorf("cache for series 50 should be evicted; got %v", got)
	}
	if got := svc.DrainMissingFromDisk(51); len(got) != 1 || got[0] != 1003 {
		t.Errorf("cache for series 51 should be preserved; got %v", got)
	}

	// Idempotent: deleting an already-empty series returns 0.
	deleted2, err := svc.DeleteBySeriesID(t.Context(), 50)
	if err != nil {
		t.Errorf("DeleteBySeriesID idempotent: %v", err)
	}
	if deleted2 != 0 {
		t.Errorf("DeleteBySeriesID idempotent rows: got %d, want 0", deleted2)
	}
}

func TestService_NewServiceErrors(t *testing.T) {
	t.Parallel()
	if _, err := episodes.NewService(nil, nil); err == nil {
		t.Errorf("NewService(nil) want error")
	}
}

// Slice 5.J26 (audit B.2 second wave). Monitored-flip publishes drive
// the `episode` SignalR Sync channel — verify the three flip paths
// each publish exactly once with the expected payload shape.
func TestService_SetEpisodeMonitored_PublishesEvent(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	r, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, err := episodes.NewService(r, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	ep := episodes.Episode{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Monitored: false}
	if _, err := r.Insert(t.Context(), &ep); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var got atomic.Int32
	var lastEv atomic.Pointer[episodes.EpisodeMonitoredChangedEvent]
	messaging.Subscribe(bus, "test.episode.monitored.single",
		func(_ context.Context, ev episodes.EpisodeMonitoredChangedEvent) error {
			got.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	if err := svc.SetEpisodeMonitored(t.Context(), ep.ID, true); err != nil {
		t.Fatalf("SetEpisodeMonitored: %v", err)
	}
	if got.Load() != 1 {
		t.Fatalf("expected 1 event, got %d", got.Load())
	}
	ev := lastEv.Load()
	if ev == nil || len(ev.IDs) != 1 || ev.IDs[0] != ep.ID || !ev.Monitored {
		t.Errorf("event payload: %+v (want IDs=[%d], Monitored=true)", ev, ep.ID)
	}
}

func TestService_SetMonitored_PublishesEventWithBulkIDs(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	r, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, err := episodes.NewService(r, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	eps := []episodes.Episode{
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Monitored: true},
		{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2, Monitored: true},
	}
	for i := range eps {
		if _, err := r.Insert(t.Context(), &eps[i]); err != nil {
			t.Fatalf("Insert seed: %v", err)
		}
	}

	var lastEv atomic.Pointer[episodes.EpisodeMonitoredChangedEvent]
	messaging.Subscribe(bus, "test.episode.monitored.bulk",
		func(_ context.Context, ev episodes.EpisodeMonitoredChangedEvent) error {
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	ids := []int{eps[0].ID, eps[1].ID}
	if err := svc.SetMonitored(t.Context(), ids, false); err != nil {
		t.Fatalf("SetMonitored: %v", err)
	}
	ev := lastEv.Load()
	if ev == nil || len(ev.IDs) != 2 || ev.Monitored {
		t.Errorf("event payload: %+v (want IDs=%v, Monitored=false)", ev, ids)
	}

	// Empty IDs → no event (skip publish, matches the guard).
	got := atomic.Int32{}
	messaging.Subscribe(bus, "test.episode.monitored.empty",
		func(_ context.Context, _ episodes.EpisodeMonitoredChangedEvent) error {
			got.Add(1)
			return nil
		})
	if err := svc.SetMonitored(t.Context(), nil, true); err != nil {
		t.Fatalf("SetMonitored(nil): %v", err)
	}
	// The previous bulk publish reached the empty subscriber too;
	// only the new empty-call must produce zero additional events.
	before := got.Load()
	if err := svc.SetMonitored(t.Context(), []int{}, false); err != nil {
		t.Fatalf("SetMonitored([]): %v", err)
	}
	if after := got.Load(); after != before {
		t.Errorf("empty-IDs path published; before=%d after=%d", before, after)
	}
}

func TestService_SetEpisodeMonitoredBySeason_PublishesOnAffected(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	r, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, err := episodes.NewService(r, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	for i := 0; i < 2; i++ {
		ep := episodes.Episode{SeriesID: 1, SeasonNumber: 1, EpisodeNumber: i + 1, Monitored: true}
		if _, err := r.Insert(t.Context(), &ep); err != nil {
			t.Fatalf("Insert seed: %v", err)
		}
	}

	var got atomic.Int32
	messaging.Subscribe(bus, "test.episode.monitored.bySeason",
		func(_ context.Context, _ episodes.EpisodeMonitoredChangedEvent) error {
			got.Add(1)
			return nil
		})

	if n, err := svc.SetEpisodeMonitoredBySeason(t.Context(), 1, 1, false); err != nil || n != 2 {
		t.Fatalf("SetEpisodeMonitoredBySeason: n=%d err=%v", n, err)
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 event on affected=2; got %d", got.Load())
	}

	// No-affected season → no publish.
	got.Store(0)
	if n, err := svc.SetEpisodeMonitoredBySeason(t.Context(), 1, 99, true); err != nil || n != 0 {
		t.Fatalf("SetEpisodeMonitoredBySeason no-match: n=%d err=%v", n, err)
	}
	if got.Load() != 0 {
		t.Errorf("expected 0 events on affected=0; got %d", got.Load())
	}
}
