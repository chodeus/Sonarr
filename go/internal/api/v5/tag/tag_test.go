package tag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/tags"
)

// fakeWriter implements Writer.
type fakeWriter struct {
	rows    []tags.Tag
	deletes atomic.Int64
}

func (f *fakeWriter) GetByID(_ context.Context, id int) (*tags.Tag, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, tags.ErrNotFound
}

func (f *fakeWriter) Add(_ context.Context, t *tags.Tag) (*tags.Tag, error) {
	t.ID = len(f.rows) + 1
	f.rows = append(f.rows, *t)
	cp := *t
	return &cp, nil
}

func (f *fakeWriter) Update(_ context.Context, t *tags.Tag) error {
	for i := range f.rows {
		if f.rows[i].ID == t.ID {
			f.rows[i] = *t
			return nil
		}
	}
	return tags.ErrNotFound
}

func (f *fakeWriter) Delete(_ context.Context, id int) error {
	f.deletes.Add(1)
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return tags.ErrNotFound
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + empty) -----

func TestDeleteHandler_Rewrites200To204(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []tags.Tag{{ID: 42, Label: "x"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/tag/42", nil)
	req.SetPathValue("id", "42")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if w.deletes.Load() != 1 {
		t.Errorf("svc.Delete should be called once; got %d", w.deletes.Load())
	}
}

func TestDeleteHandler_MissingID_Returns404Passthrough(t *testing.T) {
	t.Parallel()
	// v3 TagDeleteHandler returns 404 (NOT the J232 idempotent 200 —
	// the v3 tag handler chose strict semantics per
	// `internal/api/v3/tag.go:222-225`). The wrapper's RewriteSuccessTo204
	// matches ONLY 200 responses; 404 passes through unchanged.
	w := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/tag/9999", nil)
	req.SetPathValue("id", "9999")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (passthrough); body=%q", rr.Code, rr.Body.String())
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if ListHandler == nil || ByIDHandler == nil {
		t.Error("list/byid handler aliases must be non-nil")
	}
	if CreateHandler == nil || UpdateHandler == nil {
		t.Error("create/update handler aliases must be non-nil")
	}
	if DetailListHandler == nil || DetailByIDHandler == nil {
		t.Error("detail handler aliases must be non-nil")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	// Pure-alias slice: any drift in Resource/DetailsResource shape
	// between v3 and v5 would fail to compile.
	var _ Resource = apiv3.TagResource{}
	var _ DetailsResource = apiv3.TagDetailsResource{}
	var _ apiv3.TagResource = Resource{}
	var _ apiv3.TagDetailsResource = DetailsResource{}
}
