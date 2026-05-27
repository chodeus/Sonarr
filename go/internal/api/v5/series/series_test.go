package series

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/series"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

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
	updated []Resource
}

func (f *fakeEmitter) Created(_ context.Context, _ Resource) error { return nil }
func (f *fakeEmitter) Updated(_ context.Context, r Resource) error {
	f.updated = append(f.updated, r)
	return nil
}
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

// ----- PUT /editor — v5 returns 200+body (v3 returns 202+body) -----

func TestEditorUpdateHandler_Success_Returns200WithBodyPreserved(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []series.Series{
		{ID: 3, Title: "A", Monitored: false},
		{ID: 5, Title: "B", Monitored: false},
	}}
	em := &fakeEmitter{}
	body := []byte(`{"seriesIds":[3,5],"monitored":true}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/editor", bytesReader(body))
	EditorUpdateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (v3 202 demoted to v5 200); body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() == 0 {
		t.Errorf("body must be preserved (updated-rows array); got empty")
	}
}

func TestEditorUpdateHandler_MalformedBody_PassesThrough400(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/editor", bytesReader([]byte(`{not-json`)))
	EditorUpdateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (passthrough)", rr.Code)
	}
}

// ----- DELETE /editor — v5 returns 204 (v3 returns 200+`{}`) -----

func TestEditorDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []series.Series{{ID: 7, Title: "X"}, {ID: 9, Title: "Y"}}}
	em := &fakeEmitter{}
	body := []byte(`{"seriesIds":[7,9]}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/series/editor", bytesReader(body))
	EditorDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if len(em.deleted) != 2 {
		t.Errorf("SignalR Deleted should fire per matched row; got %d emits", len(em.deleted))
	}
}

func TestEditorDeleteHandler_MalformedBody_PassesThrough400(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/series/editor", bytesReader([]byte(`{not`)))
	EditorDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (passthrough)", rr.Code)
	}
}

// ----- PUT /{id}/season — v5-NEW per-id season-monitor endpoint -----

func TestSeasonMonitorHandler_Success_Returns200WithBodyAndUpdatesSeasonsJSON(t *testing.T) {
	t.Parallel()
	// Seed a series with a 3-season JSON blob, MIXED monitored states +
	// a richer per-season `images` payload that must round-trip
	// unchanged (verifies the []map[string]any preservation contract).
	seasonsBlob := `[{"seasonNumber":1,"monitored":true,"images":[{"coverType":"poster","url":"a"}]},{"seasonNumber":2,"monitored":true,"images":[]},{"seasonNumber":3,"monitored":true,"images":[]}]`
	w := &fakeWriter{rows: []series.Series{{ID: 11, Title: "ShowA", Seasons: seasonsBlob}}}
	em := &fakeEmitter{}
	body := []byte(`{"seasonNumber":2,"monitored":false}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/11/season", bytesReader(body))
	req.SetPathValue("id", "11")
	SeasonMonitorHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	// Wire shape: ONLY seasonNumber + monitored (matches C# season.ToResource(false)).
	got := rr.Body.String()
	if !strings.Contains(got, `"seasonNumber":2`) || !strings.Contains(got, `"monitored":false`) {
		t.Errorf("body must contain seasonNumber:2 + monitored:false; got %q", got)
	}
	if strings.Contains(got, `"images"`) {
		t.Errorf("body must NOT contain images key (includeImages=false posture); got %q", got)
	}
	if strings.Contains(got, `"statistics"`) {
		t.Errorf("body must NOT contain statistics key (deferred); got %q", got)
	}
	// Persisted Seasons JSON must have season 2 monitored=false; seasons 1+3 unchanged + images[] preserved.
	if len(w.rows) != 1 {
		t.Fatalf("rows: %v", w.rows)
	}
	persisted := w.rows[0].Seasons
	// Note: Go's `json.Marshal` on `map[string]any` reorders keys
	// alphabetically — substring search avoids depending on key order.
	if !strings.Contains(persisted, `"monitored":false`) || !strings.Contains(persisted, `"seasonNumber":2`) {
		t.Errorf("season 2 should be monitored:false; got %s", persisted)
	}
	// Defensive guard: seasons 1 + 3 must NOT have flipped to monitored:false.
	// Match the substring for `"seasonNumber":1` adjacent to `"monitored":true`
	// in any key order.
	if strings.Count(persisted, `"monitored":true`) != 2 {
		t.Errorf("seasons 1 + 3 must remain monitored:true; got %s", persisted)
	}
	if !strings.Contains(persisted, `"coverType":"poster"`) {
		t.Errorf("season 1 images must round-trip; got %s", persisted)
	}
	// SignalR Updated broadcast fires with the full series resource.
	if len(em.updated) != 1 || em.updated[0].ID != 11 {
		t.Errorf("SignalR Updated: got %+v want 1 broadcast for series 11", em.updated)
	}
}

func TestSeasonMonitorHandler_SeriesNotFound_404(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	em := &fakeEmitter{}
	body := []byte(`{"seasonNumber":1,"monitored":true}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/99/season", bytesReader(body))
	req.SetPathValue("id", "99")
	SeasonMonitorHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (series miss)", rr.Code)
	}
	if len(em.updated) != 0 {
		t.Errorf("SignalR Updated must not fire on series-miss; got %v", em.updated)
	}
}

func TestSeasonMonitorHandler_SeasonNumberNotFound_404(t *testing.T) {
	t.Parallel()
	// Series exists but the requested seasonNumber isn't in the Seasons
	// JSON — matches C# `series.Seasons.FirstOrDefault(...) == null`
	// surface (TypedResults.NotFound at SeriesController.cs:235).
	seasonsBlob := `[{"seasonNumber":1,"monitored":true}]`
	w := &fakeWriter{rows: []series.Series{{ID: 11, Title: "X", Seasons: seasonsBlob}}}
	em := &fakeEmitter{}
	body := []byte(`{"seasonNumber":99,"monitored":true}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/11/season", bytesReader(body))
	req.SetPathValue("id", "11")
	SeasonMonitorHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (season miss)", rr.Code)
	}
	// Persisted Seasons JSON must be UNCHANGED on season-miss (no half-mutate).
	if w.rows[0].Seasons != seasonsBlob {
		t.Errorf("Seasons JSON must NOT mutate on season-miss; got %s want %s", w.rows[0].Seasons, seasonsBlob)
	}
}

func TestSeasonMonitorHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []series.Series{{ID: 11, Title: "X"}}}
	em := &fakeEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/11/season", bytesReader([]byte(`{not`)))
	req.SetPathValue("id", "11")
	SeasonMonitorHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (malformed body)", rr.Code)
	}
}

func TestSeasonMonitorHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/abc/season", bytesReader([]byte(`{"seasonNumber":1,"monitored":true}`)))
	req.SetPathValue("id", "abc")
	SeasonMonitorHandler(&fakeWriter{}, &fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (invalid id)", rr.Code)
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
	var _ EditorResource = apiv3.SeriesEditorResource{}
	var _ apiv3.SeriesEditorResource = EditorResource{}
}
