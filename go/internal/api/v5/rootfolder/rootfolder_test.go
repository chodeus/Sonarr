package rootfolder

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/rootfolders"
)

// fakeLister implements Lister.
type fakeLister struct {
	rows []rootfolders.Folder
	err  error
}

func (f *fakeLister) AllWithUnmappedFolders(_ context.Context) ([]rootfolders.Folder, error) {
	return f.rows, f.err
}

// fakeWriter implements Writer.
type fakeWriter struct {
	rows    []rootfolders.Folder
	addErr  error
	added   atomic.Int64
	removes atomic.Int64
}

func (f *fakeWriter) Get(_ context.Context, id int, _ bool) (*rootfolders.Folder, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, rootfolders.ErrNotFound
}

func (f *fakeWriter) Add(_ context.Context, rf *rootfolders.RootFolder) (*rootfolders.Folder, error) {
	f.added.Add(1)
	if f.addErr != nil {
		return nil, f.addErr
	}
	folder := rootfolders.Folder{
		ID:      len(f.rows) + 1,
		Path:    rf.Path,
		IsEmpty: true,
	}
	f.rows = append(f.rows, folder)
	cp := folder
	return &cp, nil
}

func (f *fakeWriter) Remove(_ context.Context, id int) error {
	f.removes.Add(1)
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return rootfolders.ErrNotFound
}

// ----- GET list — pins v5-specific IsEmpty field -----

func TestListHandler_EmitsIsEmptyField(t *testing.T) {
	t.Parallel()
	// The canonical v5 vs v3 divergence guard: v3 omits IsEmpty;
	// v5 must include it.
	lister := &fakeLister{rows: []rootfolders.Folder{
		{ID: 1, Path: "/tv", Accessible: true, IsEmpty: false},
		{ID: 2, Path: "/movies", Accessible: true, IsEmpty: true},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/rootfolder", nil)
	ListHandler(lister).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"isEmpty":false`) {
		t.Errorf("body should contain `\"isEmpty\":false` for row 1; got %q", body)
	}
	if !strings.Contains(body, `"isEmpty":true`) {
		t.Errorf("body should contain `\"isEmpty\":true` for row 2; got %q", body)
	}
	var got []Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows: got %d, want 2", len(got))
	}
	if got[0].IsEmpty != false || got[1].IsEmpty != true {
		t.Errorf("IsEmpty round-trip: got [%v, %v]", got[0].IsEmpty, got[1].IsEmpty)
	}
}

// ----- GET /{id} — pins v5-specific IsEmpty field -----

func TestByIDHandler_EmitsIsEmptyField(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []rootfolders.Folder{{ID: 7, Path: "/x", IsEmpty: true}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/rootfolder/7", nil)
	req.SetPathValue("id", "7")
	ByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"isEmpty":true`) {
		t.Errorf("body should contain `\"isEmpty\":true`; got %q", rr.Body.String())
	}
}

func TestByIDHandler_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/rootfolder/999", nil)
	req.SetPathValue("id", "999")
	ByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// ----- POST — pins v5-specific IsEmpty in response body -----

func TestCreateHandler_Success_Returns201WithIsEmpty(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	body := strings.NewReader(`{"path":"/tv"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/rootfolder", body)
	CreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201; body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"isEmpty":true`) {
		t.Errorf("v5 response must include `isEmpty` field; got body=%q", rr.Body.String())
	}
	if w.added.Load() != 1 {
		t.Errorf("svc.Add should be called once; got %d", w.added.Load())
	}
}

func TestCreateHandler_EmptyPath_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/rootfolder", strings.NewReader(`{"path":""}`))
	CreateHandler(&fakeWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- DELETE — v5 returns 204 (v3 returns 200 + empty) -----

func TestDeleteHandler_Success_Returns204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []rootfolders.Folder{{ID: 1, Path: "/tv"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/rootfolder/1", nil)
	req.SetPathValue("id", "1")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if w.removes.Load() != 1 {
		t.Errorf("svc.Remove should be called once; got %d", w.removes.Load())
	}
}

func TestDeleteHandler_MissingID_Returns204Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/rootfolder/999", nil)
	req.SetPathValue("id", "999")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (idempotent missing-id)", rr.Code)
	}
}

// ----- empty body sanity -----

func TestCreateHandler_MalformedBody_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/rootfolder", bytes.NewReader([]byte(`{not json`)))
	CreateHandler(&fakeWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}
