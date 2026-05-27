package episode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/episode"
	"sonarr.tv/sonarr/internal/episodes"
)

type fakeWriter struct {
	mu              sync.Mutex
	rows            map[int]episodes.Episode
	setEpisodeCount int
	setBatchCount   int
}

func (f *fakeWriter) GetEpisodesBySeries(_ context.Context, _ int) ([]episodes.Episode, error) {
	return nil, nil
}
func (f *fakeWriter) GetEpisodes(_ context.Context, ids []int) ([]episodes.Episode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]episodes.Episode, 0, len(ids))
	for _, id := range ids {
		if e, ok := f.rows[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeWriter) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.rows[id]; ok {
		return &e, nil
	}
	return nil, episodes.ErrNotFound
}
func (f *fakeWriter) SetEpisodeMonitored(_ context.Context, id int, monitored bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setEpisodeCount++
	if e, ok := f.rows[id]; ok {
		e.Monitored = monitored
		f.rows[id] = e
	}
	return nil
}
func (f *fakeWriter) SetMonitored(_ context.Context, ids []int, monitored bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setBatchCount++
	for _, id := range ids {
		if e, ok := f.rows[id]; ok {
			e.Monitored = monitored
			f.rows[id] = e
		}
	}
	return nil
}

func (f *fakeWriter) setOneCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setEpisodeCount
}

func newWriter(eps ...episodes.Episode) *fakeWriter {
	w := &fakeWriter{rows: make(map[int]episodes.Episode, len(eps))}
	for _, e := range eps {
		w.rows[e.ID] = e
	}
	return w
}

func TestListHandlerAlias_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	episode.ListHandler(newWriter()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/episode?seriesId=1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestByIDHandlerAlias_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/episode/999", nil)
	req.SetPathValue("id", "999")
	episode.ByIDHandler(newWriter()).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestMonitorHandler_DemotesAcceptedTo200(t *testing.T) {
	t.Parallel()
	w := newWriter(episodes.Episode{ID: 1, Title: "ep1"})
	body := []byte(`{"episodeIds":[1],"monitored":true}`)
	rr := httptest.NewRecorder()
	episode.MonitorHandler(w).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v5/episode/monitor", bytes.NewReader(body)))

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (v5 demotes v3's 202)", rr.Code)
	}
	if w.setOneCount() != 1 {
		t.Errorf("expected SetEpisodeMonitored called once, got %d", w.setOneCount())
	}
}

func TestMonitorHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	episode.MonitorHandler(newWriter()).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v5/episode/monitor", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (wrapper passes 400 through)", rr.Code)
	}
}

func TestByIDPutHandler_Success_200(t *testing.T) {
	t.Parallel()
	w := newWriter(episodes.Episode{ID: 7, Title: "ep7", Monitored: false})
	body := []byte(`{"id":7,"monitored":true,"seriesId":0,"seasonNumber":0,"episodeNumber":0,"airDate":"","runtime":0,"finaleType":"","overview":"","unverifiedSceneNumbering":false}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/episode/7", bytes.NewReader(body))
	req.SetPathValue("id", "7")
	episode.ByIDPutHandler(w).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.setOneCount() != 1 {
		t.Errorf("expected SetEpisodeMonitored called once, got %d", w.setOneCount())
	}
	var got apiv3.EpisodeResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 7 || !got.Monitored {
		t.Errorf("body shape: %+v (expected ID=7, Monitored=true)", got)
	}
}

func TestByIDPutHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/episode/abc", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", "abc")
	episode.ByIDPutHandler(newWriter()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestByIDPutHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/episode/7", bytes.NewReader([]byte(`{bad`)))
	req.SetPathValue("id", "7")
	episode.ByIDPutHandler(newWriter()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestByIDPutHandler_MissingAfterUpdate_404(t *testing.T) {
	t.Parallel()
	// Episode doesn't exist; SetEpisodeMonitored no-ops in the fake,
	// then GetEpisodes returns empty → 404.
	w := newWriter()
	body := []byte(`{"id":999,"monitored":true}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/episode/999", bytes.NewReader(body))
	req.SetPathValue("id", "999")
	episode.ByIDPutHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}
