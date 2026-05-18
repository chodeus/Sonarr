package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/importlistexclusions"
)

type fakeImportListExclusionStore struct {
	rows        []importlistexclusions.Exclusion
	getRow      *importlistexclusions.Exclusion
	byTvdbID    *importlistexclusions.Exclusion
	getErr      error
	insertErr   error
	updateErr   error
	deleteErr   error
	byTvdbIDErr error

	insertCalledWith *importlistexclusions.Exclusion
	updateCalledWith *importlistexclusions.Exclusion
	deleteCalledIDs  []int
}

func (f *fakeImportListExclusionStore) All(_ context.Context) ([]importlistexclusions.Exclusion, error) {
	return f.rows, nil
}

func (f *fakeImportListExclusionStore) Get(_ context.Context, id int) (*importlistexclusions.Exclusion, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getRow == nil {
		return nil, importlistexclusions.ErrNotFound
	}
	return f.getRow, nil
}

func (f *fakeImportListExclusionStore) Insert(_ context.Context, e *importlistexclusions.Exclusion) (*importlistexclusions.Exclusion, error) {
	f.insertCalledWith = e
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	out := *e
	out.ID = 11
	return &out, nil
}

func (f *fakeImportListExclusionStore) Update(_ context.Context, e *importlistexclusions.Exclusion) error {
	f.updateCalledWith = e
	return f.updateErr
}

func (f *fakeImportListExclusionStore) Delete(_ context.Context, id int) error {
	f.deleteCalledIDs = append(f.deleteCalledIDs, id)
	return f.deleteErr
}

func (f *fakeImportListExclusionStore) GetByTvdbID(_ context.Context, tvdbID int) (*importlistexclusions.Exclusion, error) {
	if f.byTvdbIDErr != nil {
		return nil, f.byTvdbIDErr
	}
	if f.byTvdbID == nil {
		return nil, importlistexclusions.ErrNotFound
	}
	return f.byTvdbID, nil
}

// ----- GET /importlistexclusion -----

func TestImportListExclusionHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListExclusionHandler(&fakeImportListExclusionStore{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlistexclusion", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestImportListExclusionHandler_Populated(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{rows: []importlistexclusions.Exclusion{
		{ID: 1, TvdbID: 1234, Title: "Foo"},
		{ID: 2, TvdbID: 5678, Title: "Bar"},
	}}
	rr := httptest.NewRecorder()
	ImportListExclusionHandler(store).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlistexclusion", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	want := `[{"id":1,"tvdbId":1234,"title":"Foo"},{"id":2,"tvdbId":5678,"title":"Bar"}]`
	if got := rr.Body.String(); got != want {
		t.Errorf("body: got %q want %q", got, want)
	}
}

// ----- GET /importlistexclusion/{id} -----

func TestImportListExclusionByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{getRow: &importlistexclusions.Exclusion{ID: 5, TvdbID: 1234, Title: "Foo"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/importlistexclusion/5", nil)
	req.SetPathValue("id", "5")
	ImportListExclusionByIDHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"tvdbId":1234`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestImportListExclusionByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/importlistexclusion/99", nil)
	req.SetPathValue("id", "99")
	ImportListExclusionByIDHandler(&fakeImportListExclusionStore{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- POST /importlistexclusion -----

func TestImportListExclusionCreateHandler_Success(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{}
	rr := httptest.NewRecorder()
	body := `{"tvdbId":1234,"title":"Foo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlistexclusion", strings.NewReader(body))
	ImportListExclusionCreateHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if store.insertCalledWith == nil || store.insertCalledWith.TvdbID != 1234 {
		t.Errorf("Insert called with %+v", store.insertCalledWith)
	}
	if !strings.Contains(rr.Body.String(), `"id":11`) {
		t.Errorf("response should carry id 11: %s", rr.Body.String())
	}
}

func TestImportListExclusionCreateHandler_ZeroTvdbId400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"tvdbId":0,"title":"Foo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlistexclusion", strings.NewReader(body))
	ImportListExclusionCreateHandler(&fakeImportListExclusionStore{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"TvdbId"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestImportListExclusionCreateHandler_EmptyTitle400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"tvdbId":1234,"title":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlistexclusion", strings.NewReader(body))
	ImportListExclusionCreateHandler(&fakeImportListExclusionStore{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Title"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestImportListExclusionCreateHandler_DuplicateTvdbId400(t *testing.T) {
	t.Parallel()
	// byTvdbID returns an existing row with a DIFFERENT id → reject.
	store := &fakeImportListExclusionStore{byTvdbID: &importlistexclusions.Exclusion{ID: 7, TvdbID: 1234, Title: "Existing"}}
	rr := httptest.NewRecorder()
	body := `{"tvdbId":1234,"title":"New"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/importlistexclusion", strings.NewReader(body))
	ImportListExclusionCreateHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "TVDB id is already excluded") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// ----- PUT /importlistexclusion/{id} -----

func TestImportListExclusionUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{}
	rr := httptest.NewRecorder()
	body := `{"id":5,"tvdbId":1234,"title":"Renamed"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/importlistexclusion/5", strings.NewReader(body))
	req.SetPathValue("id", "5")
	ImportListExclusionUpdateHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if store.updateCalledWith == nil || store.updateCalledWith.ID != 5 {
		t.Errorf("Update called with %+v", store.updateCalledWith)
	}
}

func TestImportListExclusionUpdateHandler_UniquenessExcludesSelf(t *testing.T) {
	t.Parallel()
	// GetByTvdbID returns row 5 with tvdbId=1234. Updating row 5
	// (same tvdbId, new title) must succeed — the duplicate check
	// excludes the row being updated.
	store := &fakeImportListExclusionStore{byTvdbID: &importlistexclusions.Exclusion{ID: 5, TvdbID: 1234, Title: "Foo"}}
	rr := httptest.NewRecorder()
	body := `{"id":5,"tvdbId":1234,"title":"Renamed"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/importlistexclusion/5", strings.NewReader(body))
	req.SetPathValue("id", "5")
	ImportListExclusionUpdateHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202 (uniqueness must exclude self); body=%s", rr.Code, rr.Body.String())
	}
}

func TestImportListExclusionUpdateHandler_NotFound404(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{updateErr: importlistexclusions.ErrNotFound}
	rr := httptest.NewRecorder()
	body := `{"tvdbId":1234,"title":"Foo"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/importlistexclusion/99", strings.NewReader(body))
	req.SetPathValue("id", "99")
	ImportListExclusionUpdateHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- DELETE /importlistexclusion/{id} -----

func TestImportListExclusionDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlistexclusion/5", nil)
	req.SetPathValue("id", "5")
	ImportListExclusionDeleteHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if len(store.deleteCalledIDs) != 1 || store.deleteCalledIDs[0] != 5 {
		t.Errorf("Delete called with %v", store.deleteCalledIDs)
	}
}

func TestImportListExclusionDeleteHandler_NotFound404(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{deleteErr: importlistexclusions.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlistexclusion/99", nil)
	req.SetPathValue("id", "99")
	ImportListExclusionDeleteHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- DELETE /importlistexclusion/bulk -----

func TestImportListExclusionBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{}
	rr := httptest.NewRecorder()
	body := `{"ids":[3,5,7]}`
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlistexclusion/bulk", strings.NewReader(body))
	ImportListExclusionBulkDeleteHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "{}" {
		t.Errorf("body must be {}; got %s", rr.Body.String())
	}
	if len(store.deleteCalledIDs) != 3 {
		t.Errorf("Delete called %d times; want 3 (%v)", len(store.deleteCalledIDs), store.deleteCalledIDs)
	}
}

func TestImportListExclusionBulkDeleteHandler_AnyMissingIs404(t *testing.T) {
	t.Parallel()
	store := &fakeImportListExclusionStore{deleteErr: importlistexclusions.ErrNotFound}
	rr := httptest.NewRecorder()
	body := `{"ids":[99]}`
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/importlistexclusion/bulk", strings.NewReader(body))
	ImportListExclusionBulkDeleteHandler(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.I16: GET /paged tests ----------

func TestImportListExclusionPagedHandler_EmptyEnvelope(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListExclusionPagedHandler(&fakeImportListExclusionStore{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlistexclusion/paged", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got PagingResource[ImportListExclusionResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TotalRecords != 0 || got.SortKey != "id" || got.SortDirection != "descending" {
		t.Errorf("envelope: %+v", got)
	}
}

func TestImportListExclusionPagedHandler_DefaultsToIDDescending(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	store := &fakeImportListExclusionStore{rows: []importlistexclusions.Exclusion{
		{ID: 1, TvdbID: 100, Title: "Zebra"},
		{ID: 2, TvdbID: 200, Title: "Alpha"},
		{ID: 3, TvdbID: 50, Title: "Middle"},
	}}
	ImportListExclusionPagedHandler(store).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlistexclusion/paged?pageSize=10", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got PagingResource[ImportListExclusionResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TotalRecords != 3 || len(got.Records) != 3 {
		t.Fatalf("counts: total=%d records=%d", got.TotalRecords, len(got.Records))
	}
	if got.Records[0].ID != 3 || got.Records[1].ID != 2 || got.Records[2].ID != 1 {
		t.Errorf("id desc ordering: %d %d %d", got.Records[0].ID, got.Records[1].ID, got.Records[2].ID)
	}
}

func TestImportListExclusionPagedHandler_SortByTitleAscending(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	store := &fakeImportListExclusionStore{rows: []importlistexclusions.Exclusion{
		{ID: 1, TvdbID: 100, Title: "Zebra"},
		{ID: 2, TvdbID: 200, Title: "Alpha"},
		{ID: 3, TvdbID: 50, Title: "Middle"},
	}}
	ImportListExclusionPagedHandler(store).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlistexclusion/paged?sortKey=title&sortDirection=ascending&pageSize=10", nil))
	var got PagingResource[ImportListExclusionResource]
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Records) != 3 {
		t.Fatalf("len: %d", len(got.Records))
	}
	if got.Records[0].Title != "Alpha" || got.Records[1].Title != "Middle" || got.Records[2].Title != "Zebra" {
		t.Errorf("title asc ordering: %q %q %q", got.Records[0].Title, got.Records[1].Title, got.Records[2].Title)
	}
}

func TestImportListExclusionPagedHandler_PaginationPage2(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	store := &fakeImportListExclusionStore{rows: []importlistexclusions.Exclusion{
		{ID: 1, TvdbID: 100, Title: "A"},
		{ID: 2, TvdbID: 200, Title: "B"},
		{ID: 3, TvdbID: 50, Title: "C"},
		{ID: 4, TvdbID: 75, Title: "D"},
	}}
	ImportListExclusionPagedHandler(store).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlistexclusion/paged?page=2&pageSize=2&sortKey=id&sortDirection=ascending", nil))
	var got PagingResource[ImportListExclusionResource]
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.TotalRecords != 4 || len(got.Records) != 2 {
		t.Fatalf("counts: total=%d records=%d", got.TotalRecords, len(got.Records))
	}
	if got.Records[0].ID != 3 || got.Records[1].ID != 4 {
		t.Errorf("page-2 contents: %d %d", got.Records[0].ID, got.Records[1].ID)
	}
}
