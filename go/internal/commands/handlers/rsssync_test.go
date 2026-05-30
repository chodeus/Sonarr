package handlers_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/scheduledtasks"
	"sonarr.tv/sonarr/internal/series"
)

// stubRssDecider returns a fixed decision slice. Slice 6.A6d test
// scaffolding.
type stubRssDecider struct {
	decisions []decisionengine.Decision
}

func (s *stubRssDecider) GetRssDecision(_ context.Context, _ []any, _ bool) []decisionengine.Decision {
	return s.decisions
}

// stubGrabber satisfies handlers.GrabProcessor with caller-supplied
// bucket counts. The decisions argument is captured so tests can
// assert what the orchestrator received.
type stubGrabber struct {
	mu       sync.Mutex
	received []decisionengine.Decision
	result   *download.ProcessedDecisions
}

func (s *stubGrabber) ProcessDecisions(_ context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append([]decisionengine.Decision(nil), decisions...)
	if s.result != nil {
		return s.result
	}
	return download.NewProcessedDecisions()
}

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
	if _, err := handlers.NewRssSync(nil, nil, nil, nil, nil); err == nil {
		t.Fatalf("expected error for nil fetcher")
	}
}

func TestRssSync_Execute_HappyPath(t *testing.T) {
	fetcher := &stubRssFetcher{releases: []any{"r1", "r2", "r3"}}
	h, err := handlers.NewRssSync(fetcher, nil, nil, nil, nil)
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
	h, _ := handlers.NewRssSync(fetcher, nil, nil, nil, nil)
	if err := h.Execute(context.Background(), ""); !errors.Is(err, want) {
		t.Errorf("Execute err = %v, want %v", err, want)
	}
}

func TestRssSync_Execute_BodyIgnored(t *testing.T) {
	fetcher := &stubRssFetcher{}
	h, _ := handlers.NewRssSync(fetcher, nil, nil, nil, nil)
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
	h, _ := handlers.NewRssSync(fetcher, nil, nil, bus, nil)
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
	h, _ := handlers.NewRssSync(fetcher, nil, nil, nil, nil)
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

// Slice 6.A6d: grabber receives the full decider output and the
// emitted RssSyncCompleteEvent carries the orchestrator's Grabbed /
// Pending counts alongside the existing Approved / Rejected counts.
func TestRssSync_Execute_GrabberReceivesDecisionsAndCountsFlowToEvent(t *testing.T) {
	decisions := []decisionengine.Decision{
		{}, // approved (no rejections)
		{}, // approved (no rejections)
		{Rejections: []decisionengine.Rejection{decisionengine.NewRejection(decisionengine.ReasonUnknown, "spec failed")}},
	}
	bus := messaging.New(nil)
	var got handlers.RssSyncCompleteEvent
	var fired sync.WaitGroup
	fired.Add(1)
	messaging.Subscribe(bus, "test", func(_ context.Context, ev handlers.RssSyncCompleteEvent) error {
		got = ev
		fired.Done()
		return nil
	})

	fetcher := &stubRssFetcher{releases: []any{"r1", "r2", "r3"}}
	decider := &stubRssDecider{decisions: decisions}
	grabber := &stubGrabber{result: &download.ProcessedDecisions{
		Grabbed: []decisionengine.Decision{decisions[0]},
		Pending: []decisionengine.Decision{decisions[1]},
	}}

	h, err := handlers.NewRssSync(fetcher, decider, grabber, bus, nil)
	if err != nil {
		t.Fatalf("NewRssSync: %v", err)
	}
	if err := h.Execute(context.Background(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	fired.Wait()

	if len(grabber.received) != 3 {
		t.Errorf("grabber received %d decisions, want 3", len(grabber.received))
	}
	if got.ApprovedDecisions != 2 {
		t.Errorf("ApprovedDecisions = %d, want 2", got.ApprovedDecisions)
	}
	if got.RejectedDecisions != 1 {
		t.Errorf("RejectedDecisions = %d, want 1", got.RejectedDecisions)
	}
	if got.GrabbedDecisions != 1 {
		t.Errorf("GrabbedDecisions = %d, want 1", got.GrabbedDecisions)
	}
	if got.PendingDecisions != 1 {
		t.Errorf("PendingDecisions = %d, want 1", got.PendingDecisions)
	}
}

// J490: the permanently-rejected releases from ProcessDecisions are
// surfaced as match-tuples on the event so the RemoveRejected
// subscriber can purge matching pending rows.
func TestRssSync_Execute_RejectedReleasesFlowToEvent(t *testing.T) {
	sub := decisionengine.NewSubject()
	sub.Series = &series.Series{ID: 5}
	sub.Release = &parser.ReleaseInfo{Title: "Rejected.Release", Indexer: "nzbIndexer"}
	rejected := decisionengine.Decision{
		Subject:    sub,
		Rejections: []decisionengine.Rejection{decisionengine.NewRejection(decisionengine.ReasonUnknown, "spec failed")},
	}

	bus := messaging.New(nil)
	var got handlers.RssSyncCompleteEvent
	var fired sync.WaitGroup
	fired.Add(1)
	messaging.Subscribe(bus, "test", func(_ context.Context, ev handlers.RssSyncCompleteEvent) error {
		got = ev
		fired.Done()
		return nil
	})

	fetcher := &stubRssFetcher{releases: []any{"r1"}}
	decider := &stubRssDecider{decisions: []decisionengine.Decision{rejected}}
	grabber := &stubGrabber{result: &download.ProcessedDecisions{
		Rejected: []decisionengine.Decision{rejected},
	}}

	h, err := handlers.NewRssSync(fetcher, decider, grabber, bus, nil)
	if err != nil {
		t.Fatalf("NewRssSync: %v", err)
	}
	if err := h.Execute(context.Background(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	fired.Wait()

	if len(got.RejectedReleases) != 1 {
		t.Fatalf("RejectedReleases = %d, want 1", len(got.RejectedReleases))
	}
	if got.RejectedReleases[0].SeriesID != 5 || got.RejectedReleases[0].MatchTitle != "Rejected.Release" || got.RejectedReleases[0].MatchIndexer != "nzbIndexer" {
		t.Errorf("RejectedReleases[0] = %+v", got.RejectedReleases[0])
	}
}

// Slice 6.A6d: nil grabber preserves the slice-4.U behaviour —
// decisions are produced but never dispatched, event counts for
// Grabbed/Pending stay zero.
func TestRssSync_Execute_NilGrabberLeavesGrabbedZero(t *testing.T) {
	bus := messaging.New(nil)
	var got handlers.RssSyncCompleteEvent
	var fired sync.WaitGroup
	fired.Add(1)
	messaging.Subscribe(bus, "test", func(_ context.Context, ev handlers.RssSyncCompleteEvent) error {
		got = ev
		fired.Done()
		return nil
	})

	fetcher := &stubRssFetcher{releases: []any{"r1"}}
	decider := &stubRssDecider{decisions: []decisionengine.Decision{{}}}
	h, err := handlers.NewRssSync(fetcher, decider, nil, bus, nil)
	if err != nil {
		t.Fatalf("NewRssSync: %v", err)
	}
	if err := h.Execute(context.Background(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	fired.Wait()

	if got.ApprovedDecisions != 1 {
		t.Errorf("ApprovedDecisions = %d, want 1", got.ApprovedDecisions)
	}
	if got.GrabbedDecisions != 0 || got.PendingDecisions != 0 {
		t.Errorf("nil grabber must keep Grabbed/Pending zero, got %d/%d", got.GrabbedDecisions, got.PendingDecisions)
	}
}

// Slice 6.A6d: grabber not called when decider returns nothing —
// avoids a needless ProcessDecisions invocation with an empty batch.
func TestRssSync_Execute_GrabberSkippedOnEmptyDecisionSlice(t *testing.T) {
	fetcher := &stubRssFetcher{releases: []any{"r1"}}
	decider := &stubRssDecider{decisions: nil}
	grabber := &stubGrabber{}
	h, _ := handlers.NewRssSync(fetcher, decider, grabber, nil, nil)
	if err := h.Execute(context.Background(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if grabber.received != nil {
		t.Errorf("grabber should not be called on empty decisions, got %d received", len(grabber.received))
	}
}
