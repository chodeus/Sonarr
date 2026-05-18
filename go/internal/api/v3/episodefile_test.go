package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
)

type fakeEpisodeFileWriter struct {
	get        map[int]*episodefiles.File
	getMany    []episodefiles.File
	getErr     error
	deletedIDs []int
	deleteErr  error
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

func TestEpisodeFileListHandler_MissingParams_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(&fakeEpisodeFileReader{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile", nil))
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
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?seriesId=7", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":1`) || !strings.Contains(rr.Body.String(), `"id":2`) {
		t.Errorf("body should contain both ids: %s", rr.Body.String())
	}
}

func TestEpisodeFileListHandler_EpisodeFileIDsBranch(t *testing.T) {
	t.Parallel()
	svc := &fakeEpisodeFileReader{byIDs: []episodefiles.File{
		{ID: 5, SeriesID: 7, RelativePath: "x.mkv"},
	}}
	rr := httptest.NewRecorder()
	EpisodeFileListHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?episodeFileIds=5", nil))
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
	EpisodeFileListHandler(&fakeEpisodeFileReader{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/episodefile?episodeFileIds=abc", nil))
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
