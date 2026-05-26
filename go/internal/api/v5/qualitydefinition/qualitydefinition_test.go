package qualitydefinition

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
)

// fakeLister implements Lister.
type fakeLister struct {
	rows []qualitydefinitions.Decorated
}

func (f *fakeLister) All(_ context.Context) ([]qualitydefinitions.Decorated, error) {
	out := make([]qualitydefinitions.Decorated, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

// fakeWriter implements Writer.
type fakeWriter struct {
	rows    []qualitydefinitions.Decorated
	updates atomic.Int64
}

func (f *fakeWriter) GetByID(_ context.Context, id int) (*qualitydefinitions.Decorated, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, qualitydefinitions.ErrNotFound
}

func (f *fakeWriter) Update(_ context.Context, def *qualitydefinitions.Definition) error {
	f.updates.Add(1)
	for i := range f.rows {
		if f.rows[i].ID == def.ID {
			f.rows[i].Title = def.Title
			return nil
		}
	}
	return qualitydefinitions.ErrNotFound
}

// ----- GET list — v5 Resource omits size fields -----

func TestListHandler_ResourceShapeOmitsSizeFields(t *testing.T) {
	t.Parallel()
	// The canonical v5 vs v3 divergence guard: v3 has minSize/maxSize/
	// preferredSize on the wire; v5 doesn't.
	lister := &fakeLister{rows: []qualitydefinitions.Decorated{
		{ID: 1, Title: "SDTV", Quality: quality.Quality{ID: 1, Name: "SDTV", Source: quality.SourceTelevision, Resolution: 480}, Weight: 1},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/qualitydefinition", nil)
	ListHandler(lister).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	// Pin the absence of size fields.
	for _, banned := range []string{`"minSize"`, `"maxSize"`, `"preferredSize"`} {
		if strings.Contains(body, banned) {
			t.Errorf("v5 body MUST NOT contain %s; got %q", banned, body)
		}
	}
	var got []Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Quality.Name != "SDTV" {
		t.Errorf("got %+v", got)
	}
}

// ----- GET /{id} -----

func TestByIDHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []qualitydefinitions.Decorated{
		{ID: 5, Title: "HDTV-720p", Quality: quality.Quality{ID: 5, Name: "HDTV-720p", Source: quality.SourceTelevision, Resolution: 720}, Weight: 5},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/qualitydefinition/5", nil)
	req.SetPathValue("id", "5")
	ByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"name":"HDTV-720p"`) {
		t.Errorf("body should contain quality name: %s", rr.Body.String())
	}
}

func TestByIDHandler_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/qualitydefinition/999", nil)
	req.SetPathValue("id", "999")
	ByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// ----- PUT /{id} — v5 returns 202 (same as v3) but with reduced body -----

func TestUpdateHandler_Success_Returns202(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []qualitydefinitions.Decorated{
		{ID: 1, Title: "Old", Quality: quality.Quality{ID: 1, Name: "SDTV"}, Weight: 1},
	}}
	body := Resource{ID: 1, Quality: QualityResource{ID: 1, Name: "SDTV"}, Title: "Updated Title", Weight: 1}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/qualitydefinition/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	UpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%q", rr.Code, rr.Body.String())
	}
	if w.updates.Load() != 1 {
		t.Errorf("svc.Update should be called once; got %d", w.updates.Load())
	}
	// Verify body shape (no size fields).
	if strings.Contains(rr.Body.String(), `"minSize"`) {
		t.Errorf("response body MUST NOT include minSize; got %q", rr.Body.String())
	}
}

func TestUpdateHandler_EmptyTitle_Returns400(t *testing.T) {
	t.Parallel()
	body := Resource{ID: 1, Title: ""}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/qualitydefinition/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	UpdateHandler(&fakeWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- PUT / (UpdateMany) — v5 returns 200 (v3 returns 202) -----

func TestUpdateManyHandler_Success_Returns200(t *testing.T) {
	t.Parallel()
	// Custom writer that also satisfies Lister (returns same rows post-update).
	type combo struct {
		fakeWriter
	}
	w := &combo{fakeWriter: fakeWriter{rows: []qualitydefinitions.Decorated{
		{ID: 1, Title: "A", Quality: quality.Quality{ID: 1, Name: "SDTV"}, Weight: 1},
		{ID: 2, Title: "B", Quality: quality.Quality{ID: 2, Name: "WEBRip-480p"}, Weight: 2},
	}}}
	body := []Resource{
		{ID: 1, Quality: QualityResource{ID: 1}, Title: "A-new", Weight: 1},
		{ID: 2, Quality: QualityResource{ID: 2}, Title: "B-new", Weight: 2},
	}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/qualitydefinition", bytes.NewReader(buf))
	UpdateManyHandler(&w.fakeWriter, &fakeLister{rows: w.fakeWriter.rows}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (v5 demotes v3's 202); body=%q", rr.Code, rr.Body.String())
	}
	if w.fakeWriter.updates.Load() != 2 {
		t.Errorf("svc.Update should be called twice; got %d", w.fakeWriter.updates.Load())
	}
}

func TestUpdateManyHandler_MalformedBody_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/qualitydefinition", bytes.NewReader([]byte(`not json`)))
	UpdateManyHandler(&fakeWriter{}, &fakeLister{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}
