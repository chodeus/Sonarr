package v3

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
)

type fakeCustomFormatLister struct {
	rows []*customformats.Format
	err  error
}

func (f *fakeCustomFormatLister) All(context.Context) ([]*customformats.Format, error) {
	return f.rows, f.err
}

func TestCustomFormatHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestCustomFormatHandler_PopulatedShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{rows: []*customformats.Format{
		{ID: 1, Name: "x264", IncludeCustomFormatWhenRenaming: true},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`,
		`"name":"x264"`,
		`"includeCustomFormatWhenRenaming":true`,
		`"specifications":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestCustomFormatHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.B4: CRUD + bulk tests ----------

type fakeCustomFormatWriter struct {
	rows      []*customformats.Format
	getRow    *customformats.Format
	getErr    error
	allErr    error
	insertErr error
	updateErr error
	deleteErr error

	insertCalledWith *customformats.CustomFormat
	updateCalledWith *customformats.CustomFormat
	deleteCalledIDs  []int
}

func (f *fakeCustomFormatWriter) All(_ context.Context) ([]*customformats.Format, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.rows, nil
}

func (f *fakeCustomFormatWriter) GetByID(_ context.Context, id int) (*customformats.Format, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getRow == nil {
		return nil, customformats.ErrNotFound
	}
	return f.getRow, nil
}

func (f *fakeCustomFormatWriter) Insert(_ context.Context, row *customformats.CustomFormat) (*customformats.Format, error) {
	f.insertCalledWith = row
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	return &customformats.Format{ID: 42, Name: row.Name, IncludeCustomFormatWhenRenaming: row.IncludeCustomFormatWhenRenaming}, nil
}

func (f *fakeCustomFormatWriter) Update(_ context.Context, row *customformats.CustomFormat) error {
	f.updateCalledWith = row
	return f.updateErr
}

func (f *fakeCustomFormatWriter) Delete(_ context.Context, id int) error {
	f.deleteCalledIDs = append(f.deleteCalledIDs, id)
	return f.deleteErr
}

func cfBody(name string, hasSpec bool) string {
	specs := "[]"
	if hasSpec {
		specs = `[{"id":0,"name":"x264","implementation":"ReleaseTitleSpecification","implementationName":"Release Title","negate":false,"required":false,"fields":[]}]`
	}
	return fmt.Sprintf(`{"name":%q,"specifications":%s}`, name, specs)
}

// ----- GET /customformat/{id} -----

func TestCustomFormatByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{getRow: &customformats.Format{ID: 5, Name: "x264"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/customformat/5", nil)
	req.SetPathValue("id", "5")
	CustomFormatByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":5`) || !strings.Contains(rr.Body.String(), `"name":"x264"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestCustomFormatByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/customformat/99", nil)
	req.SetPathValue("id", "99")
	CustomFormatByIDHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- POST /customformat -----

func TestCustomFormatCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(cfBody("x264", true)))
	CustomFormatCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.insertCalledWith == nil || w.insertCalledWith.Name != "x264" {
		t.Errorf("svc.Insert called with %+v", w.insertCalledWith)
	}
	if !strings.Contains(w.insertCalledWith.Specifications, "ReleaseTitleSpecification") {
		t.Errorf("Specifications JSON should pass through: %q", w.insertCalledWith.Specifications)
	}
}

func TestCustomFormatCreateHandler_EmptyName400Validation(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(cfBody("", true)))
	CustomFormatCreateHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Name"`) {
		t.Errorf("body should fault Name: %s", rr.Body.String())
	}
}

func TestCustomFormatCreateHandler_EmptySpecs400Validation(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(cfBody("x264", false)))
	CustomFormatCreateHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Must contain at least one Condition") {
		t.Errorf("body should require Specifications: %s", rr.Body.String())
	}
}

func TestCustomFormatCreateHandler_DuplicateName400Validation(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{rows: []*customformats.Format{{ID: 10, Name: "x264"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(cfBody("x264", true)))
	CustomFormatCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Must be unique") {
		t.Errorf("body should reject duplicate Name: %s", rr.Body.String())
	}
}

func TestCustomFormatCreateHandler_EmptySpecNameRejected(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"name":"x264","specifications":[{"id":0,"name":"","implementation":"X","implementationName":"X","negate":false,"required":false,"fields":[]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(body))
	CustomFormatCreateHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Condition name(s) cannot be empty") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// ----- PUT /customformat/{id} -----

func TestCustomFormatUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{getRow: &customformats.Format{ID: 5, Name: "x264 renamed"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customformat/5", strings.NewReader(cfBody("x264 renamed", true)))
	req.SetPathValue("id", "5")
	CustomFormatUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updateCalledWith == nil || w.updateCalledWith.ID != 5 {
		t.Errorf("svc.Update called with %+v", w.updateCalledWith)
	}
}

func TestCustomFormatUpdateHandler_UniquenessExcludesSelf(t *testing.T) {
	t.Parallel()
	// Update on id=5 with name "x264" must SUCCEED if the only row
	// with that name IS id=5 — uniqueness excludes self.
	w := &fakeCustomFormatWriter{
		rows:   []*customformats.Format{{ID: 5, Name: "x264"}},
		getRow: &customformats.Format{ID: 5, Name: "x264"},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customformat/5", strings.NewReader(cfBody("x264", true)))
	req.SetPathValue("id", "5")
	CustomFormatUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202 (uniqueness must exclude self); body=%s", rr.Code, rr.Body.String())
	}
}

// ----- DELETE /customformat/{id} -----

func TestCustomFormatDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/customformat/5", nil)
	req.SetPathValue("id", "5")
	CustomFormatDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if len(w.deleteCalledIDs) != 1 || w.deleteCalledIDs[0] != 5 {
		t.Errorf("svc.Delete called with %v", w.deleteCalledIDs)
	}
}

// ----- PUT /customformat/bulk -----

func TestCustomFormatBulkUpdateHandler_PatchesIncludeFlag(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{getRow: &customformats.Format{ID: 5, Name: "x"}}
	rr := httptest.NewRecorder()
	body := `{"ids":[5,7],"includeCustomFormatWhenRenaming":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customformat/bulk", strings.NewReader(body))
	CustomFormatBulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updateCalledWith == nil || !w.updateCalledWith.IncludeCustomFormatWhenRenaming {
		t.Errorf("Update should have set include=true; got %+v", w.updateCalledWith)
	}
}

func TestCustomFormatBulkUpdateHandler_EmptyIds400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customformat/bulk", strings.NewReader(`{"ids":[]}`))
	CustomFormatBulkUpdateHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ids must be provided") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// ----- DELETE /customformat/bulk -----

func TestCustomFormatBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/customformat/bulk", strings.NewReader(`{"ids":[3,5,7]}`))
	CustomFormatBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "{}" {
		t.Errorf("body should be {}; got %s", rr.Body.String())
	}
	if len(w.deleteCalledIDs) != 3 || w.deleteCalledIDs[0] != 3 || w.deleteCalledIDs[2] != 7 {
		t.Errorf("delete called with %v want [3 5 7]", w.deleteCalledIDs)
	}
}

func TestCustomFormatBulkDeleteHandler_AnyMissingIs404(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{deleteErr: customformats.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/customformat/bulk", strings.NewReader(`{"ids":[99]}`))
	CustomFormatBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d want 404", rr.Code)
	}
}
