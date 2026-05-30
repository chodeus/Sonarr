package main

import (
	"context"
	"log/slog"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadeventhub"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// hubFakeConfig is the ConfigLister the DownloadEventHub reads the
// per-client RemoveFailedDownloads flag from.
type hubFakeConfig struct {
	rows []downloadclients.DownloadClient
}

func (c hubFakeConfig) All(context.Context) ([]downloadclients.DownloadClient, error) {
	return c.rows, nil
}

// hubFakeStopper satisfies the hub's TrackingStopper.
type hubFakeStopper struct{ stopped []string }

func (s *hubFakeStopper) StopTracking(id string) bool {
	s.stopped = append(s.stopped, id)
	return true
}

// runChangeCategoryBlocklist wires the REAL queueRemoveAdapter and the
// REAL downloadeventhub.Service onto one bus (exactly as cmd/sonarr's
// serve() does — the adapter publishes DownloadFailedEvent on its
// blocklist arm; the hub subscribes HandleDownloadFailed). It performs a
// manual queue-delete with ChangeCategory+Blocklist against a single
// recording client shared by both, then drains the bus. Returns the
// shared client + stopper for assertions.
//
// This is the J461 pin for the Phase-1 audit question: does the J457
// hub failed-arm cause an INCORRECT double side-effect on the
// changeCategory path? The answer is faithful-to-C#: changeCategory runs
// MarkItemAsImported synchronously (QueueController.cs:345), the
// blocklist arm publishes DownloadFailedEvent (MarkAsFailed,
// QueueController.cs:360 → FailedDownloadService), and the hub removes
// from the client ONLY when CanBeRemoved && the client's
// RemoveFailedDownloads flag is set (DownloadEventHub.cs:29-47). So the
// removal is gated identically to C#, not unconditional.
func runChangeCategoryBlocklist(t *testing.T, removeFailedDownloads, canBeRemoved bool) (*recordingClient, *hubFakeStopper) {
	t.Helper()
	bus := messaging.New(slog.Default())

	client := &recordingClient{}
	resolver := fakeClientResolver{provider: client}

	qs := queue.NewService()
	qs.SetQueue([]queue.Queue{{ID: 1, DownloadID: "dl1"}})
	tracked := trackeddownloads.NewService(slog.Default())
	item := &downloadclients.Item{DownloadID: "dl1", CanBeRemoved: canBeRemoved}
	_ = tracked.Track(&trackeddownloads.TrackedDownload{
		DownloadID:     "dl1",
		DownloadClient: 7,
		DownloadItem:   item,
		IsTrackable:    true,
	})

	adapter := &queueRemoveAdapter{Service: qs, tracked: tracked, clients: resolver, bus: bus, log: slog.Default()}

	stopper := &hubFakeStopper{}
	hub, err := downloadeventhub.NewService(
		resolver,
		hubFakeConfig{rows: []downloadclients.DownloadClient{{ID: 7, RemoveFailedDownloads: removeFailedDownloads}}},
		stopper,
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("NewService(hub): %v", err)
	}
	messaging.Subscribe(bus, "downloadeventhub.HandleDownloadFailed",
		func(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			return hub.HandleDownloadFailed(ctx, &ev)
		})

	if _, err := adapter.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{
		ChangeCategory: true,
		Blocklist:      true,
	}); err != nil {
		t.Fatalf("RemoveQueueItem: %v", err)
	}

	bus.BeginShutdown()
	bus.Wait()
	return client, stopper
}

// TestQueueRemove_ChangeCategoryBlocklist_NoRemoveFailed_J461 pins the
// no-double-side-effect case: when the client's RemoveFailedDownloads
// flag is OFF, a changeCategory+blocklist delete recategorizes the item
// (MarkItemAsImported) and must NOT also remove it from the client — the
// changeCategory intent (keep the item, change its category) is
// preserved. Faithful to C#: DownloadEventHub.cs:42 returns early when
// !definition.RemoveFailedDownloads.
func TestQueueRemove_ChangeCategoryBlocklist_NoRemoveFailed_J461(t *testing.T) {
	t.Parallel()
	client, stopper := runChangeCategoryBlocklist(t, false, true)
	if len(client.imported) != 1 {
		t.Errorf("MarkItemAsImported calls = %d, want 1 (changeCategory)", len(client.imported))
	}
	if len(client.removed) != 0 {
		t.Errorf("RemoveItem calls = %d, want 0 (RemoveFailedDownloads off → no removal)", len(client.removed))
	}
	if len(stopper.stopped) != 0 {
		t.Errorf("hub StopTracking calls = %d, want 0", len(stopper.stopped))
	}
}

// TestQueueRemove_ChangeCategoryBlocklist_RemoveFailed_J461 pins the
// faithful-to-C# case: when the client's RemoveFailedDownloads flag is
// ON and the item CanBeRemoved, the hub DOES remove from the client on
// the published DownloadFailedEvent — same as C# (the blocklist arm runs
// MarkAsFailed regardless of changeCategory, and DownloadEventHub honours
// the client's RemoveFailedDownloads setting). So MarkItemAsImported AND
// RemoveItem both run; this is C# behaviour, not a Go-introduced bug.
func TestQueueRemove_ChangeCategoryBlocklist_RemoveFailed_J461(t *testing.T) {
	t.Parallel()
	client, stopper := runChangeCategoryBlocklist(t, true, true)
	if len(client.imported) != 1 {
		t.Errorf("MarkItemAsImported calls = %d, want 1 (changeCategory)", len(client.imported))
	}
	if len(client.removed) != 1 {
		t.Errorf("RemoveItem calls = %d, want 1 (RemoveFailedDownloads on + CanBeRemoved)", len(client.removed))
	}
	if len(stopper.stopped) != 1 {
		t.Errorf("hub StopTracking calls = %d, want 1", len(stopper.stopped))
	}
}

// TestQueueRemove_ChangeCategoryBlocklist_NotRemovable_J461 pins the
// CanBeRemoved gate: even with RemoveFailedDownloads ON, an item that
// cannot be removed (e.g. a torrent still seeding) is recategorized but
// not removed — DownloadEventHub.cs:35 returns early on
// !DownloadItem.CanBeRemoved.
func TestQueueRemove_ChangeCategoryBlocklist_NotRemovable_J461(t *testing.T) {
	t.Parallel()
	client, stopper := runChangeCategoryBlocklist(t, true, false)
	if len(client.imported) != 1 {
		t.Errorf("MarkItemAsImported calls = %d, want 1 (changeCategory)", len(client.imported))
	}
	if len(client.removed) != 0 {
		t.Errorf("RemoveItem calls = %d, want 0 (CanBeRemoved=false → no removal)", len(client.removed))
	}
	if len(stopper.stopped) != 0 {
		t.Errorf("hub StopTracking calls = %d, want 0", len(stopper.stopped))
	}
}
