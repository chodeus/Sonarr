package qualityprofiles_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

func newTestService(t *testing.T) (*qualityprofiles.Service, *messaging.Bus) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := qualityprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	svc, err := qualityprofiles.NewService(repo, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, bus
}

func sampleProfile(name string) *qualityprofiles.Profile {
	return &qualityprofiles.Profile{
		Name:                  name,
		Cutoff:                1,
		Items:                 `[]`,
		FormatItems:           `[]`,
		MinFormatScore:        0,
		CutoffFormatScore:     0,
		MinUpgradeFormatScore: 1,
	}
}

func TestService_Add_PersistsProfile(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	got, err := svc.Add(t.Context(), sampleProfile("Any"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.ID == 0 {
		t.Errorf("Add: expected non-zero ID, got %+v", got)
	}
	if got.Name != "Any" {
		t.Errorf("Add: Name=%q want Any", got.Name)
	}
}

func TestService_Add_RejectsNil(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	if _, err := svc.Add(t.Context(), nil); err == nil {
		t.Errorf("Add(nil) succeeded; want error")
	}
}

// Verifies Update publishes [QualityProfileUpdatedEvent] but Add does
// not — mirrors the C# semantic where only `Update` calls
// `_eventAggregator.PublishEvent(...)`.
func TestService_Update_PublishesEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newTestService(t)
	var got atomic.Int32
	var lastID atomic.Int32
	messaging.Subscribe(bus, "test.qp.updated",
		func(_ context.Context, ev qualityprofiles.QualityProfileUpdatedEvent) error {
			got.Add(1)
			lastID.Store(int32(ev.ID))
			return nil
		})

	added, err := svc.Add(t.Context(), sampleProfile("Any"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.Load() != 0 {
		t.Errorf("Add should not publish; got %d", got.Load())
	}

	added.Cutoff = 2
	if err := svc.Update(t.Context(), added); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("Update should publish exactly once; got %d", got.Load())
	}
	if int(lastID.Load()) != added.ID {
		t.Errorf("event payload ID=%d want %d", lastID.Load(), added.ID)
	}
}

func TestService_Update_RejectsNil(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	if err := svc.Update(t.Context(), nil); err == nil {
		t.Errorf("Update(nil) succeeded; want error")
	}
}

func TestService_Update_NilBusSkipsPublish(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, err := qualityprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := qualityprofiles.NewService(repo, nil)
	if err != nil {
		t.Fatalf("NewService(nil bus): %v", err)
	}
	added, err := svc.Add(t.Context(), sampleProfile("Any"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	added.Cutoff = 2
	if err := svc.Update(t.Context(), added); err != nil {
		t.Errorf("Update(nil bus): %v", err)
	}
}

func TestService_All_ReturnsEveryRow(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	for _, name := range []string{"Any", "SD", "HD-720p"} {
		if _, err := svc.Add(t.Context(), sampleProfile(name)); err != nil {
			t.Fatalf("Add(%q): %v", name, err)
		}
	}
	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("All: len=%d want 3", len(all))
	}
}

func TestService_Get_RoundTripsProfile(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	added, err := svc.Add(t.Context(), sampleProfile("Any"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := svc.Get(t.Context(), added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Any" || got.ID != added.ID {
		t.Errorf("Get: %+v want Name=Any ID=%d", got, added.ID)
	}
}

func TestService_Get_MissingReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	if _, err := svc.Get(t.Context(), 9999); !errors.Is(err, qualityprofiles.ErrNotFound) {
		t.Errorf("Get(missing): got %v want ErrNotFound", err)
	}
}

func TestService_Exists(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	if ok, err := svc.Exists(t.Context(), 9999); err != nil || ok {
		t.Errorf("Exists(missing): ok=%v err=%v want false,nil", ok, err)
	}

	added, err := svc.Add(t.Context(), sampleProfile("Any"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if ok, err := svc.Exists(t.Context(), added.ID); err != nil || !ok {
		t.Errorf("Exists(present): ok=%v err=%v want true,nil", ok, err)
	}
}
