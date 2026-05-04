package series_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

func newServiceWithBus(t *testing.T) (*series.Service, *messaging.Bus) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, err := series.NewService(repo, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, bus
}

func sampleSeries(tvdbID int, title string) *series.Series {
	return &series.Series{
		TvdbID:     tvdbID,
		Title:      title,
		CleanTitle: title,
		Status:     1,
		Images:     "[]",
		Path:       "/tv/" + title,
	}
}

func TestService_AddSeries_PublishesSeriesAddedEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var got atomic.Int32
	var lastID atomic.Int32
	messaging.Subscribe(bus, "test.series.added",
		func(_ context.Context, ev series.SeriesAddedEvent) error {
			got.Add(1)
			if ev.Series != nil {
				lastID.Store(int32(ev.Series.ID))
			}
			return nil
		})

	added, err := svc.AddSeries(t.Context(), sampleSeries(361753, "Mando"))
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if added.ID == 0 {
		t.Errorf("AddSeries: expected non-zero ID")
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 SeriesAddedEvent, got %d", got.Load())
	}
	if int(lastID.Load()) != added.ID {
		t.Errorf("event payload ID=%d want %d", lastID.Load(), added.ID)
	}
}

func TestService_AddSeries_RejectsNil(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	if _, err := svc.AddSeries(t.Context(), nil); err == nil {
		t.Errorf("AddSeries(nil) should error")
	}
}

func TestService_AddSeries_NilBusSkipsPublish(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithFixtures(t, datastoretest.OpenSQLite(t))
	_ = svc // just ensure no panic; nil-bus svc constructed in fixture helper
	// Re-build a fresh nil-bus Service.
	repo, err := series.NewRepo(datastoretest.OpenSQLite(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	nilBus, err := series.NewService(repo, nil)
	if err != nil {
		t.Fatalf("NewService(nil bus): %v", err)
	}
	if _, err := nilBus.AddSeries(t.Context(), sampleSeries(123, "X")); err != nil {
		t.Errorf("AddSeries(nil bus): %v", err)
	}
}

func TestService_AddMany_PublishesSeriesImportedEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var got atomic.Int32
	var ids atomic.Pointer[[]int]
	messaging.Subscribe(bus, "test.series.imported",
		func(_ context.Context, ev series.SeriesImportedEvent) error {
			got.Add(1)
			cp := append([]int(nil), ev.SeriesIDs...)
			ids.Store(&cp)
			return nil
		})

	list := []*series.Series{
		sampleSeries(1, "A"),
		sampleSeries(2, "B"),
		sampleSeries(3, "C"),
	}
	out, err := svc.AddMany(t.Context(), list)
	if err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	if len(out) != 3 {
		t.Errorf("AddMany: returned len=%d", len(out))
	}
	if got.Load() != 1 {
		t.Errorf("expected exactly 1 SeriesImportedEvent, got %d", got.Load())
	}
	got2 := ids.Load()
	if got2 == nil || len(*got2) != 3 {
		t.Errorf("event SeriesIDs: %v", got2)
	}
}

func TestService_UpdateSeries_PublishesEditedEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	added, err := svc.AddSeries(t.Context(), sampleSeries(361753, "Mando"))
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	var got atomic.Int32
	var lastEv atomic.Pointer[series.SeriesEditedEvent]
	messaging.Subscribe(bus, "test.series.edited",
		func(_ context.Context, ev series.SeriesEditedEvent) error {
			got.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	added.Title = "The Mandalorian"
	added.CleanTitle = "themandalorian"
	updated, err := svc.UpdateSeries(t.Context(), added, series.UpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	if updated.Title != "The Mandalorian" {
		t.Errorf("Title not persisted: %q", updated.Title)
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 SeriesEditedEvent, got %d", got.Load())
	}
	ev := lastEv.Load()
	if ev == nil {
		t.Fatal("nil event")
	}
	if ev.Series == nil || ev.OldSeries == nil {
		t.Errorf("event missing Series/OldSeries: %+v", ev)
	}
	if ev.Series.Title != "The Mandalorian" || ev.OldSeries.Title != "Mando" {
		t.Errorf("event diff: new=%q old=%q", ev.Series.Title, ev.OldSeries.Title)
	}
	if ev.EpisodesChanged {
		t.Errorf("EpisodesChanged should be false this slice (deferred)")
	}
}

func TestService_UpdateSeries_SkipPublish(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	added, err := svc.AddSeries(t.Context(), sampleSeries(1, "X"))
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	var got atomic.Int32
	messaging.Subscribe(bus, "test.series.edited.skip",
		func(_ context.Context, _ series.SeriesEditedEvent) error {
			got.Add(1)
			return nil
		})

	added.Title = "X2"
	if _, err := svc.UpdateSeries(t.Context(), added, series.UpdateOptions{SkipPublishUpdatedEvent: true}); err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	if got.Load() != 0 {
		t.Errorf("SkipPublishUpdatedEvent didn't suppress event; got %d", got.Load())
	}
}

func TestService_UpdateSeries_PreservesAddOptions(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	row := sampleSeries(1, "X")
	row.AddOptions = `{"searchForMissingEpisodes":true}`
	added, err := svc.AddSeries(t.Context(), row)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	// Caller-supplied AddOptions should be ignored.
	added.AddOptions = `{"searchForMissingEpisodes":false}`
	added.Title = "X2"
	updated, err := svc.UpdateSeries(t.Context(), added, series.UpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	if updated.AddOptions != `{"searchForMissingEpisodes":true}` {
		t.Errorf("AddOptions overwritten: got %q want preserved", updated.AddOptions)
	}
}

func TestService_UpdateSeries_RejectsNil(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	if _, err := svc.UpdateSeries(t.Context(), nil, series.UpdateOptions{}); err == nil {
		t.Errorf("UpdateSeries(nil) should error")
	}
}

func TestService_RemoveAddOptions(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithBus(t)
	row := sampleSeries(1, "X")
	row.AddOptions = `{"searchForMissingEpisodes":true}`
	added, err := svc.AddSeries(t.Context(), row)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if err := svc.RemoveAddOptions(t.Context(), added.ID); err != nil {
		t.Fatalf("RemoveAddOptions: %v", err)
	}
	got, err := svc.GetSeries(t.Context(), added.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.AddOptions != "" {
		t.Errorf("AddOptions not cleared: %q", got.AddOptions)
	}
}

func TestService_DeleteSeries_PublishesEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	a, err := svc.AddSeries(t.Context(), sampleSeries(1, "A"))
	if err != nil {
		t.Fatalf("Add A: %v", err)
	}
	b, err := svc.AddSeries(t.Context(), sampleSeries(2, "B"))
	if err != nil {
		t.Fatalf("Add B: %v", err)
	}

	var got atomic.Int32
	var lastEv atomic.Pointer[series.SeriesDeletedEvent]
	messaging.Subscribe(bus, "test.series.deleted",
		func(_ context.Context, ev series.SeriesDeletedEvent) error {
			got.Add(1)
			cp := ev
			lastEv.Store(&cp)
			return nil
		})

	if err := svc.DeleteSeries(t.Context(), []int{a.ID, b.ID, 99999}, true, false); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("expected 1 SeriesDeletedEvent, got %d", got.Load())
	}
	ev := lastEv.Load()
	if ev == nil || len(ev.Series) != 2 {
		t.Fatalf("event payload: %+v", ev)
	}
	if !ev.DeleteFiles || ev.AddImportListExclusion {
		t.Errorf("flags lost: deleteFiles=%v addExclusion=%v", ev.DeleteFiles, ev.AddImportListExclusion)
	}
	// The rows are gone.
	if _, err := svc.GetSeries(t.Context(), a.ID); err == nil {
		t.Errorf("series A still present after delete")
	}
}

func TestService_DeleteSeries_EmptyListNoOp(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var got atomic.Int32
	messaging.Subscribe(bus, "test.series.deleted.empty",
		func(_ context.Context, _ series.SeriesDeletedEvent) error {
			got.Add(1)
			return nil
		})
	if err := svc.DeleteSeries(t.Context(), nil, false, false); err != nil {
		t.Errorf("DeleteSeries(nil): %v", err)
	}
	if got.Load() != 0 {
		t.Errorf("empty DeleteSeries should not publish; got %d", got.Load())
	}
}

func TestService_AddMany_EmptyListNoOp(t *testing.T) {
	t.Parallel()
	svc, bus := newServiceWithBus(t)
	var fired atomic.Int32
	messaging.Subscribe(bus, "test.series.imported.empty",
		func(_ context.Context, _ series.SeriesImportedEvent) error {
			fired.Add(1)
			return nil
		})
	out, err := svc.AddMany(t.Context(), nil)
	if err != nil {
		t.Errorf("AddMany(nil): %v", err)
	}
	if out != nil {
		t.Errorf("expected nil out, got %v", out)
	}
	if fired.Load() != 0 {
		t.Errorf("empty AddMany should not publish; got %d events", fired.Load())
	}
}
