package customformats_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/messaging"
)

func TestCFService_Insert_PublishesAddedEvent(t *testing.T) {
	repo := newTestRepo(t)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var added atomic.Int32
	messaging.Subscribe(bus, "test.cf.added",
		func(_ context.Context, ev customformats.CustomFormatAddedEvent) error {
			if ev.Format == nil {
				t.Errorf("expected non-nil Format in event payload")
			}
			added.Add(1)
			return nil
		})

	svc := customformats.NewService(repo, bus)
	row := &customformats.CustomFormat{
		Name:           "test-format",
		Specifications: `[{"type":"ResolutionSpecification","body":{"name":"1080p","value":1080}}]`,
	}
	if _, err := svc.Insert(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if added.Load() != 1 {
		t.Errorf("expected 1 added event, got %d", added.Load())
	}
}

// Slice 5.J27 (audit B.2 second wave). Update publishes
// CustomFormatUpdatedEvent — Go-port additive (C# `:51-55` doesn't
// emit; its SignalR sync flows through the inherited generic
// ModelEvent<CustomFormat>). Subscribers (SignalR layer) Sync on
// the customformat channel.
func TestCFService_Update_PublishesUpdatedEvent(t *testing.T) {
	repo := newTestRepo(t)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var added, deleted, updated atomic.Int32
	var updatedPayload atomic.Pointer[customformats.Format]
	messaging.Subscribe(bus, "test.cf.added",
		func(_ context.Context, _ customformats.CustomFormatAddedEvent) error {
			added.Add(1)
			return nil
		})
	messaging.Subscribe(bus, "test.cf.deleted",
		func(_ context.Context, _ customformats.CustomFormatDeletedEvent) error {
			deleted.Add(1)
			return nil
		})
	messaging.Subscribe(bus, "test.cf.updated",
		func(_ context.Context, ev customformats.CustomFormatUpdatedEvent) error {
			updated.Add(1)
			updatedPayload.Store(ev.Format)
			return nil
		})

	svc := customformats.NewService(repo, bus)
	row := &customformats.CustomFormat{Name: "x", Specifications: `[]`}
	f, err := svc.Insert(context.Background(), row)
	if err != nil {
		t.Fatal(err)
	}
	row.ID = f.ID
	row.Name = "renamed"
	if err := svc.Update(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if added.Load() != 1 || deleted.Load() != 0 {
		t.Errorf("Insert→1 Added; no Deleted: got added=%d deleted=%d", added.Load(), deleted.Load())
	}
	if updated.Load() != 1 {
		t.Errorf("Update should publish exactly 1 Updated event; got %d", updated.Load())
	}
	if pl := updatedPayload.Load(); pl == nil || pl.ID != f.ID || pl.Name != "renamed" {
		t.Errorf("Update event payload: got %+v, want non-nil Format with ID=%d Name=renamed", pl, f.ID)
	}
}

func TestCFService_Delete_PublishesBeforeRowGone(t *testing.T) {
	repo := newTestRepo(t)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var deletedPayload *customformats.Format
	messaging.Subscribe(bus, "test.cf.deleted",
		func(_ context.Context, ev customformats.CustomFormatDeletedEvent) error {
			deletedPayload = ev.Format
			return nil
		})

	svc := customformats.NewService(repo, bus)
	row := &customformats.CustomFormat{Name: "to-delete", Specifications: `[]`}
	f, err := svc.Insert(context.Background(), row)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(context.Background(), f.ID); err != nil {
		t.Fatal(err)
	}

	if deletedPayload == nil {
		t.Fatal("expected non-nil Format in delete payload")
	}
	if deletedPayload.ID != f.ID {
		t.Errorf("delete payload ID = %d, want %d", deletedPayload.ID, f.ID)
	}

	if _, err := svc.GetByID(context.Background(), f.ID); !errors.Is(err, customformats.ErrNotFound) {
		t.Errorf("after Delete, GetByID should return ErrNotFound, got %v", err)
	}
}

func TestCFService_AllUsesCache(t *testing.T) {
	repo := newTestRepo(t)
	svc := customformats.NewService(repo, nil)

	if _, err := svc.Insert(context.Background(), &customformats.CustomFormat{
		Name: "n", Specifications: `[]`,
	}); err != nil {
		t.Fatal(err)
	}

	first, err := svc.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("expected 1 format, got %d", len(first))
	}

	// Insert via Repo directly (bypasses cache invalidation).
	if _, err := repo.Insert(context.Background(), &customformats.CustomFormat{
		Name: "n2", Specifications: `[]`,
	}); err != nil {
		t.Fatal(err)
	}
	cached, err := svc.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 {
		t.Errorf("cache should still show 1 (direct-Repo bypass), got %d", len(cached))
	}
}

func TestCFService_NilInputErrors(t *testing.T) {
	svc := customformats.NewService(newTestRepo(t), nil)
	if _, err := svc.Insert(context.Background(), nil); err == nil {
		t.Error("Insert(nil) should error")
	}
	if err := svc.Update(context.Background(), nil); err == nil {
		t.Error("Update(nil) should error")
	}
}

func TestCFService_GetByID_NotFound(t *testing.T) {
	svc := customformats.NewService(newTestRepo(t), nil)
	_, err := svc.GetByID(context.Background(), 9999)
	if !errors.Is(err, customformats.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
