package seriesfolder_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/seriesfolder"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/series"
)

type fakeSeries struct {
	s   *series.Series
	err error
}

func (f *fakeSeries) GetSeries(_ context.Context, _ int) (*series.Series, error) {
	return f.s, f.err
}

type fakeNaming struct {
	cfg *namingconfig.Config
}

func (f *fakeNaming) GetConfig(_ context.Context) (*namingconfig.Config, error) {
	return f.cfg, nil
}

func TestGetHandler_Success_EmitsFolderField(t *testing.T) {
	t.Parallel()
	svc := &fakeSeries{s: &series.Series{ID: 1, Title: "Breaking Bad"}}
	naming := &fakeNaming{cfg: &namingconfig.Config{SeriesFolderFormat: "{Series Title}"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/series/1/folder", nil)
	req.SetPathValue("id", "1")
	seriesfolder.GetHandler(svc, naming).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got struct {
		Folder string `json:"folder"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Folder == "" {
		t.Errorf("folder must be non-empty for valid series + format")
	}
}

func TestGetHandler_SeriesNotFound_404(t *testing.T) {
	t.Parallel()
	svc := &fakeSeries{err: series.ErrNotFound}
	naming := &fakeNaming{cfg: &namingconfig.Config{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/series/999/folder", nil)
	req.SetPathValue("id", "999")
	seriesfolder.GetHandler(svc, naming).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestGetHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	svc := &fakeSeries{}
	naming := &fakeNaming{cfg: &namingconfig.Config{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/series/abc/folder", nil)
	req.SetPathValue("id", "abc")
	seriesfolder.GetHandler(svc, naming).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestGetHandler_NamingConfigError_500(t *testing.T) {
	t.Parallel()
	svc := &fakeSeries{s: &series.Series{ID: 1, Title: "x"}}
	naming := &errNaming{err: errors.New("boom")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/series/1/folder", nil)
	req.SetPathValue("id", "1")
	seriesfolder.GetHandler(svc, naming).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

type errNaming struct {
	err error
}

func (e *errNaming) GetConfig(_ context.Context) (*namingconfig.Config, error) {
	return nil, e.err
}

func TestHandlerAlias_NonNil(t *testing.T) {
	t.Parallel()
	if seriesfolder.GetHandler == nil {
		t.Errorf("GetHandler nil")
	}
}
