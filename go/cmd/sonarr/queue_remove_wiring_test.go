package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// recordingClient records RemoveItem / MarkItemAsImported calls.
type recordingClient struct {
	trackerTestProvider
	removed  []*downloadclients.Item
	imported []*downloadclients.Item
}

func (c *recordingClient) RemoveItem(_ context.Context, item *downloadclients.Item, _ bool) error {
	c.removed = append(c.removed, item)
	return nil
}
func (c *recordingClient) MarkItemAsImported(_ context.Context, item *downloadclients.Item) error {
	c.imported = append(c.imported, item)
	return nil
}

type fakeClientResolver struct {
	provider downloadclients.Provider
	err      error
}

func (f fakeClientResolver) ResolveClient(context.Context, int) (downloadclients.Provider, error) {
	return f.provider, f.err
}

func newQueueRemoveFixture(t *testing.T, resolver queueClientResolver) (*queueRemoveAdapter, *queue.Service, *trackeddownloads.Service) {
	t.Helper()
	qs := queue.NewService()
	qs.SetQueue([]queue.Queue{{ID: 1, DownloadID: "dl1"}})
	tracked := trackeddownloads.NewService(slog.Default())
	item := &downloadclients.Item{DownloadID: "dl1"}
	_ = tracked.Track(&trackeddownloads.TrackedDownload{DownloadID: "dl1", DownloadClient: 5, DownloadItem: item, IsTrackable: true})
	a := &queueRemoveAdapter{Service: qs, tracked: tracked, clients: resolver, log: slog.Default()}
	return a, qs, tracked
}

// J395: removeFromClient → client.RemoveItem + StopTracking + cache remove.
func TestQueueRemoveAdapter_RemoveFromClient_J395(t *testing.T) {
	t.Parallel()
	client := &recordingClient{}
	a, qs, tracked := newQueueRemoveFixture(t, fakeClientResolver{provider: client})
	removed, err := a.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{RemoveFromClient: true})
	if err != nil || !removed {
		t.Fatalf("RemoveQueueItem = (%v,%v), want (true,nil)", removed, err)
	}
	if len(client.removed) != 1 {
		t.Errorf("RemoveItem calls = %d, want 1", len(client.removed))
	}
	if tracked.Find("dl1") != nil {
		t.Error("tracked download should be StopTracking'd")
	}
	if qs.Find(1) != nil {
		t.Error("queue row should be removed from cache")
	}
}

// J395: changeCategory (no removeFromClient) → MarkItemAsImported.
func TestQueueRemoveAdapter_ChangeCategory_J395(t *testing.T) {
	t.Parallel()
	client := &recordingClient{}
	a, _, _ := newQueueRemoveFixture(t, fakeClientResolver{provider: client})
	if _, err := a.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{ChangeCategory: true}); err != nil {
		t.Fatalf("RemoveQueueItem: %v", err)
	}
	if len(client.imported) != 1 || len(client.removed) != 0 {
		t.Errorf("imported=%d removed=%d, want 1/0", len(client.imported), len(client.removed))
	}
}

// J395: unresolvable client → ErrQueueDownloadClientUnavailable.
func TestQueueRemoveAdapter_ClientUnavailable_J395(t *testing.T) {
	t.Parallel()
	a, _, _ := newQueueRemoveFixture(t, fakeClientResolver{err: errors.New("not registered")})
	_, err := a.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{RemoveFromClient: true})
	if !errors.Is(err, apiv3.ErrQueueDownloadClientUnavailable) {
		t.Fatalf("err = %v, want ErrQueueDownloadClientUnavailable", err)
	}
}

// J395: no side-effect flags → no client resolve, still stops tracking + removes.
func TestQueueRemoveAdapter_NoFlags_J395(t *testing.T) {
	t.Parallel()
	a, qs, tracked := newQueueRemoveFixture(t, fakeClientResolver{err: errors.New("must not be called")})
	removed, err := a.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{})
	if err != nil || !removed {
		t.Fatalf("RemoveQueueItem = (%v,%v), want (true,nil)", removed, err)
	}
	if tracked.Find("dl1") != nil || qs.Find(1) != nil {
		t.Error("should still stop tracking + remove cache row")
	}
}

// J395: unknown queue id → (false, nil) → handler maps to 404.
func TestQueueRemoveAdapter_NotFound_J395(t *testing.T) {
	t.Parallel()
	a, _, _ := newQueueRemoveFixture(t, fakeClientResolver{})
	removed, err := a.RemoveQueueItem(t.Context(), 99, apiv3.QueueRemoveOptions{RemoveFromClient: true})
	if err != nil || removed {
		t.Errorf("RemoveQueueItem(99) = (%v,%v), want (false,nil)", removed, err)
	}
}
