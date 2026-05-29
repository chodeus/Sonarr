package v3

import (
	"context"
	"encoding/json"
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

// TestAutoTaggingDeleteHandler_Idempotent verifies C.1.i-follow-4
// alignment: live C# `DELETE /api/v3/autotagging/99999` returns 200
// (not 404) on a non-existent id. The Go-port mirrors this idempotent
// posture as of J232. The v5 sibling (internal/api/v5/autotagging
// DeleteHandler) uses TypedResults.NoContent semantics and returns 404
// — that's a deliberate v5-surface divergence pinned by the
// internal/api/v5/autotagging tests.
func TestAutoTaggingDeleteHandler_Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeAutoTaggingWriter{deleteErr: autotagging.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/autotagging/99", nil)
	req.SetPathValue("id", "99")
	AutoTaggingDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (v3 DELETE is idempotent — matches live C# v5-develop 7c41dcbe2)", rr.Code)
	}
}

// --- G4-followup autotagging (J433): write persists envelope shape +
// GET projects typed specs → full schema. Mirrors the customformat
// J431/J432 pair.

func atField(name string, value any) map[string]any {
	return map[string]any{"name": name, "value": value}
}

func TestAutoTaggingSpecsToEnvelopeJSON_RoundTripsThroughLoader(t *testing.T) {
	t.Parallel()
	specs := []AutoTaggingSpecificationSchema{
		{Name: "anime", Implementation: "SeriesTypeSpecification", Negate: true, Required: true,
			Fields: []any{atField("value", float64(2))}},
		{Name: "ended", Implementation: "StatusSpecification",
			Fields: []any{atField("status", float64(1))}},
		{Name: "genres", Implementation: "GenreSpecification",
			Fields: []any{atField("value", []any{"Animation", "Comedy"})}},
		{Name: "years", Implementation: "YearSpecification",
			Fields: []any{atField("min", float64(2010)), atField("max", float64(2020))}},
	}
	out, err := autoTaggingSpecsToEnvelopeJSON(specs)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	// The load path's exact call.
	typed, err := autotagging.UnmarshalSpecs([]byte(out))
	if err != nil {
		t.Fatalf("UnmarshalSpecs rejected persisted shape: %v\njson=%s", err, out)
	}
	if len(typed) != 4 {
		t.Fatalf("got %d specs, want 4", len(typed))
	}
	// StatusSpecification uses the "status" body key, not "value".
	if !strings.Contains(out, `"status":1`) {
		t.Errorf("status key not mapped: %s", out)
	}
	if !strings.Contains(out, `"min":2010`) || !strings.Contains(out, `"max":2020`) {
		t.Errorf("year min/max not mapped: %s", out)
	}
}

func TestAutoTaggingSpecsToEnvelopeJSON_EmptyAndUnknown(t *testing.T) {
	t.Parallel()
	got, err := autoTaggingSpecsToEnvelopeJSON(nil)
	if err != nil || got != "[]" {
		t.Fatalf("nil: %q %v", got, err)
	}
	_, err = autoTaggingSpecsToEnvelopeJSON([]AutoTaggingSpecificationSchema{
		{Name: "x", Implementation: "BogusSpecification", Fields: []any{atField("value", 1)}},
	})
	if err == nil {
		t.Fatal("expected error for unknown implementation")
	}
}

func TestToAutoTaggingResource_ProjectsTypedSpecs(t *testing.T) {
	t.Parallel()
	env := `[{"type":"StatusSpecification","body":{"name":"ended","negate":true,"required":false,"status":1}}]`
	typed, err := autotagging.UnmarshalSpecs([]byte(env))
	if err != nil {
		t.Fatal(err)
	}
	res := toAutoTaggingResource(&autotagging.Tag{ID: 3, Name: "a", Specifications: typed})
	if len(res.Specifications) != 1 {
		t.Fatalf("specs = %d", len(res.Specifications))
	}
	s := res.Specifications[0]
	if s.Implementation != "StatusSpecification" || s.ImplementationName != "Status" {
		t.Errorf("impl = %q/%q", s.Implementation, s.ImplementationName)
	}
	if s.Name != "ended" || !s.Negate {
		t.Errorf("base = %+v", s)
	}
	body, _ := json.Marshal(res)
	str := string(body)
	if !strings.Contains(str, `"value":1`) { // status field's value injected
		t.Errorf("status value not injected: %s", str)
	}
	// id=0 must be omitted on each spec.
	b2, _ := json.Marshal(s)
	if strings.Contains(string(b2), `"id"`) {
		t.Errorf("spec must omit id=0: %s", b2)
	}
}
