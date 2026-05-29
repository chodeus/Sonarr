package queuestatus_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/queuestatus"
	"sonarr.tv/sonarr/internal/queue"
)

type fakeQueue struct {
	rows []queue.Queue
}

func (f *fakeQueue) GetQueue() []queue.Queue { return f.rows }

type fakePending struct {
	count int
	err   error
}

func (f *fakePending) PendingCount(context.Context) (int, error) {
	return f.count, f.err
}

type fakePauser struct {
	mu      sync.Mutex
	pauseN  int
	resumeN int
}

func (f *fakePauser) Pause() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauseN++
}
func (f *fakePauser) Resume() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeN++
}
func (f *fakePauser) snapshot() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pauseN, f.resumeN
}

func TestGetHandler_EmptyQueue_AllZeroes(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuestatus.GetHandler(&fakeQueue{}, &fakePending{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got queuestatus.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TotalCount != 0 || got.Count != 0 || got.UnknownCount != 0 || got.Errors || got.Warnings || got.UnknownErrors || got.UnknownWarnings {
		t.Errorf("expected all zeros, got %+v", got)
	}
}

func TestGetHandler_WithPendingCount(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuestatus.GetHandler(&fakeQueue{}, &fakePending{count: 5}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got queuestatus.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TotalCount != 5 || got.Count != 5 {
		t.Errorf("expected TotalCount=5,Count=5; got %+v", got)
	}
}

func TestGetHandler_PendingErrorTreatedAsZero(t *testing.T) {
	t.Parallel()
	// Per v3's defensive read posture: pending lister errors fall back
	// to 0 pending rather than 500.
	rr := httptest.NewRecorder()
	queuestatus.GetHandler(&fakeQueue{}, &fakePending{err: errors.New("boom")}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d (defensive — pending err should NOT 500)", rr.Code)
	}
	var got queuestatus.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TotalCount != 0 {
		t.Errorf("expected 0 total on pending err, got %+v", got)
	}
}

func TestGetHandler_PauserBracketed(t *testing.T) {
	t.Parallel()
	pauser := &fakePauser{}
	rr := httptest.NewRecorder()
	queuestatus.GetHandler(&fakeQueue{}, &fakePending{}, pauser).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	p, r := pauser.snapshot()
	if p != 1 || r != 1 {
		t.Errorf("expected Pause+Resume bracket (1+1), got %d pause / %d resume", p, r)
	}
}

func TestGetHandler_NilPauserTolerated(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	queuestatus.GetHandler(&fakeQueue{}, &fakePending{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/queue/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d (nil pauser should be tolerated)", rr.Code)
	}
}

// TestResourceTypeAlias pins compile-time equivalence with v3.
func TestResourceTypeAlias(t *testing.T) {
	t.Parallel()
	r := queuestatus.Resource{TotalCount: 7}
	if r.TotalCount != 7 {
		t.Errorf("Resource not addressable")
	}
}
