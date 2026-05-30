package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
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

// J396: opts.Blocklist publishes a DownloadFailedEvent built from the
// tracked download's RemoteEpisode (consumed by the subscribed blocklist
// handler). Verifies the essential fields + SkipRedownload pass-through.
func TestQueueRemoveAdapter_Blocklist_J396(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	got := make(chan trackeddownloads.DownloadFailedEvent, 1)
	messaging.Subscribe(bus, "test.blocklist.capture",
		func(_ context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			select {
			case got <- ev:
			default:
			}
			return nil
		})

	qs := queue.NewService()
	qs.SetQueue([]queue.Queue{{ID: 1, DownloadID: "dl1"}})
	tracked := trackeddownloads.NewService(slog.Default())
	item := &downloadclients.Item{DownloadID: "dl1", Title: "Show.S01E01.WEBDL-1080p"}
	_ = tracked.Track(&trackeddownloads.TrackedDownload{
		DownloadID:   "dl1",
		DownloadItem: item,
		IsTrackable:  true,
		RemoteEpisode: &decisionengine.Subject{
			Series:            &series.Series{ID: 42},
			Episodes:          []episodes.Episode{{ID: 100}},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Show.S01E01.WEBDL-1080p"},
		},
	})

	a := &queueRemoveAdapter{Service: qs, tracked: tracked, clients: fakeClientResolver{}, bus: bus, log: slog.Default()}
	if _, err := a.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{Blocklist: true, SkipRedownload: true}); err != nil {
		t.Fatalf("RemoveQueueItem: %v", err)
	}
	bus.BeginShutdown()
	bus.Wait()

	select {
	case ev := <-got:
		if ev.SeriesID != 42 {
			t.Errorf("ev.SeriesID = %d, want 42", ev.SeriesID)
		}
		if len(ev.EpisodeIDs) != 1 || ev.EpisodeIDs[0] != 100 {
			t.Errorf("ev.EpisodeIDs = %v, want [100]", ev.EpisodeIDs)
		}
		if !ev.SkipRedownload {
			t.Error("SkipRedownload should propagate")
		}
		if ev.SourceTitle != "Show.S01E01.WEBDL-1080p" {
			t.Errorf("ev.SourceTitle = %q", ev.SourceTitle)
		}
	default:
		t.Fatal("no DownloadFailedEvent published")
	}
}

// J462: the no-flags delete (no removeFromClient/blocklist/changeCategory)
// is an "ignore" — it must publish a DownloadIgnoredEvent built from the
// tracked download's RemoteEpisode, mirroring C#
// IgnoredDownloadService.IgnoreDownload (QueueController.cs:355-358).
// Before J462 the two DownloadIgnored consumers (history +
// downloadhistory) had no producer — dead consumers.
func TestQueueRemoveAdapter_NoFlags_PublishesIgnored_J462(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	got := make(chan trackeddownloads.DownloadIgnoredEvent, 1)
	messaging.Subscribe(bus, "test.ignored.capture.j462",
		func(_ context.Context, ev trackeddownloads.DownloadIgnoredEvent) error {
			select {
			case got <- ev:
			default:
			}
			return nil
		})

	qs := queue.NewService()
	qs.SetQueue([]queue.Queue{{ID: 1, DownloadID: "dl1"}})
	tracked := trackeddownloads.NewService(slog.Default())
	_ = tracked.Track(&trackeddownloads.TrackedDownload{
		DownloadID:   "dl1",
		DownloadItem: &downloadclients.Item{DownloadID: "dl1", Title: "Show.S01E01"},
		IsTrackable:  true,
		RemoteEpisode: &decisionengine.Subject{
			Series:            &series.Series{ID: 42},
			Episodes:          []episodes.Episode{{ID: 100}, {ID: 101}},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Show.S01E01"},
		},
	})

	a := &queueRemoveAdapter{Service: qs, tracked: tracked, clients: fakeClientResolver{}, bus: bus, log: slog.Default()}
	if _, err := a.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{}); err != nil {
		t.Fatalf("RemoveQueueItem: %v", err)
	}
	bus.BeginShutdown()
	bus.Wait()

	select {
	case ev := <-got:
		if ev.SeriesID != 42 {
			t.Errorf("ev.SeriesID = %d, want 42", ev.SeriesID)
		}
		if len(ev.EpisodeIDs) != 2 || ev.EpisodeIDs[0] != 100 || ev.EpisodeIDs[1] != 101 {
			t.Errorf("ev.EpisodeIDs = %v, want [100 101]", ev.EpisodeIDs)
		}
		if ev.SourceTitle != "Show.S01E01" {
			t.Errorf("ev.SourceTitle = %q", ev.SourceTitle)
		}
		if ev.Message != "Manually ignored" {
			t.Errorf("ev.Message = %q, want 'Manually ignored'", ev.Message)
		}
	default:
		t.Fatal("no DownloadIgnoredEvent published on the no-flags delete")
	}
}

// J462: the ignore path skips publishing (and logs) when the series is
// unknown — mirrors C# IgnoredDownloadService returning false without an
// event (IgnoredDownloadService.cs:30-34). StopTracking + queue removal
// still happen.
func TestQueueRemoveAdapter_NoFlags_UnknownSeries_NoIgnored_J462(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	got := make(chan trackeddownloads.DownloadIgnoredEvent, 1)
	messaging.Subscribe(bus, "test.ignored.capture.unknown.j462",
		func(_ context.Context, ev trackeddownloads.DownloadIgnoredEvent) error {
			select {
			case got <- ev:
			default:
			}
			return nil
		})

	qs := queue.NewService()
	qs.SetQueue([]queue.Queue{{ID: 1, DownloadID: "dl1"}})
	tracked := trackeddownloads.NewService(slog.Default())
	_ = tracked.Track(&trackeddownloads.TrackedDownload{
		DownloadID:   "dl1",
		DownloadItem: &downloadclients.Item{DownloadID: "dl1", Title: "Unknown"},
		IsTrackable:  true,
		// RemoteEpisode with no Series → unknown series.
		RemoteEpisode: &decisionengine.Subject{},
	})

	a := &queueRemoveAdapter{Service: qs, tracked: tracked, clients: fakeClientResolver{}, bus: bus, log: slog.Default()}
	if _, err := a.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{}); err != nil {
		t.Fatalf("RemoveQueueItem: %v", err)
	}
	if tracked.Find("dl1") != nil || qs.Find(1) != nil {
		t.Error("unknown-series ignore must still StopTracking + remove the queue row")
	}
	bus.BeginShutdown()
	bus.Wait()

	select {
	case ev := <-got:
		t.Errorf("unexpected DownloadIgnoredEvent for unknown series: %+v", ev)
	default:
	}
}

// J439: the blocklist DownloadFailedEvent carries the release Indexer +
// Size (via Data) so the blocklist row's columns are populated.
func TestQueueRemoveAdapter_Blocklist_CarriesIndexerSize_J439(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	got := make(chan trackeddownloads.DownloadFailedEvent, 1)
	messaging.Subscribe(bus, "test.blocklist.capture.j439",
		func(_ context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			select {
			case got <- ev:
			default:
			}
			return nil
		})

	qs := queue.NewService()
	qs.SetQueue([]queue.Queue{{ID: 1, DownloadID: "dl1"}})
	tracked := trackeddownloads.NewService(slog.Default())
	_ = tracked.Track(&trackeddownloads.TrackedDownload{
		DownloadID:   "dl1",
		DownloadItem: &downloadclients.Item{DownloadID: "dl1", Title: "T"},
		IsTrackable:  true,
		RemoteEpisode: &decisionengine.Subject{
			Series:            &series.Series{ID: 42},
			Episodes:          []episodes.Episode{{ID: 100}},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "T"},
			Release:           &parser.ReleaseInfo{Indexer: "RARBG", Size: 2_500_000_000},
		},
	})

	a := &queueRemoveAdapter{Service: qs, tracked: tracked, clients: fakeClientResolver{}, bus: bus, log: slog.Default()}
	if _, err := a.RemoveQueueItem(t.Context(), 1, apiv3.QueueRemoveOptions{Blocklist: true}); err != nil {
		t.Fatalf("RemoveQueueItem: %v", err)
	}
	bus.BeginShutdown()
	bus.Wait()

	select {
	case ev := <-got:
		if ev.Data["indexer"] != "RARBG" {
			t.Errorf("Data[indexer] = %q, want RARBG", ev.Data["indexer"])
		}
		if ev.Data["size"] != "2500000000" {
			t.Errorf("Data[size] = %q, want 2500000000", ev.Data["size"])
		}
	default:
		t.Fatal("no DownloadFailedEvent published")
	}
}
