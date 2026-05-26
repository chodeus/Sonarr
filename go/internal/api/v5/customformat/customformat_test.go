package customformat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
)

// fakeWriter implements Writer (alias of apiv3.CustomFormatWriter) for
// the three v5-specific handlers. Keeps the test surface narrow — we
// only need to verify status-code differences against v3.
type fakeWriter struct {
	rows       []*customformats.Format
	rowByID    map[int]*customformats.Format
	getErr     error
	updateErr  error
	deleteErr  error
	deleteCall []int
	updateCall []*customformats.CustomFormat
}

func (f *fakeWriter) All(_ context.Context) ([]*customformats.Format, error) {
	return f.rows, nil
}

func (f *fakeWriter) GetByID(_ context.Context, id int) (*customformats.Format, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if row, ok := f.rowByID[id]; ok {
		return row, nil
	}
	return nil, customformats.ErrNotFound
}

func (f *fakeWriter) Insert(_ context.Context, row *customformats.CustomFormat) (*customformats.Format, error) {
	return &customformats.Format{ID: 999, Name: row.Name, IncludeCustomFormatWhenRenaming: row.IncludeCustomFormatWhenRenaming}, nil
}

func (f *fakeWriter) Update(_ context.Context, row *customformats.CustomFormat) error {
	f.updateCall = append(f.updateCall, row)
	return f.updateErr
}

func (f *fakeWriter) Delete(_ context.Context, id int) error {
	f.deleteCall = append(f.deleteCall, id)
	return f.deleteErr
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200) -----

func TestDeleteHandler_NoContent204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rowByID: map[int]*customformats.Format{5: {ID: 5, Name: "x264"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customformat/5", nil)
	req.SetPathValue("id", "5")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204: got %q", rr.Body.String())
	}
	if len(w.deleteCall) != 1 || w.deleteCall[0] != 5 {
		t.Errorf("delete called with %v want [5]", w.deleteCall)
	}
}

func TestDeleteHandler_NotFound404(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{deleteErr: customformats.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customformat/99", nil)
	req.SetPathValue("id", "99")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestDeleteHandler_InvalidID400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customformat/abc", nil)
	req.SetPathValue("id", "abc")
	DeleteHandler(&fakeWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestDeleteHandler_ServiceErr500(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{deleteErr: errors.New("db down")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customformat/5", nil)
	req.SetPathValue("id", "5")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ----- PUT /bulk — v5 returns 200 (v3 returns 202) -----

func TestBulkUpdateHandler_OK200(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{
		rowByID: map[int]*customformats.Format{
			1: {ID: 1, Name: "x264", IncludeCustomFormatWhenRenaming: false},
			2: {ID: 2, Name: "x265", IncludeCustomFormatWhenRenaming: false},
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/customformat/bulk",
		strings.NewReader(`{"ids":[1,2],"includeCustomFormatWhenRenaming":true}`))
	BulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (v5 PUT /bulk diverges from v3 202); body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"id":1`, `"id":2`, `"includeCustomFormatWhenRenaming":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
	if len(w.updateCall) != 2 {
		t.Errorf("update calls: got %d want 2", len(w.updateCall))
	}
	for _, row := range w.updateCall {
		if !row.IncludeCustomFormatWhenRenaming {
			t.Errorf("update %d: IncludeCustomFormatWhenRenaming not patched to true (got %v)", row.ID, row.IncludeCustomFormatWhenRenaming)
		}
	}
}

func TestBulkUpdateHandler_EmptyIds400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/customformat/bulk",
		strings.NewReader(`{"ids":[],"includeCustomFormatWhenRenaming":true}`))
	BulkUpdateHandler(&fakeWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ids must be provided") {
		t.Errorf("body should explain missing ids: %s", rr.Body.String())
	}
}

func TestBulkUpdateHandler_NullIncludeFlagPreservesExisting(t *testing.T) {
	t.Parallel()
	// C# null-coalesce: when includeCustomFormatWhenRenaming is null/
	// absent on the request, each row's existing value is preserved.
	w := &fakeWriter{
		rowByID: map[int]*customformats.Format{
			1: {ID: 1, Name: "x264", IncludeCustomFormatWhenRenaming: true},
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/customformat/bulk",
		strings.NewReader(`{"ids":[1]}`))
	BulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updateCall) != 1 || !w.updateCall[0].IncludeCustomFormatWhenRenaming {
		t.Errorf("existing true should be preserved on null patch: got %+v", w.updateCall)
	}
}

func TestBulkUpdateHandler_MissingID404(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rowByID: map[int]*customformats.Format{1: {ID: 1, Name: "x264"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/customformat/bulk",
		strings.NewReader(`{"ids":[1,99],"includeCustomFormatWhenRenaming":true}`))
	BulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

// ----- DELETE /bulk — v5 returns 204 empty (v3 returns 200 "{}") -----

func TestBulkDeleteHandler_NoContent204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rowByID: map[int]*customformats.Format{1: {ID: 1}, 2: {ID: 2}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customformat/bulk",
		strings.NewReader(`{"ids":[1,2]}`))
	BulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (v5 DELETE /bulk diverges from v3 200); body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204 (v3 emits %q, v5 must NOT): got %q", "{}", rr.Body.String())
	}
	if len(w.deleteCall) != 2 || w.deleteCall[0] != 1 || w.deleteCall[1] != 2 {
		t.Errorf("delete calls: got %v want [1,2]", w.deleteCall)
	}
}

func TestBulkDeleteHandler_MissingID404Short(t *testing.T) {
	t.Parallel()
	// First missing id short-circuits; subsequent ids must not be touched.
	w := &fakeWriter{deleteErr: customformats.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customformat/bulk",
		strings.NewReader(`{"ids":[99,1,2]}`))
	BulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
	// Only the first delete attempt should have fired before short-circuit.
	if len(w.deleteCall) != 1 || w.deleteCall[0] != 99 {
		t.Errorf("delete calls: got %v want [99] (short-circuit on first miss)", w.deleteCall)
	}
}

func TestBulkDeleteHandler_MalformedBody400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customformat/bulk",
		strings.NewReader(`not-json`))
	BulkDeleteHandler(&fakeWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- Re-exported handlers are wired correctly (smoke) -----

func TestReexportedHandlers_NonNil(t *testing.T) {
	t.Parallel()
	// Smoke: confirm the package-level var bindings to the v3 handlers
	// produce non-nil http.Handler values. A regression here would
	// indicate the v3→v5 re-export aliasing was broken.
	cases := []struct {
		name string
		got  http.Handler
	}{
		{"List", ListHandler(&fakeWriter{})},
		{"ByID", ByIDHandler(&fakeWriter{})},
		{"Create", CreateHandler(&fakeWriter{})},
		{"Update", UpdateHandler(&fakeWriter{})},
		{"Schema", SchemaHandler()},
	}
	for _, tc := range cases {
		if tc.got == nil {
			t.Errorf("%s handler was nil — v3 re-export broken", tc.name)
		}
	}
}
