package downloadmonitoring_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadmonitoring"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// stubProvider implements downloadclients.Provider with a pinned
// item-list / error for GetItems. The other Provider methods are
// no-ops; the service only calls Name() + GetItems().
type stubProvider struct {
	name  string
	items []*downloadclients.Item
	err   error
	calls int
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.items, nil
}

func (s *stubProvider) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", nil
}

func (s *stubProvider) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, nil
}

func (s *stubProvider) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return nil
}

func (s *stubProvider) Definition() *downloadclients.DownloadClient { return nil }

func (s *stubProvider) Protocol() indexers.DownloadProtocol { return indexers.DownloadProtocolUnknown }

func (s *stubProvider) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

func (s *stubProvider) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return nil
}

func (s *stubProvider) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	return nil, nil
}

func (s *stubProvider) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// stubLister returns pinned per-protocol provider lists.
type stubLister struct {
	usenet     []downloadclients.Provider
	torrent    []downloadclients.Provider
	usenetErr  error
	torrentErr error
}

func (s *stubLister) ListByProtocol(_ context.Context, p indexers.DownloadProtocol) ([]downloadclients.Provider, error) {
	switch p {
	case indexers.DownloadProtocolUsenet:
		return s.usenet, s.usenetErr
	case indexers.DownloadProtocolTorrent:
		return s.torrent, s.torrentErr
	}
	return nil, nil
}

func TestNewService_RequiresLister(t *testing.T) {
	t.Parallel()
	if _, err := downloadmonitoring.NewService(nil, nil, nil); err == nil {
		t.Errorf("nil lister: want error")
	}
}

func TestRefresh_PollsAllProtocolsAndPublishes(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	usenetProv := &stubProvider{name: "sab", items: []*downloadclients.Item{{}, {}}}
	torrentProv := &stubProvider{name: "qbit", items: []*downloadclients.Item{{}, {}, {}}}
	lister := &stubLister{
		usenet:  []downloadclients.Provider{usenetProv},
		torrent: []downloadclients.Provider{torrentProv},
	}

	var queueEvents atomic.Int32
	messaging.Subscribe(bus, "test.queue.updated",
		func(_ context.Context, _ queue.QueueUpdatedEvent) error {
			queueEvents.Add(1)
			return nil
		})
	var trackedEvents atomic.Int32
	messaging.Subscribe(bus, "test.tracked.refreshed",
		func(_ context.Context, _ trackeddownloads.TrackedDownloadRefreshedEvent) error {
			trackedEvents.Add(1)
			return nil
		})

	svc, err := downloadmonitoring.NewService(lister, bus, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	items, err := svc.Refresh(context.Background())
	if err != nil {
		t.Errorf("Refresh: unexpected err: %v", err)
	}
	if items != 5 {
		t.Errorf("Refresh items: got %d, want 5 (usenet 2 + torrent 3)", items)
	}
	if usenetProv.calls != 1 || torrentProv.calls != 1 {
		t.Errorf("per-client GetItems calls: usenet=%d, torrent=%d, want 1 each", usenetProv.calls, torrentProv.calls)
	}
	if queueEvents.Load() != 1 {
		t.Errorf("QueueUpdatedEvent: got %d, want 1", queueEvents.Load())
	}
	if trackedEvents.Load() != 1 {
		t.Errorf("TrackedDownloadRefreshedEvent: got %d, want 1", trackedEvents.Load())
	}
}

func TestRefresh_PerClientFailureIsNonFatal(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	good := &stubProvider{name: "good", items: []*downloadclients.Item{{}}}
	bad := &stubProvider{name: "bad", err: errors.New("offline")}
	lister := &stubLister{
		usenet:  []downloadclients.Provider{good, bad},
		torrent: nil,
	}

	svc, _ := downloadmonitoring.NewService(lister, bus, slog.Default())
	items, err := svc.Refresh(context.Background())
	if err == nil {
		t.Errorf("Refresh: want non-nil error (one client failed)")
	}
	if items != 1 {
		t.Errorf("Refresh items: got %d, want 1 (good client's contribution)", items)
	}
	if good.calls != 1 || bad.calls != 1 {
		t.Errorf("both clients should be polled: good=%d bad=%d", good.calls, bad.calls)
	}
}

func TestRefresh_ListByProtocolFailureContinues(t *testing.T) {
	t.Parallel()
	// Usenet lookup fails entirely → torrent lookup still runs.
	torrent := &stubProvider{name: "qbit", items: []*downloadclients.Item{{}, {}}}
	lister := &stubLister{
		usenetErr: errors.New("config broken"),
		torrent:   []downloadclients.Provider{torrent},
	}
	svc, _ := downloadmonitoring.NewService(lister, nil, slog.Default())
	items, err := svc.Refresh(context.Background())
	if err == nil {
		t.Errorf("Refresh: want non-nil error")
	}
	if items != 2 {
		t.Errorf("Refresh items: got %d, want 2 (torrent still polled)", items)
	}
}

func TestRefresh_NilBusSkipsPublish(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{name: "sab", items: []*downloadclients.Item{{}}}
	lister := &stubLister{usenet: []downloadclients.Provider{prov}}
	svc, _ := downloadmonitoring.NewService(lister, nil, slog.Default())
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Errorf("Refresh with nil bus: %v", err)
	}
}

// stubTracker returns one TrackedDownload per item (echoing the item's
// DownloadID), recording how many times it was invoked.
type stubTracker struct{ calls int }

func (s *stubTracker) TrackDownload(_ context.Context, _ downloadclients.Provider, item *downloadclients.Item) *trackeddownloads.TrackedDownload {
	s.calls++
	return &trackeddownloads.TrackedDownload{DownloadID: item.DownloadID, IsTrackable: true}
}

// J394: with a tracker wired, Refresh maps polled items into the
// TrackedDownloadRefreshedEvent so the queue can populate.
func TestRefresh_TrackerPopulatesEvent_J394(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	usenetProv := &stubProvider{name: "sab", items: []*downloadclients.Item{{DownloadID: "a"}, {DownloadID: "b"}}}
	torrentProv := &stubProvider{name: "qbit", items: []*downloadclients.Item{{DownloadID: "c"}}}

	gotCount := make(chan int, 1)
	messaging.Subscribe(bus, "test.tracked.count",
		func(_ context.Context, ev trackeddownloads.TrackedDownloadRefreshedEvent) error {
			select {
			case gotCount <- len(ev.TrackedDownloads):
			default:
			}
			return nil
		})

	svc, err := downloadmonitoring.NewService(&stubLister{usenet: []downloadclients.Provider{usenetProv}, torrent: []downloadclients.Provider{torrentProv}}, bus, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	tracker := &stubTracker{}
	svc.SetTracker(tracker)

	if _, err := svc.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	bus.BeginShutdown()
	bus.Wait()

	if tracker.calls != 3 {
		t.Errorf("tracker calls = %d, want 3 (2 usenet + 1 torrent)", tracker.calls)
	}
	select {
	case n := <-gotCount:
		if n != 3 {
			t.Errorf("published TrackedDownloads = %d, want 3", n)
		}
	default:
		t.Error("no TrackedDownloadRefreshedEvent observed")
	}
}

type stubProcessor struct{ calls int }

func (s *stubProcessor) ProcessTrackedDownloads(context.Context) int { s.calls++; return 0 }

// J399: Refresh runs the wired processor after populating the cache.
func TestRefresh_RunsProcessor_J399(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	prov := &stubProvider{name: "sab", items: []*downloadclients.Item{{DownloadID: "a"}}}
	svc, err := downloadmonitoring.NewService(&stubLister{usenet: []downloadclients.Provider{prov}}, bus, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	proc := &stubProcessor{}
	svc.SetProcessor(proc)
	if _, err := svc.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if proc.calls != 1 {
		t.Errorf("processor calls = %d, want 1", proc.calls)
	}
}
