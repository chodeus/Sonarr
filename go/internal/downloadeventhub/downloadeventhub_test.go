package downloadeventhub_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadeventhub"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// fakeProvider embeds the Provider interface (nil) so it satisfies the
// full surface; only the two methods the hub calls are overridden — any
// other call would nil-panic, proving the hub touches nothing else.
type fakeProvider struct {
	downloadclients.Provider
	markErr   error
	removeErr error

	marked            bool
	removed           bool
	removedDeleteData bool
}

func (f *fakeProvider) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	f.marked = true
	return f.markErr
}

func (f *fakeProvider) RemoveItem(_ context.Context, _ *downloadclients.Item, deleteData bool) error {
	f.removed = true
	f.removedDeleteData = deleteData
	return f.removeErr
}

type fakeResolver struct {
	provider downloadclients.Provider
	err      error
}

func (r fakeResolver) ResolveClient(_ context.Context, _ int) (downloadclients.Provider, error) {
	return r.provider, r.err
}

type fakeConfig struct {
	rows []downloadclients.DownloadClient
}

func (c fakeConfig) All(_ context.Context) ([]downloadclients.DownloadClient, error) {
	return c.rows, nil
}

type fakeStopper struct{ stopped []string }

func (s *fakeStopper) StopTracking(downloadID string) bool {
	s.stopped = append(s.stopped, downloadID)
	return true
}

func newHub(t *testing.T, prov downloadclients.Provider, rows []downloadclients.DownloadClient, stopper *fakeStopper) *downloadeventhub.Service {
	t.Helper()
	svc, err := downloadeventhub.NewService(fakeResolver{provider: prov}, fakeConfig{rows: rows}, stopper, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func completedEvent(clientID int) *trackeddownloads.DownloadCompletedEvent {
	return &trackeddownloads.DownloadCompletedEvent{
		TrackedDownload: &trackeddownloads.TrackedDownload{
			DownloadID:     "dl1",
			DownloadClient: clientID,
			DownloadItem: &downloadclients.Item{
				DownloadID:   "dl1",
				CanBeRemoved: true,
				Status:       downloadclients.DownloadItemStatusCompleted,
			},
		},
	}
}

func TestHandle_MarksAndRemovesWhenConfigured(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveCompletedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	if err := hub.HandleDownloadCompleted(context.Background(), completedEvent(3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !prov.marked {
		t.Error("MarkItemAsImported not called")
	}
	if !prov.removed || !prov.removedDeleteData {
		t.Errorf("RemoveItem not called with deleteData=true (removed=%v deleteData=%v)", prov.removed, prov.removedDeleteData)
	}
	if len(stopper.stopped) != 1 || stopper.stopped[0] != "dl1" {
		t.Errorf("StopTracking = %v, want [dl1]", stopper.stopped)
	}
}

func TestHandle_MarksButSkipsRemoveWhenConfigOff(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveCompletedDownloads: false}}
	hub := newHub(t, prov, rows, stopper)

	if err := hub.HandleDownloadCompleted(context.Background(), completedEvent(3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !prov.marked {
		t.Error("MarkItemAsImported should always run")
	}
	if prov.removed || len(stopper.stopped) != 0 {
		t.Errorf("RemoveCompletedDownloads=false → no remove/stop (removed=%v stopped=%v)", prov.removed, stopper.stopped)
	}
}

func TestHandle_SkipsRemoveWhenNotRemovable(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveCompletedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	ev := completedEvent(3)
	ev.TrackedDownload.DownloadItem.CanBeRemoved = false
	if err := hub.HandleDownloadCompleted(context.Background(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !prov.marked {
		t.Error("MarkItemAsImported should still run")
	}
	if prov.removed {
		t.Error("CanBeRemoved=false → must not remove")
	}
}

func TestHandle_SkipsRemoveWhenStillDownloading(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveCompletedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	ev := completedEvent(3)
	ev.TrackedDownload.DownloadItem.Status = downloadclients.DownloadItemStatusDownloading
	if err := hub.HandleDownloadCompleted(context.Background(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prov.removed {
		t.Error("status Downloading → must not remove")
	}
}

func TestHandle_UnsupportedMarkStillRemoves(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{markErr: downloadclients.ErrMarkAsImportedUnsupported}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveCompletedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	if err := hub.HandleDownloadCompleted(context.Background(), completedEvent(3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !prov.removed {
		t.Error("an unsupported MarkItemAsImported must not abort the removal arm")
	}
}

func TestHandle_ResolveErrorIsSwallowed(t *testing.T) {
	t.Parallel()
	stopper := &fakeStopper{}
	svc, err := downloadeventhub.NewService(fakeResolver{err: errors.New("no client")}, fakeConfig{}, stopper, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.HandleDownloadCompleted(context.Background(), completedEvent(3)); err != nil {
		t.Errorf("resolve error should be swallowed, got %v", err)
	}
	if len(stopper.stopped) != 0 {
		t.Error("nothing should be stopped when the client can't be resolved")
	}
}

func failedEvent(clientID int) *trackeddownloads.DownloadFailedEvent {
	return &trackeddownloads.DownloadFailedEvent{
		DownloadID: "dl1",
		TrackedDownload: &trackeddownloads.TrackedDownload{
			DownloadID:     "dl1",
			DownloadClient: clientID,
			DownloadItem:   &downloadclients.Item{DownloadID: "dl1", CanBeRemoved: true},
		},
	}
}

func TestHandleFailed_RemovesWhenConfigured(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveFailedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	if err := hub.HandleDownloadFailed(context.Background(), failedEvent(3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prov.marked {
		t.Error("failed arm must NOT MarkItemAsImported")
	}
	if !prov.removed || !prov.removedDeleteData {
		t.Errorf("RemoveItem not called with deleteData=true (removed=%v deleteData=%v)", prov.removed, prov.removedDeleteData)
	}
	if len(stopper.stopped) != 1 || stopper.stopped[0] != "dl1" {
		t.Errorf("StopTracking = %v, want [dl1]", stopper.stopped)
	}
}

func TestHandleFailed_SkipsWhenConfigOff(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveFailedDownloads: false}}
	hub := newHub(t, prov, rows, stopper)

	if err := hub.HandleDownloadFailed(context.Background(), failedEvent(3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prov.removed || len(stopper.stopped) != 0 {
		t.Errorf("RemoveFailedDownloads=false → no remove/stop (removed=%v stopped=%v)", prov.removed, stopper.stopped)
	}
}

func TestHandleFailed_SkipsWhenNotRemovable(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveFailedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	ev := failedEvent(3)
	ev.TrackedDownload.DownloadItem.CanBeRemoved = false
	if err := hub.HandleDownloadFailed(context.Background(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prov.removed {
		t.Error("CanBeRemoved=false → must not remove")
	}
}

func TestHandleFailed_NilTrackedDownload(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveFailedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	// Manual-failed push with no tracked record → skipped (can't resolve client).
	if err := hub.HandleDownloadFailed(context.Background(), &trackeddownloads.DownloadFailedEvent{DownloadID: "x"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prov.removed || len(stopper.stopped) != 0 {
		t.Error("nil TrackedDownload must be a no-op")
	}
}

func canBeRemovedEvent(clientID int, removable bool) *trackeddownloads.DownloadCanBeRemovedEvent {
	return &trackeddownloads.DownloadCanBeRemovedEvent{
		TrackedDownload: &trackeddownloads.TrackedDownload{
			DownloadID:     "dl1",
			DownloadClient: clientID,
			State:          trackeddownloads.StateImported,
			DownloadItem:   &downloadclients.Item{DownloadID: "dl1", CanBeRemoved: removable},
		},
	}
}

func TestHandleCanBeRemoved_RemovesWhenConfigured(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveCompletedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	if err := hub.HandleDownloadCanBeRemoved(context.Background(), canBeRemovedEvent(3, true)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prov.marked {
		t.Error("can-be-removed arm must NOT MarkItemAsImported")
	}
	if !prov.removed || len(stopper.stopped) != 1 {
		t.Errorf("expected remove+stop (removed=%v stopped=%v)", prov.removed, stopper.stopped)
	}
}

func TestHandleCanBeRemoved_SkipsWhenConfigOff(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveCompletedDownloads: false}}
	hub := newHub(t, prov, rows, stopper)

	if err := hub.HandleDownloadCanBeRemoved(context.Background(), canBeRemovedEvent(3, true)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prov.removed || len(stopper.stopped) != 0 {
		t.Errorf("RemoveCompletedDownloads=false → no remove/stop")
	}
}

func TestHandleCanBeRemoved_SkipsWhenNotRemovable(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{}
	stopper := &fakeStopper{}
	rows := []downloadclients.DownloadClient{{ID: 3, RemoveCompletedDownloads: true}}
	hub := newHub(t, prov, rows, stopper)

	if err := hub.HandleDownloadCanBeRemoved(context.Background(), canBeRemovedEvent(3, false)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prov.removed {
		t.Error("CanBeRemoved=false → must not remove")
	}
}

func TestHandle_NilGuards(t *testing.T) {
	t.Parallel()
	hub := newHub(t, &fakeProvider{}, nil, &fakeStopper{})
	ctx := context.Background()
	if err := hub.HandleDownloadCompleted(ctx, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if err := hub.HandleDownloadCompleted(ctx, &trackeddownloads.DownloadCompletedEvent{}); err != nil {
		t.Errorf("nil tracked download: %v", err)
	}
	if err := hub.HandleDownloadCompleted(ctx, &trackeddownloads.DownloadCompletedEvent{TrackedDownload: &trackeddownloads.TrackedDownload{}}); err != nil {
		t.Errorf("nil download item: %v", err)
	}
}
