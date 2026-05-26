package importlist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/importlists"
)

// ----- fakes (parallel to internal/api/v3 fakes, kept in-package so
// the v5 tests stand alone) -----

type fakeWriter struct {
	rows   []importlists.ImportList
	nextID int64
	getErr error
}

func (f *fakeWriter) Get(_ context.Context, id int) (*importlists.ImportList, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, importlists.ErrNotFound
}

func (f *fakeWriter) All(_ context.Context) ([]importlists.ImportList, error) {
	out := make([]importlists.ImportList, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeWriter) Insert(_ context.Context, n *importlists.ImportList) (*importlists.ImportList, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}

func (f *fakeWriter) Update(_ context.Context, n *importlists.ImportList) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return importlists.ErrNotFound
}

func (f *fakeWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return importlists.ErrNotFound
}

type fakeEmitter struct {
	deleted []apiv3.ImportListResource
	updated []apiv3.ImportListResource
	created []apiv3.ImportListResource
}

func (f *fakeEmitter) Created(_ context.Context, r apiv3.ImportListResource) error {
	f.created = append(f.created, r)
	return nil
}

func (f *fakeEmitter) Updated(_ context.Context, r apiv3.ImportListResource) error {
	f.updated = append(f.updated, r)
	return nil
}

func (f *fakeEmitter) Deleted(_ context.Context, r apiv3.ImportListResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

type fakeTester struct {
	failures []importlists.TestFailure
	err      error
}

func (f *fakeTester) Test(_ context.Context, _ *importlists.ImportList) ([]importlists.TestFailure, error) {
	return f.failures, f.err
}

func (f *fakeTester) RequestAction(_ context.Context, _ *importlists.ImportList, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// seedRow inserts a row directly into the in-memory writer so the test
// can exercise Delete + Bulk handlers without going through the
// validate+insert flow.
func seedRow(w *fakeWriter, id int, name string) {
	cc := "ImdbListSettings"
	w.rows = append(w.rows, importlists.ImportList{
		ID:             id,
		Name:           name,
		Implementation: "ImdbListImport",
		ConfigContract: &cc,
	})
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestDeleteHandler_NoContent204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	seedRow(w, 5, "imdb-top-250")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/importlist/5", nil)
	req.SetPathValue("id", "5")
	DeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204 (v5 DELETE diverges from v3 200); body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204 (v3 emits `{}`, v5 must NOT): got %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type should be unset on 204: got %q", ct)
	}
	if len(em.deleted) != 1 || em.deleted[0].ID != 5 {
		t.Errorf("Deleted broadcast: got %+v want [{ID:5}]", em.deleted)
	}
}

// TestDeleteHandler_MissingId204Idempotent — C.1.i-follow-4 (J232) propagation.
// v3.ImportListDeleteHandler is idempotent (200 + `{}` on missing id, matches C# v3).
// The v5 rewriteSuccessTo204 wrapper transforms that to 204 NoContent (matches C# v5
// ProviderControllerBase.DeleteProvider → TypedResults.NoContent). Idempotent on both
// surfaces — the missing-id status flips 200→204 across v3→v5 but neither is 404.
func TestDeleteHandler_MissingId204Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{} // empty — Delete returns ErrNotFound
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/importlist/99", nil)
	req.SetPathValue("id", "99")
	DeleteHandler(w, &fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (v5 idempotent via rewriteSuccessTo204 of v3's 200+`{}`)", rr.Code)
	}
}

func TestDeleteHandler_InvalidID400PassesThrough(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/importlist/abc", nil)
	req.SetPathValue("id", "abc")
	DeleteHandler(&fakeWriter{}, &fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- POST /test — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestTestHandler_NoContent204(t *testing.T) {
	t.Parallel()
	tester := &fakeTester{} // no failures, no error → happy path
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"x","implementation":"ImdbListImport","configContract":"ImdbListSettings","fields":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v5/importlist/test", body)
	TestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204 (v5 POST /test diverges from v3 200); body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204: got %q", rr.Body.String())
	}
}

func TestTestHandler_ValidationFailurePassesThrough(t *testing.T) {
	t.Parallel()
	tester := &fakeTester{failures: []importlists.TestFailure{
		{PropertyName: "Url", ErrorMessage: "is not valid"},
	}}
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"x","implementation":"ImdbListImport","configContract":"ImdbListSettings","fields":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v5/importlist/test", body)
	TestHandler(tester).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (validation failures bubble unchanged)", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Errorf("validation failure body should be populated, got empty")
	}
}

// ----- PUT /bulk — v5 returns 200 (v3 returns 202) -----

func TestBulkUpdateHandler_OK200(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	seedRow(w, 1, "a")
	seedRow(w, 2, "b")
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"ids":[1,2],"enableAutomaticAdd":true}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v5/importlist/bulk", body)
	BulkUpdateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (v5 PUT /bulk diverges from v3 202); body=%q", rr.Code, rr.Body.String())
	}
	respBody := rr.Body.String()
	for _, want := range []string{`"id":1`, `"id":2`} {
		if !strings.Contains(respBody, want) {
			t.Errorf("body missing %s: %s", want, respBody)
		}
	}
	if len(em.updated) != 2 {
		t.Errorf("Updated broadcasts: got %d want 2", len(em.updated))
	}
}

func TestBulkUpdateHandler_EmptyIds400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"ids":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v5/importlist/bulk", body)
	BulkUpdateHandler(&fakeWriter{}, &fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- DELETE /bulk — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestBulkDeleteHandler_NoContent204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	seedRow(w, 1, "a")
	seedRow(w, 2, "b")
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"ids":[1,2]}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/importlist/bulk", body)
	BulkDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204 (v5 DELETE /bulk diverges from v3 200); body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204 (v3 emits `{}`, v5 must NOT): got %q", rr.Body.String())
	}
	if len(em.deleted) != 2 {
		t.Errorf("Deleted broadcasts: got %d want 2", len(em.deleted))
	}
}

func TestBulkDeleteHandler_EmptyIds400PassesThrough(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"ids":[]}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/importlist/bulk", body)
	BulkDeleteHandler(&fakeWriter{}, &fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (empty-ids bubbles through unchanged)", rr.Code)
	}
}

// ----- Re-exported handlers smoke -----

func TestReexportedHandlers_NonNil(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	tester := &fakeTester{}
	cases := []struct {
		name string
		got  http.Handler
	}{
		{"List", ListHandler(w)},
		{"ByID", ByIDHandler(w)},
		{"Schema", SchemaHandler()},
		{"Create", CreateHandler(w, em)},
		{"Update", UpdateHandler(w, em)},
		{"TestAll", TestAllHandler(w, tester)},
		{"RequestAction", RequestActionHandler(tester)},
	}
	for _, tc := range cases {
		if tc.got == nil {
			t.Errorf("%s handler was nil — v3 re-export broken", tc.name)
		}
	}
}

// Response-rewriting middleware unit tests moved to
// internal/api/v5/internal/v5util/v5util_test.go at J256
// (consolidation). The wrappers are now exported as
// v5util.RewriteSuccessTo204 / RewriteAcceptedToOK and tested there.

// guard against an unused-import lint error: errors is imported by the
// fakeTester err path even though no test arm currently sets it.
var _ = errors.New
