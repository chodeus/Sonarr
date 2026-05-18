package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/tags"
)

type fakeTagLister struct {
	rows []tags.Tag
	err  error
}

func (f *fakeTagLister) All(context.Context) ([]tags.Tag, error) {
	return f.rows, f.err
}

func TestTagHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	TagHandler(&fakeTagLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/tag", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestTagHandler_Populated(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	TagHandler(&fakeTagLister{rows: []tags.Tag{
		{ID: 1, Label: "alpha"},
		{ID: 2, Label: "beta"},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/tag", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	want := `[{"id":1,"label":"alpha"},{"id":2,"label":"beta"}]`
	if got := rr.Body.String(); got != want {
		t.Errorf("body: got %q want %q", got, want)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
}

func TestTagHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	TagHandler(&fakeTagLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/tag", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.B1: CRUD tests ----------

// fakeTagWriter is the test double for the TagWriter interface used
// by the create/update/delete/by-id handlers. Tracks calls + lets the
// test inject errors per method.
type fakeTagWriter struct {
	row              *tags.Tag
	getByIDErr       error
	addErr           error
	updateErr        error
	deleteErr        error
	addCalledWith    *tags.Tag
	updateCalledWith *tags.Tag
	deleteCalledID   int
}

func (f *fakeTagWriter) GetByID(_ context.Context, id int) (*tags.Tag, error) {
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}
	if f.row == nil {
		return nil, tags.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeTagWriter) Add(_ context.Context, t *tags.Tag) (*tags.Tag, error) {
	f.addCalledWith = t
	if f.addErr != nil {
		return nil, f.addErr
	}
	return &tags.Tag{ID: 42, Label: t.Label}, nil
}

func (f *fakeTagWriter) Update(_ context.Context, t *tags.Tag) error {
	f.updateCalledWith = t
	return f.updateErr
}

func (f *fakeTagWriter) Delete(_ context.Context, id int) error {
	f.deleteCalledID = id
	return f.deleteErr
}

// ----- GET /tag/{id} -----

func TestTagByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{row: &tags.Tag{ID: 7, Label: "anime"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/tag/7", nil)
	req.SetPathValue("id", "7")
	TagByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got, want := rr.Body.String(), `{"id":7,"label":"anime"}`; got != want {
		t.Errorf("body: got %q want %q", got, want)
	}
}

func TestTagByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{row: nil} // GetByID returns ErrNotFound
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/tag/99", nil)
	req.SetPathValue("id", "99")
	TagByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not found") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestTagByIDHandler_BadIDIs400(t *testing.T) {
	t.Parallel()
	tests := []string{"abc", "0", "-1", ""}
	for _, raw := range tests {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v3/tag/"+raw, nil)
		req.SetPathValue("id", raw)
		TagByIDHandler(&fakeTagWriter{}).ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("id=%q: got %d want 400", raw, rr.Code)
		}
	}
}

// ----- POST /tag -----

func TestTagCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/tag", strings.NewReader(`{"label":"new"}`))
	TagCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201", rr.Code)
	}
	if got, want := rr.Body.String(), `{"id":42,"label":"new"}`; got != want {
		t.Errorf("body: got %q want %q", got, want)
	}
	if w.addCalledWith == nil || w.addCalledWith.Label != "new" {
		t.Errorf("svc.Add called with %v", w.addCalledWith)
	}
}

func TestTagCreateHandler_EmptyLabelEmits400Validation(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/tag", strings.NewReader(`{"label":""}`))
	TagCreateHandler(&fakeTagWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rr.Code)
	}
	// Validation 400s emit the FluentValidation array shape, not the
	// envelope — body starts with [.
	if !strings.HasPrefix(rr.Body.String(), "[") {
		t.Errorf("body should be FluentValidation array: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Label"`) {
		t.Errorf("body should mention Label property: %s", rr.Body.String())
	}
}

func TestTagCreateHandler_DisallowedCharsEmits400Validation(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/tag", strings.NewReader(`{"label":"bad space"}`))
	TagCreateHandler(&fakeTagWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Allowed characters a-z, 0-9 and -") {
		t.Errorf("must surface FluentValidation override message: %s", rr.Body.String())
	}
}

func TestTagCreateHandler_MalformedBodyIs400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/tag", strings.NewReader(`not json`))
	TagCreateHandler(&fakeTagWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"message"`) {
		t.Errorf("400 envelope expected: %s", rr.Body.String())
	}
}

// ----- PUT /tag/{id} -----

func TestTagUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/tag/7", strings.NewReader(`{"id":7,"label":"renamed"}`))
	req.SetPathValue("id", "7")
	TagUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202", rr.Code)
	}
	if w.updateCalledWith == nil || w.updateCalledWith.ID != 7 || w.updateCalledWith.Label != "renamed" {
		t.Errorf("svc.Update called with %v", w.updateCalledWith)
	}
}

func TestTagUpdateHandler_NotFoundEmits404(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{updateErr: tags.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/tag/99", strings.NewReader(`{"label":"x"}`))
	req.SetPathValue("id", "99")
	TagUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestTagUpdateHandler_PathIDOverridesBody(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{}
	rr := httptest.NewRecorder()
	// Body says id=999; path says id=7. C# RestPutById uses the URL id.
	req := httptest.NewRequest(http.MethodPut, "/api/v3/tag/7", strings.NewReader(`{"id":999,"label":"x"}`))
	req.SetPathValue("id", "7")
	TagUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202", rr.Code)
	}
	if w.updateCalledWith.ID != 7 {
		t.Errorf("path id must win: got %d want 7", w.updateCalledWith.ID)
	}
}

// ----- DELETE /tag/{id} -----

func TestTagDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/tag/7", nil)
	req.SetPathValue("id", "7")
	TagDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if w.deleteCalledID != 7 {
		t.Errorf("svc.Delete called with %d", w.deleteCalledID)
	}
}

func TestTagDeleteHandler_NotFoundEmits404(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{deleteErr: tags.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/tag/99", nil)
	req.SetPathValue("id", "99")
	TagDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestTagDeleteHandler_ServiceErrorIs500(t *testing.T) {
	t.Parallel()
	w := &fakeTagWriter{deleteErr: errors.New("db down")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/tag/7", nil)
	req.SetPathValue("id", "7")
	TagDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
