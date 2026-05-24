package v3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/episodemonitored"
	"sonarr.tv/sonarr/internal/series"
)

func boolPtrSP(b bool) *bool { return &b }

func TestSeasonPassHandler_MonitoredToggle(t *testing.T) {
	t.Parallel()
	rows := []series.Series{
		{ID: 1, Monitored: true},
		{ID: 2, Monitored: true},
	}
	w := &fakeSeriesWriter{rows: rows}
	em := &fakeSeriesEmitter{}
	body := SeasonPassResource{Series: []SeasonPassSeriesResource{
		{ID: 1, Monitored: boolPtrSP(false)},
		{ID: 2, Monitored: boolPtrSP(false)},
	}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, em, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if w.rows[0].Monitored || w.rows[1].Monitored {
		t.Errorf("Monitored not toggled: %+v", w.rows)
	}
	if len(em.updated) != 2 {
		t.Errorf("Updated count: %d", len(em.updated))
	}
}

func TestSeasonPassHandler_MonitoringOptionsNone_ForcesMonitoredFalse(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, Monitored: true}}
	w := &fakeSeriesWriter{rows: rows}
	body := SeasonPassResource{
		Series:            []SeasonPassSeriesResource{{ID: 1}},
		MonitoringOptions: &MonitoringOptions{Monitor: "None"},
	}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Monitored {
		t.Errorf("Monitored should be false (MonitoringOptions.Monitor=None): %+v", w.rows[0])
	}
}

func TestSeasonPassHandler_SeasonsToggleExistingJSON(t *testing.T) {
	t.Parallel()
	// Existing row has 3 seasons. The body toggles season 2.
	existingSeasons := `[{"seasonNumber":1,"monitored":true,"statistics":{"episodeCount":10}},{"seasonNumber":2,"monitored":true},{"seasonNumber":3,"monitored":false}]`
	rows := []series.Series{{ID: 1, Seasons: existingSeasons}}
	w := &fakeSeriesWriter{rows: rows}
	body := SeasonPassResource{Series: []SeasonPassSeriesResource{
		{ID: 1, Seasons: []SeasonPassSeasonOnWrite{
			{SeasonNumber: 2, Monitored: false},
		}},
	}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(w.rows[0].Seasons), &got); err != nil {
		t.Fatalf("seasons JSON: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("seasons len: %d", len(got))
	}
	// Season 1 untouched (monitored true preserved + statistics preserved).
	if got[0]["monitored"] != true || got[0]["statistics"] == nil {
		t.Errorf("season 1 should be preserved: %+v", got[0])
	}
	// Season 2 toggled to false.
	if got[1]["monitored"] != false {
		t.Errorf("season 2 should be false: %+v", got[1])
	}
	// Season 3 untouched.
	if got[2]["monitored"] != false {
		t.Errorf("season 3 should still be false: %+v", got[2])
	}
}

func TestSeasonPassHandler_SeasonsSynthesizedWhenEmptyExistingJSON(t *testing.T) {
	t.Parallel()
	// Empty existing Seasons — the synth path should populate from
	// the body for the first-toggle case.
	rows := []series.Series{{ID: 1, Seasons: ""}}
	w := &fakeSeriesWriter{rows: rows}
	body := SeasonPassResource{Series: []SeasonPassSeriesResource{
		{ID: 1, Seasons: []SeasonPassSeasonOnWrite{
			{SeasonNumber: 1, Monitored: true},
		}},
	}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Seasons == "" {
		t.Errorf("Seasons should be synthesised from body")
	}
	var got []map[string]any
	_ = json.Unmarshal([]byte(w.rows[0].Seasons), &got)
	if len(got) != 1 || got[0]["seasonNumber"] != float64(1) || got[0]["monitored"] != true {
		t.Errorf("synthesised seasons: %+v", got)
	}
}

func TestSeasonPassHandler_NilMonitoredPreserves(t *testing.T) {
	t.Parallel()
	// Body has no Monitored override and no MonitoringOptions, just
	// the empty body row → series.Monitored should be preserved.
	rows := []series.Series{{ID: 1, Monitored: true}}
	w := &fakeSeriesWriter{rows: rows}
	body := SeasonPassResource{Series: []SeasonPassSeriesResource{{ID: 1}}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if !w.rows[0].Monitored {
		t.Errorf("Monitored should be preserved: %+v", w.rows[0])
	}
}

func TestSeasonPassHandler_EmptySeriesArray_400(t *testing.T) {
	t.Parallel()
	body := SeasonPassResource{Series: nil}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeasonPassHandler_MissingSeries_404_NoWrites(t *testing.T) {
	t.Parallel()
	// One id exists, another doesn't — the C# `SingleOrDefault`
	// throws on miss; the Go port pre-validates all ids exist
	// before any write so a partial-write can't happen.
	rows := []series.Series{{ID: 1, Monitored: true}}
	w := &fakeSeriesWriter{rows: rows}
	body := SeasonPassResource{Series: []SeasonPassSeriesResource{
		{ID: 1, Monitored: boolPtrSP(false)},
		{ID: 999, Monitored: boolPtrSP(false)},
	}}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
	// Row 1 must NOT have been mutated (pre-validation aborts the batch).
	if !w.rows[0].Monitored {
		t.Errorf("row 1 should NOT have been written when row 999 misses: %+v", w.rows[0])
	}
}

func TestSeasonPassHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeasonPassHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---- J214 — cascader-wired path tests ----

type fakeCascader struct {
	calls []fakeCascaderCall
	err   error
}

type fakeCascaderCall struct {
	seriesID int
	monitor  series.MonitorType
}

func (c *fakeCascader) SetEpisodeMonitoredStatus(_ context.Context, ser *series.Series, opts *episodemonitored.MonitoringOptions) error {
	if c.err != nil {
		return c.err
	}
	c.calls = append(c.calls, fakeCascaderCall{seriesID: ser.ID, monitor: opts.Monitor})
	return nil
}

// TestSeasonPassHandler_CascaderWired_RoutesThroughCascader verifies J214's
// happy path: when both a cascader is wired AND the body has
// MonitoringOptions, the per-series persist is delegated to
// cascader.SetEpisodeMonitoredStatus rather than svc.Update.
func TestSeasonPassHandler_CascaderWired_RoutesThroughCascader(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, Monitored: true}}
	w := &fakeSeriesWriter{rows: rows}
	c := &fakeCascader{}
	body := SeasonPassResource{
		Series:            []SeasonPassSeriesResource{{ID: 1}},
		MonitoringOptions: &MonitoringOptions{Monitor: "all"},
	}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, &fakeSeriesEmitter{}, c).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(c.calls) != 1 {
		t.Fatalf("cascader calls: got %d, want 1", len(c.calls))
	}
	if c.calls[0].seriesID != 1 || c.calls[0].monitor != series.MonitorAll {
		t.Errorf("cascader call: got %+v, want {1, MonitorAll}", c.calls[0])
	}
}

// TestSeasonPassHandler_CascaderWired_NoMonitoringOptions_FallsBackToUpdate
// verifies the fallback: when the body has NO MonitoringOptions, the
// cascader is NOT invoked (svc.Update fires through the existing path
// since there's no Monitor option to cascade).
func TestSeasonPassHandler_CascaderWired_NoMonitoringOptions_FallsBackToUpdate(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, Monitored: true}}
	w := &fakeSeriesWriter{rows: rows}
	c := &fakeCascader{}
	body := SeasonPassResource{
		Series: []SeasonPassSeriesResource{{ID: 1, Monitored: boolPtrSP(false)}},
	}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, &fakeSeriesEmitter{}, c).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(c.calls) != 0 {
		t.Errorf("cascader must NOT fire without MonitoringOptions; got %d calls", len(c.calls))
	}
	// fakeSeriesWriter.Update mutated the row (the fallback path).
	if w.rows[0].Monitored {
		t.Errorf("fallback path: svc.Update should have flipped Monitored to false")
	}
}

// TestSeasonPassHandler_CascaderError_Propagates500 verifies that a
// cascader failure surfaces as a 500 rather than silently swallowing.
func TestSeasonPassHandler_CascaderError_Propagates500(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, Monitored: true}}
	w := &fakeSeriesWriter{rows: rows}
	c := &fakeCascader{err: errors.New("DB down")}
	body := SeasonPassResource{
		Series:            []SeasonPassSeriesResource{{ID: 1}},
		MonitoringOptions: &MonitoringOptions{Monitor: "missing"},
	}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeasonPassHandler(w, &fakeSeriesEmitter{}, c).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/seasonpass", bytes.NewReader(buf)))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d, want 500", rr.Code)
	}
}
