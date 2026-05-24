package seriesadd_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/episodemonitored"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/seriesadd"
)

// ---- Stubs ----

type stubCascader struct {
	calls []struct {
		seriesID int
		opts     *episodemonitored.MonitoringOptions
	}
	err error
}

func (s *stubCascader) SetEpisodeMonitoredStatus(_ context.Context, ser *series.Series, opts *episodemonitored.MonitoringOptions) error {
	s.calls = append(s.calls, struct {
		seriesID int
		opts     *episodemonitored.MonitoringOptions
	}{ser.ID, opts})
	return s.err
}

type stubPusher struct {
	mu    sync.Mutex
	calls []struct {
		name string
		body string
	}
	err error
}

func (s *stubPusher) Push(_ context.Context, name, body string, _ commands.Priority, _ commands.Trigger) (*commands.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, struct {
		name string
		body string
	}{name, body})
	if s.err != nil {
		return nil, s.err
	}
	return &commands.Command{ID: 1, Name: name, Body: body}, nil
}

type stubClearer struct {
	cleared []int
	err     error
}

func (s *stubClearer) RemoveAddOptions(_ context.Context, seriesID int) error {
	s.cleared = append(s.cleared, seriesID)
	return s.err
}

func mustNewSvc(t *testing.T, c *stubCascader, p *stubPusher, cl *stubClearer, bus *messaging.Bus) *seriesadd.Service {
	t.Helper()
	svc, err := seriesadd.NewService(c, p, cl, bus, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// ---- Constructor ----

func TestNewService_NilCascader_Errors(t *testing.T) {
	t.Parallel()
	if _, err := seriesadd.NewService(nil, &stubPusher{}, &stubClearer{}, nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestNewService_NilPusher_Errors(t *testing.T) {
	t.Parallel()
	if _, err := seriesadd.NewService(&stubCascader{}, nil, &stubClearer{}, nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestNewService_NilClearer_Errors(t *testing.T) {
	t.Parallel()
	if _, err := seriesadd.NewService(&stubCascader{}, &stubPusher{}, nil, nil, nil); err == nil {
		t.Fatal("want error")
	}
}

// ---- AddOptions gate ----

func TestHandle_NilSeries_NoOp(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: nil}); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 || len(p.calls) != 0 || len(cl.cleared) != 0 {
		t.Errorf("nil Series: nothing should run")
	}
}

func TestHandle_NoAddOptions_NoOp(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, Title: "Test", AddOptions: ""}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Errorf("AddOptions absent: cascade must NOT fire; got %d", len(c.calls))
	}
	if len(cl.cleared) != 0 {
		t.Errorf("AddOptions absent: clearer must NOT fire; got %v", cl.cleared)
	}
}

func TestHandle_MalformedAddOptions_NoOp(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, AddOptions: `{not json`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Errorf("malformed AddOptions: nothing should run; got %d cascade calls", len(c.calls))
	}
}

// ---- 4-step sequence ----

func TestHandle_BothSearchFlags_PushesSeriesSearch(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, Title: "Test", AddOptions: `{"monitor":"all","searchForMissingEpisodes":true,"searchForCutoffUnmetEpisodes":true}`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 1 || c.calls[0].seriesID != 42 || c.calls[0].opts.Monitor != series.MonitorAll {
		t.Errorf("cascade: got %+v, want one call w/ seriesID=42 MonitorAll", c.calls)
	}
	if len(p.calls) != 1 || p.calls[0].name != "SeriesSearch" {
		t.Errorf("push: got %+v, want one SeriesSearch call", p.calls)
	}
	if !strings.Contains(p.calls[0].body, `"seriesId":42`) {
		t.Errorf("push body: %q, want seriesId=42", p.calls[0].body)
	}
	if len(cl.cleared) != 1 || cl.cleared[0] != 42 {
		t.Errorf("clearer: got %v, want [42]", cl.cleared)
	}
}

func TestHandle_OnlyMissingFlag_PushesMissingEpisodeSearch(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, AddOptions: `{"monitor":"missing","searchForMissingEpisodes":true,"searchForCutoffUnmetEpisodes":false}`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 1 || p.calls[0].name != "MissingEpisodeSearch" {
		t.Errorf("push: got %+v, want one MissingEpisodeSearch call", p.calls)
	}
	if !strings.Contains(p.calls[0].body, `"seriesId":42`) {
		t.Errorf("missing body should contain seriesId=42: %q", p.calls[0].body)
	}
}

func TestHandle_OnlyCutoffUnmetFlag_DeferredNoPush(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, AddOptions: `{"monitor":"all","searchForMissingEpisodes":false,"searchForCutoffUnmetEpisodes":true}`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 0 {
		t.Errorf("CutoffUnmet-only DEFERRED: no push should fire; got %+v", p.calls)
	}
	// But Steps 1, 2, 4 should still run.
	if len(c.calls) != 1 {
		t.Errorf("cascade should still run; got %d", len(c.calls))
	}
	if len(cl.cleared) != 1 {
		t.Errorf("clearer should still run; got %v", cl.cleared)
	}
}

func TestHandle_NoSearchFlags_NoPush_OtherStepsStillRun(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, AddOptions: `{"monitor":"all"}`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 0 {
		t.Errorf("no search flags: no push; got %+v", p.calls)
	}
	if len(c.calls) != 1 || len(cl.cleared) != 1 {
		t.Errorf("Steps 1+4 should still run; cascade=%d clear=%v", len(c.calls), cl.cleared)
	}
}

// ---- Error absorption ----

func TestHandle_CascadeErr_DoesNotAbortSequence(t *testing.T) {
	t.Parallel()
	c := &stubCascader{err: errors.New("cascade fail")}
	p, cl := &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, AddOptions: `{"monitor":"all","searchForMissingEpisodes":true,"searchForCutoffUnmetEpisodes":true}`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatalf("cascade err must be absorbed: %v", err)
	}
	// Steps 3 + 4 should still run.
	if len(p.calls) != 1 || len(cl.cleared) != 1 {
		t.Errorf("post-cascade steps must still run; push=%d clear=%v", len(p.calls), cl.cleared)
	}
}

func TestHandle_PusherErr_DoesNotAbortClearer(t *testing.T) {
	t.Parallel()
	c, cl := &stubCascader{}, &stubClearer{}
	p := &stubPusher{err: errors.New("push fail")}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, AddOptions: `{"monitor":"all","searchForMissingEpisodes":true,"searchForCutoffUnmetEpisodes":true}`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatalf("pusher err must be absorbed: %v", err)
	}
	if len(cl.cleared) != 1 {
		t.Errorf("clearer must still fire after pusher err; got %v", cl.cleared)
	}
}

func TestHandle_ClearerErr_DoesNotReturn(t *testing.T) {
	t.Parallel()
	c, p := &stubCascader{}, &stubPusher{}
	cl := &stubClearer{err: errors.New("clear fail")}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, AddOptions: `{"monitor":"all","searchForMissingEpisodes":true,"searchForCutoffUnmetEpisodes":true}`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatalf("clearer err must be absorbed: %v", err)
	}
}

// ---- Scan-skipped event path ----

func TestHandleScanSkipped_RoutesThroughSameLogic(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	svc := mustNewSvc(t, c, p, cl, nil)
	ser := &series.Series{ID: 42, AddOptions: `{"monitor":"all"}`}
	if err := svc.HandleSeriesScanSkipped(t.Context(), mediafiles.SeriesScanSkippedEvent{Series: ser}); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 1 || len(cl.cleared) != 1 {
		t.Errorf("scan-skipped should route through same 4-step sequence; cascade=%d clear=%v", len(c.calls), cl.cleared)
	}
}

// ---- Event publish ----

func TestHandle_PublishesSeriesAddCompletedEvent(t *testing.T) {
	t.Parallel()
	c, p, cl := &stubCascader{}, &stubPusher{}, &stubClearer{}
	bus := messaging.New(nil)
	var got []series.SeriesAddCompletedEvent
	var mu sync.Mutex
	messaging.Subscribe(bus, "test.SeriesAddCompleted",
		func(_ context.Context, ev series.SeriesAddCompletedEvent) error {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
			return nil
		})

	svc := mustNewSvc(t, c, p, cl, bus)
	ser := &series.Series{ID: 42, Title: "Test", AddOptions: `{"monitor":"all"}`}
	if err := svc.HandleSeriesScanned(t.Context(), mediafiles.SeriesScannedEvent{Series: ser}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Series == nil || got[0].Series.ID != 42 {
		t.Errorf("SeriesAddCompletedEvent: got %+v, want one with Series.ID=42", got)
	}
}
