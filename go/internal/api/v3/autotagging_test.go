package v3

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/autotagging"
)

type fakeAutoTaggingLister struct {
	rows []*autotagging.Tag
	err  error
}

func (f *fakeAutoTaggingLister) All(context.Context) ([]*autotagging.Tag, error) {
	return f.rows, f.err
}

func TestAutoTaggingHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	AutoTaggingHandler(&fakeAutoTaggingLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/autotagging", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestAutoTaggingHandler_PopulatedNilTagsNormalised(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	AutoTaggingHandler(&fakeAutoTaggingLister{rows: []*autotagging.Tag{
		{ID: 1, Name: "alpha", RemoveTagsAutomatically: false, Tags: nil},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/autotagging", nil))
	body := rr.Body.String()
	// Nil tags must serialise as [] not null — C# HashSet<int>
	// renders as [] when empty.
	if !strings.Contains(body, `"tags":[]`) {
		t.Errorf("tags should be []: %s", body)
	}
	if !strings.Contains(body, `"specifications":[]`) {
		t.Errorf("specifications should be [] (deferred port): %s", body)
	}
}

func TestAutoTaggingHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	AutoTaggingHandler(&fakeAutoTaggingLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/autotagging", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.B10: CRUD tests ----------

type fakeAutoTaggingWriter struct {
	rows      []*autotagging.Tag
	getRow    *autotagging.Tag
	getErr    error
	insertErr error
	updateErr error
	deleteErr error

	insertCalledWith *autotagging.AutoTag
	updateCalledWith *autotagging.AutoTag
	deleteCalledID   int
}

func (f *fakeAutoTaggingWriter) All(_ context.Context) ([]*autotagging.Tag, error) {
	return f.rows, nil
}

func (f *fakeAutoTaggingWriter) GetByID(_ context.Context, id int) (*autotagging.Tag, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getRow == nil {
		return nil, autotagging.ErrNotFound
	}
	return f.getRow, nil
}

func (f *fakeAutoTaggingWriter) Insert(_ context.Context, row *autotagging.AutoTag) (*autotagging.Tag, error) {
	f.insertCalledWith = row
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	return &autotagging.Tag{ID: 21, Name: row.Name, Tags: row.Tags, RemoveTagsAutomatically: row.RemoveTagsAutomatically}, nil
}

func (f *fakeAutoTaggingWriter) Update(_ context.Context, row *autotagging.AutoTag) error {
	f.updateCalledWith = row
	return f.updateErr
}

func (f *fakeAutoTaggingWriter) Delete(_ context.Context, id int) error {
	f.deleteCalledID = id
	return f.deleteErr
}

func atBody(name string, hasSpec bool) string {
	specs := "[]"
	if hasSpec {
		specs = `[{"id":0,"name":"by-genre","implementation":"GenreSpecification","implementationName":"Genre","negate":false,"required":false,"fields":[]}]`
	}
	return fmt.Sprintf(`{"name":%q,"removeTagsAutomatically":false,"tags":[1,2],"specifications":%s}`, name, specs)
}

func TestAutoTaggingByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeAutoTaggingWriter{getRow: &autotagging.Tag{ID: 5, Name: "anime", Tags: []int{1}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/autotagging/5", nil)
	req.SetPathValue("id", "5")
	AutoTaggingByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":5`) || !strings.Contains(rr.Body.String(), `"name":"anime"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestAutoTaggingByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/autotagging/99", nil)
	req.SetPathValue("id", "99")
	AutoTaggingByIDHandler(&fakeAutoTaggingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestAutoTaggingCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeAutoTaggingWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/autotagging", strings.NewReader(atBody("anime", true)))
	AutoTaggingCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.insertCalledWith == nil || w.insertCalledWith.Name != "anime" {
		t.Errorf("svc.Insert args: %+v", w.insertCalledWith)
	}
	if !strings.Contains(w.insertCalledWith.Specifications, "GenreSpecification") {
		t.Errorf("Specifications JSON should pass through: %q", w.insertCalledWith.Specifications)
	}
}

func TestAutoTaggingCreateHandler_EmptyName400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/autotagging", strings.NewReader(atBody("", true)))
	AutoTaggingCreateHandler(&fakeAutoTaggingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Name"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestAutoTaggingCreateHandler_EmptyTags400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"name":"x","removeTagsAutomatically":false,"tags":[],"specifications":[{"id":0,"name":"x","implementation":"GenreSpecification","implementationName":"Genre","negate":false,"required":false,"fields":[]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/autotagging", strings.NewReader(body))
	AutoTaggingCreateHandler(&fakeAutoTaggingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Tags"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestAutoTaggingCreateHandler_EmptySpecs400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/autotagging", strings.NewReader(atBody("x", false)))
	AutoTaggingCreateHandler(&fakeAutoTaggingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Must contain at least one Condition") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestAutoTaggingCreateHandler_DuplicateName400(t *testing.T) {
	t.Parallel()
	w := &fakeAutoTaggingWriter{rows: []*autotagging.Tag{{ID: 10, Name: "anime"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/autotagging", strings.NewReader(atBody("anime", true)))
	AutoTaggingCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Must be unique") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestAutoTaggingUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeAutoTaggingWriter{getRow: &autotagging.Tag{ID: 5, Name: "anime"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/autotagging/5", strings.NewReader(atBody("anime", true)))
	req.SetPathValue("id", "5")
	AutoTaggingUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updateCalledWith == nil || w.updateCalledWith.ID != 5 {
		t.Errorf("svc.Update args: %+v", w.updateCalledWith)
	}
}

func TestAutoTaggingUpdateHandler_UniquenessExcludesSelf(t *testing.T) {
	t.Parallel()
	w := &fakeAutoTaggingWriter{
		rows:   []*autotagging.Tag{{ID: 5, Name: "anime"}},
		getRow: &autotagging.Tag{ID: 5, Name: "anime"},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/autotagging/5", strings.NewReader(atBody("anime", true)))
	req.SetPathValue("id", "5")
	AutoTaggingUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202 (uniqueness must exclude self); body=%s", rr.Code, rr.Body.String())
	}
}

func TestAutoTaggingDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeAutoTaggingWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/autotagging/5", nil)
	req.SetPathValue("id", "5")
	AutoTaggingDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if w.deleteCalledID != 5 {
		t.Errorf("Delete called with %d", w.deleteCalledID)
	}
}

func TestAutoTaggingDeleteHandler_NotFound404(t *testing.T) {
	t.Parallel()
	w := &fakeAutoTaggingWriter{deleteErr: autotagging.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/autotagging/99", nil)
	req.SetPathValue("id", "99")
	AutoTaggingDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}
