package importlistexclusion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/importlistexclusion"
	"sonarr.tv/sonarr/internal/importlistexclusions"
)

type fakeStore struct {
	mu       sync.Mutex
	rows     map[int]importlistexclusions.Exclusion
	nextID   int
	deleted  []int
	deleteEr error
}

func newStore(rows ...importlistexclusions.Exclusion) *fakeStore {
	s := &fakeStore{rows: make(map[int]importlistexclusions.Exclusion, len(rows)), nextID: 0}
	for _, r := range rows {
		if r.ID > s.nextID {
			s.nextID = r.ID
		}
		s.rows[r.ID] = r
	}
	return s
}

func (f *fakeStore) All(context.Context) ([]importlistexclusions.Exclusion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]importlistexclusions.Exclusion, 0, len(f.rows))
	for _, e := range f.rows {
		out = append(out, e)
	}
	return out, nil
}
func (f *fakeStore) Get(_ context.Context, id int) (*importlistexclusions.Exclusion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.rows[id]; ok {
		return &e, nil
	}
	return nil, importlistexclusions.ErrNotFound
}
func (f *fakeStore) Insert(_ context.Context, e *importlistexclusions.Exclusion) (*importlistexclusions.Exclusion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	e.ID = f.nextID
	f.rows[e.ID] = *e
	return e, nil
}
func (f *fakeStore) Update(_ context.Context, e *importlistexclusions.Exclusion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[e.ID]; !ok {
		return importlistexclusions.ErrNotFound
	}
	f.rows[e.ID] = *e
	return nil
}
func (f *fakeStore) Delete(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteEr != nil {
		return f.deleteEr
	}
	f.deleted = append(f.deleted, id)
	delete(f.rows, id)
	return nil
}
func (f *fakeStore) GetByTvdbID(_ context.Context, tvdbID int) (*importlistexclusions.Exclusion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.rows {
		if e.TvdbID == tvdbID {
			return &e, nil
		}
	}
	return nil, importlistexclusions.ErrNotFound
}

func (f *fakeStore) deletedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deleted)
}

func TestGetHandler_EmptyPagedShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	importlistexclusion.GetHandler(newStore()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/importlistexclusion", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["records"]; !ok {
		t.Errorf("paged shape must include records key: %v", got)
	}
}

func TestByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/importlistexclusion/999", nil)
	req.SetPathValue("id", "999")
	importlistexclusion.ByIDHandler(newStore()).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestCreateHandler_Created201(t *testing.T) {
	t.Parallel()
	body := []byte(`{"tvdbId":12345,"title":"Excluded Show"}`)
	rr := httptest.NewRecorder()
	importlistexclusion.CreateHandler(newStore()).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/importlistexclusion", bytes.NewReader(body)))
	if rr.Code != http.StatusCreated {
		t.Errorf("status: got %d want 201", rr.Code)
	}
}

func TestUpdateHandler_Accepted202(t *testing.T) {
	t.Parallel()
	store := newStore(importlistexclusions.Exclusion{ID: 1, TvdbID: 12345, Title: "Old Title"})
	body := []byte(`{"id":1,"tvdbId":12345,"title":"New Title"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/importlistexclusion/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	importlistexclusion.UpdateHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202", rr.Code)
	}
}

func TestDeleteHandler_RewritesTo204(t *testing.T) {
	t.Parallel()
	store := newStore(importlistexclusions.Exclusion{ID: 7, TvdbID: 12345, Title: "x"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/importlistexclusion/7", nil)
	req.SetPathValue("id", "7")
	importlistexclusion.DeleteHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body must be empty: %q", rr.Body.String())
	}
	if store.deletedCount() != 1 {
		t.Errorf("Delete not called")
	}
}

func TestBulkDeleteHandler_RewritesTo204(t *testing.T) {
	t.Parallel()
	store := newStore(
		importlistexclusions.Exclusion{ID: 1, TvdbID: 1, Title: "a"},
		importlistexclusions.Exclusion{ID: 2, TvdbID: 2, Title: "b"},
	)
	body := []byte(`{"ids":[1,2]}`)
	rr := httptest.NewRecorder()
	importlistexclusion.BulkDeleteHandler(store).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v5/importlistexclusion/bulk", bytes.NewReader(body)))
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body must be empty: %q", rr.Body.String())
	}
}

func TestHandlerAliases_NonNil(t *testing.T) {
	t.Parallel()
	if importlistexclusion.GetHandler == nil {
		t.Errorf("GetHandler nil")
	}
	if importlistexclusion.ByIDHandler == nil {
		t.Errorf("ByIDHandler nil")
	}
	if importlistexclusion.CreateHandler == nil {
		t.Errorf("CreateHandler nil")
	}
	if importlistexclusion.UpdateHandler == nil {
		t.Errorf("UpdateHandler nil")
	}
}

// TestTypeAliases pins compile-time equivalence with v3.
func TestTypeAliases(t *testing.T) {
	t.Parallel()
	r := importlistexclusion.Resource{ID: 1, TvdbID: 100, Title: "x"}
	if r.Title != "x" {
		t.Errorf("Resource not addressable")
	}
	b := importlistexclusion.BulkResource{Ids: []int{1, 2}}
	if len(b.Ids) != 2 {
		t.Errorf("BulkResource not addressable")
	}
}
