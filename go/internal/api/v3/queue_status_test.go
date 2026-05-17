package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type fakeQueueLister struct{ rows []queue.Queue }

func (f *fakeQueueLister) GetQueue() []queue.Queue { return f.rows }

type fakePending struct {
	n   int
	err error
}

func (f *fakePending) PendingCount(context.Context) (int, error) { return f.n, f.err }

func TestQueueStatusHandler_EmptyAllZero(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueStatusHandler(&fakeQueueLister{rows: nil}, &fakePending{n: 0}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got QueueStatusResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := QueueStatusResource{}
	if got != want {
		t.Errorf("empty status: got %+v want %+v", got, want)
	}
}

func TestQueueStatusHandler_NilPendingTolerated(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueStatusHandler(&fakeQueueLister{}, nil, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
}

func TestQueueStatusHandler_TalliesByStatus(t *testing.T) {
	t.Parallel()
	warn := trackeddownloads.StatusWarning
	err := trackeddownloads.StatusError
	ok := trackeddownloads.StatusOK
	rows := []queue.Queue{
		{Series: &series.Series{ID: 1}, TrackedDownloadStatus: &ok},  // known, ok
		{Series: &series.Series{ID: 2}, TrackedDownloadStatus: &err}, // known, error
		{Series: nil, TrackedDownloadStatus: &warn},                  // unknown, warning
		{Series: nil, TrackedDownloadStatus: nil},                    // unknown, ok (no status)
	}
	rr := httptest.NewRecorder()
	QueueStatusHandler(&fakeQueueLister{rows: rows}, &fakePending{n: 3}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/status", nil))
	var got QueueStatusResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	want := QueueStatusResource{
		TotalCount:      7,    // 4 queue + 3 pending
		Count:           5,    // 2 known + 3 pending
		UnknownCount:    2,    // 2 nil-series rows
		Errors:          true, // known-error row exists
		Warnings:        false,
		UnknownErrors:   false,
		UnknownWarnings: true,
	}
	if got != want {
		t.Errorf("tally: got %+v want %+v", got, want)
	}
}

// recordingPauser counts Pause/Resume calls + records their order
// (1 for Pause, 2 for Resume) so the test can assert the GET handler
// brackets the read with Pause-before, Resume-after.
type recordingPauser struct {
	pause  int
	resume int
	order  []int
}

func (p *recordingPauser) Pause()  { p.pause++; p.order = append(p.order, 1) }
func (p *recordingPauser) Resume() { p.resume++; p.order = append(p.order, 2) }

func TestQueueStatusHandler_PauserBracketsRead(t *testing.T) {
	t.Parallel()
	p := &recordingPauser{}
	rr := httptest.NewRecorder()
	QueueStatusHandler(&fakeQueueLister{}, &fakePending{n: 0}, p).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if p.pause != 1 || p.resume != 1 {
		t.Errorf("pauser counts: pause=%d resume=%d want 1/1", p.pause, p.resume)
	}
	// Pause must come before Resume — the read sits inside the
	// bracket so a producer that fires mid-read can't re-arm the
	// debouncer's 5s timer with the snapshot the GET already
	// covers.
	if len(p.order) != 2 || p.order[0] != 1 || p.order[1] != 2 {
		t.Errorf("pause/resume order: %v want [1,2]", p.order)
	}
}

func TestQueueStatusHandler_PendingErrorFallsBackToZero(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueStatusHandler(&fakeQueueLister{}, &fakePending{n: 0, err: context.Canceled}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("pending error should still 200; got %d", rr.Code)
	}
	var got QueueStatusResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.TotalCount != 0 || got.Count != 0 {
		t.Errorf("counts after pending error: %+v", got)
	}
}
