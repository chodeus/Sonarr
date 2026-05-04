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
