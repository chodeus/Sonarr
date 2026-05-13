package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
