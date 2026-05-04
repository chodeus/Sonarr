package tags_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/tags"
)

func newTestRepo(t *testing.T) *tags.Repo {
	t.Helper()
	dir := t.TempDir()
	db, err := datastore.Open(context.Background(), datastore.Config{
		Engine: datastore.EngineSQLite,
		Path:   filepath.Join(dir, "test.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := datastore.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	r, err := tags.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return r
}

func TestService_Add_Lowercases(t *testing.T) {
	repo := newTestRepo(t)
	svc := tags.NewService(repo, nil)
	got, err := svc.Add(context.Background(), &tags.Tag{Label: "MixedCase"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "mixedcase" {
		t.Errorf("Label = %q, want lowercase", got.Label)
	}
}

func TestService_Add_DedupeByLabel(t *testing.T) {
	repo := newTestRepo(t)
	svc := tags.NewService(repo, nil)
	first, err := svc.Add(context.Background(), &tags.Tag{Label: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Add(context.Background(), &tags.Tag{Label: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Errorf("Add(existing) should return the existing row, got %d != %d", second.ID, first.ID)
	}
}

func TestService_Update_Lowercases(t *testing.T) {
	repo := newTestRepo(t)
	svc := tags.NewService(repo, nil)
	tag, err := svc.Add(context.Background(), &tags.Tag{Label: "old"})
	if err != nil {
		t.Fatal(err)
	}
	tag.Label = "RENAMED"
	if err := svc.Update(context.Background(), tag); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetByID(context.Background(), tag.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "renamed" {
		t.Errorf("Label = %q, want lowercase", got.Label)
	}
}

func TestService_GetByString_AllDigitsRoutesToID(t *testing.T) {
	repo := newTestRepo(t)
	svc := tags.NewService(repo, nil)
	tag, err := svc.Add(context.Background(), &tags.Tag{Label: "kids"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetByString(context.Background(), itoa(tag.ID))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != tag.ID {
		t.Errorf("got Id=%d, want %d", got.ID, tag.ID)
	}
}

func TestService_GetByString_NonDigitRoutesToLabel(t *testing.T) {
	repo := newTestRepo(t)
	svc := tags.NewService(repo, nil)
	tag, err := svc.Add(context.Background(), &tags.Tag{Label: "drama"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetByString(context.Background(), "drama")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != tag.ID {
		t.Errorf("got Id=%d, want %d", got.ID, tag.ID)
	}
}

func TestService_GetByIDs_SkipsMissing(t *testing.T) {
	repo := newTestRepo(t)
	svc := tags.NewService(repo, nil)
	a, _ := svc.Add(context.Background(), &tags.Tag{Label: "a"})
	b, _ := svc.Add(context.Background(), &tags.Tag{Label: "b"})

	got, err := svc.GetByIDs(context.Background(), []int{a.ID, 99999, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 found tags (missing id 99999 silently dropped), got %d", len(got))
	}
}

func TestService_All_SortsByLabel(t *testing.T) {
	repo := newTestRepo(t)
	svc := tags.NewService(repo, nil)
	for _, label := range []string{"zeta", "alpha", "mu"} {
		_, _ = svc.Add(context.Background(), &tags.Tag{Label: label})
	}
	got, err := svc.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "mu", "zeta"}
	for i, w := range want {
		if i >= len(got) || got[i].Label != w {
			t.Errorf("got[%d].Label = %q, want %q", i, got[i].Label, w)
		}
	}
}

func TestService_PublishesEventOnAddAndUpdate(t *testing.T) {
	repo := newTestRepo(t)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var received atomic.Int32
	messaging.Subscribe(bus, "test.tags.updated",
		func(_ context.Context, _ tags.TagsUpdatedEvent) error {
			received.Add(1)
			return nil
		})

	svc := tags.NewService(repo, bus)
	tag, err := svc.Add(context.Background(), &tags.Tag{Label: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Errorf("expected 1 event after Add, got %d", received.Load())
	}

	tag.Label = "renamed"
	if err := svc.Update(context.Background(), tag); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 2 {
		t.Errorf("expected 2 events after Add+Update, got %d", received.Load())
	}
}

func TestService_AddDoesNotPublishOnDedupe(t *testing.T) {
	repo := newTestRepo(t)
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	var received atomic.Int32
	messaging.Subscribe(bus, "test.tags.updated",
		func(_ context.Context, _ tags.TagsUpdatedEvent) error {
			received.Add(1)
			return nil
		})

	svc := tags.NewService(repo, bus)
	if _, err := svc.Add(context.Background(), &tags.Tag{Label: "x"}); err != nil {
		t.Fatal(err)
	}
	// First Add publishes; second Add is a dedupe and should NOT.
	if _, err := svc.Add(context.Background(), &tags.Tag{Label: "x"}); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Errorf("expected 1 event (Add+dedupe-Add), got %d", received.Load())
	}
}

func TestService_AddNilErrors(t *testing.T) {
	svc := tags.NewService(newTestRepo(t), nil)
	if _, err := svc.Add(context.Background(), nil); err == nil {
		t.Error("expected error for nil tag")
	}
}

func TestService_UpdateNilErrors(t *testing.T) {
	svc := tags.NewService(newTestRepo(t), nil)
	if err := svc.Update(context.Background(), nil); err == nil {
		t.Error("expected error for nil tag")
	}
}

func TestService_GetByID_NotFound(t *testing.T) {
	svc := tags.NewService(newTestRepo(t), nil)
	_, err := svc.GetByID(context.Background(), 99999)
	if !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
