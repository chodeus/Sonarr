package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeLifecycle is a recorder for the lifecycle calls. waitFor blocks
// until callsMade reaches the expected count or the timeout fires.
type fakeLifecycle struct {
	mu       sync.Mutex
	shutdown int
	restart  int
	err      error
}

func (f *fakeLifecycle) Shutdown(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdown++
	return f.err
}

func (f *fakeLifecycle) Restart(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restart++
	return f.err
}

func (f *fakeLifecycle) shutdownCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdown
}

func (f *fakeLifecycle) restartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restart
}

func waitFor(t *testing.T, get func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if get() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waitFor: never reached %d (last seen %d)", want, get())
}

func TestSystemShutdownHandler_RespondsImmediatelyAndDispatches(t *testing.T) {
	t.Parallel()
	lc := &fakeLifecycle{}
	rr := httptest.NewRecorder()
	SystemShutdownHandler(lc).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/system/shutdown", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got struct {
		ShuttingDown bool `json:"shuttingDown"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.ShuttingDown {
		t.Errorf("expected shuttingDown=true; got %+v", got)
	}
	waitFor(t, lc.shutdownCount, 1)
}

func TestSystemRestartHandler_RespondsImmediatelyAndDispatches(t *testing.T) {
	t.Parallel()
	lc := &fakeLifecycle{}
	rr := httptest.NewRecorder()
	SystemRestartHandler(lc).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/system/restart", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got struct {
		Restarting bool `json:"restarting"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Restarting {
		t.Errorf("expected restarting=true; got %+v", got)
	}
	waitFor(t, lc.restartCount, 1)
}

// Errors from the lifecycle service must not affect the HTTP response
// (the response is sent BEFORE the goroutine runs). The handler logs
// the failure via the request-scoped logger.
func TestSystemShutdownHandler_ServiceErrorDoesNotChangeResponse(t *testing.T) {
	t.Parallel()
	lc := &fakeLifecycle{err: errors.New("boom")}
	rr := httptest.NewRecorder()
	SystemShutdownHandler(lc).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/system/shutdown", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	waitFor(t, lc.shutdownCount, 1)
}
