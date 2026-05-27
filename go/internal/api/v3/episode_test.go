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
