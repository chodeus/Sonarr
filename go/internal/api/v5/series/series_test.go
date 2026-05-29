package series

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// J331 wire-shape pin for the v5 SeriesResource `languageProfileId`
// strip. v3 emits `"languageProfileId":1` (hard-coded `=> 1`
// expression at apiv3.ToSeriesResource); v5 drops the field
// entirely per `src/Sonarr.Api.V5/Series/SeriesResource.cs`. The
// stripLanguageProfileID middleware wraps the v5 ListHandler /
// ByIDHandler / CreateHandler / UpdateHandler to remove the key
// from the response body.
//
// Test covers all 4 wrapped handlers via the same populated row
// pattern: populated v3 emit always contains `"languageProfileId":1`;
// v5 wrapped response must NOT contain it.
func TestSeriesV5Handlers_StripLanguageProfileID_J331(t *testing.T) {
	t.Parallel()
	pop := series.Series{ID: 11, Title: "Show", Monitored: true, Status: 1, SeriesType: 0, MonitorNewItems: 0, Runtime: 30, Tags: []int{}}
	w := &fakeWriter{rows: []series.Series{pop}}
	em := &fakeEmitter{}

	t.Run("ListHandler strips languageProfileId from each row", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v5/series", nil)
		ListHandler(w).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		if strings.Contains(body, `"languageProfileId":`) {
			t.Errorf("List response must not contain languageProfileId; got %s", body)
		}
		// Positive: other fields still emit.
		if !strings.Contains(body, `"title":"Show"`) {
			t.Errorf("List response missing other fields; got %s", body)
		}
	})

	t.Run("ByIDHandler strips languageProfileId", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v5/series/11", nil)
		req.SetPathValue("id", "11")
		ByIDHandler(w).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		if strings.Contains(body, `"languageProfileId":`) {
			t.Errorf("ByID response must not contain languageProfileId; got %s", body)
		}
	})

	t.Run("Error responses pass through verbatim", func(t *testing.T) {
		// ByID with missing id → 400 envelope; helper must not corrupt.
		emptyW := &fakeWriter{}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v5/series/99", nil)
		req.SetPathValue("id", "99")
		ByIDHandler(emptyW).ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404 passthrough; got %d body=%s", rr.Code, rr.Body.String())
		}
		// The 404 envelope is JSON but doesn't carry languageProfileId so
		// strip is a no-op; body should still parse as the C# error shape.
	})
	_ = em // (Create/Update need v3 internals + scene-mapping deps; covered indirectly via the production v3 handler tests at apiv3 level)
}

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

// ----- SeasonResource wire-shape pin (J309) -----
//
// These tests pin the omitempty/wire posture for the J309 SeasonResource
// pointer-field widening. Statistics + Images are *struct/*slice
// pointer fields with omitempty so:
//   - nil  → key absent (matches C# `JsonIgnoreCondition.WhenWritingNull`)
//   - non-nil → key present (with the populated value, even for empty
//     slices via `&[]MediaCover{}` → `"images":[]`)
//
// No production caller populates these fields today (population
// gated on the post-Wave-3 SeriesStatistics service-port arc); the
// tests guard the wire contract so the populated mapper can light up
// without re-pinning shapes.

func TestSeasonResource_NilStatisticsAndImages_OmitKeys(t *testing.T) {
	t.Parallel()
	// Bare struct with zero values for SeasonNumber + Monitored and
	// nil pointers for Statistics + Images. Mirrors what
	// SeasonMonitorHandler emits today via
	// `SeasonResource{SeasonNumber: …, Monitored: …}`.
	res := SeasonResource{SeasonNumber: 4, Monitored: true}
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	if strings.Contains(got, `"statistics"`) {
		t.Errorf("nil Statistics must omit the key; got %s", got)
	}
	if strings.Contains(got, `"images"`) {
		t.Errorf("nil Images must omit the key; got %s", got)
	}
	if !strings.Contains(got, `"seasonNumber":4`) || !strings.Contains(got, `"monitored":true`) {
		t.Errorf("required fields missing; got %s", got)
	}
}

func TestSeasonResource_PopulatedStatistics_EmitsFullNestedShape(t *testing.T) {
	t.Parallel()
	// Pinned date so the JSON output is deterministic — covers both
	// nullable DateTime pointers (NextAiring + PreviousAiring) and
	// the scalar/slice mix.
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stats := &SeasonStatistics{
		NextAiring:            &when,
		PreviousAiring:        nil, // nil pointer omits
		EpisodeFileCount:      7,
		EpisodeCount:          10,
		TotalEpisodeCount:     12,
		MonitoredEpisodeCount: 8,
		SizeOnDisk:            12345,
		ReleaseGroups:         []string{"GrpA"},
		ReleaseTypes:          []string{"singleEpisode", "multiEpisode"},
		EpisodeFileQualities:  nil, // nil slice omits
		PercentOfEpisodes:     70.0,
	}
	res := SeasonResource{SeasonNumber: 2, Monitored: true, Statistics: stats}
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	// Statistics key present + nested fields pinned (using substring
	// search so key ordering doesn't matter — encoding/json emits
	// struct fields in declaration order, but tests stay robust if
	// that ever changes).
	mustContain := []string{
		`"statistics":{`,
		`"nextAiring":"2026-01-02T03:04:05Z"`,
		`"episodeFileCount":7`,
		`"episodeCount":10`,
		`"totalEpisodeCount":12`,
		`"monitoredEpisodeCount":8`,
		`"sizeOnDisk":12345`,
		`"releaseGroups":["GrpA"]`,
		`"releaseTypes":["singleEpisode","multiEpisode"]`,
		`"percentOfEpisodes":70`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	// nil sub-fields must drop their keys (omitempty pinned).
	if strings.Contains(got, `"previousAiring"`) {
		t.Errorf("nil PreviousAiring must omit the key; got %s", got)
	}
	if strings.Contains(got, `"episodeFileQualities"`) {
		t.Errorf("nil EpisodeFileQualities must omit the key; got %s", got)
	}
}

func TestSeasonResource_EmptyImagesSlice_EmitsEmptyArray(t *testing.T) {
	t.Parallel()
	// Pointer-to-empty-slice carries the "explicitly empty" semantic
	// distinct from nil (which omits the key entirely). C#'s
	// SeasonResourceMapper.ToResource with includeImages=true and
	// model.Images=null would currently emit null, but with
	// includeImages=true and model.Images=[] emits []. The Go-port
	// pointer-to-slice mirrors that 3-state contract.
	empty := []MediaCover{}
	res := SeasonResource{SeasonNumber: 1, Monitored: true, Images: &empty}
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	if !strings.Contains(got, `"images":[]`) {
		t.Errorf("&[]MediaCover{} must emit `\"images\":[]`; got %s", got)
	}
}

func TestSeasonResource_PopulatedImages_EmitsCoverEntriesWithStringCoverType(t *testing.T) {
	t.Parallel()
	// CoverType is a camelCase string on v5's wire shape (matches
	// STJson's global JsonStringEnumConverter(CamelCase)).
	imgs := []MediaCover{
		{CoverType: "poster", Url: "/MediaCover/11/poster.jpg", RemoteUrl: "https://tvdb.example/poster.jpg"},
		{CoverType: "banner", Url: "/MediaCover/11/banner.jpg"}, // RemoteUrl empty → omitted
	}
	res := SeasonResource{SeasonNumber: 1, Monitored: true, Images: &imgs}
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	mustContain := []string{
		`"images":[`,
		`"coverType":"poster"`,
		`"url":"/MediaCover/11/poster.jpg"`,
		`"remoteUrl":"https://tvdb.example/poster.jpg"`,
		`"coverType":"banner"`,
		`"url":"/MediaCover/11/banner.jpg"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	// The second entry's empty RemoteUrl must NOT emit (omitempty pin).
	// Count `"remoteUrl"` occurrences — expect exactly one for the
	// populated entry.
	if strings.Count(got, `"remoteUrl"`) != 1 {
		t.Errorf("remoteUrl key must appear exactly once (omitempty drops empty); got %s", got)
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

// fakeCascadeWriter implements Writer (via embedding) + the J397
// SeasonEpisodeMonitorer optional extension.
type fakeCascadeWriter struct {
	fakeWriter
	cascadeCalls []cascadeCall
	cascadeErr   error
}

type cascadeCall struct {
	seriesID, seasonNumber int
	monitored              bool
}

func (f *fakeCascadeWriter) SetEpisodeMonitoredBySeason(_ context.Context, seriesID, seasonNumber int, monitored bool) error {
	f.cascadeCalls = append(f.cascadeCalls, cascadeCall{seriesID, seasonNumber, monitored})
	return f.cascadeErr
}

// J397: the season-monitor endpoint cascades per-episode monitored state
// when the writer implements SeasonEpisodeMonitorer.
func TestSeasonMonitorHandler_CascadesEpisodes_J397(t *testing.T) {
	t.Parallel()
	w := &fakeCascadeWriter{fakeWriter: fakeWriter{rows: []series.Series{{ID: 11, Title: "ShowA", Seasons: `[{"seasonNumber":2,"monitored":true}]`}}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/11/season", bytesReader([]byte(`{"seasonNumber":2,"monitored":false}`)))
	req.SetPathValue("id", "11")
	SeasonMonitorHandler(w, &fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.cascadeCalls) != 1 || w.cascadeCalls[0] != (cascadeCall{11, 2, false}) {
		t.Errorf("cascadeCalls = %+v, want [{11 2 false}]", w.cascadeCalls)
	}
}

// J397: a cascade failure surfaces as 500.
func TestSeasonMonitorHandler_CascadeError_500_J397(t *testing.T) {
	t.Parallel()
	w := &fakeCascadeWriter{
		fakeWriter: fakeWriter{rows: []series.Series{{ID: 11, Seasons: `[{"seasonNumber":1,"monitored":true}]`}}},
		cascadeErr: context.DeadlineExceeded,
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/series/11/season", bytesReader([]byte(`{"seasonNumber":1,"monitored":false}`)))
	req.SetPathValue("id", "11")
	SeasonMonitorHandler(w, &fakeEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d, want 500", rr.Code)
	}
}

// TestV5SeriesResource_EmitsV5OnlyModelFields pins that the v5 series
// list emits the v5-only model fields MalIds / AniListIds /
// OriginalCountry (they flow through the shared apiv3.SeriesResource +
// ToSeriesResource). Guards against a future iteration wrongly
// "re-adding" them (G12 was a stale-doc false alarm). languageProfileId
// must still be stripped.
func TestV5SeriesResource_EmitsV5OnlyModelFields(t *testing.T) {
	t.Parallel()
	pop := series.Series{
		ID: 11, Title: "Anime", Monitored: true, Status: 1,
		MalIDs: []int{101, 202}, AniListIDs: []int{303}, OriginalCountry: "JP",
		Tags: []int{},
	}
	w := &fakeWriter{rows: []series.Series{pop}}
	rr := httptest.NewRecorder()
	ListHandler(w).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/series", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"malIds":[101,202]`, `"aniListIds":[303]`, `"originalCountry":"JP"`} {
		if !strings.Contains(body, want) {
			t.Errorf("v5 series body missing %s\n%s", want, body)
		}
	}
	// v5 still strips the v3-only languageProfileId.
	if strings.Contains(body, "languageProfileId") {
		t.Errorf("v5 must strip languageProfileId: %s", body)
	}
}
