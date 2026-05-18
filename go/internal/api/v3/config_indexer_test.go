package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeIndexerConfig struct {
	min, retention, max, rss int
}

func (f *fakeIndexerConfig) MinimumAge() int      { return f.min }
func (f *fakeIndexerConfig) Retention() int       { return f.retention }
func (f *fakeIndexerConfig) MaximumSize() int     { return f.max }
func (f *fakeIndexerConfig) RssSyncInterval() int { return f.rss }

func TestIndexerConfigHandler_Defaults(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerConfigHandler(&fakeIndexerConfig{rss: 15}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/indexer", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got IndexerConfigResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := IndexerConfigResource{ID: 1, RssSyncInterval: 15}
	if got != want {
		t.Errorf("body: got %+v want %+v", got, want)
	}
}

func TestIndexerConfigHandler_AllSet(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerConfigHandler(&fakeIndexerConfig{min: 30, retention: 1000, max: 50, rss: 60}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/indexer", nil))
	var got IndexerConfigResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	want := IndexerConfigResource{ID: 1, MinimumAge: 30, Retention: 1000, MaximumSize: 50, RssSyncInterval: 60}
	if got != want {
		t.Errorf("body: got %+v want %+v", got, want)
	}
}

// ---------- 5.B13: PUT tests ----------

type fakeIndexerConfigWriter struct {
	*fakeIndexerConfig
	setMinErr, setRetErr, setMaxErr, setRssErr error
	setMin, setRet, setMax, setRss             *int
}

func (f *fakeIndexerConfigWriter) SetMinimumAge(_ context.Context, v int) error {
	if f.setMinErr != nil {
		return f.setMinErr
	}
	x := v
	f.setMin = &x
	f.min = v
	return nil
}
func (f *fakeIndexerConfigWriter) SetRetention(_ context.Context, v int) error {
	if f.setRetErr != nil {
		return f.setRetErr
	}
	x := v
	f.setRet = &x
	f.retention = v
	return nil
}
func (f *fakeIndexerConfigWriter) SetMaximumSize(_ context.Context, v int) error {
	if f.setMaxErr != nil {
		return f.setMaxErr
	}
	x := v
	f.setMax = &x
	f.max = v
	return nil
}
func (f *fakeIndexerConfigWriter) SetRssSyncInterval(_ context.Context, v int) error {
	if f.setRssErr != nil {
		return f.setRssErr
	}
	x := v
	f.setRss = &x
	f.rss = v
	return nil
}

func TestIndexerConfigUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeIndexerConfigWriter{fakeIndexerConfig: &fakeIndexerConfig{}}
	rr := httptest.NewRecorder()
	body := `{"id":1,"minimumAge":15,"retention":1000,"maximumSize":50,"rssSyncInterval":60}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/indexer/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	IndexerConfigUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.setMin == nil || *w.setMin != 15 {
		t.Errorf("SetMinimumAge: %v", w.setMin)
	}
	if w.setRet == nil || *w.setRet != 1000 {
		t.Errorf("SetRetention: %v", w.setRet)
	}
	if w.setMax == nil || *w.setMax != 50 {
		t.Errorf("SetMaximumSize: %v", w.setMax)
	}
	if w.setRss == nil || *w.setRss != 60 {
		t.Errorf("SetRssSyncInterval: %v", w.setRss)
	}
}

func TestIndexerConfigUpdateHandler_NegativeRetention400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"id":1,"minimumAge":0,"retention":-1,"maximumSize":0,"rssSyncInterval":0}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/indexer/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	IndexerConfigUpdateHandler(&fakeIndexerConfigWriter{fakeIndexerConfig: &fakeIndexerConfig{}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Retention"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestIndexerConfigUpdateHandler_RssSyncIntervalOutOfRange400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"id":1,"minimumAge":0,"retention":0,"maximumSize":0,"rssSyncInterval":5}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/indexer/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	IndexerConfigUpdateHandler(&fakeIndexerConfigWriter{fakeIndexerConfig: &fakeIndexerConfig{}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"RssSyncInterval"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestIndexerConfigUpdateHandler_RssSyncIntervalZeroSucceeds(t *testing.T) {
	t.Parallel()
	// 0 disables RSS — must be allowed despite being below the [10,120] range.
	w := &fakeIndexerConfigWriter{fakeIndexerConfig: &fakeIndexerConfig{}}
	rr := httptest.NewRecorder()
	body := `{"id":1,"minimumAge":0,"retention":0,"maximumSize":0,"rssSyncInterval":0}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/indexer/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	IndexerConfigUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
}
