package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

type fakeEpisodeBySeries struct {
	rows []episodes.Episode
	err  error
	got  int
}

func (f *fakeEpisodeBySeries) GetEpisodesBySeries(_ context.Context, id int) ([]episodes.Episode, error) {
	f.got = id
	return f.rows, f.err
}

func TestEpisodeListHandler_EmptyForKnownSeries(t *testing.T) {
	t.Parallel()
	lister := &fakeEpisodeBySeries{}
	rr := httptest.NewRecorder()
	EpisodeListHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
	if lister.got != 42 {
		t.Errorf("seriesId not forwarded: got %d", lister.got)
	}
}

func TestEpisodeListHandler_MissingSeriesId400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestEpisodeListHandler_PopulatedShape(t *testing.T) {
	t.Parallel()
	tvdb := 12345
	lister := &fakeEpisodeBySeries{rows: []episodes.Episode{
		{
			ID: 1, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1,
			Title: "Pilot", TvdbID: &tvdb, Runtime: 60, Monitored: true,
		},
	}}
	rr := httptest.NewRecorder()
	EpisodeListHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=42", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`, `"seriesId":42`, `"tvdbId":12345`,
		`"seasonNumber":1`, `"episodeNumber":1`,
		`"title":"Pilot"`, `"runtime":60`,
		`"monitored":true`, `"hasFile":false`,
	} {
		if !contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

// J327 wire-shape pin for the 3 new pointer fields on EpisodeResource
// (EndTime + GrabDate + Grabbed). All omitempty pointers: nil drops
// the key; populated emits the value. ToEpisodeResource doesn't
// populate them yet (persisted Episode model has no fields backing
// them — the populated-mapper port will allocate when the C# Episode
// model fields land), so the test validates via direct struct
// construction.
func TestEpisodeResource_NewPointerFields_J327(t *testing.T) {
	t.Parallel()
	// Branch 1: nil pointers (default state today) → omitempty drops all 3 keys.
	nilCase := EpisodeResource{ID: 1, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}
	buf, err := json.Marshal(nilCase)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	got := string(buf)
	for _, want := range []string{`"endTime":`, `"grabDate":`, `"grabbed":`} {
		if strings.Contains(got, want) {
			t.Errorf("nil pointer must omit %q; got %s", want, got)
		}
	}
	// Branch 2: populated → all 3 fields emit.
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	grabbed := true
	populated := EpisodeResource{ID: 2, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 2, EndTime: &when, GrabDate: &when, Grabbed: &grabbed}
	buf2, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got2 := string(buf2)
	mustContain := []string{
		`"endTime":"2026-01-02T03:04:05Z"`,
		`"grabDate":"2026-01-02T03:04:05Z"`,
		`"grabbed":true`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got2, want) {
			t.Errorf("populated missing %q in %s", want, got2)
		}
	}
}

// J342 wire-shape pin for the includeEpisodeFile query-param + the
// EpisodeFileLookupForEpisode optional-extension type-assertion
// integration on EpisodeListHandler + EpisodeByIDHandler. When BOTH
// the query-param is true AND the lister/svc implements
// EpisodeFileLookupForEpisode, the handler populates the J340-pinned
// EpisodeResource.EpisodeFile subresource via ToEpisodeFileResource
// + ApplyEpisodeFilePopulator. When either condition is false
// (default or non-implementing lister), the subresource stays nil
// and omitempty drops the key.
//
// Test covers 3 branches: (a) no includeEpisodeFile param → no
// populate; (b) includeEpisodeFile=true + non-implementing lister
// (default fake) → no populate; (c) includeEpisodeFile=true +
// implementing lister → populated subresource.
func TestEpisodeListHandler_IncludeEpisodeFileSubresource_J342(t *testing.T) {
	t.Parallel()
	tvdb := 12345
	fileID := 99
	rows := []episodes.Episode{{ID: 1, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", TvdbID: &tvdb, EpisodeFileID: &fileID, Runtime: 60, Monitored: true}}

	t.Run("no includeEpisodeFile param → subresource omitted", func(t *testing.T) {
		lister := &fakeEpisodeBySeriesWithFileLookup{
			rows:       rows,
			fileLookup: map[int]*episodefiles.File{99: {ID: 99, SeriesID: 42, RelativePath: "Show.S01E01.mkv"}},
		}
		rr := httptest.NewRecorder()
		EpisodeListHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=42", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status: %d", rr.Code)
		}
		if strings.Contains(rr.Body.String(), `"episodeFile":`) {
			t.Errorf("absent includeEpisodeFile must keep the subresource omitted; got %s", rr.Body.String())
		}
	})

	t.Run("includeEpisodeFile=true with non-implementing lister → subresource omitted", func(t *testing.T) {
		lister := &fakeEpisodeBySeries{rows: rows}
		rr := httptest.NewRecorder()
		EpisodeListHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=42&includeEpisodeFile=true", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status: %d", rr.Code)
		}
		if strings.Contains(rr.Body.String(), `"episodeFile":`) {
			t.Errorf("non-implementing lister must skip the subresource; got %s", rr.Body.String())
		}
	})

	t.Run("includeEpisodeFile=true with implementing lister → populated subresource", func(t *testing.T) {
		lister := &fakeEpisodeBySeriesWithFileLookup{
			rows:       rows,
			fileLookup: map[int]*episodefiles.File{99: {ID: 99, SeriesID: 42, RelativePath: "Show.S01E01.mkv"}},
		}
		rr := httptest.NewRecorder()
		EpisodeListHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=42&includeEpisodeFile=true", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		if !strings.Contains(body, `"episodeFile":{`) {
			t.Errorf("implementing lister must populate the subresource; got %s", body)
		}
		if !strings.Contains(body, `"relativePath":"Show.S01E01.mkv"`) {
			t.Errorf("inner file's relativePath missing; got %s", body)
		}
	})
}

// fakeEpisodeBySeriesWithFileLookup satisfies BOTH EpisodeBySeriesLister
// AND EpisodeFileLookupForEpisode — exercises the J342 type-assertion
// branch.
type fakeEpisodeBySeriesWithFileLookup struct {
	rows       []episodes.Episode
	fileLookup map[int]*episodefiles.File
}

func (f *fakeEpisodeBySeriesWithFileLookup) GetEpisodesBySeries(_ context.Context, _ int) ([]episodes.Episode, error) {
	return f.rows, nil
}
func (f *fakeEpisodeBySeriesWithFileLookup) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if file, ok := f.fileLookup[id]; ok {
		return file, nil
	}
	return nil, episodefiles.ErrNotFound
}

// J340 wire-shape pin for the EpisodeResource.EpisodeFile pointer-
// to-struct widening (J302 pattern). Test covers both branches:
// nil → omitempty drops the key; populated → emits the nested
// envelope.
func TestEpisodeResource_EpisodeFileField_J340(t *testing.T) {
	t.Parallel()
	// Branch 1: nil (today's default) → omitempty drops the key.
	nilCase := EpisodeResource{ID: 1, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}
	buf, err := json.Marshal(nilCase)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if strings.Contains(string(buf), `"episodeFile":`) {
		t.Errorf("nil EpisodeFile must omit the key; got %s", buf)
	}
	// Branch 2: populated → emits the nested envelope.
	populated := EpisodeResource{
		ID: 2, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 2,
		EpisodeFile: &EpisodeFileResource{ID: 99, SeriesID: 42, RelativePath: "S01E02.mkv", Languages: []LanguageRef{}, CustomFormats: []CustomFormatResource{}},
	}
	buf2, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("marshal populated: %v", err)
	}
	got := string(buf2)
	mustContain := []string{
		`"episodeFile":{`,
		`"id":99`,
		`"relativePath":"S01E02.mkv"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("populated EpisodeFile missing %q in %s", want, got)
		}
	}
}

func TestEpisodeListHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=1", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestEpisodeListHandler_InvalidSeriesId400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// strconv reference to silence unused-import nags when tests change.
var _ = strconv.Itoa

// ---------- 5.X8: GET /episode/{id} tests ----------

type fakeEpisodeByIDGetter struct {
	row *episodes.Episode
	err error
}

func (f *fakeEpisodeByIDGetter) GetEpisode(_ context.Context, _ int) (*episodes.Episode, error) {
	return f.row, f.err
}

func TestEpisodeByIDHandler_Found(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/episode/5", nil)
	req.SetPathValue("id", "5")
	EpisodeByIDHandler(&fakeEpisodeByIDGetter{row: &episodes.Episode{ID: 5, SeriesID: 1, Title: "Pilot"}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
}

func TestEpisodeByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/episode/999", nil)
	req.SetPathValue("id", "999")
	EpisodeByIDHandler(&fakeEpisodeByIDGetter{err: episodes.ErrNotFound}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeByIDHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/episode/abc", nil)
	req.SetPathValue("id", "abc")
	EpisodeByIDHandler(&fakeEpisodeByIDGetter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.D4a: PUT /episode/monitor tests ----------

type fakeEpisodeMonitorWriter struct {
	rows       []episodes.Episode
	setOne     *int
	setOneVal  *bool
	setBulkIDs []int
	setBulkVal *bool
	err        error
}

func (f *fakeEpisodeMonitorWriter) GetEpisodesBySeries(_ context.Context, _ int) ([]episodes.Episode, error) {
	return f.rows, f.err
}
func (f *fakeEpisodeMonitorWriter) GetEpisodes(_ context.Context, _ []int) ([]episodes.Episode, error) {
	return f.rows, f.err
}
func (f *fakeEpisodeMonitorWriter) SetEpisodeMonitored(_ context.Context, id int, monitored bool) error {
	f.setOne = &id
	f.setOneVal = &monitored
	return f.err
}
func (f *fakeEpisodeMonitorWriter) SetMonitored(_ context.Context, ids []int, monitored bool) error {
	f.setBulkIDs = append([]int(nil), ids...)
	f.setBulkVal = &monitored
	return f.err
}

// fakeEpisodeMultiLister implements every branch of EpisodeListHandler
// and records which one fired so the dispatch precedence can be pinned.
type fakeEpisodeMultiLister struct {
	called string
	gotInt int
	gotIDs []int
	rows   []episodes.Episode
}

func (f *fakeEpisodeMultiLister) GetEpisodesBySeries(_ context.Context, id int) ([]episodes.Episode, error) {
	f.called, f.gotInt = "bySeries", id
	return f.rows, nil
}
func (f *fakeEpisodeMultiLister) GetEpisodesBySeason(_ context.Context, seriesID, season int) ([]episodes.Episode, error) {
	f.called, f.gotInt = "bySeason", season
	return f.rows, nil
}
func (f *fakeEpisodeMultiLister) GetEpisodes(_ context.Context, ids []int) ([]episodes.Episode, error) {
	f.called, f.gotIDs = "byIDs", append([]int(nil), ids...)
	return f.rows, nil
}
func (f *fakeEpisodeMultiLister) GetEpisodesByFileID(_ context.Context, fileID int) ([]episodes.Episode, error) {
	f.called, f.gotInt = "byFile", fileID
	return f.rows, nil
}

func TestEpisodeListHandler_DispatchBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		query      string
		wantCalled string
		wantCode   int
	}{
		{"seriesId only", "seriesId=3", "bySeries", http.StatusOK},
		{"seriesId+seasonNumber", "seriesId=3&seasonNumber=2", "bySeason", http.StatusOK},
		{"episodeIds repeated", "episodeIds=4&episodeIds=5", "byIDs", http.StatusOK},
		{"episodeIds comma", "episodeIds=4,5", "byIDs", http.StatusOK},
		{"episodeFileId", "episodeFileId=9", "byFile", http.StatusOK},
		{"none → 400", "", "", http.StatusBadRequest},
		{"bad seriesId → 400", "seriesId=x", "", http.StatusBadRequest},
		{"bad seasonNumber → 400", "seriesId=3&seasonNumber=x", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeEpisodeMultiLister{rows: []episodes.Episode{{ID: 1, SeriesID: 3}}}
			req := httptest.NewRequest(http.MethodGet, "/api/v3/episode?"+tc.query, nil)
			rr := httptest.NewRecorder()
			EpisodeListHandler(f).ServeHTTP(rr, req)
			if rr.Code != tc.wantCode {
				t.Fatalf("status: got %d want %d (body=%s)", rr.Code, tc.wantCode, rr.Body)
			}
			if tc.wantCalled != "" && f.called != tc.wantCalled {
				t.Errorf("dispatch: got %q want %q", f.called, tc.wantCalled)
			}
		})
	}
}

func TestEpisodeByIDPutHandler_SetsMonitoredAndReturnsRow(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeMonitorWriter{rows: []episodes.Episode{{ID: 7, SeriesID: 1, Monitored: false}}}
	rr := httptest.NewRecorder()
	// Full EpisodeResource-shaped body: only `monitored` is read, the
	// extra fields must not trip a strict-decode rejection.
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episode/7", strings.NewReader(`{"id":7,"seriesId":1,"monitored":false,"title":"x"}`))
	req.SetPathValue("id", "7")
	EpisodeByIDPutHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d want 202; body=%s", rr.Code, rr.Body.String())
	}
	if w.setOne == nil || *w.setOne != 7 || w.setOneVal == nil || *w.setOneVal {
		t.Errorf("SetEpisodeMonitored not called for id=7,false: %+v %+v", w.setOne, w.setOneVal)
	}
	var got EpisodeResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("response row: %+v", got)
	}
}

func TestEpisodeByIDPutHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episode/abc", strings.NewReader(`{"monitored":true}`))
	req.SetPathValue("id", "abc")
	EpisodeByIDPutHandler(&fakeEpisodeMonitorWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d want 400", rr.Code)
	}
}

func TestEpisodeByIDPutHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeMonitorWriter{err: episodes.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episode/9", strings.NewReader(`{"monitored":true}`))
	req.SetPathValue("id", "9")
	EpisodeByIDPutHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: %d want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestEpisodeMonitorHandler_SingleID(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeMonitorWriter{rows: []episodes.Episode{{ID: 5, SeriesID: 1, Monitored: true}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episode/monitor", strings.NewReader(`{"episodeIds":[5],"monitored":true}`))
	EpisodeMonitorHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.setOne == nil || *w.setOne != 5 || w.setOneVal == nil || !*w.setOneVal {
		t.Errorf("SetEpisodeMonitored not called for id=5,true: %+v %+v", w.setOne, w.setOneVal)
	}
	if w.setBulkIDs != nil {
		t.Errorf("bulk path should not fire for single id: %+v", w.setBulkIDs)
	}
	var got []EpisodeResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].ID != 5 {
		t.Errorf("response: %+v", got)
	}
}

func TestEpisodeMonitorHandler_BulkIDs(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeMonitorWriter{rows: []episodes.Episode{
		{ID: 1, SeriesID: 1, Monitored: false},
		{ID: 2, SeriesID: 1, Monitored: false},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episode/monitor", strings.NewReader(`{"episodeIds":[1,2],"monitored":false}`))
	EpisodeMonitorHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.setOne != nil {
		t.Errorf("single path should not fire for bulk: %+v", w.setOne)
	}
	if len(w.setBulkIDs) != 2 || w.setBulkIDs[0] != 1 || w.setBulkIDs[1] != 2 {
		t.Errorf("bulk ids: %+v", w.setBulkIDs)
	}
	if w.setBulkVal == nil || *w.setBulkVal {
		t.Errorf("bulk monitored value: %+v", w.setBulkVal)
	}
}

func TestEpisodeMonitorHandler_MissingIDs_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episode/monitor", strings.NewReader(`{"episodeIds":[],"monitored":true}`))
	EpisodeMonitorHandler(&fakeEpisodeMonitorWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeMonitorHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episode/monitor", strings.NewReader(`not json`))
	EpisodeMonitorHandler(&fakeEpisodeMonitorWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeMonitorHandler_ServiceError_500(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeMonitorWriter{err: errors.New("boom")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episode/monitor", strings.NewReader(`{"episodeIds":[1],"monitored":true}`))
	EpisodeMonitorHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

// TestEpisodeImages_J371 covers the includeImages branch: nil when the
// param is absent (pointer-to-slice key absent), and a decoded list
// (int-ordinal coverType → camelCase string; url/remoteUrl passthrough,
// NO local-URL rewrite) when includeImages=true.
func TestEpisodeImages_J371(t *testing.T) {
	t.Parallel()
	row := episodes.Episode{
		ID: 5, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1,
		Images: `[{"coverType":4,"url":"/persisted/screenshot","remoteUrl":"https://r/s.jpg"}]`,
	}

	// includeImages absent → Images pointer nil → key absent.
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{rows: []episodes.Episode{row}}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=1", nil))
	if strings.Contains(rr.Body.String(), `"images"`) {
		t.Errorf("images must be absent without includeImages; got %s", rr.Body.String())
	}

	// includeImages=true → list with screenshot mapping + persisted url.
	rr2 := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{rows: []episodes.Episode{row}}).ServeHTTP(rr2,
		httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=1&includeImages=true", nil))
	var got []EpisodeResource
	if err := json.Unmarshal(rr2.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Images == nil || len(*got[0].Images) != 1 {
		t.Fatalf("images not populated: %+v", got)
	}
	img := (*got[0].Images)[0]
	if img.CoverType != "screenshot" || img.Url != "/persisted/screenshot" || img.RemoteUrl != "https://r/s.jpg" {
		t.Errorf("image projection: %+v", img)
	}
}

// TestEpisodeImages_EmptyList_J371 — includeImages=true with no images
// emits "images":[] (pointer-to-non-nil-empty), matching C#'s non-null
// List default, NOT an absent key.
func TestEpisodeImages_EmptyList_J371(t *testing.T) {
	t.Parallel()
	row := episodes.Episode{ID: 5, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, Images: ""}
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{rows: []episodes.Episode{row}}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=1&includeImages=true", nil))
	if !strings.Contains(rr.Body.String(), `"images":[]`) {
		t.Errorf("empty images must emit []; got %s", rr.Body.String())
	}
}

// fakeEpisodeBySeriesWithSeriesLookup satisfies BOTH EpisodeBySeriesLister
// AND EpisodeSeriesLookup — exercises the J372 includeSeries branch and
// counts GetSeries calls so the per-request cache can be verified.
type fakeEpisodeBySeriesWithSeriesLookup struct {
	rows         []episodes.Episode
	seriesByID   map[int]*series.Series
	getSeriesHit int
}

func (f *fakeEpisodeBySeriesWithSeriesLookup) GetEpisodesBySeries(_ context.Context, _ int) ([]episodes.Episode, error) {
	return f.rows, nil
}
func (f *fakeEpisodeBySeriesWithSeriesLookup) GetSeries(_ context.Context, id int) (*series.Series, error) {
	f.getSeriesHit++
	if s, ok := f.seriesByID[id]; ok {
		return s, nil
	}
	return nil, series.ErrNotFound
}

// TestEpisodeSeries_J372 covers the includeSeries embed: absent without
// the param; absent when the lister doesn't implement EpisodeSeriesLookup;
// populated as a BASE SeriesResource (no statistics) when the param fires
// and the lister implements the lookup.
func TestEpisodeSeries_J372(t *testing.T) {
	t.Parallel()
	row := episodes.Episode{ID: 5, SeriesID: 7, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}

	// (a) includeSeries absent → series key absent.
	rr := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeriesWithSeriesLookup{
		rows:       []episodes.Episode{row},
		seriesByID: map[int]*series.Series{7: {ID: 7, Title: "Breaking Bad"}},
	}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=7", nil))
	if strings.Contains(rr.Body.String(), `"series"`) {
		t.Errorf("series must be absent without includeSeries; got %s", rr.Body.String())
	}

	// (b) includeSeries=true but lister doesn't implement the lookup → absent.
	rr2 := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeries{rows: []episodes.Episode{row}}).ServeHTTP(rr2,
		httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=7&includeSeries=true", nil))
	if strings.Contains(rr2.Body.String(), `"series"`) {
		t.Errorf("series must be absent when lister lacks EpisodeSeriesLookup; got %s", rr2.Body.String())
	}

	// (c) includeSeries=true + implementing lister → populated base embed.
	rr3 := httptest.NewRecorder()
	EpisodeListHandler(&fakeEpisodeBySeriesWithSeriesLookup{
		rows:       []episodes.Episode{row},
		seriesByID: map[int]*series.Series{7: {ID: 7, Title: "Breaking Bad"}},
	}).ServeHTTP(rr3, httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=7&includeSeries=true", nil))
	var got []EpisodeResource
	if err := json.Unmarshal(rr3.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Series == nil {
		t.Fatalf("series not populated: %+v", got)
	}
	if got[0].Series.ID != 7 || got[0].Series.Title != "Breaking Bad" {
		t.Errorf("embedded series fields: %+v", got[0].Series)
	}
	// BASE projection: no statistics linked on the embed.
	if strings.Contains(rr3.Body.String(), `"statistics"`) {
		t.Errorf("embedded series must NOT carry statistics (base ToResource); got %s", rr3.Body.String())
	}
}

// TestEpisodeSeries_PerRequestCache_J372 — the list path resolves each
// distinct seriesId exactly once across rows (the per-request cache that
// mirrors C#'s seriesDict).
func TestEpisodeSeries_PerRequestCache_J372(t *testing.T) {
	t.Parallel()
	lister := &fakeEpisodeBySeriesWithSeriesLookup{
		rows: []episodes.Episode{
			{ID: 1, SeriesID: 7, SeasonNumber: 1, EpisodeNumber: 1, Title: "A"},
			{ID: 2, SeriesID: 7, SeasonNumber: 1, EpisodeNumber: 2, Title: "B"},
			{ID: 3, SeriesID: 7, SeasonNumber: 1, EpisodeNumber: 3, Title: "C"},
		},
		seriesByID: map[int]*series.Series{7: {ID: 7, Title: "Breaking Bad"}},
	}
	rr := httptest.NewRecorder()
	EpisodeListHandler(lister).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/episode?seriesId=7&includeSeries=true", nil))
	if lister.getSeriesHit != 1 {
		t.Errorf("expected 1 GetSeries call across 3 same-series rows, got %d", lister.getSeriesHit)
	}
	var got []EpisodeResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, e := range got {
		if e.Series == nil || e.Series.ID != 7 {
			t.Errorf("row %d series not populated from cache: %+v", i, e.Series)
		}
	}
}
