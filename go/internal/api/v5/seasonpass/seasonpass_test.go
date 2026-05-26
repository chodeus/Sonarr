package seasonpass

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/series"
)

// fakeSeriesWriter implements SeriesWriter (alias of apiv3.SeriesWriter)
// — narrow surface for the SeasonPass test (Get + Update only paths
// exercised; Insert/Delete unused but present for interface satisfaction).
type fakeSeriesWriter struct {
	rows []series.Series
}

func (f *fakeSeriesWriter) GetSeries(_ context.Context, id int) (*series.Series, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, series.ErrNotFound
}

func (f *fakeSeriesWriter) GetAllSeries(_ context.Context) ([]series.Series, error) {
	out := make([]series.Series, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeSeriesWriter) Insert(_ context.Context, n *series.Series) (*series.Series, error) {
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}

func (f *fakeSeriesWriter) Update(_ context.Context, n *series.Series) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return series.ErrNotFound
}

func (f *fakeSeriesWriter) Delete(_ context.Context, _ int) error {
	return nil
}

// fakeSeriesEmitter implements SeriesEmitter — captures Updated broadcasts.
type fakeSeriesEmitter struct {
	updated []apiv3.SeriesResource
}

func (f *fakeSeriesEmitter) Created(_ context.Context, _ apiv3.SeriesResource) error { return nil }
func (f *fakeSeriesEmitter) Updated(_ context.Context, r apiv3.SeriesResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeSeriesEmitter) Deleted(_ context.Context, _ apiv3.SeriesResource) error { return nil }

// ----- POST — v5 returns 204 (v3 returns 202 + `{}`) -----

func TestPostHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesWriter{rows: []series.Series{{ID: 42, Title: "Show A", Monitored: true}}}
	em := &fakeSeriesEmitter{}
	body := Resource{
		Series: []SeriesResource{
			{ID: 42, Monitored: ptrBool(false)},
		},
	}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/seasonpass", bytes.NewReader(buf))
	PostHandler(w, em, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type should be stripped for 204; got %q", ct)
	}
	if len(em.updated) != 1 {
		t.Errorf("SignalR Updated should fire on actual update; got %d emits", len(em.updated))
	}
	// Verify Monitored mutation actually landed.
	got, _ := w.GetSeries(context.Background(), 42)
	if got.Monitored {
		t.Errorf("series Monitored not toggled to false (cascade through v3 handler failed)")
	}
}

func TestPostHandler_MalformedBody_PassesThrough400(t *testing.T) {
	t.Parallel()
	// The wrapper only rewrites 202+`{}` → 204; other shapes pass through.
	// A malformed body emits 400+envelope from the v3 handler; the v5
	// wrapper should preserve that verbatim.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/seasonpass", bytes.NewReader([]byte(`{not json`)))
	PostHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (malformed body passthrough)", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Error("error envelope should NOT be stripped on 400 passthrough")
	}
}

func TestPostHandler_MissingSeries_PassesThrough404(t *testing.T) {
	t.Parallel()
	// v3 handler returns 404 when a series id doesn't exist; wrapper
	// must pass through verbatim.
	w := &fakeSeriesWriter{} // empty — no series at all
	body := Resource{Series: []SeriesResource{{ID: 999}}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/seasonpass", bytes.NewReader(buf))
	PostHandler(w, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (missing series passthrough)", rr.Code)
	}
}

// ----- type aliases (compile-time invariant guard) -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.SeasonPassResource{}
	var _ SeriesResource = apiv3.SeasonPassSeriesResource{}
	var _ SeasonOnWrite = apiv3.SeasonPassSeasonOnWrite{}
	var _ MonitoringOptions = apiv3.MonitoringOptions{}
	var _ apiv3.SeasonPassResource = Resource{}
}

func ptrBool(b bool) *bool { return &b }
