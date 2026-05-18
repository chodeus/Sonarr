package faileddownloads_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/faileddownloads"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type stubHistory struct {
	entry *history.Entry
	err   error
}

func (s *stubHistory) Get(_ context.Context, _ int) (*history.Entry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entry, nil
}

func TestNewService_NilReader(t *testing.T) {
	t.Parallel()
	bus := messaging.New(nil)
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	if _, err := faileddownloads.NewService(nil, bus); err == nil {
		t.Errorf("expected error for nil reader")
	}
}

func TestNewService_NilBus(t *testing.T) {
	t.Parallel()
	if _, err := faileddownloads.NewService(&stubHistory{}, nil); err == nil {
		t.Errorf("expected error for nil bus")
	}
}

func TestMarkAsFailed_PublishesEvent(t *testing.T) {
	t.Parallel()
	bus := messaging.New(nil)
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })

	got := make(chan trackeddownloads.DownloadFailedEvent, 1)
	messaging.Subscribe(bus, "test.capture",
		func(_ context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			got <- ev
			return nil
		})

	h := &stubHistory{entry: &history.Entry{
		ID:          5,
		SeriesID:    11,
		EpisodeID:   23,
		SourceTitle: "Show.S01E01.WEB-DL-GROUP",
		Date:        time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC),
		Quality:     `{}`,
		Data: map[string]string{
			"DownloadClient": "QBittorrent",
			"Indexer":        "AnIndexer",
		},
		DownloadID: "abc123",
		Languages:  []int{1},
	}}
	svc, err := faileddownloads.NewService(h, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.MarkAsFailed(context.Background(), 5); err != nil {
		t.Fatalf("MarkAsFailed: %v", err)
	}

	select {
	case ev := <-got:
		if ev.SeriesID != 11 {
			t.Errorf("SeriesID: %d", ev.SeriesID)
		}
		if len(ev.EpisodeIDs) != 1 || ev.EpisodeIDs[0] != 23 {
			t.Errorf("EpisodeIDs: %v", ev.EpisodeIDs)
		}
		if ev.SourceTitle != "Show.S01E01.WEB-DL-GROUP" {
			t.Errorf("SourceTitle: %q", ev.SourceTitle)
		}
		if ev.DownloadClient != "QBittorrent" {
			t.Errorf("DownloadClient: %q", ev.DownloadClient)
		}
		if ev.DownloadID != "abc123" {
			t.Errorf("DownloadID: %q", ev.DownloadID)
		}
		if ev.Message != "Manually marked as failed" {
			t.Errorf("Message: %q", ev.Message)
		}
		if ev.Source != "ManualOverride" {
			t.Errorf("Source: %q", ev.Source)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DownloadFailedEvent not received")
	}
}

func TestMarkAsFailed_HistoryNotFoundPropagates(t *testing.T) {
	t.Parallel()
	bus := messaging.New(nil)
	t.Cleanup(func() { bus.BeginShutdown(); bus.Wait() })
	h := &stubHistory{err: history.ErrNotFound}
	svc, err := faileddownloads.NewService(h, bus)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	err = svc.MarkAsFailed(context.Background(), 999)
	if !errors.Is(err, history.ErrNotFound) {
		t.Errorf("expected history.ErrNotFound; got %v", err)
	}
}
