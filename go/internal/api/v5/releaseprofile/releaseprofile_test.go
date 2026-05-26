package releaseprofile_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/releaseprofile"
	"sonarr.tv/sonarr/internal/releaseprofiles"
)

type fakeSvc struct {
	mu      sync.Mutex
	rows    []releaseprofiles.Profile
	added   []releaseprofiles.Profile
	updated []releaseprofiles.Profile
	deleted []int
}

func (f *fakeSvc) All(context.Context) ([]releaseprofiles.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows, nil
}
func (f *fakeSvc) Get(_ context.Context, id int) (*releaseprofiles.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.rows {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, releaseprofiles.ErrNotFound
}
func (f *fakeSvc) Add(_ context.Context, p *releaseprofiles.Profile) (*releaseprofiles.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p.ID = len(f.rows) + 1
	f.added = append(f.added, *p)
	f.rows = append(f.rows, *p)
	return p, nil
}
func (f *fakeSvc) Update(_ context.Context, p *releaseprofiles.Profile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, row := range f.rows {
		if row.ID == p.ID {
			f.rows[i] = *p
			f.updated = append(f.updated, *p)
			return nil
		}
	}
	return releaseprofiles.ErrNotFound
}
func (f *fakeSvc) Delete(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	for i, row := range f.rows {
		if row.ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return releaseprofiles.ErrNotFound
}

func (f *fakeSvc) deletedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deleted)
}

func TestListHandler_EmitsIndexerIdsPlural(t *testing.T) {
	t.Parallel()
	svc := &fakeSvc{rows: []releaseprofiles.Profile{{
		ID: 1, Name: "p1", Enabled: true, Required: []string{"a"}, IndexerIDs: []int{10, 20},
	}}}
	rr := httptest.NewRecorder()
	releaseprofile.ListHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/releaseprofile", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"indexerIds":[10,20]`) {
		t.Errorf("v5 wire MUST include indexerIds list: %s", body)
	}
	if strings.Contains(body, `"indexerId"`) {
		t.Errorf("v5 wire MUST NOT include v3's singular indexerId field: %s", body)
	}
}

func TestListHandler_EmptyReturnsArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	releaseprofile.ListHandler(&fakeSvc{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/releaseprofile", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestByIDHandler_Found_PopulatesIndexerIds(t *testing.T) {
	t.Parallel()
	svc := &fakeSvc{rows: []releaseprofiles.Profile{{
		ID: 7, Name: "p7", Enabled: true, Required: []string{"r"}, IndexerIDs: []int{42},
	}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/releaseprofile/7", nil)
	req.SetPathValue("id", "7")
	releaseprofile.ByIDHandler(svc).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got releaseprofile.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 7 || len(got.IndexerIDs) != 1 || got.IndexerIDs[0] != 42 {
		t.Errorf("got %+v", got)
	}
}

func TestByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/releaseprofile/999", nil)
	req.SetPathValue("id", "999")
	releaseprofile.ByIDHandler(&fakeSvc{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestCreateHandler_AcceptsIndexerIdsPlural_Returns201(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, releaseprofile.Resource{
		Name: "p1", Enabled: true, Required: []string{"a"}, IndexerIDs: []int{1, 2, 3}, Tags: []int{}, ExcludedTags: []int{},
	})
	svc := &fakeSvc{}
	rr := httptest.NewRecorder()
	releaseprofile.CreateHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/releaseprofile", bytes.NewReader(body)))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got releaseprofile.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.IndexerIDs) != 3 {
		t.Errorf("IndexerIDs not round-tripped: %+v", got)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.added) != 1 || len(svc.added[0].IndexerIDs) != 3 {
		t.Errorf("svc.Add not called with the indexerIds list: %+v", svc.added)
	}
}

func TestCreateHandler_RejectsV3IndexerIdField(t *testing.T) {
	t.Parallel()
	// DisallowUnknownFields means a v3-shaped body with `indexerId`
	// (singular) MUST 400 on the v5 surface.
	body := []byte(`{"name":"p","enabled":true,"required":["a"],"indexerId":42}`)
	rr := httptest.NewRecorder()
	releaseprofile.CreateHandler(&fakeSvc{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/releaseprofile", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (v5 rejects v3-style indexerId)", rr.Code)
	}
}

func TestCreateHandler_ValidationFires(t *testing.T) {
	t.Parallel()
	// Empty Required + Ignored + airDateRestriction=false should 400.
	body := mustEncode(t, releaseprofile.Resource{
		Name: "p", Enabled: true, Required: []string{}, Ignored: []string{}, AirDateRestriction: false,
	})
	rr := httptest.NewRecorder()
	releaseprofile.CreateHandler(&fakeSvc{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/releaseprofile", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestUpdateHandler_Found_Returns202(t *testing.T) {
	t.Parallel()
	svc := &fakeSvc{rows: []releaseprofiles.Profile{{
		ID: 7, Name: "old", Enabled: true, Required: []string{"r"},
	}}}
	body := mustEncode(t, releaseprofile.Resource{
		Name: "new", Enabled: true, Required: []string{"r"}, IndexerIDs: []int{99},
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/releaseprofile/7", bytes.NewReader(body))
	req.SetPathValue("id", "7")
	releaseprofile.UpdateHandler(svc).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rr.Code, rr.Body.String())
	}
	var got releaseprofile.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "new" || len(got.IndexerIDs) != 1 || got.IndexerIDs[0] != 99 {
		t.Errorf("got %+v", got)
	}
}

func TestUpdateHandler_NotFound(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, releaseprofile.Resource{Name: "x", Required: []string{"r"}})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/releaseprofile/999", bytes.NewReader(body))
	req.SetPathValue("id", "999")
	releaseprofile.UpdateHandler(&fakeSvc{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestDeleteHandler_RewritesTo204(t *testing.T) {
	t.Parallel()
	svc := &fakeSvc{rows: []releaseprofiles.Profile{{ID: 7}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/releaseprofile/7", nil)
	req.SetPathValue("id", "7")
	releaseprofile.DeleteHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body must be empty: %q", rr.Body.String())
	}
	if svc.deletedCount() != 1 {
		t.Errorf("Delete not invoked")
	}
}

func TestDeleteHandler_MissingID_IdempotentRewritten(t *testing.T) {
	t.Parallel()
	// v3 swallows ErrNotFound → 200 → wrapper rewrites to 204.
	svc := &fakeSvc{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/releaseprofile/999", nil)
	req.SetPathValue("id", "999")
	releaseprofile.DeleteHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (idempotent + wrapper)", rr.Code)
	}
}

func mustEncode(t *testing.T, r releaseprofile.Resource) []byte {
	t.Helper()
	buf, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}
