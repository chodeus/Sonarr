package seriesimport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/seriesimport"
	"sonarr.tv/sonarr/internal/series"
)

type fakeWriter struct {
	mu      sync.Mutex
	rows    []series.Series
	nextID  int
	insertE error
}

func (f *fakeWriter) GetSeries(context.Context, int) (*series.Series, error) {
	return nil, errors.New("not used in import")
}
func (f *fakeWriter) GetAllSeries(context.Context) ([]series.Series, error) {
	return nil, nil
}
func (f *fakeWriter) Insert(_ context.Context, s *series.Series) (*series.Series, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertE != nil {
		return nil, f.insertE
	}
	f.nextID++
	s.ID = f.nextID
	f.rows = append(f.rows, *s)
	return s, nil
}
func (f *fakeWriter) Update(context.Context, *series.Series) error { return nil }
func (f *fakeWriter) Delete(context.Context, int) error            { return nil }
func (f *fakeWriter) insertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

type fakeEmitter struct {
	mu      sync.Mutex
	created []apiv3.SeriesResource
}

func (f *fakeEmitter) Created(_ context.Context, r apiv3.SeriesResource) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, r)
	return nil
}
func (f *fakeEmitter) Updated(context.Context, apiv3.SeriesResource) error { return nil }
func (f *fakeEmitter) Deleted(context.Context, apiv3.SeriesResource) error { return nil }
func (f *fakeEmitter) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

func validRow() apiv3.SeriesResource {
	return apiv3.SeriesResource{
		Title:            "Breaking Bad",
		Path:             "/data/Breaking Bad",
		QualityProfileID: 1,
		TvdbID:           81189,
		TitleSlug:        "breaking-bad",
	}
}

func TestPostHandler_EmptyArray_Returns200(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	seriesimport.PostHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/series/import", bytes.NewReader([]byte(`[]`))))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want []", rr.Body.String())
	}
	if w.insertCount() != 0 {
		t.Errorf("Insert should not be called on empty input")
	}
}

func TestPostHandler_NonEmpty_DemotesCreated201To200(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	body, _ := json.Marshal([]apiv3.SeriesResource{validRow()})
	rr := httptest.NewRecorder()
	seriesimport.PostHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/series/import", bytes.NewReader(body)))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (v5 demotes v3's 201); body=%s", rr.Code, rr.Body.String())
	}
	if w.insertCount() != 1 {
		t.Errorf("expected 1 Insert call, got %d", w.insertCount())
	}
	if em.createdCount() != 1 {
		t.Errorf("expected 1 emitter.Created call, got %d", em.createdCount())
	}
	var out []apiv3.SeriesResource
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].ID <= 0 || out[0].Title != "Breaking Bad" {
		t.Errorf("body shape: %+v", out)
	}
}

func TestPostHandler_MultiRow_DemotesAndPersistsAll(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	row1 := validRow()
	row2 := validRow()
	row2.Title = "Better Call Saul"
	row2.Path = "/data/Better Call Saul"
	row2.TvdbID = 273181
	row2.TitleSlug = "better-call-saul"
	body, _ := json.Marshal([]apiv3.SeriesResource{row1, row2})
	rr := httptest.NewRecorder()
	seriesimport.PostHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/series/import", bytes.NewReader(body)))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if w.insertCount() != 2 {
		t.Errorf("expected 2 Insert calls, got %d", w.insertCount())
	}
}

func TestPostHandler_InvalidRow_PassesValidationErrorThrough400(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	bad := apiv3.SeriesResource{Title: "", Path: "", QualityProfileID: 0}
	body, _ := json.Marshal([]apiv3.SeriesResource{bad})
	rr := httptest.NewRecorder()
	seriesimport.PostHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/series/import", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (wrapper must NOT swallow 400)", rr.Code)
	}
	if w.insertCount() != 0 {
		t.Errorf("Insert should not run when validation fails")
	}
}

func TestPostHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	rr := httptest.NewRecorder()
	seriesimport.PostHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/series/import", bytes.NewReader([]byte(`{not-an-array`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}
