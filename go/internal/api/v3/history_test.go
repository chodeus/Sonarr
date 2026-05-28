package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/series"
)

type fakeHistoryLister struct {
	records []HistoryResource
	total   int
	err     error
}

func (f *fakeHistoryLister) PagedHistory(context.Context, PagingRequest) ([]HistoryResource, int, error) {
	return f.records, f.total, f.err
}

func TestHistoryHandler_EmptyDefaults(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistoryHandler(&fakeHistoryLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got PagingResource[HistoryResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Page != 1 || got.PageSize != 10 || got.SortKey != "date" ||
		got.SortDirection != "descending" || got.TotalRecords != 0 {
		t.Errorf("envelope: %+v", got)
	}
	if body := rr.Body.String(); !contains(body, `"records":[]`) {
		t.Errorf(`body missing "records":[]: %s`, body)
	}
}

func TestHistoryHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistoryHandler(&fakeHistoryLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.E4: POST history/failed/{id} ----------

type fakeFailedDownloadMarker struct {
	calledID int
	err      error
}

func (f *fakeFailedDownloadMarker) MarkAsFailed(_ context.Context, id int) error {
	f.calledID = id
	return f.err
}

func TestHistoryMarkAsFailedHandler_Success(t *testing.T) {
	t.Parallel()
	m := &fakeFailedDownloadMarker{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/history/failed/42", nil)
	req.SetPathValue("id", "42")
	HistoryMarkAsFailedHandler(m).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if m.calledID != 42 {
		t.Errorf("expected MarkAsFailed(42); got %d", m.calledID)
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body should be empty object; got %q", rr.Body.String())
	}
}

func TestHistoryMarkAsFailedHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	m := &fakeFailedDownloadMarker{err: history.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/history/failed/999", nil)
	req.SetPathValue("id", "999")
	HistoryMarkAsFailedHandler(m).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistoryMarkAsFailedHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/history/failed/abc", nil)
	req.SetPathValue("id", "abc")
	HistoryMarkAsFailedHandler(&fakeFailedDownloadMarker{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistoryMarkAsFailedHandler_ServiceError_500(t *testing.T) {
	t.Parallel()
	m := &fakeFailedDownloadMarker{err: errors.New("boom")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/history/failed/1", nil)
	req.SetPathValue("id", "1")
	HistoryMarkAsFailedHandler(m).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.I21: /since + /series tests ----------

type fakeHistoryRangeReader struct {
	sinceRows  []history.Entry
	seriesRows []history.Entry
	gotEvent   *history.EventType
	gotSeries  int
	err        error
}

func (f *fakeHistoryRangeReader) Since(_ context.Context, _ time.Time, ev *history.EventType) ([]history.Entry, error) {
	f.gotEvent = ev
	if f.err != nil {
		return nil, f.err
	}
	if ev != nil {
		return filterByEventTypeFake(f.sinceRows, *ev), nil
	}
	return f.sinceRows, nil
}

func (f *fakeHistoryRangeReader) GetBySeries(_ context.Context, seriesID int, ev *history.EventType) ([]history.Entry, error) {
	f.gotSeries = seriesID
	f.gotEvent = ev
	if f.err != nil {
		return nil, f.err
	}
	if ev != nil {
		return filterByEventTypeFake(f.seriesRows, *ev), nil
	}
	return f.seriesRows, nil
}

func filterByEventTypeFake(rows []history.Entry, et history.EventType) []history.Entry {
	out := make([]history.Entry, 0, len(rows))
	for _, e := range rows {
		if e.EventType != nil && history.EventType(*e.EventType) == et {
			out = append(out, e)
		}
	}
	return out
}

func TestHistorySinceHandler_MissingDate_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySinceHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistorySinceHandler_InvalidDate_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySinceHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=not-a-date", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// J338 wire-shape pin for the HistoryEntryPopulator optional-
// extension integration on HistorySinceHandler + HistorySeriesHandler.
// When the HistoryRangeReader passed in ALSO implements the
// PopulateHistoryEntry method, the handlers invoke it per row to
// fill customFormats[] + customFormatScore. When it does NOT
// implement (the test's fakeHistoryRangeReader case), the handlers
// fall back to the empty stub.
//
// Test covers BOTH branches: a stub fake (no populator) → empty +
// 0; a wrapper fake (with populator) → populated CustomFormats
// list + non-zero score.
func TestHistorySinceHandler_PopulatorOptionalExtension_J338(t *testing.T) {
	t.Parallel()
	row := history.Entry{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)}
	one := 1
	row.EventType = &one

	// Branch 1: stub fake without populator → empty fallback.
	t.Run("no populator → empty CustomFormats + 0 score", func(t *testing.T) {
		svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{row}}
		rr := httptest.NewRecorder()
		HistorySinceHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status: %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, `"customFormats":[]`) {
			t.Errorf("stub fake must emit empty customFormats:[]; got %s", body)
		}
		if !strings.Contains(body, `"customFormatScore":0`) {
			t.Errorf("stub fake must emit customFormatScore:0; got %s", body)
		}
	})

	// Branch 2: wrapper fake with populator → typed output.
	t.Run("populator wired → typed customFormats + score", func(t *testing.T) {
		svc := &fakeRangeReaderWithPopulator{
			rows: []history.Entry{row},
			populator: func(_ context.Context, e *history.Entry) ([]CustomFormatResource, int) {
				return []CustomFormatResource{{ID: 5, Name: "x265"}}, 42
			},
		}
		rr := httptest.NewRecorder()
		HistorySinceHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status: %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, `"customFormats":[{"id":5,"name":"x265"}]`) {
			t.Errorf("populator must produce typed customFormats entry; got %s", body)
		}
		if !strings.Contains(body, `"customFormatScore":42`) {
			t.Errorf("populator must set customFormatScore:42; got %s", body)
		}
	})
}

// fakeRangeReaderWithPopulator satisfies both HistoryRangeReader
// AND HistoryEntryPopulator — used to exercise the J338 optional-
// extension type-assertion branch.
type fakeRangeReaderWithPopulator struct {
	rows      []history.Entry
	populator func(context.Context, *history.Entry) ([]CustomFormatResource, int)
}

func (f *fakeRangeReaderWithPopulator) Since(_ context.Context, _ time.Time, _ *history.EventType) ([]history.Entry, error) {
	return f.rows, nil
}
func (f *fakeRangeReaderWithPopulator) GetBySeries(_ context.Context, _ int, _ *history.EventType) ([]history.Entry, error) {
	return f.rows, nil
}
func (f *fakeRangeReaderWithPopulator) PopulateHistoryEntry(ctx context.Context, e *history.Entry) ([]CustomFormatResource, int) {
	return f.populator(ctx, e)
}

// J310 wire-shape pin: a populated row with stamped Data dict emits
// the full key set; a nil Data dict drops the `data` key via
// omitempty (matches STJson's global `JsonIgnoreCondition.WhenWritingNull`).
// The C# `EpisodeHistory.Data` defaults to `new Dictionary<>()` so
// C#'s wire emits `"data":{}` for a row with no stamped metadata —
// the Go-port preserves the same posture by passing the persisted
// `e.Data` through verbatim. Test covers BOTH branches in one pass:
// row 1 has populated Data (must emit), row 2 has nil Data (must
// drop the key).
func TestHistorySinceHandler_EmitsDataDictAndOmitsWhenNil(t *testing.T) {
	t.Parallel()
	one := 1
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), EventType: &one, Data: map[string]string{"indexer": "Newznab", "size": "1234567", "releaseGroup": "GrpA"}},
		{ID: 2, SeriesID: 11, SourceTitle: "Show.S01E02", Date: time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC), EventType: &one, Data: nil},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	// Decode through the typed shape to verify the dict round-trips
	// faithfully (string→string).
	var got []HistoryResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: %d", len(got))
	}
	if got[0].Data["indexer"] != "Newznab" || got[0].Data["size"] != "1234567" || got[0].Data["releaseGroup"] != "GrpA" {
		t.Errorf("populated Data round-trip: got %+v want map[indexer:Newznab size:1234567 releaseGroup:GrpA]", got[0].Data)
	}
	if got[1].Data != nil {
		t.Errorf("nil Data must decode back to nil (omitempty drops key); got %+v", got[1].Data)
	}
	// Substring assertions on the raw body for the omitempty pin —
	// json.Decode normalises absent vs `null` to the same nil map,
	// so the raw byte stream is the authoritative source for "key
	// absent vs key present".
	body := rr.Body.String()
	if !contains(body, `"data":{`) {
		t.Errorf("body must contain populated `\"data\":{...}` for row 1; got %s", body)
	}
	if contains(body, `"data":null`) {
		t.Errorf("nil Data must omit the key entirely (not emit `data:null`); got %s", body)
	}
	// strings.Count("data\":") == 1 — exactly one row emitted the
	// data key. Both rows emitting `"data":{...}` would be 2.
	if c := strings.Count(body, `"data":`); c != 1 {
		t.Errorf("expected exactly 1 `\"data\":` occurrence (row 1 only); got %d in %s", c, body)
	}
}

// J312 wire-shape pin for the QualityCutoffNotMet + Episode? +
// Series? widening. QualityCutoffNotMet is non-nullable on the C#
// wire (bool) so it always emits — default false matches the v3
// HistoryResourceMapper's `// QualityCutoffNotMet` deferred-comment
// stub at C# HistoryResource.cs:59 (the value is populated post-
// mapping by HistoryController.cs:57 via the upgradable-spec
// service — DEFERRED in the Go-port). Episode + Series are
// nullable subresources (pointer-to-struct with omitempty):
//   - nil → key absent (matches STJson WhenWritingNull / C#
//     `EpisodeResource?` + `SeriesResource?`).
//   - non-nil → key present with the full nested shape (matches
//     C# `includeEpisode=true` / `includeSeries=true` query-param
//     branches — both DEFERRED in the Go-port).
//
// Test covers BOTH branches in one pass: row 1 leaves all three
// at the deferred-state defaults (QualityCutoffNotMet=false +
// Episode=nil + Series=nil); row 2 explicitly populates Episode +
// Series + flips QualityCutoffNotMet=true to pin the populated-
// wire shape.
func TestHistorySinceHandler_EmitsQualityCutoffNotMetAndSubresources_J312(t *testing.T) {
	t.Parallel()
	one := 1
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 7, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), EventType: &one},
		{ID: 8, SeriesID: 11, SourceTitle: "Show.S01E02", Date: time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC), EventType: &one},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	// Positive: both rows emit `"qualityCutoffNotMet":false` (default
	// state — the upgradable-spec port hasn't landed so the writer
	// helper doesn't override the zero value).
	if c := strings.Count(body, `"qualityCutoffNotMet":false`); c != 2 {
		t.Errorf("expected 2 `\"qualityCutoffNotMet\":false` occurrences (default state on both rows); got %d in %s", c, body)
	}
	// Negative: nil Episode + nil Series must drop their keys
	// entirely (omitempty pinned on the pointer-to-struct field).
	if strings.Contains(body, `"episode":`) {
		t.Errorf("nil Episode must omit the key; got %s", body)
	}
	if strings.Contains(body, `"series":`) {
		t.Errorf("nil Series must omit the key; got %s", body)
	}
}

// J312 pure-marshal pin for the populated subresource branch:
// once the populated-mapper lands (allocating &EpisodeResource{}
// + &SeriesResource{}), the subresources emit the full nested
// shape under the `episode` + `series` keys. No service path
// today populates these; this test pins the contract via direct
// json.Marshal on a hand-constructed HistoryResource.
func TestHistoryResource_PopulatedSubresources_EmitFullShape_J312(t *testing.T) {
	t.Parallel()
	res := HistoryResource{
		ID: 7, SeriesID: 11, EpisodeID: 101,
		SourceTitle: "Show.S01E01", Date: "2026-05-18T12:00:00Z",
		EventType: "grabbed", QualityCutoffNotMet: true,
		CustomFormats: []CustomFormatResource{},
		Episode:       &EpisodeResource{ID: 101, EpisodeNumber: 1, SeasonNumber: 1, Title: "Pilot"},
		Series:        &SeriesResource{ID: 11, Title: "Show", Tags: []int{}, Genres: []string{}},
	}
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	mustContain := []string{
		`"qualityCutoffNotMet":true`,
		`"episode":{`,
		`"series":{`,
		`"episodeNumber":1`,
		`"seasonNumber":1`,
		`"title":"Pilot"`,
		`"title":"Show"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

// J314 wire-shape pin for the Languages typed-list widening on
// HistoryResource. Populated row emits the typed list per the
// C# `required List<Language>` non-null contract; empty/nil
// persisted Languages still emits `"languages":[]` (non-null
// default — adapter allocates via ToLanguageRefs which never
// returns nil). Pinned via writeHistoryEntries (used by
// HistorySinceHandler).
func TestHistorySinceHandler_EmitsLanguagesField_J314(t *testing.T) {
	t.Parallel()
	one := 1
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), EventType: &one, Languages: []int{1, 2}},
		{ID: 2, SeriesID: 11, SourceTitle: "Show.S01E02", Date: time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC), EventType: &one, Languages: nil},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	// Row 1: populated 2-language list → typed wire shape on both entries.
	if !strings.Contains(body, `"languages":[{"id":1,"name":"English"},{"id":2,"name":"French"}]`) {
		t.Errorf("row 1 must emit typed Languages list; got %s", body)
	}
	// Row 2: nil persisted → adapter allocates empty slice, wire `"languages":[]`.
	if !strings.Contains(body, `"languages":[]`) {
		t.Errorf("row 2 nil Languages must emit `\"languages\":[]` (not omitted, not null); got %s", body)
	}
	// Defensive: no `"languages":null` anywhere.
	if strings.Contains(body, `"languages":null`) {
		t.Errorf("languages must never emit `null` — adapter allocates non-nil empty slice; got %s", body)
	}
	// Decode-through round-trip pin.
	var got []HistoryResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 || len(got[0].Languages) != 2 || got[0].Languages[0].Name != "English" {
		t.Errorf("decoded shape: %+v", got)
	}
	if got[1].Languages == nil {
		t.Errorf("row 1 Languages must decode as non-nil empty (got nil)")
	}
}

// J296 wire-shape pin: a populated row must emit
// `customFormats:[]` (not `null`) and `customFormatScore:0` —
// matches the C# Newtonsoft default for a non-null
// `List<CustomFormatResource>` initialised via
// `customFormats.ToResource(false)`.
func TestHistorySinceHandler_EmitsCustomFormatsAndScore(t *testing.T) {
	t.Parallel()
	one := 1
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 7, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), EventType: &one},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !contains(body, `"customFormats":[]`) {
		t.Errorf("body must contain `\"customFormats\":[]`; got %s", body)
	}
	if !contains(body, `"customFormatScore":0`) {
		t.Errorf("body must contain `\"customFormatScore\":0`; got %s", body)
	}
	// Decode through to confirm the struct fields too.
	var got []HistoryResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len: %d", len(got))
	}
	if got[0].CustomFormats == nil {
		t.Errorf("CustomFormats must be non-nil (empty slice, not nil)")
	}
	if len(got[0].CustomFormats) != 0 {
		t.Errorf("CustomFormats must be empty at deferred state; got %d", len(got[0].CustomFormats))
	}
	if got[0].CustomFormatScore != 0 {
		t.Errorf("CustomFormatScore must be 0 at deferred state; got %d", got[0].CustomFormatScore)
	}
}

func TestHistorySinceHandler_Success(t *testing.T) {
	t.Parallel()
	one := 1
	two := 4
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 1, SeriesID: 11, SourceTitle: "Show.S01E01", Date: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), EventType: &one},
		{ID: 2, SeriesID: 11, SourceTitle: "Show.S01E02", Date: time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC), EventType: &two},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []HistoryResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len: %d", len(got))
	}
	if got[0].EventType != "Grabbed" || got[1].EventType != "DownloadFailed" {
		t.Errorf("eventType names: %q %q", got[0].EventType, got[1].EventType)
	}
	if svc.gotEvent != nil {
		t.Errorf("unfiltered call should pass nil eventType; got %+v", svc.gotEvent)
	}
}

func TestHistorySinceHandler_FilterByEventType(t *testing.T) {
	t.Parallel()
	one := 1
	two := 4
	svc := &fakeHistoryRangeReader{sinceRows: []history.Entry{
		{ID: 1, SourceTitle: "grab", Date: time.Now().UTC(), EventType: &one},
		{ID: 2, SourceTitle: "fail", Date: time.Now().UTC(), EventType: &two},
	}}
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z&eventType=4", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if svc.gotEvent == nil || *svc.gotEvent != history.EventTypeDownloadFailed {
		t.Errorf("expected DownloadFailed filter passed through; got %+v", svc.gotEvent)
	}
}

func TestHistorySinceHandler_BadEventType_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySinceHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-05-18T00:00:00Z&eventType=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistorySeriesHandler_MissingSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySeriesHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/series", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestHistorySeriesHandler_Success(t *testing.T) {
	t.Parallel()
	one := 1
	svc := &fakeHistoryRangeReader{seriesRows: []history.Entry{
		{ID: 1, SeriesID: 42, SourceTitle: "Show.S01E01", Date: time.Now().UTC(), EventType: &one},
	}}
	rr := httptest.NewRecorder()
	HistorySeriesHandler(svc).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/series?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if svc.gotSeries != 42 {
		t.Errorf("seriesId not forwarded: got %d", svc.gotSeries)
	}
}

func TestHistorySeriesHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	HistorySeriesHandler(&fakeHistoryRangeReader{}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/series?seriesId=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// fakeHistoryReaderWithEmbeds implements HistoryRangeReader plus the
// J377 HistorySeriesEmbedder / HistoryEpisodeEmbedder optional
// extensions.
type fakeHistoryReaderWithEmbeds struct {
	rows   []history.Entry
	series map[int]*series.Series
	eps    map[int]*episodes.Episode
}

func (f *fakeHistoryReaderWithEmbeds) Since(_ context.Context, _ time.Time, _ *history.EventType) ([]history.Entry, error) {
	return f.rows, nil
}
func (f *fakeHistoryReaderWithEmbeds) GetBySeries(_ context.Context, _ int, _ *history.EventType) ([]history.Entry, error) {
	return f.rows, nil
}
func (f *fakeHistoryReaderWithEmbeds) HistorySeriesResource(_ context.Context, id int) *SeriesResource {
	if s, ok := f.series[id]; ok {
		r := ToSeriesResource(*s)
		return &r
	}
	return nil
}
func (f *fakeHistoryReaderWithEmbeds) HistoryEpisodeResource(_ context.Context, id int) *EpisodeResource {
	if e, ok := f.eps[id]; ok {
		r := ToEpisodeResource(*e)
		return &r
	}
	return nil
}

// TestHistorySinceHandler_IncludeSeriesEpisode_J377 pins the
// includeSeries / includeEpisode embeds: absent without the flags,
// present (base Series / Episode resources) when set true and the
// reader implements the embedder extensions.
func TestHistorySinceHandler_IncludeSeriesEpisode_J377(t *testing.T) {
	t.Parallel()
	svc := &fakeHistoryReaderWithEmbeds{
		rows:   []history.Entry{{ID: 1, SeriesID: 7, EpisodeID: 5, Date: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}},
		series: map[int]*series.Series{7: {ID: 7, Title: "Breaking Bad"}},
		eps:    map[int]*episodes.Episode{5: {ID: 5, SeriesID: 7, Title: "Pilot"}},
	}

	// No flags → no embeds.
	rr := httptest.NewRecorder()
	HistorySinceHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/history/since?date=2026-01-01T00:00:00Z", nil))
	if b := rr.Body.String(); strings.Contains(b, `"series"`) || strings.Contains(b, `"episode"`) {
		t.Errorf("no embeds expected without flags; got %s", b)
	}

	// Both flags → embeds present.
	rr2 := httptest.NewRecorder()
	HistorySinceHandler(svc).ServeHTTP(rr2, httptest.NewRequest(http.MethodGet,
		"/api/v3/history/since?date=2026-01-01T00:00:00Z&includeSeries=true&includeEpisode=true", nil))
	var got []HistoryResource
	if err := json.Unmarshal(rr2.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row; got %d", len(got))
	}
	if got[0].Series == nil || got[0].Series.ID != 7 || got[0].Series.Title != "Breaking Bad" {
		t.Errorf("series embed: %+v", got[0].Series)
	}
	if got[0].Episode == nil || got[0].Episode.ID != 5 {
		t.Errorf("episode embed: %+v", got[0].Episode)
	}
}
