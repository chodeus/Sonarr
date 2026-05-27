package series

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/series"
)

// fakeWriter implements Writer (alias of apiv3.SeriesWriter) — the
// minimum surface DeleteHandler needs. Mirrors v3's fakeSeriesWriter
// pattern.
type fakeWriter struct {
	rows   []series.Series
	nextID int64
}

func (f *fakeWriter) GetSeries(_ context.Context, id int) (*series.Series, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, series.ErrNotFound
}

func (f *fakeWriter) GetAllSeries(_ context.Context) ([]series.Series, error) {
	out := make([]series.Series, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeWriter) Insert(_ context.Context, n *series.Series) (*series.Series, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}

func (f *fakeWriter) Update(_ context.Context, n *series.Series) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return series.ErrNotFound
}

func (f *fakeWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return series.ErrNotFound
}

type fakeEmitter struct {
	deleted []Resource
}

func (f *fakeEmitter) Created(_ context.Context, _ Resource) error { return nil }
func (f *fakeEmitter) Updated(_ context.Context, _ Resource) error { return nil }
func (f *fakeEmitter) Deleted(_ context.Context, r Resource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []series.Series{{ID: 7, Title: "Doctor Who"}}}
	em := &fakeEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/series/7", nil)
	req.SetPathValue("id", "7")
	DeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type should be stripped for 204; got %q", ct)
	}
	if len(em.deleted) != 1 || em.deleted[0].ID != 7 {
		t.Errorf("SignalR Deleted broadcast missing or wrong id: %+v", em.deleted)
	}
}

func TestDeleteHandler_NotFound_PassesThrough404(t *testing.T) {
	t.Parallel()
	// Series DELETE is NOT in the J232 idempotency-aligned set —
	// v3 returns 404 explicitly (mirroring C# `_seriesService
	// .DeleteSeries` throwing ModelNotFoundException → 404 via
	// global exception filter). The v5 wrapper preserves 404 verbatim.
	w := &fakeWriter{}
	em := &fakeEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/series/999", nil)
	req.SetPathValue("id", "999")
	DeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (passthrough; series DELETE non-idempotent)", rr.Code)
	}
	if len(em.deleted) != 0 {
		t.Errorf("SignalR Deleted must not fire on missing-id: got %d emits", len(em.deleted))
	}
}

// ----- type aliases (compile-time invariant guard) -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.SeriesResource{}
	var _ apiv3.SeriesResource = Resource{}
	var _ Lister = (apiv3.SeriesLister)(nil)
	var _ ByIDGetter = (apiv3.SeriesByIDGetter)(nil)
	var _ Writer = (apiv3.SeriesWriter)(nil)
	var _ Emitter = (apiv3.SeriesEmitter)(nil)
}
