package v3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

type fakeSeriesLister struct {
	rows []series.Series
	err  error
}

func (f *fakeSeriesLister) GetAllSeries(context.Context) ([]series.Series, error) {
	return f.rows, f.err
}

func TestSeriesListHandler_EmptyList(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesListHandler(&fakeSeriesLister{}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body should be []: %q", rr.Body.String())
	}
}

func TestSeriesListHandler_ScalarProjection(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesListHandler(&fakeSeriesLister{rows: []series.Series{
		{ID: 1, Title: "Demo", SortTitle: "demo", Status: 1, Path: "/tv/Demo", Monitored: true, SeriesType: 2, MonitorNewItems: 1, Runtime: 30, Tags: []int{1, 2}},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))

	var got []SeriesResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("count: %+v", got)
	}
	r := got[0]
	if r.Title != "Demo" || r.Path != "/tv/Demo" || !r.Monitored || r.Runtime != 30 {
		t.Errorf("scalar projection: %+v", r)
	}
	if r.Status != "ended" || r.SeriesType != "anime" || r.MonitorNewItems != "none" {
		t.Errorf("enum projection: status=%q seriesType=%q monitorNewItems=%q", r.Status, r.SeriesType, r.MonitorNewItems)
	}
	if !r.Ended {
		t.Errorf("ended computed flag: false, want true (status=1)")
	}
	if r.LanguageProfileID != 1 {
		t.Errorf("languageProfileId hard-coded: %d, want 1", r.LanguageProfileID)
	}
	if len(r.Tags) != 2 || r.Tags[0] != 1 {
		t.Errorf("tags: %v", r.Tags)
	}
}

func TestSeriesListHandler_NilTagsEmitsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesListHandler(&fakeSeriesLister{rows: []series.Series{
		{ID: 1, Title: "NoTags", Tags: nil},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `"tags":[]`) {
		t.Errorf("nil Tags should emit []: %s", body)
	}
}

// ---------- 5.X7: GET /series/{id} tests ----------

type fakeSeriesByIDGetter struct {
	row *series.Series
	err error
}

func (f *fakeSeriesByIDGetter) GetSeries(_ context.Context, _ int) (*series.Series, error) {
	return f.row, f.err
}

func TestSeriesByIDHandler_Found(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series/42", nil)
	req.SetPathValue("id", "42")
	SeriesByIDHandler(&fakeSeriesByIDGetter{row: &series.Series{ID: 42, Title: "My Show"}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"title":"My Show"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestSeriesByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series/999", nil)
	req.SetPathValue("id", "999")
	SeriesByIDHandler(&fakeSeriesByIDGetter{err: series.ErrNotFound}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesByIDHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series/abc", nil)
	req.SetPathValue("id", "abc")
	SeriesByIDHandler(&fakeSeriesByIDGetter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesListHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesListHandler(&fakeSeriesLister{err: errors.New("boom")}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestSeriesEnumNames(t *testing.T) {
	t.Parallel()
	if seriesStatusName(0) != "continuing" || seriesStatusName(1) != "ended" {
		t.Errorf("seriesStatusName mapping wrong")
	}
	if seriesTypeName(0) != "standard" || seriesTypeName(2) != "anime" {
		t.Errorf("seriesTypeName mapping wrong")
	}
	if monitorNewItemsName(0) != "all" || monitorNewItemsName(1) != "none" {
		t.Errorf("monitorNewItemsName mapping wrong")
	}
	if seriesStatusName(99) != "continuing" {
		t.Errorf("fallback for unknown status")
	}
}

// fakeSeriesWriter implements SeriesWriter against an in-memory
// table. Same shape as the 5.C-family fakes — Insert auto-assigns
// IDs; Update + Delete match by id with ErrNotFound on miss.
//
// 5.D5 added cascade tracking via [fakeSeriesCascadeWriter] (a
// type-aliased variant that ALSO satisfies SeriesCascader). The
// plain fakeSeriesWriter doesn't — letting tests opt in to the
// type-assert cascade behavior only when relevant.
type fakeSeriesWriter struct {
	rows   []series.Series
	nextID int64
	getErr error
}

func (f *fakeSeriesWriter) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
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
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
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
func (f *fakeSeriesWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return series.ErrNotFound
}

type fakeSeriesEmitter struct {
	created []SeriesResource
	updated []SeriesResource
	deleted []SeriesResource
}

func (f *fakeSeriesEmitter) Created(_ context.Context, r SeriesResource) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeSeriesEmitter) Updated(_ context.Context, r SeriesResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeSeriesEmitter) Deleted(_ context.Context, r SeriesResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

func validSeriesBody() SeriesResource {
	return SeriesResource{
		Title:            "Breaking Bad",
		SortTitle:        "breaking bad",
		Status:           "ended",
		Path:             "/tv/Breaking Bad",
		QualityProfileID: 1,
		Monitored:        true,
		SeasonFolder:     true,
		TvdbID:           81189,
		SeriesType:       "standard",
		CleanTitle:       "breakingbad",
		TitleSlug:        "breaking-bad",
		MonitorNewItems:  "all",
		Tags:             []int{},
		Genres:           []string{},
	}
}

func TestSeriesCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesWriter{}
	em := &fakeSeriesEmitter{}
	body := validSeriesBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesCreateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series", bytes.NewReader(buf)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Title != "Breaking Bad" || w.rows[0].ID <= 0 {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	if w.rows[0].Status != series.SeriesStatusEnded {
		t.Errorf("Status enum: %d", w.rows[0].Status)
	}
	if w.rows[0].SeriesType != series.SeriesTypeStandard {
		t.Errorf("SeriesType enum: %d", w.rows[0].SeriesType)
	}
	if len(em.created) != 1 || em.created[0].Title != "Breaking Bad" {
		t.Errorf("emitter.Created: %+v", em.created)
	}
}

func TestSeriesCreateHandler_TitleMissing_400(t *testing.T) {
	t.Parallel()
	body := validSeriesBody()
	body.Title = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesCreateHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesCreateHandler_PathMissing_400(t *testing.T) {
	t.Parallel()
	body := validSeriesBody()
	body.Path = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesCreateHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesCreateHandler_QualityProfileMissing_400(t *testing.T) {
	t.Parallel()
	body := validSeriesBody()
	body.QualityProfileID = 0
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesCreateHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesCreateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesCreateHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	existing := series.Series{ID: 42, Title: "Breaking Bad", Path: "/tv/Breaking Bad", QualityProfileID: 1, Images: "[]"}
	w := &fakeSeriesWriter{rows: []series.Series{existing}}
	em := &fakeSeriesEmitter{}
	body := validSeriesBody()
	body.ID = 42
	body.Title = "Breaking Bad (US)"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/series/42", bytes.NewReader(buf))
	req.SetPathValue("id", "42")
	rr := httptest.NewRecorder()
	SeriesUpdateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Title != "Breaking Bad (US)" {
		t.Errorf("row not renamed: %+v", w.rows[0])
	}
	if w.rows[0].Images != "[]" {
		t.Errorf("Images JSON should carry forward from existing: %q", w.rows[0].Images)
	}
	if len(em.updated) != 1 {
		t.Errorf("emitter.Updated not fired")
	}
}

func TestSeriesUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	body := validSeriesBody()
	body.ID = 999
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/series/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	SeriesUpdateHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesUpdateHandler_MoveFilesParamParsedButIgnored(t *testing.T) {
	t.Parallel()
	// 5.D1a defers the MoveSeriesCommand cascade. The ?moveFiles=true
	// param is parsed but ignored — no error returned, DB updates
	// happen, no on-disk side-effect (which we can't verify here but
	// confirms the param doesn't break the happy path).
	existing := series.Series{ID: 1, Title: "Original", Path: "/old/path", QualityProfileID: 1}
	w := &fakeSeriesWriter{rows: []series.Series{existing}}
	body := validSeriesBody()
	body.ID = 1
	body.Path = "/new/path"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/series/1?moveFiles=true", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	SeriesUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Path != "/new/path" {
		t.Errorf("Path should update: %q", w.rows[0].Path)
	}
}

func TestSeriesDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesWriter{rows: []series.Series{{ID: 7, Title: "victim"}}}
	em := &fakeSeriesEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(em.deleted) != 1 || em.deleted[0].ID != 7 {
		t.Errorf("emitter.Deleted: %+v", em.deleted)
	}
}

func TestSeriesDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			SeriesDeleteHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
}

func TestSeriesEnumFromName_Inverses(t *testing.T) {
	t.Parallel()
	if seriesStatusFromName("ended") != series.SeriesStatusEnded {
		t.Error("seriesStatusFromName ended")
	}
	if seriesStatusFromName("CONTINUING") != series.SeriesStatusContinuing {
		t.Error("case-insensitive")
	}
	if seriesStatusFromName("bogus") != series.SeriesStatusContinuing {
		t.Error("default → continuing")
	}
	if seriesTypeFromName("anime") != series.SeriesTypeAnime {
		t.Error("seriesTypeFromName anime")
	}
	if monitorNewItemsFromName("none") != 1 {
		t.Error("monitorNewItemsFromName none")
	}
}

func stringPtrSeries(s string) *string { return &s }

func TestSeriesEditorUpdateHandler_MonitoredToggle(t *testing.T) {
	t.Parallel()
	rows := []series.Series{
		{ID: 1, Title: "A", Monitored: true},
		{ID: 2, Title: "B", Monitored: true},
	}
	w := &fakeSeriesWriter{rows: rows}
	em := &fakeSeriesEmitter{}
	mon := false
	body := SeriesEditorResource{SeriesIDs: []int{1, 2}, Monitored: &mon}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Monitored || w.rows[1].Monitored {
		t.Errorf("Monitored not toggled: %+v", w.rows)
	}
	if len(em.updated) != 2 {
		t.Errorf("Updated count: %d", len(em.updated))
	}
}

func TestSeriesEditorUpdateHandler_QualityProfileChange(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, QualityProfileID: 1}}
	w := &fakeSeriesWriter{rows: rows}
	qp := 42
	body := SeriesEditorResource{SeriesIDs: []int{1}, QualityProfileID: &qp}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].QualityProfileID != 42 {
		t.Errorf("QualityProfileID: %d", w.rows[0].QualityProfileID)
	}
}

func TestSeriesEditorUpdateHandler_PerFieldNilSkip(t *testing.T) {
	t.Parallel()
	// Only Monitored is in the body; Series fields stay intact.
	rows := []series.Series{{
		ID:               1,
		Title:            "A",
		Monitored:        true,
		QualityProfileID: 10,
		SeriesType:       series.SeriesTypeAnime,
		SeasonFolder:     true,
	}}
	w := &fakeSeriesWriter{rows: rows}
	mon := false
	body := SeriesEditorResource{SeriesIDs: []int{1}, Monitored: &mon}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Monitored {
		t.Errorf("Monitored should be false: %v", w.rows[0].Monitored)
	}
	if w.rows[0].QualityProfileID != 10 || w.rows[0].SeriesType != series.SeriesTypeAnime || !w.rows[0].SeasonFolder {
		t.Errorf("other fields should be preserved: %+v", w.rows[0])
	}
}

func TestSeriesEditorUpdateHandler_TagsAddRemoveReplace(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, Tags: []int{10, 20}}}
	w := &fakeSeriesWriter{rows: rows}
	// Add 30
	body := SeriesEditorResource{SeriesIDs: []int{1}, Tags: []int{30}, ApplyTags: applyTagsAdd}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if !equalIntSet(w.rows[0].Tags, []int{10, 20, 30}) {
		t.Errorf("after Add: %v", w.rows[0].Tags)
	}
	// Remove 20
	body = SeriesEditorResource{SeriesIDs: []int{1}, Tags: []int{20}, ApplyTags: applyTagsRemove}
	buf, _ = json.Marshal(body)
	rr = httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if !equalIntSet(w.rows[0].Tags, []int{10, 30}) {
		t.Errorf("after Remove: %v", w.rows[0].Tags)
	}
	// Replace with [99]
	body = SeriesEditorResource{SeriesIDs: []int{1}, Tags: []int{99}, ApplyTags: applyTagsReplace}
	buf, _ = json.Marshal(body)
	rr = httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if !equalIntSet(w.rows[0].Tags, []int{99}) {
		t.Errorf("after Replace: %v", w.rows[0].Tags)
	}
}

func TestSeriesEditorUpdateHandler_SeriesTypeStringFromWire(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, SeriesType: series.SeriesTypeStandard}}
	w := &fakeSeriesWriter{rows: rows}
	st := "anime"
	body := SeriesEditorResource{SeriesIDs: []int{1}, SeriesType: &st}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].SeriesType != series.SeriesTypeAnime {
		t.Errorf("SeriesType: %d (want anime=2)", w.rows[0].SeriesType)
	}
}

func TestSeriesEditorUpdateHandler_RootFolderPathUpdates(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, RootFolderPath: "/old"}}
	w := &fakeSeriesWriter{rows: rows}
	body := SeriesEditorResource{SeriesIDs: []int{1}, RootFolderPath: "/new", MoveFiles: true}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].RootFolderPath != "/new" {
		t.Errorf("RootFolderPath: %q", w.rows[0].RootFolderPath)
	}
	// MoveFiles is parsed-and-ignored (BulkMoveSeriesCommand DEFERRED).
}

func TestSeriesEditorUpdateHandler_EmptyIDs_400(t *testing.T) {
	t.Parallel()
	body := SeriesEditorResource{SeriesIDs: nil}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesEditorUpdateHandler_MissingIdsSilentlySkipped(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1}}
	w := &fakeSeriesWriter{rows: rows}
	em := &fakeSeriesEmitter{}
	mon := false
	body := SeriesEditorResource{SeriesIDs: []int{1, 999}, Monitored: &mon}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(em.updated) != 1 {
		t.Errorf("Updated count: %d (want 1)", len(em.updated))
	}
}

func TestSeriesEditorDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	rows := []series.Series{
		{ID: 1, Title: "a"},
		{ID: 2, Title: "b"},
		{ID: 3, Title: "c"},
	}
	w := &fakeSeriesWriter{rows: rows}
	em := &fakeSeriesEmitter{}
	body := SeriesEditorResource{SeriesIDs: []int{1, 3}, DeleteFiles: true}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorDeleteHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].ID != 2 {
		t.Errorf("rows after delete: %+v", w.rows)
	}
	if len(em.deleted) != 2 {
		t.Errorf("Deleted count: %d", len(em.deleted))
	}
}

func TestSeriesEditorDeleteHandler_EmptyIDs_400(t *testing.T) {
	t.Parallel()
	body := SeriesEditorResource{SeriesIDs: nil}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorDeleteHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesEditorUpdateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesEditorUpdateHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v3/series/editor", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
	_ = stringPtrSeries("") // silence unused
}

func TestSeriesImportHandler_EmptyArray_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesImportHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series/import", bytes.NewReader([]byte(`[]`))))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `[]` {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestSeriesImportHandler_SingleRow_Success(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesWriter{}
	em := &fakeSeriesEmitter{}
	body := []SeriesResource{validSeriesBody()}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesImportHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series/import", bytes.NewReader(buf)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Title != "Breaking Bad" {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	if len(em.created) != 1 {
		t.Errorf("emitter.Created: %+v", em.created)
	}
	var got []SeriesResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 1 || got[0].ID <= 0 {
		t.Errorf("response: %+v", got)
	}
}

func TestSeriesImportHandler_MultiRow_Success(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesWriter{}
	em := &fakeSeriesEmitter{}
	row1 := validSeriesBody()
	row2 := validSeriesBody()
	row2.Title = "Better Call Saul"
	row2.TvdbID = 273181
	row2.TitleSlug = "better-call-saul"
	body := []SeriesResource{row1, row2}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesImportHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series/import", bytes.NewReader(buf)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 2 {
		t.Errorf("len: %d", len(w.rows))
	}
	if w.rows[0].Title != "Breaking Bad" || w.rows[1].Title != "Better Call Saul" {
		t.Errorf("rows: %+v %+v", w.rows[0].Title, w.rows[1].Title)
	}
	if len(em.created) != 2 {
		t.Errorf("emitter.Created count: %d", len(em.created))
	}
}

func TestSeriesImportHandler_PerRowValidationFailure_AbortsBatchWith400(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesWriter{}
	row1 := validSeriesBody()
	row2 := validSeriesBody()
	row2.Title = "" // invalid
	body := []SeriesResource{row1, row2}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesImportHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series/import", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	// Verify NO rows inserted — the validation pre-pass aborts before
	// any DB write.
	if len(w.rows) != 0 {
		t.Errorf("rows should be empty on validation failure: %+v", w.rows)
	}
	// Verify the failure carries the [1].Title prefix (row index 1
	// failed; the React UI uses this to highlight the offending row).
	var failures []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &failures)
	if len(failures) == 0 {
		t.Fatalf("body: %s", rr.Body.String())
	}
	prop, _ := failures[0]["propertyName"].(string)
	if !strings.HasPrefix(prop, "[1].") {
		t.Errorf("propertyName should be prefixed [1].* : %q", prop)
	}
}

func TestSeriesImportHandler_MissingRequiredFields_400(t *testing.T) {
	t.Parallel()
	body := []SeriesResource{{Title: "OnlyTitle"}} // path + qualityProfileId missing
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesImportHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series/import", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesImportHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesImportHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series/import", bytes.NewReader([]byte(`{not-array`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesImportHandler_NotAnArray_400(t *testing.T) {
	t.Parallel()
	// Body is a single SeriesResource object, not an array — should
	// fail decoding.
	body := validSeriesBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesImportHandler(&fakeSeriesWriter{}, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/series/import", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// fakeSeriesCascadeWriter is the cascade-aware variant of
// fakeSeriesWriter. Embeds the plain writer + tracks DeleteCascade
// invocations so tests can confirm the type-assert path fired.
type fakeSeriesCascadeWriter struct {
	*fakeSeriesWriter
	cascadeErr  error
	cascadedIDs []int
}

func (f *fakeSeriesCascadeWriter) DeleteCascade(ctx context.Context, id int) error {
	if f.cascadeErr != nil {
		return f.cascadeErr
	}
	f.cascadedIDs = append(f.cascadedIDs, id)
	// On a real cascade the episodefiles + episodes get cleaned
	// first; the fake collapses to the underlying Delete for the
	// series-row removal so post-conditions assert the same way as
	// the non-cascade tests (row count drops by 1).
	return f.Delete(ctx, id)
}

func TestSeriesDeleteHandler_DeleteFilesTrue_FiresCascade(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesCascadeWriter{fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{{ID: 7, Title: "victim"}}}}
	em := &fakeSeriesEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7?deleteFiles=true", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.cascadedIDs) != 1 || w.cascadedIDs[0] != 7 {
		t.Errorf("DeleteCascade should be invoked with id 7: %v", w.cascadedIDs)
	}
	if len(w.rows) != 0 {
		t.Errorf("row should be removed: %+v", w.rows)
	}
	if len(em.deleted) != 1 || em.deleted[0].ID != 7 {
		t.Errorf("emitter.Deleted should fire even with cascade: %+v", em.deleted)
	}
}

func TestSeriesDeleteHandler_DeleteFilesAbsent_NoCascade(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesCascadeWriter{fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{{ID: 7}}}}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.cascadedIDs) != 0 {
		t.Errorf("cascade must NOT fire when ?deleteFiles is absent: %v", w.cascadedIDs)
	}
}

func TestSeriesDeleteHandler_DeleteFilesFalse_NoCascade(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesCascadeWriter{fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{{ID: 7}}}}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7?deleteFiles=false", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.cascadedIDs) != 0 {
		t.Errorf("cascade must NOT fire when ?deleteFiles=false: %v", w.cascadedIDs)
	}
}

func TestSeriesDeleteHandler_NonCascaderImpl_FallsThroughToDelete(t *testing.T) {
	t.Parallel()
	// fakeSeriesWriter (non-cascade) + ?deleteFiles=true should
	// quietly fall through to svc.Delete (no error; no cascade
	// fires because the impl doesn't satisfy SeriesCascader).
	w := &fakeSeriesWriter{rows: []series.Series{{ID: 7}}}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7?deleteFiles=true", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.rows) != 0 {
		t.Errorf("row should be deleted via fallback Delete: %+v", w.rows)
	}
}

func TestSeriesDeleteHandler_CascadeError_500(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesCascadeWriter{
		fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{{ID: 7}}},
		cascadeErr:       errors.New("episodefiles cleanup failed"),
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7?deleteFiles=true", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
	// Series row preserved on cascade failure (partial-failure
	// semantics from the adapter doc-comment).
	if len(w.rows) != 1 {
		t.Errorf("series row should be preserved on cascade failure: %+v", w.rows)
	}
}

func TestSeriesEditorDeleteHandler_BodyDeleteFiles_FiresCascade(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1}, {ID: 2}, {ID: 3}}
	w := &fakeSeriesCascadeWriter{fakeSeriesWriter: &fakeSeriesWriter{rows: rows}}
	em := &fakeSeriesEmitter{}
	body := SeriesEditorResource{SeriesIDs: []int{1, 3}, DeleteFiles: true}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorDeleteHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.cascadedIDs) != 2 || w.cascadedIDs[0] != 1 || w.cascadedIDs[1] != 3 {
		t.Errorf("DeleteCascade should fire for ids 1+3: %v", w.cascadedIDs)
	}
	if len(em.deleted) != 2 {
		t.Errorf("emitter count: %d", len(em.deleted))
	}
}

func TestSeriesEditorDeleteHandler_BodyDeleteFilesAbsent_NoCascade(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1}, {ID: 2}}
	w := &fakeSeriesCascadeWriter{fakeSeriesWriter: &fakeSeriesWriter{rows: rows}}
	body := SeriesEditorResource{SeriesIDs: []int{1, 2}} // DeleteFiles defaults to false
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.cascadedIDs) != 0 {
		t.Errorf("cascade must NOT fire when body.DeleteFiles=false: %v", w.cascadedIDs)
	}
}

// fakeSeriesExclusionWriter combines the cascade-aware writer with
// an AddImportListExclusion tracker so the type-assert in
// SeriesDeleteHandler picks up both interfaces.
type fakeSeriesExclusionWriter struct {
	*fakeSeriesCascadeWriter
	excludeErr  error
	excludedIDs []int
}

func (f *fakeSeriesExclusionWriter) AddImportListExclusion(_ context.Context, id int) error {
	if f.excludeErr != nil {
		return f.excludeErr
	}
	f.excludedIDs = append(f.excludedIDs, id)
	return nil
}

func TestSeriesDeleteHandler_AddImportListExclusion_FiresAdder(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesExclusionWriter{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{{ID: 7, Title: "victim", TvdbID: 12345}}},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7?addImportListExclusion=true", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.excludedIDs) != 1 || w.excludedIDs[0] != 7 {
		t.Errorf("AddImportListExclusion should be invoked with id 7: %v", w.excludedIDs)
	}
	if len(w.rows) != 0 {
		t.Errorf("row should still be deleted after exclusion-add: %+v", w.rows)
	}
}

func TestSeriesDeleteHandler_AddImportListExclusionAbsent_NoAdderCall(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesExclusionWriter{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{{ID: 7}}},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.excludedIDs) != 0 {
		t.Errorf("AddImportListExclusion must NOT fire when flag absent: %v", w.excludedIDs)
	}
}

func TestSeriesDeleteHandler_AddImportListExclusionError_500_RowPreserved(t *testing.T) {
	t.Parallel()
	w := &fakeSeriesExclusionWriter{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{{ID: 7, TvdbID: 12345}}},
		},
		excludeErr: errors.New("constraint violation"),
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7?addImportListExclusion=true", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
	// Series row preserved when exclusion-add fails — the C# behaviour.
	if len(w.rows) != 1 {
		t.Errorf("series row should be preserved on exclusion-add failure: %+v", w.rows)
	}
}

func TestSeriesDeleteHandler_NonAdderImpl_FlagSilentlyNoOp(t *testing.T) {
	t.Parallel()
	// Plain fakeSeriesWriter doesn't implement SeriesExclusionAdder
	// — the ?addImportListExclusion=true flag silently no-ops.
	w := &fakeSeriesWriter{rows: []series.Series{{ID: 7}}}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/series/7?addImportListExclusion=true", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	SeriesDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
}

func TestSeriesEditorDeleteHandler_BodyAddImportListExclusion(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1, TvdbID: 100}, {ID: 2, TvdbID: 200}}
	w := &fakeSeriesExclusionWriter{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: rows},
		},
	}
	body := SeriesEditorResource{SeriesIDs: []int{1, 2}, AddImportListExclusion: true}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.excludedIDs) != 2 || w.excludedIDs[0] != 1 || w.excludedIDs[1] != 2 {
		t.Errorf("AddImportListExclusion should fire for ids 1+2: %v", w.excludedIDs)
	}
}

func TestSeriesEditorDeleteHandler_BodyAddImportListExclusionAbsent(t *testing.T) {
	t.Parallel()
	rows := []series.Series{{ID: 1}}
	w := &fakeSeriesExclusionWriter{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: rows},
		},
	}
	body := SeriesEditorResource{SeriesIDs: []int{1}} // AddImportListExclusion default false
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	SeriesEditorDeleteHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v3/series/editor", bytes.NewReader(buf)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.excludedIDs) != 0 {
		t.Errorf("AddImportListExclusion must NOT fire when flag false: %v", w.excludedIDs)
	}
}

// fakeSeriesMover combines the cascade-aware writer with a
// MoveFolder recorder so the SeriesUpdateHandler type-assert can
// dispatch to it for ?moveFiles=true.
type fakeSeriesMover struct {
	*fakeSeriesCascadeWriter
	moveErr error
	moves   []struct{ src, dst string }
}

func (f *fakeSeriesMover) MoveFolder(_ context.Context, src, dst string) error {
	if f.moveErr != nil {
		return f.moveErr
	}
	f.moves = append(f.moves, struct{ src, dst string }{src, dst})
	return nil
}

func TestSeriesUpdateHandler_MoveFilesTrue_PathChanged_FiresMover(t *testing.T) {
	t.Parallel()
	existing := series.Series{ID: 1, Title: "Show", Path: "/old/path", QualityProfileID: 1}
	w := &fakeSeriesMover{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{existing}},
		},
	}
	body := validSeriesBody()
	body.ID = 1
	body.Path = "/new/path"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/series/1?moveFiles=true", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	SeriesUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.moves) != 1 || w.moves[0].src != "/old/path" || w.moves[0].dst != "/new/path" {
		t.Errorf("MoveFolder should be invoked with old→new: %+v", w.moves)
	}
	if w.rows[0].Path != "/new/path" {
		t.Errorf("DB row Path should update: %q", w.rows[0].Path)
	}
}

func TestSeriesUpdateHandler_MoveFilesTrue_SamePath_NoMove(t *testing.T) {
	t.Parallel()
	existing := series.Series{ID: 1, Path: "/same/path", QualityProfileID: 1}
	w := &fakeSeriesMover{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{existing}},
		},
	}
	body := validSeriesBody()
	body.ID = 1
	body.Path = "/same/path"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/series/1?moveFiles=true", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	SeriesUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.moves) != 0 {
		t.Errorf("MoveFolder must NOT fire when Path unchanged: %+v", w.moves)
	}
}

func TestSeriesUpdateHandler_MoveFilesAbsent_PathChanged_NoMove(t *testing.T) {
	t.Parallel()
	existing := series.Series{ID: 1, Path: "/old", QualityProfileID: 1}
	w := &fakeSeriesMover{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{existing}},
		},
	}
	body := validSeriesBody()
	body.ID = 1
	body.Path = "/new"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/series/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	SeriesUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.moves) != 0 {
		t.Errorf("MoveFolder must NOT fire without ?moveFiles=true: %+v", w.moves)
	}
	// DB row still updates to the new path (5.D1a behaviour).
	if w.rows[0].Path != "/new" {
		t.Errorf("Path should still update via DB: %q", w.rows[0].Path)
	}
}

func TestSeriesUpdateHandler_MoveFolderError_500_RowPreserved(t *testing.T) {
	t.Parallel()
	existing := series.Series{ID: 1, Path: "/old", QualityProfileID: 1}
	w := &fakeSeriesMover{
		fakeSeriesCascadeWriter: &fakeSeriesCascadeWriter{
			fakeSeriesWriter: &fakeSeriesWriter{rows: []series.Series{existing}},
		},
		moveErr: errors.New("target exists"),
	}
	body := validSeriesBody()
	body.ID = 1
	body.Path = "/new"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/series/1?moveFiles=true", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	SeriesUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
	// DB row preserved with OLD path on move failure (the C# behaviour).
	if w.rows[0].Path != "/old" {
		t.Errorf("DB row should be preserved on move failure: %q", w.rows[0].Path)
	}
}

func TestSeriesUpdateHandler_NonMoverImpl_FlagSilentlyNoOp(t *testing.T) {
	t.Parallel()
	// Plain fakeSeriesWriter doesn't satisfy SeriesMover — the
	// ?moveFiles=true flag silently no-ops + DB still updates.
	existing := series.Series{ID: 1, Path: "/old", QualityProfileID: 1}
	w := &fakeSeriesWriter{rows: []series.Series{existing}}
	body := validSeriesBody()
	body.ID = 1
	body.Path = "/new"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/series/1?moveFiles=true", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	SeriesUpdateHandler(w, &fakeSeriesEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Path != "/new" {
		t.Errorf("Path should still update via DB: %q", w.rows[0].Path)
	}
}
