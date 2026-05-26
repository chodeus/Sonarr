package autotagging

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/autotagging"
)

// fakeWriter implements Writer (alias of apiv3.AutoTaggingWriter) for
// the v5 DeleteHandler — keeps the test surface narrow.
type fakeWriter struct {
	rowByID    map[int]*autotagging.Tag
	deleteErr  error
	deleteCall []int
}

func (f *fakeWriter) All(_ context.Context) ([]*autotagging.Tag, error) {
	out := make([]*autotagging.Tag, 0, len(f.rowByID))
	for _, v := range f.rowByID {
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeWriter) GetByID(_ context.Context, id int) (*autotagging.Tag, error) {
	if row, ok := f.rowByID[id]; ok {
		return row, nil
	}
	return nil, autotagging.ErrNotFound
}

func (f *fakeWriter) Insert(_ context.Context, row *autotagging.AutoTag) (*autotagging.Tag, error) {
	return &autotagging.Tag{ID: 999, Name: row.Name}, nil
}

func (f *fakeWriter) Update(_ context.Context, _ *autotagging.AutoTag) error {
	return nil
}

func (f *fakeWriter) Delete(_ context.Context, id int) error {
	f.deleteCall = append(f.deleteCall, id)
	return f.deleteErr
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200) -----

func TestDeleteHandler_NoContent204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rowByID: map[int]*autotagging.Tag{5: {ID: 5, Name: "x"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/autotagging/5", nil)
	req.SetPathValue("id", "5")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204 (v5 DELETE diverges from v3 200); body=%q", rr.Code, rr.Body.String())
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
	w := &fakeWriter{deleteErr: autotagging.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/autotagging/99", nil)
	req.SetPathValue("id", "99")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestDeleteHandler_InvalidID400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/autotagging/abc", nil)
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
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/autotagging/5", nil)
	req.SetPathValue("id", "5")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ----- Re-exported handlers smoke -----

func TestReexportedHandlers_NonNil(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	cases := []struct {
		name string
		got  http.Handler
	}{
		{"List", ListHandler(w)},
		{"ByID", ByIDHandler(w)},
		{"Schema", SchemaHandler()},
		{"Create", CreateHandler(w)},
		{"Update", UpdateHandler(w)},
	}
	for _, tc := range cases {
		if tc.got == nil {
			t.Errorf("%s handler was nil — v3 re-export broken", tc.name)
		}
	}
}
