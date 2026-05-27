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
)

type fakeQueuePagedLister struct {
	records []QueueResource
	total   int
	err     error
	gotReq  PagingRequest
}

func (f *fakeQueuePagedLister) PagedQueue(_ context.Context, req PagingRequest) ([]QueueResource, int, error) {
	f.gotReq = req
	return f.records, f.total, f.err
}

func TestQueueHandler_DefaultEnvelope(t *testing.T) {
	t.Parallel()
	lister := &fakeQueuePagedLister{}
	rr := httptest.NewRecorder()
	QueueHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
	if lister.gotReq.SortKey != "timeleft" {
		t.Errorf("default sortKey: got %q want %q", lister.gotReq.SortKey, "timeleft")
	}
	var got PagingResource[QueueResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SortKey != "timeleft" || got.SortDirection != "descending" || got.TotalRecords != 0 {
		t.Errorf("envelope: %+v", got)
	}
	if !strings.Contains(rr.Body.String(), `"records":[]`) {
		t.Errorf(`expected "records":[] on empty body: %s`, rr.Body.String())
	}
}

func TestQueueHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueHandler(&fakeQueuePagedLister{err: errors.New("boom")}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestQueueHandler_RecordsPassthrough(t *testing.T) {
	t.Parallel()
	// Populated records still flow through the envelope. Exercises
	// the records-non-nil branch so future per-record mapping work
	// has a regression net.
	seriesID := 42
	lister := &fakeQueuePagedLister{
		records: []QueueResource{{ID: 1, SeriesID: &seriesID, Title: "demo", Status: "downloading", Protocol: "torrent"}},
		total:   1,
	}
	rr := httptest.NewRecorder()
	QueueHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue", nil))
	var got PagingResource[QueueResource]
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.TotalRecords != 1 || len(got.Records) != 1 || got.Records[0].Title != "demo" {
		t.Errorf("records passthrough: %+v", got)
	}
}

// J315 wire-shape pin for the Languages typed-list widening on
// QueueResource. The field uses omitempty (unlike History +
// Blocklist which use no-omitempty), because today's empty-DB
// QueueResource stub never allocates Languages — preserving the
// nil → key-absent contract avoids a wire-shape break for the
// existing empty-corpus contract goldens. The populated-row
// mapper (gated on queue.Service's paged surface) will allocate
// via `apiv3.ToLanguageRefs(model.Languages)` once the paged-
// projection port lands.
//
// Note on the empty-vs-nil collapse: Go's `omitempty` on a slice
// drops BOTH nil AND empty non-nil to "key absent" — the
// distinction C# preserves under `WhenWritingNull` (null → absent;
// empty List → `[]`) is lost. The cost is acceptable for the
// queue path: clients have no semantic reason to distinguish
// "no languages reported" from "explicitly empty list" for a
// download-queue item. If a future need to preserve the
// distinction arrives, switch to `*[]LanguageRef` (J309
// SeasonResource.Images pattern).
//
// Test covers 2 branches: nil → key omitted; populated → typed
// `[{"id":N,"name":"X"}]` shape.
func TestQueueResource_LanguagesWireShape_J315(t *testing.T) {
	t.Parallel()
	// Branch 1: nil (today's stub default) — omitempty drops the key.
	buf, err := json.Marshal(QueueResource{ID: 1, Title: "demo"})
	if err != nil {
		t.Fatalf("marshal nil-languages: %v", err)
	}
	if strings.Contains(string(buf), `"languages":`) {
		t.Errorf("nil Languages must omit the key; got %s", buf)
	}
	// Branch 2: populated 2-entry via ToLanguageRefs — typed shape.
	buf2, err := json.Marshal(QueueResource{ID: 2, Title: "demo", Languages: ToLanguageRefs([]int{1, 2})})
	if err != nil {
		t.Fatalf("marshal populated-languages: %v", err)
	}
	if !strings.Contains(string(buf2), `"languages":[{"id":1,"name":"English"},{"id":2,"name":"French"}]`) {
		t.Errorf("populated Languages must emit typed shape; got %s", buf2)
	}
}

// J317 wire-shape pin for the 5-field scalar widening on
// QueueResource (Size + Indexer + DownloadClient + OutputPath +
// ErrorMessage). All omitempty-tagged so the empty-DB stub still
// emits no change; populated values flow through verbatim.
//
// Test covers BOTH branches in one pass via direct json.Marshal:
// row 1 populated with non-zero Size + all 4 strings → all 5
// fields emit; row 2 zero Size + empty strings → omitempty drops
// all 5 keys.
func TestQueueResource_ScalarFieldWidening_J317(t *testing.T) {
	t.Parallel()
	// Branch 1: populated → all 5 fields emit.
	populated := QueueResource{ID: 1, Title: "demo", Size: 1234567, Indexer: "Newznab", DownloadClient: "SABnzbd", OutputPath: "/downloads/Show.S01E01", ErrorMessage: "Permission denied"}
	buf, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf)
	mustContain := []string{
		`"size":1234567`,
		`"indexer":"Newznab"`,
		`"downloadClient":"SABnzbd"`,
		`"outputPath":"/downloads/Show.S01E01"`,
		`"errorMessage":"Permission denied"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("populated missing %q in %s", want, got)
		}
	}
	// Branch 2: zero-value scalars → omitempty drops all 5 keys.
	empty := QueueResource{ID: 2, Title: "demo"}
	buf2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	got2 := string(buf2)
	mustNotContain := []string{
		`"size":`,
		`"indexer":`,
		`"downloadClient":`,
		`"outputPath":`,
		`"errorMessage":`,
	}
	for _, want := range mustNotContain {
		if strings.Contains(got2, want) {
			t.Errorf("empty must omit %q (omitempty); got %s", want, got2)
		}
	}
}

// J318 wire-shape pin for the 4-field bool/int widening on
// QueueResource (DownloadClientHasPostImportCategory bool +
// CustomFormatScore int + EpisodesWithFilesCount int +
// IsFullSeason bool). All omitempty-tagged: bool false / int 0
// drop the key; populated values emit.
//
// Test covers BOTH branches: row 1 populated with non-default
// values → all 4 fields emit; row 2 zero/false-value scalars →
// omitempty drops all 4 keys.
func TestQueueResource_BoolIntFieldWidening_J318(t *testing.T) {
	t.Parallel()
	// Branch 1: populated → all 4 fields emit.
	populated := QueueResource{
		ID:                                  1,
		Title:                               "demo",
		DownloadClientHasPostImportCategory: true,
		CustomFormatScore:                   42,
		EpisodesWithFilesCount:              3,
		IsFullSeason:                        true,
	}
	buf, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf)
	mustContain := []string{
		`"downloadClientHasPostImportCategory":true`,
		`"customFormatScore":42`,
		`"episodesWithFilesCount":3`,
		`"isFullSeason":true`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("populated missing %q in %s", want, got)
		}
	}
	// Branch 2: zero-value bool/int → omitempty drops all 4 keys.
	empty := QueueResource{ID: 2, Title: "demo"}
	buf2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	got2 := string(buf2)
	mustNotContain := []string{
		`"downloadClientHasPostImportCategory":`,
		`"customFormatScore":`,
		`"episodesWithFilesCount":`,
		`"isFullSeason":`,
	}
	for _, want := range mustNotContain {
		if strings.Contains(got2, want) {
			t.Errorf("empty must omit %q (omitempty); got %s", want, got2)
		}
	}
}

// J319 wire-shape pin for the *time.Time pointer-field widening on
// QueueResource (Added + EstimatedCompletionTime). Both fields
// omitempty pointer-to-struct (J302 pattern): nil → key absent
// (matches C# WhenWritingNull / `DateTime?`); non-nil → emit the
// RFC3339 timestamp (Go's encoding/json default time.Time marshal,
// matches C# ISO-8601).
//
// Test covers BOTH branches: row 1 populated with pinned
// 2026-01-02T03:04:05Z timestamps → both keys emit; row 2 nil
// pointers → omitempty drops both keys.
func TestQueueResource_TimePointerFieldWidening_J319(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	populated := QueueResource{ID: 1, Title: "demo", Added: &when, EstimatedCompletionTime: &when}
	buf, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf)
	mustContain := []string{
		`"added":"2026-01-02T03:04:05Z"`,
		`"estimatedCompletionTime":"2026-01-02T03:04:05Z"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("populated missing %q in %s", want, got)
		}
	}
	// Branch 2: nil pointers → omitempty drops both keys.
	empty := QueueResource{ID: 2, Title: "demo"}
	buf2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	got2 := string(buf2)
	if strings.Contains(got2, `"added":`) {
		t.Errorf("nil Added must omit the key; got %s", got2)
	}
	if strings.Contains(got2, `"estimatedCompletionTime":`) {
		t.Errorf("nil EstimatedCompletionTime must omit the key; got %s", got2)
	}
}

// J320 wire-shape pin for the TrackedDownloadStatus +
// TrackedDownloadState enum-string widening on QueueResource. Both
// fields are nullable on C# (`TrackedDownloadStatus?` /
// `TrackedDownloadState?`); wire values are the camelCase form per
// STJson's `JsonStringEnumConverter(CamelCase)` applied to the C#
// enum names. Go-port uses plain `string` with omitempty (empty →
// key absent matching C# null).
//
// Test covers BOTH branches: row 1 populated with "downloading" +
// "warning" → both keys emit; row 2 empty strings → omitempty
// drops both keys.
func TestQueueResource_TrackedDownloadStatusState_J320(t *testing.T) {
	t.Parallel()
	populated := QueueResource{ID: 1, Title: "demo", TrackedDownloadStatus: "warning", TrackedDownloadState: "downloading"}
	buf, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf)
	if !strings.Contains(got, `"trackedDownloadStatus":"warning"`) {
		t.Errorf("populated trackedDownloadStatus missing; got %s", got)
	}
	if !strings.Contains(got, `"trackedDownloadState":"downloading"`) {
		t.Errorf("populated trackedDownloadState missing; got %s", got)
	}
	// Branch 2: empty strings → omitempty drops both keys.
	empty := QueueResource{ID: 2, Title: "demo"}
	buf2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	got2 := string(buf2)
	if strings.Contains(got2, `"trackedDownloadStatus":`) {
		t.Errorf("empty TrackedDownloadStatus must omit; got %s", got2)
	}
	if strings.Contains(got2, `"trackedDownloadState":`) {
		t.Errorf("empty TrackedDownloadState must omit; got %s", got2)
	}
}

// J321 wire-shape pin for the SizeLeft (int64) + TimeLeft (string)
// widening on QueueResource. SizeLeft uses v5-form camelCase key
// (`"sizeLeft"`); TimeLeft formats C# TimeSpan default. Both
// omitempty: 0/empty drops the key.
//
// Test covers BOTH branches: row 1 populated with non-zero
// SizeLeft + non-empty TimeLeft → both keys emit; row 2 zero/
// empty → omitempty drops both keys.
func TestQueueResource_SizeLeftTimeLeftWidening_J321(t *testing.T) {
	t.Parallel()
	populated := QueueResource{ID: 1, Title: "demo", SizeLeft: 12345, TimeLeft: "00:05:23"}
	buf, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf)
	if !strings.Contains(got, `"sizeLeft":12345`) {
		t.Errorf("populated sizeLeft missing; got %s", got)
	}
	if !strings.Contains(got, `"timeLeft":"00:05:23"`) {
		t.Errorf("populated timeLeft missing; got %s", got)
	}
	// Branch 2: zero/empty → omitempty drops both keys.
	empty := QueueResource{ID: 2, Title: "demo"}
	buf2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	got2 := string(buf2)
	if strings.Contains(got2, `"sizeLeft":`) {
		t.Errorf("zero SizeLeft must omit; got %s", got2)
	}
	if strings.Contains(got2, `"timeLeft":`) {
		t.Errorf("empty TimeLeft must omit; got %s", got2)
	}
}

// J321 helper-pin: FormatTimeLeft maps *time.Duration to the C#
// TimeSpan default `[d.]hh:mm:ss` format. nil / zero / negative
// returns "" so the omitempty json tag drops the key.
func TestFormatTimeLeft_AllBranches_J321(t *testing.T) {
	t.Parallel()
	d5m23s := 5*time.Minute + 23*time.Second
	d3d4h12m := 3*24*time.Hour + 4*time.Hour + 12*time.Minute
	zero := time.Duration(0)
	neg := -1 * time.Second
	cases := []struct {
		name string
		in   *time.Duration
		want string
	}{
		{"nil → empty (omitempty drops key)", nil, ""},
		{"zero → empty", &zero, ""},
		{"negative → empty (clamped)", &neg, ""},
		{"5m23s → 00:05:23 (no-days form)", &d5m23s, "00:05:23"},
		{"3d4h12m → 3.04:12:00 (days form, no day-pad, hour-pad)", &d3d4h12m, "3.04:12:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatTimeLeft(tc.in); got != tc.want {
				t.Errorf("FormatTimeLeft(%v): got %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

// J322 wire-shape pin for the Series + Episode + Episodes
// subresource widening on QueueResource. Series + Episode are
// pointer-to-Resource with omitempty (J302 pattern: nil → key
// absent, populated → full nested shape). Episodes is
// pointer-to-slice for the 3-state distinction (nil → absent;
// non-nil empty → `[]`; populated → `[{...}]`).
//
// Test covers BOTH branches in one pass: row 1 fully populated;
// row 2 all nil pointers → all 3 keys drop via omitempty.
func TestQueueResource_SubresourceWidening_J322(t *testing.T) {
	t.Parallel()
	// Branch 1: fully populated.
	episodes := []EpisodeResource{{ID: 101, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}}
	populated := QueueResource{
		ID:       1,
		Title:    "demo",
		Series:   &SeriesResource{ID: 11, Title: "Show", Tags: []int{}, Genres: []string{}},
		Episode:  &EpisodeResource{ID: 101, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"},
		Episodes: &episodes,
	}
	buf, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf)
	mustContain := []string{
		`"series":{`,
		`"episode":{`,
		`"episodes":[`,
		`"title":"Show"`,
		`"title":"Pilot"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("populated missing %q in %s", want, got)
		}
	}
	// Branch 2: all nil pointers → omitempty drops all 3 keys.
	empty := QueueResource{ID: 2, Title: "demo"}
	buf2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	got2 := string(buf2)
	for _, want := range []string{`"series":`, `"episode":`, `"episodes":`} {
		if strings.Contains(got2, want) {
			t.Errorf("nil subresource must omit %q; got %s", want, got2)
		}
	}
	// Branch 3: explicit empty Episodes slice → emits `"episodes":[]`
	// (pointer-to-slice preserves the 3-state distinction).
	emptyEps := []EpisodeResource{}
	row3 := QueueResource{ID: 3, Title: "demo", Episodes: &emptyEps}
	buf3, err := json.Marshal(row3)
	if err != nil {
		t.Fatalf("marshal empty-eps: %v", err)
	}
	if !strings.Contains(string(buf3), `"episodes":[]`) {
		t.Errorf("&[]EpisodeResource{} must emit `\"episodes\":[]`; got %s", buf3)
	}
}

// J323 wire-shape pin for QueueStatusMessage typed-list +
// StatusMessages pointer-to-slice 3-state widening on QueueResource.
//
// Test covers all 3 branches: row 1 nil → key absent; row 2
// explicit empty-slice pointer → `"statusMessages":[]`; row 3
// populated with 2-element list → typed nested shape with `title`
// + `messages` keys.
func TestQueueResource_StatusMessagesWidening_J323(t *testing.T) {
	t.Parallel()
	// Branch 1: nil → omitempty drops the key.
	nilCase := QueueResource{ID: 1, Title: "demo"}
	buf, err := json.Marshal(nilCase)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if strings.Contains(string(buf), `"statusMessages":`) {
		t.Errorf("nil StatusMessages must omit the key; got %s", buf)
	}
	// Branch 2: explicit empty-slice pointer → emits `[]`.
	empty := []QueueStatusMessage{}
	emptyCase := QueueResource{ID: 2, Title: "demo", StatusMessages: &empty}
	buf2, err := json.Marshal(emptyCase)
	if err != nil {
		t.Fatalf("marshal empty-slice: %v", err)
	}
	if !strings.Contains(string(buf2), `"statusMessages":[]`) {
		t.Errorf("&[]QueueStatusMessage{} must emit `\"statusMessages\":[]`; got %s", buf2)
	}
	// Branch 3: populated 2-element list.
	msgs := []QueueStatusMessage{
		{Title: "Cannot import:", Messages: []string{"Sample file", "Missing audio"}},
		{Title: "Warning", Messages: []string{"Episode already imported"}},
	}
	populated := QueueResource{ID: 3, Title: "demo", StatusMessages: &msgs}
	buf3, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf3)
	mustContain := []string{
		`"statusMessages":[`,
		`"title":"Cannot import:"`,
		`"messages":["Sample file","Missing audio"]`,
		`"title":"Warning"`,
		`"messages":["Episode already imported"]`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("populated missing %q in %s", want, got)
		}
	}
}

// J324 wire-shape pin for v5-additive list-form widening on
// QueueResource (EpisodeIDs + SeasonNumbers — both pointer-to-
// []int with omitempty per J309 pointer-to-slice 3-state).
//
// Test covers all 3 branches: nil → key absent; non-nil empty →
// `[]`; populated → `[N,N]`.
func TestQueueResource_EpisodeIdsSeasonNumbersWidening_J324(t *testing.T) {
	t.Parallel()
	// Branch 1: nil pointers → omitempty drops both keys.
	nilCase := QueueResource{ID: 1, Title: "demo"}
	buf, err := json.Marshal(nilCase)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	got := string(buf)
	for _, want := range []string{`"episodeIds":`, `"seasonNumbers":`} {
		if strings.Contains(got, want) {
			t.Errorf("nil pointer must omit %q; got %s", want, got)
		}
	}
	// Branch 2: explicit empty slices → emit `[]`.
	emptyIDs := []int{}
	emptyNums := []int{}
	emptyCase := QueueResource{ID: 2, Title: "demo", EpisodeIDs: &emptyIDs, SeasonNumbers: &emptyNums}
	buf2, err := json.Marshal(emptyCase)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	got2 := string(buf2)
	for _, want := range []string{`"episodeIds":[]`, `"seasonNumbers":[]`} {
		if !strings.Contains(got2, want) {
			t.Errorf("empty non-nil slice must emit %q; got %s", want, got2)
		}
	}
	// Branch 3: populated.
	ids := []int{101, 102}
	nums := []int{1, 2}
	populated := QueueResource{ID: 3, Title: "demo", EpisodeIDs: &ids, SeasonNumbers: &nums}
	buf3, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got3 := string(buf3)
	for _, want := range []string{`"episodeIds":[101,102]`, `"seasonNumbers":[1,2]`} {
		if !strings.Contains(got3, want) {
			t.Errorf("populated must emit %q; got %s", want, got3)
		}
	}
}

// ---------- 5.I18: /queue/details tests ----------

type fakeQueueDetailsLister struct {
	rows         []QueueResource
	err          error
	gotSeriesID  *int
	gotEpisodes  []int
	rememberArgs bool
}

func (f *fakeQueueDetailsLister) QueueDetails(_ context.Context, seriesID *int, episodeIDs []int) ([]QueueResource, error) {
	if f.rememberArgs {
		f.gotSeriesID = seriesID
		f.gotEpisodes = append([]int(nil), episodeIDs...)
	}
	return f.rows, f.err
}

func TestQueueDetailsHandler_EmptyArrayShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueDetailsHandler(&fakeQueueDetailsLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestQueueDetailsHandler_SeriesIDParsed(t *testing.T) {
	t.Parallel()
	l := &fakeQueueDetailsLister{rememberArgs: true}
	rr := httptest.NewRecorder()
	QueueDetailsHandler(l).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if l.gotSeriesID == nil || *l.gotSeriesID != 42 {
		t.Errorf("seriesId: %+v", l.gotSeriesID)
	}
}

func TestQueueDetailsHandler_EpisodeIDsParsed(t *testing.T) {
	t.Parallel()
	l := &fakeQueueDetailsLister{rememberArgs: true}
	rr := httptest.NewRecorder()
	QueueDetailsHandler(l).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details?episodeIds=1&episodeIds=2&episodeIds=3", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(l.gotEpisodes) != 3 || l.gotEpisodes[0] != 1 || l.gotEpisodes[2] != 3 {
		t.Errorf("episodeIds: %+v", l.gotEpisodes)
	}
}

func TestQueueDetailsHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueDetailsHandler(&fakeQueueDetailsLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details?seriesId=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestQueueDetailsHandler_InvalidEpisodeID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueDetailsHandler(&fakeQueueDetailsLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details?episodeIds=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestQueueDetailsHandler_ListerError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	QueueDetailsHandler(&fakeQueueDetailsLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestQueueDetailsHandler_RecordsPassthrough(t *testing.T) {
	t.Parallel()
	seriesID := 7
	l := &fakeQueueDetailsLister{rows: []QueueResource{{ID: 1, SeriesID: &seriesID, Title: "demo"}}}
	rr := httptest.NewRecorder()
	QueueDetailsHandler(l).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/queue/details", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"title":"demo"`) {
		t.Errorf("body should include title; got %s", rr.Body.String())
	}
}
