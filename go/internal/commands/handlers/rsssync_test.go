package handlers_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// stubRssFetcher implements handlers.RssFetcher.
type stubRssFetcher struct {
	releases []any
	err      error
	calls    int32
}

func (s *stubRssFetcher) FetchRSS(_ context.Context) ([]any, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.releases, s.err
}

func (s *stubRssFetcher) callCount() int { return int(atomic.LoadInt32(&s.calls)) }

func TestNewRssSync_NilFetcherErrors(t *testing.T) {
	if _, err := handlers.NewRssSync(nil, nil, nil); err == nil {
		t.Fatalf("expected error for nil fetcher")
	}
}

func TestRssSync_Execute_HappyPath(t *testing.T) {
	fetcher := &stubRssFetcher{releases: []any{"r1", "r2", "r3"}}
	h, err := handlers.NewRssSync(fetcher, nil, nil)
	if err != nil {
		t.Fatalf("NewRssSync: %v", err)
	}
	if err := h.Execute(context.Background(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fetcher.callCount(); got != 1 {
		t.Errorf("FetchRSS calls = %d, want 1", got)
	}
}

func TestRssSync_Execute_FetcherErrorPropagates(t *testing.T) {
	want := errors.New("indexer source down")
	fetcher := &stubRssFetcher{err: want}
	h, _ := handlers.NewRssSync(fetcher, nil, nil)
	if err := h.Execute(context.Background(), ""); !errors.Is(err, want) {
		t.Errorf("Execute err = %v, want %v", err, want)
	}
}

func TestRssSync_Execute_BodyIgnored(t *testing.T) {
	fetcher := &stubRssFetcher{}
	h, _ := handlers.NewRssSync(fetcher, nil, nil)
	if err := h.Execute(context.Background(), `{"junk":"ignored"}`); err != nil {
		t.Errorf("Execute should ignore body, got %v", err)
	}
	if got := fetcher.callCount(); got != 1 {
		t.Errorf("FetchRSS calls = %d, want 1", got)
	}
}

func TestRssSync_Execute_PublishesCompleteEvent(t *testing.T) {
	bus := messaging.New(nil)
	var got handlers.RssSyncCompleteEvent
	var fired sync.WaitGroup
	fired.Add(1)
	messaging.Subscribe(bus, "test", func(_ context.Context, ev handlers.RssSyncCompleteEvent) error {
		got = ev
		fired.Done()
		return nil
	})

	fetcher := &stubRssFetcher{releases: []any{"r1", "r2"}}
	h, _ := handlers.NewRssSync(fetcher, bus, nil)
	if err := h.Execute(context.Background(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	fired.Wait()

	if got.ReportsFound != 2 {
		t.Errorf("ReportsFound = %d, want 2", got.ReportsFound)
	}
	if got.StartedAt.IsZero() || got.CompletedAt.IsZero() {
		t.Errorf("StartedAt/CompletedAt should be populated")
	}
	if got.CompletedAt.Before(got.StartedAt) {
		t.Errorf("CompletedAt should not predate StartedAt: %v < %v", got.CompletedAt, got.StartedAt)
	}
}

func TestRssSync_Execute_NilBusSafe(t *testing.T) {
	fetcher := &stubRssFetcher{releases: []any{"r1"}}
	h, _ := handlers.NewRssSync(fetcher, nil, nil)
	if err := h.Execute(context.Background(), ""); err != nil {
		t.Errorf("nil bus should be safe, got %v", err)
	}
}

func TestRssSync_DefaultScheduledTasks_HasFifteenMinuteInterval(t *testing.T) {
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.RssSyncName {
			if task.Interval != handlers.RssSyncIntervalDefault {
				t.Errorf("RssSync interval = %d, want %d", task.Interval, handlers.RssSyncIntervalDefault)
			}
			return
		}
	}
	t.Errorf("RssSync not in DefaultScheduledTasks")
}

// Compile-time check: scheduledtasks.Task is the expected element
// type — guards against silent shape drift.
var _ *scheduledtasks.Task = (handlers.DefaultScheduledTasks())[0]
