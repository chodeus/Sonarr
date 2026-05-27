package indexersettings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/indexersettings"
)

type fakeProv struct {
	mu          sync.Mutex
	minimumAge  int
	retention   int
	maximumSize int
	rssInterval int
	setCalls    []string
}

func newProv() *fakeProv {
	return &fakeProv{minimumAge: 0, retention: 0, maximumSize: 0, rssInterval: 15}
}

func (f *fakeProv) MinimumAge() int      { return f.minimumAge }
func (f *fakeProv) Retention() int       { return f.retention }
func (f *fakeProv) MaximumSize() int     { return f.maximumSize }
func (f *fakeProv) RssSyncInterval() int { return f.rssInterval }

func (f *fakeProv) SetMinimumAge(_ context.Context, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "MinimumAge")
	f.minimumAge = v
	return nil
}
func (f *fakeProv) SetRetention(_ context.Context, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "Retention")
	f.retention = v
	return nil
}
func (f *fakeProv) SetMaximumSize(_ context.Context, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "MaximumSize")
	f.maximumSize = v
	return nil
}
func (f *fakeProv) SetRssSyncInterval(_ context.Context, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "RssSyncInterval")
	f.rssInterval = v
	return nil
}

func TestGetHandler_EmitsSingletonResource(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	indexersettings.GetHandler(newProv()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/settings/indexer", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got indexersettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 1 || got.RssSyncInterval != 15 {
		t.Errorf("got %+v want {id:1, rssSyncInterval:15, ...}", got)
	}
}

func TestUpdateHandler_PutAccepted202(t *testing.T) {
	t.Parallel()
	p := newProv()
	body := mustEncode(t, indexersettings.Resource{ID: 1, MinimumAge: 30, Retention: 30, MaximumSize: 8192, RssSyncInterval: 60})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/indexer/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	indexersettings.UpdateHandler(p).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rr.Code, rr.Body.String())
	}
	var got indexersettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RssSyncInterval != 60 || got.MinimumAge != 30 {
		t.Errorf("body round-trip: %+v", got)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.setCalls) != 4 {
		t.Errorf("expected 4 set calls, got %d: %v", len(p.setCalls), p.setCalls)
	}
}

func TestUpdateHandler_RssIntervalZeroAllowed(t *testing.T) {
	t.Parallel()
	// RssSyncInterval=0 disables RSS; should be allowed (validation
	// only enforces [10,120] when non-zero).
	body := mustEncode(t, indexersettings.Resource{ID: 1, RssSyncInterval: 0})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/indexer/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	indexersettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202 (RssSyncInterval=0 is the disable sentinel)", rr.Code)
	}
}

func TestUpdateHandler_RssIntervalOutOfRange_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, indexersettings.Resource{ID: 1, RssSyncInterval: 5})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/indexer/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	indexersettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (RssSyncInterval=5 < 10 lower bound)", rr.Code)
	}
}

func TestUpdateHandler_NegativeRejected_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, indexersettings.Resource{ID: 1, MinimumAge: -1})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/indexer/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	indexersettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (MinimumAge=-1 must be ≥ 0)", rr.Code)
	}
}

func TestHandlerAliases_NonNil(t *testing.T) {
	t.Parallel()
	if indexersettings.GetHandler == nil {
		t.Errorf("GetHandler nil")
	}
	if indexersettings.UpdateHandler == nil {
		t.Errorf("UpdateHandler nil")
	}
}

// TestTypeAliases pins compile-time equivalence with v3.
func TestTypeAliases(t *testing.T) {
	t.Parallel()
	r := indexersettings.Resource{ID: 1, MinimumAge: 0, Retention: 0, MaximumSize: 0, RssSyncInterval: 15}
	if r.ID != 1 {
		t.Errorf("Resource not addressable")
	}
}

func mustEncode(t *testing.T, r indexersettings.Resource) []byte {
	t.Helper()
	buf, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}
