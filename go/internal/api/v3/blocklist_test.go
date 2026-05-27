package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/blocklist"
)

type fakeBlocklistLister struct {
	records []BlocklistResource
	total   int
	err     error
}

func (f *fakeBlocklistLister) PagedBlocklist(context.Context, PagingRequest) ([]BlocklistResource, int, error) {
	return f.records, f.total, f.err
}

func TestBlocklistHandler_EmptyDefaults(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	BlocklistHandler(&fakeBlocklistLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/blocklist", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got PagingResource[BlocklistResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := PagingResource[BlocklistResource]{
		Page: 1, PageSize: 10, Records: []BlocklistResource{},
		SortDirection: "descending", SortKey: "date", TotalRecords: 0,
	}
	if got.Page != want.Page || got.PageSize != want.PageSize ||
		got.SortDirection != want.SortDirection || got.SortKey != want.SortKey ||
		got.TotalRecords != want.TotalRecords {
		t.Errorf("envelope mismatch: got %+v want %+v", got, want)
	}
	// Body must contain literal "[]" (records nil would marshal as null).
	if body := rr.Body.String(); !contains(body, `"records":[]`) {
		t.Errorf(`body missing "records":[]: %s`, body)
	}
}

// J297 wire-shape pin: a populated row must emit `customFormats:[]`
// (not `null`) — matches the C# Newtonsoft default for a non-null
// `List<CustomFormatResource>` initialised via
// `formatCalculator.ParseCustomFormat(model, model.Series).ToResource(false)`.
// Blocklist carries `customFormats` only (no `customFormatScore`).
func TestBlocklistHandler_EmitsCustomFormatsField(t *testing.T) {
	t.Parallel()
	svc := &fakeBlocklistLister{
		records: []BlocklistResource{
			{ID: 7, SeriesID: 11, SourceTitle: "Show.S01E01", Date: "2026-05-18T12:00:00Z", CustomFormats: []CustomFormatResource{}},
		},
		total: 1,
	}
	rr := httptest.NewRecorder()
	BlocklistHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/blocklist", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"customFormats":[]`) {
		t.Errorf("body must contain `\"customFormats\":[]`; got %s", body)
	}
	if strings.Contains(body, `"customFormatScore"`) {
		t.Errorf("Blocklist must NOT carry customFormatScore (unlike History); got %s", body)
	}
	var got PagingResource[BlocklistResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Records) != 1 {
		t.Fatalf("records len: %d", len(got.Records))
	}
	if got.Records[0].CustomFormats == nil {
		t.Errorf("CustomFormats must be non-nil (empty slice, not nil)")
	}
	if len(got.Records[0].CustomFormats) != 0 {
		t.Errorf("CustomFormats must be empty at deferred state; got %d", len(got.Records[0].CustomFormats))
	}
}

// J311 wire-shape pin: a populated row emits the four newly-wired
// scalar fields (EpisodeIDs / Protocol / Indexer / Message) with
// the correct nil-handling per field:
//   - EpisodeIDs: always emit (matches C# `required List<int>` on v5);
//     nil decodes back to nil (omitempty NOT set on this field, but the
//     constructor must default to [] for the empty case — that's the
//     production-adapter responsibility, verified via host_wiring.go).
//   - Protocol: always emit (matches C# `DownloadProtocol` non-nullable
//     enum). Wired as camelCase string per STJson's global
//     `JsonStringEnumConverter(CamelCase)` — "usenet"/"torrent"/"unknown".
//   - Indexer / Message: omitempty (matches C# `string?` semantic
//     under `JsonIgnoreCondition.WhenWritingNull`).
//
// 2 rows in the table cover both branches:
//   - Row 1: populated indexer + message + episodeIds + protocol=torrent.
//   - Row 2: empty indexer + empty message + nil episodeIds (decoded
//     back to nil — adapter-allocated empty slice is the production
//     concern) + protocol=unknown.
func TestBlocklistHandler_EmitsScalarFieldWidening_J311(t *testing.T) {
	t.Parallel()
	svc := &fakeBlocklistLister{
		records: []BlocklistResource{
			{ID: 7, SeriesID: 11, EpisodeIDs: []int{101, 102}, SourceTitle: "Show.S01E01-E02", Date: "2026-05-18T12:00:00Z", Protocol: "torrent", Indexer: "Newznab", Message: "rejected by user", CustomFormats: []CustomFormatResource{}},
			{ID: 8, SeriesID: 11, EpisodeIDs: nil, SourceTitle: "Show.S01E03", Date: "2026-05-18T13:00:00Z", Protocol: "unknown", Indexer: "", Message: "", CustomFormats: []CustomFormatResource{}},
		},
		total: 2,
	}
	rr := httptest.NewRecorder()
	BlocklistHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/blocklist", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	// Row 1 positive substring pins.
	mustContain := []string{
		`"episodeIds":[101,102]`,
		`"protocol":"torrent"`,
		`"indexer":"Newznab"`,
		`"message":"rejected by user"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; got %s", want, body)
		}
	}
	// Row 2 negative pin: empty Indexer + Message must drop their keys
	// (omitempty pinned). Use strings.Count to confirm the keys
	// appear exactly once (row 1 only).
	if c := strings.Count(body, `"indexer":`); c != 1 {
		t.Errorf("expected exactly 1 `\"indexer\":` occurrence (row 1 only); got %d in %s", c, body)
	}
	if c := strings.Count(body, `"message":`); c != 1 {
		t.Errorf("expected exactly 1 `\"message\":` occurrence (row 1 only); got %d in %s", c, body)
	}
	// Row 2 protocol must still emit (non-nullable; default "unknown").
	if !strings.Contains(body, `"protocol":"unknown"`) {
		t.Errorf("body missing row 2 `\"protocol\":\"unknown\"`; got %s", body)
	}
	// Decode through to confirm typed shape.
	var got PagingResource[BlocklistResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Records) != 2 {
		t.Fatalf("records len: %d", len(got.Records))
	}
	if got.Records[0].EpisodeIDs[0] != 101 || got.Records[0].EpisodeIDs[1] != 102 {
		t.Errorf("row 0 EpisodeIDs: %+v", got.Records[0].EpisodeIDs)
	}
	if got.Records[0].Protocol != "torrent" {
		t.Errorf("row 0 Protocol: %q", got.Records[0].Protocol)
	}
	if got.Records[1].EpisodeIDs != nil {
		t.Errorf("row 1 EpisodeIDs must decode back to nil (no omitempty on field but the test fixture set nil); got %+v", got.Records[1].EpisodeIDs)
	}
}

// J311 helper-pin: BlocklistProtocolName maps the persisted
// `*int` Protocol value back to the camelCase wire string used by
// the C# DownloadProtocol enum under JsonStringEnumConverter.
func TestBlocklistProtocolName_Mapping(t *testing.T) {
	t.Parallel()
	usenet := 1
	torrent := 2
	bogus := 99
	cases := []struct {
		name string
		in   *int
		want string
	}{
		{"nil → unknown (matches C# enum default)", nil, "unknown"},
		{"usenet (1)", &usenet, "usenet"},
		{"torrent (2)", &torrent, "torrent"},
		{"unrecognised int falls through to enum.String() default", &bogus, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BlocklistProtocolName(tc.in); got != tc.want {
				t.Errorf("BlocklistProtocolName(%v): got %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBlocklistHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	BlocklistHandler(&fakeBlocklistLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/blocklist", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// TestBlocklistHandler_ErrorEmitsCSharpEnvelope pins the post-5.A4
// wire shape on an error path — the body must be the JSON envelope
// matching `Sonarr.Http/ErrorManagement/ErrorModel.cs` rather than
// plain text (the pre-sweep shape `http.Error` emits). Catches a
// regression to text/plain on any error handler.
func TestBlocklistHandler_ErrorEmitsCSharpEnvelope(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	BlocklistHandler(&fakeBlocklistLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/blocklist", nil))
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q want application/json", ct)
	}
	body := rr.Body.String()
	if !contains(body, `"message"`) {
		t.Errorf("body must carry message key: %s", body)
	}
	if !contains(body, "blocklist") {
		t.Errorf("body must include the blocklist prefix in message: %s", body)
	}
	if !contains(body, "boom") {
		t.Errorf("body must include the underlying error: %s", body)
	}
}

// ---------- 5.E3: DELETE tests ----------

type fakeBlocklistWriter struct {
	deletedIDs []int
	bulkIDs    []int
	notFound   bool
}

func (f *fakeBlocklistWriter) Delete(_ context.Context, id int) error {
	if f.notFound {
		return blocklist.ErrNotFound
	}
	f.deletedIDs = append(f.deletedIDs, id)
	return nil
}
func (f *fakeBlocklistWriter) DeleteByIDs(_ context.Context, ids []int) (int64, error) {
	f.bulkIDs = append(f.bulkIDs, ids...)
	return int64(len(ids)), nil
}

func TestBlocklistDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeBlocklistWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/blocklist/7", nil)
	req.SetPathValue("id", "7")
	BlocklistDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.deletedIDs) != 1 || w.deletedIDs[0] != 7 {
		t.Errorf("deleted: %+v", w.deletedIDs)
	}
}

// TestBlocklistDeleteHandler_Idempotent — C.1.i-follow-4 alignment (J232).
// Live C# v5-develop returns 200 + `{}` on DELETE of a non-existent id.
func TestBlocklistDeleteHandler_Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeBlocklistWriter{notFound: true}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/blocklist/999", nil)
	req.SetPathValue("id", "999")
	BlocklistDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (idempotent — matches live C# v5-develop)", rr.Code)
	}
}

func TestBlocklistDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/blocklist/abc", nil)
	req.SetPathValue("id", "abc")
	BlocklistDeleteHandler(&fakeBlocklistWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestBlocklistBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeBlocklistWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/blocklist/bulk", strings.NewReader(`{"ids":[1,2,3]}`))
	BlocklistBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.bulkIDs) != 3 {
		t.Errorf("bulk ids: %+v", w.bulkIDs)
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body should be empty object; got %q", rr.Body.String())
	}
}

func TestBlocklistBulkDeleteHandler_EmptyIDs_NoOp(t *testing.T) {
	t.Parallel()
	w := &fakeBlocklistWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/blocklist/bulk", strings.NewReader(`{"ids":[]}`))
	BlocklistBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if len(w.bulkIDs) != 0 {
		t.Errorf("expected no bulk calls; got %+v", w.bulkIDs)
	}
}

func TestBlocklistBulkDeleteHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/blocklist/bulk", strings.NewReader(`not json`))
	BlocklistBulkDeleteHandler(&fakeBlocklistWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
