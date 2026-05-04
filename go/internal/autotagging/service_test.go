package autotagging_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/messaging"
)

func TestService_Insert_DecodesAndPublishes(t *testing.T) {
	repo := newTestRepo(t)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var received atomic.Int32
	messaging.Subscribe(bus, "test.at.updated",
		func(_ context.Context, _ autotagging.AutoTagsUpdatedEvent) error {
			received.Add(1)
			return nil
		})

	svc := autotagging.NewService(repo, bus)
	row := &autotagging.AutoTag{
		Name:           "anime",
		Tags:           []int{42},
		Specifications: `[{"type":"SeriesTypeSpecification","body":{"name":"st","value":2}}]`,
	}
	tag, err := svc.Insert(context.Background(), row)
	if err != nil {
		t.Fatal(err)
	}
	if tag.Name != "anime" || len(tag.Specifications) != 1 {
		t.Errorf("Insert returned malformed tag: %+v", tag)
	}
	if received.Load() != 1 {
		t.Errorf("expected 1 event, got %d", received.Load())
	}
}

func TestService_AllUsesCache(t *testing.T) {
	repo := newTestRepo(t)
	svc := autotagging.NewService(repo, nil)

	row := &autotagging.AutoTag{
		Name:           "n",
		Tags:           []int{1},
		Specifications: `[{"type":"MonitoredSpecification","body":{"name":"m"}}]`,
	}
	if _, err := svc.Insert(context.Background(), row); err != nil {
		t.Fatal(err)
	}

	first, err := svc.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(first))
	}

	// Insert a second row directly via the underlying Repo —
	// bypasses the cache invalidation. The cached result should
	// still show 1.
	if _, err := repo.Insert(context.Background(), &autotagging.AutoTag{
		Name:           "n2",
		Specifications: `[]`,
	}); err != nil {
		t.Fatal(err)
	}
	cached, err := svc.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 {
		t.Errorf("cache should still show 1 (direct-Repo insert bypassed invalidation), got %d", len(cached))
	}
}

func TestService_UpdateInvalidatesCache(t *testing.T) {
	repo := newTestRepo(t)
	svc := autotagging.NewService(repo, nil)

	row := &autotagging.AutoTag{
		Name:           "old",
		Specifications: `[]`,
	}
	tag, err := svc.Insert(context.Background(), row)
	if err != nil {
		t.Fatal(err)
	}

	// Warm the cache.
	if _, err := svc.All(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Update through Service → cache should refresh.
	row.ID = tag.ID
	row.Name = "renamed"
	if err := svc.Update(context.Background(), row); err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetByID(context.Background(), tag.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want renamed (cache should have been invalidated)", got.Name)
	}
}

func TestService_DeleteInvalidatesCache(t *testing.T) {
	repo := newTestRepo(t)
	svc := autotagging.NewService(repo, nil)

	tag, err := svc.Insert(context.Background(), &autotagging.AutoTag{
		Name: "to-delete", Specifications: `[]`,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Warm cache.
	if _, err := svc.All(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(context.Background(), tag.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.GetByID(context.Background(), tag.ID); !errors.Is(err, autotagging.ErrNotFound) {
		t.Errorf("expected ErrNotFound after Delete, got %v", err)
	}
}

func TestService_AllForTag(t *testing.T) {
	repo := newTestRepo(t)
	svc := autotagging.NewService(repo, nil)

	insert := func(name string, tags []int) {
		_, err := svc.Insert(context.Background(), &autotagging.AutoTag{
			Name: name, Tags: tags, Specifications: `[]`,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("with-tag-1", []int{1, 2})
	insert("with-tag-2", []int{2, 3})
	insert("with-tag-3", []int{4})

	got, err := svc.AllForTag(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 AutoTags containing tag 2, got %d", len(got))
	}

	got, err = svc.AllForTag(context.Background(), 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("unknown tag should return empty, got %d", len(got))
	}
}

func TestService_GetByID_NotFound(t *testing.T) {
	repo := newTestRepo(t)
	svc := autotagging.NewService(repo, nil)
	_, err := svc.GetByID(context.Background(), 9999)
	if !errors.Is(err, autotagging.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestService_NilInputErrors(t *testing.T) {
	repo := newTestRepo(t)
	svc := autotagging.NewService(repo, nil)
	if _, err := svc.Insert(context.Background(), nil); err == nil {
		t.Error("Insert(nil) should error")
	}
	if err := svc.Update(context.Background(), nil); err == nil {
		t.Error("Update(nil) should error")
	}
}
