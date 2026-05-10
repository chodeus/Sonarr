package httpclient

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// fakeNow is a tiny clock for deterministic host-status tests.
type fakeNow struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeNow) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeNow) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func newTestHostStatus() (*hostStatusTracker, *fakeNow) {
	clk := &fakeNow{t: time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)}
	t := &hostStatusTracker{
		entries: map[string]*hostEntry{},
		now:     clk.Now,
		periods: HostBackoffPeriods,
	}
	return t, clk
}

func TestHostStatus_AllowsByDefault(t *testing.T) {
	hs, _ := newTestHostStatus()
	allowed, retry := hs.Allow("example.com")
	if !allowed || retry != 0 {
		t.Errorf("default state: allowed=%v retry=%v", allowed, retry)
	}
}

func TestHostStatus_FailureEscalatesLadder(t *testing.T) {
	hs, clk := newTestHostStatus()

	// Level 1 cooldown after first failure: 5s.
	hs.Record("example.com", false)
	allowed, retry := hs.Allow("example.com")
	if allowed {
		t.Fatalf("expected blocked, got allowed; retry=%v", retry)
	}
	if retry != HostBackoffPeriods[1] {
		t.Errorf("level 1 cooldown = %v want %v", retry, HostBackoffPeriods[1])
	}

	// Advance past level 1 — should allow again (level retained for next escalation).
	clk.Advance(HostBackoffPeriods[1] + time.Millisecond)
	allowed, _ = hs.Allow("example.com")
	if !allowed {
		t.Errorf("expected allowed after cooldown elapsed")
	}

	// Another failure → level 2 (30s).
	hs.Record("example.com", false)
	_, retry = hs.Allow("example.com")
	if retry != HostBackoffPeriods[2] {
		t.Errorf("level 2 cooldown = %v want %v", retry, HostBackoffPeriods[2])
	}
}

func TestHostStatus_SuccessResetsLevel(t *testing.T) {
	hs, _ := newTestHostStatus()
	// Drive to level 3.
	hs.Record("example.com", false)
	hs.Record("example.com", false)
	hs.Record("example.com", false)

	// Success drops it back to level 0.
	hs.Record("example.com", true)
	allowed, retry := hs.Allow("example.com")
	if !allowed || retry != 0 {
		t.Errorf("after reset: allowed=%v retry=%v", allowed, retry)
	}

	// Next failure starts again from level 1.
	hs.Record("example.com", false)
	_, retry = hs.Allow("example.com")
	if retry != HostBackoffPeriods[1] {
		t.Errorf("post-reset level 1 = %v want %v", retry, HostBackoffPeriods[1])
	}
}

func TestHostStatus_LadderCapsAtMax(t *testing.T) {
	hs, _ := newTestHostStatus()
	// Force a deeper escalation than the table has entries.
	for i := 0; i < len(HostBackoffPeriods)+5; i++ {
		hs.Record("example.com", false)
	}
	_, retry := hs.Allow("example.com")
	last := HostBackoffPeriods[len(HostBackoffPeriods)-1]
	if retry != last {
		t.Errorf("capped cooldown = %v want %v", retry, last)
	}
}

func TestHostStatus_DifferentHostsIndependent(t *testing.T) {
	hs, _ := newTestHostStatus()
	hs.Record("a.example", false)
	hs.Record("a.example", false)

	if allowed, _ := hs.Allow("a.example"); allowed {
		t.Error("a.example should be cooled down")
	}
	if allowed, _ := hs.Allow("b.example"); !allowed {
		t.Error("b.example should be unaffected")
	}
}

// ---- Client integration ----

func TestClient_HostStatus_ShortCircuitsOnCooldown(t *testing.T) {
	hs, _ := newTestHostStatus()
	hs.Record("example.com", false) // cool down host
	stub := &stubDispatcher{}       // no steps configured — must NOT be called
	c, err := NewClientWithOptions(ClientOptions{Dispatcher: stub, HostStatusTracker: hs})
	if err != nil {
		t.Fatal(err)
	}

	req, _ := NewRequest("https://example.com/x")
	_, err = c.Execute(context.Background(), req)
	var blocked *HostBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected *HostBlockedError, got %v", err)
	}
	if blocked.Host != "example.com" {
		t.Errorf("blocked.Host = %q", blocked.Host)
	}
	if len(stub.calls) != 0 {
		t.Errorf("dispatcher was called despite cooldown (calls=%d)", len(stub.calls))
	}
}

func TestClient_HostStatus_RecordsTransportError(t *testing.T) {
	hs, _ := newTestHostStatus()
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) {
			return nil, errors.New("dial: connection refused")
		},
	}}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub, HostStatusTracker: hs})

	req, _ := NewRequest("https://example.com/")
	if _, err := c.Execute(context.Background(), req); err == nil {
		t.Fatal("expected dispatcher error")
	}
	// Tracker should have escalated to level 1.
	allowed, retry := hs.Allow("example.com")
	if allowed {
		t.Errorf("expected cooldown after transport error")
	}
	if retry != HostBackoffPeriods[1] {
		t.Errorf("retry = %v want %v", retry, HostBackoffPeriods[1])
	}
}

func TestClient_HostStatus_Records5xx(t *testing.T) {
	hs, _ := newTestHostStatus()
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, http.StatusBadGateway, nil, nil), nil },
	}}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub, HostStatusTracker: hs})

	req, _ := NewRequest("https://example.com/")
	req.SuppressHTTPError = true // we want the response back, not an HTTPError
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := hs.Allow("example.com"); allowed {
		t.Error("expected cooldown after 5xx response")
	}
}

func TestClient_HostStatus_DoesNotRecord4xx(t *testing.T) {
	// 4xx is request-side, not host-side — must NOT trip the breaker.
	hs, _ := newTestHostStatus()
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, http.StatusNotFound, nil, nil), nil },
	}}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub, HostStatusTracker: hs})

	req, _ := NewRequest("https://example.com/")
	req.SuppressHTTPError = true
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := hs.Allow("example.com"); !allowed {
		t.Error("4xx should not trip host status")
	}
}

func TestClient_HostStatus_2xxResetsLevel(t *testing.T) {
	hs, _ := newTestHostStatus()
	hs.Record("example.com", false) // pre-existing cooldown
	hs.Record("example.com", false) // level 2

	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, 200, nil, nil), nil },
	}}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub, HostStatusTracker: hs})

	// First Allow blocks (level 2 cooldown active).
	req, _ := NewRequest("https://example.com/")
	if _, err := c.Execute(context.Background(), req); err == nil {
		t.Fatal("expected HostBlockedError before cooldown elapsed")
	}

	// Bypass cooldown by clearing entries via successful Record.
	hs.Record("example.com", true)
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatalf("expected success after manual reset, got %v", err)
	}
	// Tracker should still be clear.
	if allowed, _ := hs.Allow("example.com"); !allowed {
		t.Error("post-success: expected allowed")
	}
}

func TestClient_HostStatus_NilTrackerNoOp(t *testing.T) {
	stub := &stubDispatcher{steps: []func(req *Request) (*Response, error){
		func(req *Request) (*Response, error) { return mkResp(req, 500, nil, nil), nil },
	}}
	c, _ := NewClientWithOptions(ClientOptions{Dispatcher: stub})

	req, _ := NewRequest("https://example.com/")
	req.SuppressHTTPError = true
	if _, err := c.Execute(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No tracker → no panic, no observable state.
}
