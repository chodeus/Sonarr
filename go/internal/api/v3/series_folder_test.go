package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/series"
)

type fakeSeriesFolderGetter struct {
	row *series.Series
	err error
}

func (f *fakeSeriesFolderGetter) GetSeries(_ context.Context, _ int) (*series.Series, error) {
	return f.row, f.err
}

type fakeNamingConfigReader struct {
	cfg *namingconfig.Config
	err error
}

func (f *fakeNamingConfigReader) GetConfig(_ context.Context) (*namingconfig.Config, error) {
	return f.cfg, f.err
}

func TestSeriesFolderHandler_Success(t *testing.T) {
	t.Parallel()
	svc := &fakeSeriesFolderGetter{row: &series.Series{ID: 42, Title: "My Show"}}
	naming := &fakeNamingConfigReader{cfg: &namingconfig.Config{SeriesFolderFormat: "{Series Title}"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series/42/folder", nil)
	req.SetPathValue("id", "42")
	SeriesFolderHandler(svc, naming).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Folder string `json:"folder"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Folder != "My Show" {
		t.Errorf("folder: %q want %q", got.Folder, "My Show")
	}
}

func TestSeriesFolderHandler_SeriesNotFound_404(t *testing.T) {
	t.Parallel()
	svc := &fakeSeriesFolderGetter{err: series.ErrNotFound}
	naming := &fakeNamingConfigReader{cfg: &namingconfig.Config{SeriesFolderFormat: "{Series Title}"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series/999/folder", nil)
	req.SetPathValue("id", "999")
	SeriesFolderHandler(svc, naming).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesFolderHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series/abc/folder", nil)
	req.SetPathValue("id", "abc")
	SeriesFolderHandler(&fakeSeriesFolderGetter{}, &fakeNamingConfigReader{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesFolderHandler_NamingConfigError_500(t *testing.T) {
	t.Parallel()
	svc := &fakeSeriesFolderGetter{row: &series.Series{ID: 1, Title: "X"}}
	naming := &fakeNamingConfigReader{err: errors.New("boom")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series/1/folder", nil)
	req.SetPathValue("id", "1")
	SeriesFolderHandler(svc, naming).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSeriesFolderHandler_TitleYearFormat(t *testing.T) {
	t.Parallel()
	svc := &fakeSeriesFolderGetter{row: &series.Series{ID: 1, Title: "The Show", Year: 2020}}
	naming := &fakeNamingConfigReader{cfg: &namingconfig.Config{SeriesFolderFormat: "{Series Title} ({Series Year})"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/series/1/folder", nil)
	req.SetPathValue("id", "1")
	SeriesFolderHandler(svc, naming).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Folder string `json:"folder"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Folder != "The Show (2020)" {
		t.Errorf("folder: %q", got.Folder)
	}
}
