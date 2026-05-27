package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
)

type fakeEpisodeFileWriter struct {
	get         map[int]*episodefiles.File
	getMany     []episodefiles.File
	getErr      error
	deletedIDs  []int
	deleteErr   error
	updated     *episodefiles.File
	updatedMany []*episodefiles.File
	updateErr   error
}

func (f *fakeEpisodeFileWriter) Update(_ context.Context, file *episodefiles.File) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	cp := *file
	f.updated = &cp
	return nil
}

func (f *fakeEpisodeFileWriter) UpdateMany(_ context.Context, files []*episodefiles.File) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for _, fi := range files {
		cp := *fi
		f.updatedMany = append(f.updatedMany, &cp)
	}
	return nil
}

func (f *fakeEpisodeFileWriter) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if v, ok := f.get[id]; ok {
		return v, nil
	}
	return nil, episodefiles.ErrNotFound
}

func (f *fakeEpisodeFileWriter) GetFiles(_ context.Context, _ []int) ([]episodefiles.File, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getMany, nil
}

func (f *fakeEpisodeFileWriter) Delete(_ context.Context, file *episodefiles.File, _ episodefiles.DeleteMediaFileReason) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedIDs = append(f.deletedIDs, file.ID)
	return nil
}

func TestEpisodeFileDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{
		7: {ID: 7, SeriesID: 1, RelativePath: "Show.S01E01.mkv"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/7", nil)
	req.SetPathValue("id", "7")
	EpisodeFileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.deletedIDs) != 1 || w.deletedIDs[0] != 7 {
		t.Errorf("deleted: %+v", w.deletedIDs)
	}
}

func TestEpisodeFileDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/999", nil)
	req.SetPathValue("id", "999")
	EpisodeFileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/abc", nil)
	req.SetPathValue("id", "abc")
	EpisodeFileDeleteHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileDeleteHandler_ServiceError_500(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{
		get:       map[int]*episodefiles.File{1: {ID: 1}},
		deleteErr: errors.New("boom"),
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/1", nil)
	req.SetPathValue("id", "1")
	EpisodeFileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: []episodefiles.File{
		{ID: 1, SeriesID: 1, RelativePath: "a.mkv"},
		{ID: 2, SeriesID: 1, RelativePath: "b.mkv"},
		{ID: 3, SeriesID: 1, RelativePath: "c.mkv"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/bulk", strings.NewReader(`{"episodeFileIds":[1,2,3]}`))
	EpisodeFileBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(w.deletedIDs) != 3 {
		t.Errorf("deleted count: %d; ids=%+v", len(w.deletedIDs), w.deletedIDs)
	}
}

func TestEpisodeFileBulkDeleteHandler_EmptyList(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/bulk", strings.NewReader(`{"episodeFileIds":[]}`))
	EpisodeFileBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(w.deletedIDs) != 0 {
		t.Errorf("should not have deleted anything: %+v", w.deletedIDs)
	}
}

func TestEpisodeFileBulkDeleteHandler_NoMatchingFiles_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: nil} // GetFiles returns empty slice
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/bulk", strings.NewReader(`{"episodeFileIds":[999]}`))
	EpisodeFileBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileBulkDeleteHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/episodefile/bulk", strings.NewReader(`not json`))
	EpisodeFileBulkDeleteHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.I2: GET /episodefile tests ----------

type fakeEpisodeFileReader struct {
	bySeries    map[int][]episodefiles.File
	byIDs       []episodefiles.File
	byID        map[int]*episodefiles.File
	bySeriesErr error
}

func (f *fakeEpisodeFileReader) GetFile(_ context.Context, id int) (*episodefiles.File, error) {
	if v, ok := f.byID[id]; ok {
		return v, nil
	}
	return nil, episodefiles.ErrNotFound
}
func (f *fakeEpisodeFileReader) GetFiles(_ context.Context, _ []int) ([]episodefiles.File, error) {
	return f.byIDs, nil
}
func (f *fakeEpisodeFileReader) GetFilesBySeries(_ context.Context, seriesID int) ([]episodefiles.File, error) {
	if f.bySeriesErr != nil {
		return nil, f.bySeriesErr
	}
	return f.bySeries[seriesID], nil
}

// stubSeriesExistsChecker implements SeriesExistsChecker with a
// fixed set of "known" series ids. Used by the EpisodeFileListHandler
// tests below — the seriesId path requires this checker (5.J180) so
// the handler can short-circuit to 404 when the series is missing.
type stubSeriesExistsChecker struct {
	known map[int]bool
	err   error
}

func (s *stubSeriesExistsChecker) SeriesExists(_ context.Context, id int) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.known[id], nil
}

func TestEpisodeFileListHandler_MissingParams_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(&fakeEpisodeFileReader{}, &stubSeriesExistsChecker{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileListHandler_SeriesIDBranch(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{bySeries: map[int][]episodefiles.File{
		7: {
			{ID: 1, SeriesID: 7, RelativePath: "a.mkv"},
			{ID: 2, SeriesID: 7, RelativePath: "b.mkv"},
		},
	}}
	checker := &stubSeriesExistsChecker{known: map[int]bool{7: true}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":1`) || !strings.Contains(rr.Body.String(), `"id":2`) {
		t.Errorf("body should contain both ids: %s", rr.Body.String())
	}
}

// J298 wire-shape pin: a populated row must emit `customFormats:[]`
// (non-nil empty) + `customFormatScore:0` — matches the C# v3
// EpisodeFileResource mapper at `EpisodeFileResource.cs:46-66`
// which initialises CustomFormats via
// `formatCalculator?.ParseCustomFormat(model, model.Series).ToResource(false)`
// and CustomFormatScore via the QualityProfile lookup. Both fields
// land on the wire today as empty/zero pending the populated-row
// calculator wire-through; their presence on the struct is what
// the future mapper widens into. Same widening simultaneously
// affects Wanted, Calendar, and Episode families because their
// EpisodeFile subresource shares this type.
func TestEpisodeFileListHandler_EmitsCustomFormatsAndScore(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{bySeries: map[int][]episodefiles.File{
		7: {
			{ID: 1, SeriesID: 7, RelativePath: "a.mkv"},
		},
	}}
	checker := &stubSeriesExistsChecker{known: map[int]bool{7: true}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"customFormats":[]`) {
		t.Errorf("body must contain `\"customFormats\":[]`; got %s", body)
	}
	if !strings.Contains(body, `"customFormatScore":0`) {
		t.Errorf("body must contain `\"customFormatScore\":0`; got %s", body)
	}
	var got []EpisodeFileResource
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

// J180: when the supplied seriesId doesn't exist, the handler must
// short-circuit to 404 with a descriptive body matching C#
// EpisodeFileController.cs:72's ModelNotFoundException → 404
// pipeline. Mirrors the v3.episodefile.list contract golden's 404
// status — closing the 404-vs-200 drift.
func TestEpisodeFileListHandler_MissingSeriesReturns404(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{}
	checker := &stubSeriesExistsChecker{known: map[int]bool{} /* nothing known */}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=1", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Series with ID 1 does not exist") {
		t.Errorf("body should explain the missing series: %s", rr.Body.String())
	}
}

// J180: existing series → 200 + []. The series-exists-but-no-files
// case stays a 200, matching C# `:75-78` `if (files == null) return
// new List<EpisodeFileResource>()` fallback. This guards against an
// over-eager 404 path that would lump this case with the missing-
// series 404.
func TestEpisodeFileListHandler_ExistingSeriesEmptyFiles_200(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{bySeries: map[int][]episodefiles.File{}}
	checker := &stubSeriesExistsChecker{known: map[int]bool{7: true}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc, checker).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Errorf("body: got %q, want []", rr.Body.String())
	}
}

func TestEpisodeFileListHandler_EpisodeFileIDsBranch(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{byIDs: []episodefiles.File{
		{ID: 5, SeriesID: 7, RelativePath: "x.mkv"},
	}}
	rr := httptest.NewRecorder()
	// episodeFileIds path doesn't precondition on series existence
	// (matches C# :85-90 per-row join after-the-fact) — checker can
	// be empty.
	EpisodeFileListHandler(svc, &stubSeriesExistsChecker{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?episodeFileIds=5", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":5`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestEpisodeFileListHandler_InvalidEpisodeFileID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(&fakeEpisodeFileReader{}, &stubSeriesExistsChecker{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?episodeFileIds=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileByIDHandler_Found(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{byID: map[int]*episodefiles.File{
		3: {ID: 3, SeriesID: 1, RelativePath: "y.mkv"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/episodefile/3", nil)
	req.SetPathValue("id", "3")
	EpisodeFileByIDHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":3`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestEpisodeFileByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/episodefile/999", nil)
	req.SetPathValue("id", "999")
	EpisodeFileByIDHandler(&fakeEpisodeFileReader{byID: map[int]*episodefiles.File{}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.D5b: PUT /episodefile/{id} tests ----------

func TestEpisodeFileUpdateHandler_UpdatesSceneNameAndReleaseGroup(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{
		7: {ID: 7, SeriesID: 1, SceneName: "old.scene", ReleaseGroup: "OLD"},
	}}
	rr := httptest.NewRecorder()
	body := `{"id":7,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z","sceneName":"New.Scene-GROUP","releaseGroup":"NEW"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/7", strings.NewReader(body))
	req.SetPathValue("id", "7")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updated == nil || w.updated.SceneName != "New.Scene-GROUP" || w.updated.ReleaseGroup != "NEW" {
		t.Errorf("updated: %+v", w.updated)
	}
}

func TestEpisodeFileUpdateHandler_EmptyStringFieldsPreserveExisting(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{
		7: {ID: 7, SceneName: "keep.scene", ReleaseGroup: "KEEP"},
	}}
	rr := httptest.NewRecorder()
	body := `{"id":7,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/7", strings.NewReader(body))
	req.SetPathValue("id", "7")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.updated == nil || w.updated.SceneName != "keep.scene" || w.updated.ReleaseGroup != "KEEP" {
		t.Errorf("existing values should be preserved: %+v", w.updated)
	}
}

func TestEpisodeFileUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{get: map[int]*episodefiles.File{}}
	rr := httptest.NewRecorder()
	body := `{"id":999,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/999", strings.NewReader(body))
	req.SetPathValue("id", "999")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileUpdateHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/abc", strings.NewReader(`{}`))
	req.SetPathValue("id", "abc")
	EpisodeFileUpdateHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileUpdateHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/1", strings.NewReader(`not json`))
	req.SetPathValue("id", "1")
	EpisodeFileUpdateHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileUpdateHandler_UpdateError_500(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{
		get:       map[int]*episodefiles.File{1: {ID: 1}},
		updateErr: errors.New("boom"),
	}
	rr := httptest.NewRecorder()
	body := `{"id":1,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z","sceneName":"x"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	EpisodeFileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- 5.D5c: PUT /episodefile/editor + /bulk tests ----------

func TestEpisodeFileEditorHandler_AppliesSameOverridesToAll(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: []episodefiles.File{
		{ID: 1, SceneName: "a", ReleaseGroup: "OLD"},
		{ID: 2, SceneName: "b", ReleaseGroup: "OLD"},
		{ID: 3, SceneName: "c", ReleaseGroup: "OLD"},
	}}
	rr := httptest.NewRecorder()
	body := `{"episodeFileIds":[1,2,3],"releaseGroup":"NEW"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/editor", strings.NewReader(body))
	EpisodeFileEditorHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updatedMany) != 3 {
		t.Fatalf("expected 3 updated; got %d", len(w.updatedMany))
	}
	for _, f := range w.updatedMany {
		if f.ReleaseGroup != "NEW" {
			t.Errorf("ReleaseGroup not propagated: %+v", f)
		}
		// SceneName not set in body -> preserved.
		if f.SceneName == "" {
			t.Errorf("SceneName should be preserved: %+v", f)
		}
	}
}

func TestEpisodeFileEditorHandler_NoMatchingFiles_404(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/editor", strings.NewReader(`{"episodeFileIds":[999]}`))
	EpisodeFileEditorHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileEditorHandler_MissingIDs_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/editor", strings.NewReader(`{"episodeFileIds":[]}`))
	EpisodeFileEditorHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileBulkUpdateHandler_PerIDOverrides(t *testing.T) {
	t.Parallel()
	w := &fakeEpisodeFileWriter{getMany: []episodefiles.File{
		{ID: 1, SceneName: "old.1", ReleaseGroup: "OLD"},
		{ID: 2, SceneName: "old.2", ReleaseGroup: "OLD"},
	}}
	rr := httptest.NewRecorder()
	body := `[{"id":1,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z","sceneName":"new.1"},{"id":2,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z","releaseGroup":"NEW2"}]`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/bulk", strings.NewReader(body))
	EpisodeFileBulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updatedMany) != 2 {
		t.Fatalf("expected 2 updated; got %d", len(w.updatedMany))
	}
	// Per-id overrides: id=1 gets sceneName='new.1' + preserves OLD release group;
	// id=2 gets releaseGroup='NEW2' + preserves old.2 scene name.
	for _, f := range w.updatedMany {
		switch f.ID {
		case 1:
			if f.SceneName != "new.1" || f.ReleaseGroup != "OLD" {
				t.Errorf("id=1: %+v", f)
			}
		case 2:
			if f.SceneName != "old.2" || f.ReleaseGroup != "NEW2" {
				t.Errorf("id=2: %+v", f)
			}
		}
	}
}

func TestEpisodeFileBulkUpdateHandler_EmptyArray_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/bulk", strings.NewReader(`[]`))
	EpisodeFileBulkUpdateHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestEpisodeFileBulkUpdateHandler_MissingID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `[{"id":0,"seriesId":1,"seasonNumber":0,"relativePath":"","path":"","size":0,"dateAdded":"0001-01-01T00:00:00Z"}]`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/episodefile/bulk", strings.NewReader(body))
	EpisodeFileBulkUpdateHandler(&fakeEpisodeFileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
