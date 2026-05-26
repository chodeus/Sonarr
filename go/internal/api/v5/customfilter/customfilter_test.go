package customfilter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/customfilters"
)

// fakeWriter implements Writer (alias of apiv3.CustomFilterWriter)
// for DeleteHandler test coverage.
type fakeWriter struct {
	rows      []customfilters.Filter
	deleteErr error
	deletes   atomic.Int64
}

func (f *fakeWriter) Get(_ context.Context, id int) (*customfilters.Filter, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, customfilters.ErrNotFound
}

func (f *fakeWriter) Add(_ context.Context, n *customfilters.Filter) (*customfilters.Filter, error) {
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}

func (f *fakeWriter) Update(_ context.Context, n *customfilters.Filter) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return customfilters.ErrNotFound
}

func (f *fakeWriter) Delete(_ context.Context, id int) error {
	f.deletes.Add(1)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return customfilters.ErrNotFound
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200+empty) -----

func TestDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []customfilters.Filter{{ID: 42, Label: "x"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customfilter/42", nil)
	req.SetPathValue("id", "42")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type should be stripped for 204; got %q", ct)
	}
	if w.deletes.Load() != 1 {
		t.Errorf("svc.Delete should be called once; got %d", w.deletes.Load())
	}
}

func TestDeleteHandler_MissingID_Returns204Idempotent(t *testing.T) {
	t.Parallel()
	// J232 idempotency carry-through: v3 returns 200 + empty body on
	// missing id (DELETE signals "deleted or already gone"); v5 wrapper
	// demotes to 204. svc.Delete still runs (the v3 handler doesn't
	// pre-check existence — it lets the service Delete return
	// ErrNotFound which is then swallowed at the handler level).
	w := &fakeWriter{} // no rows
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/customfilter/999", nil)
	req.SetPathValue("id", "999")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (idempotent missing-id)", rr.Code)
	}
}

// ----- Other handlers are pure v3 re-exports (variable aliases) —
// the only assertion needed here is that the alias is non-nil. The
// v3 tests at internal/api/v3/customfilter_test.go cover the actual
// behaviour. -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  any
	}{
		{"ListHandler", ListHandler},
		{"ByIDHandler", ByIDHandler},
		{"CreateHandler", CreateHandler},
		{"UpdateHandler", UpdateHandler},
	}
	for _, tc := range cases {
		if tc.got == nil {
			t.Errorf("%s alias is nil — v3 re-export broken", tc.name)
		}
	}
}

// TestTypeAliasesMatchV3 — compile-time invariant guard per the
// J251-pattern. If a future slice forks the Resource type away from
// v3, this assignment fails and the build breaks.
func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.CustomFilterResource{}
	var _ apiv3.CustomFilterResource = Resource{}
}
