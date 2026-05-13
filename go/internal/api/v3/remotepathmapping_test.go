package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/remotepathmappings"
)

type fakeRemotePathLister struct {
	rows []remotepathmappings.Mapping
	err  error
}

func (f *fakeRemotePathLister) All(context.Context) ([]remotepathmappings.Mapping, error) {
	return f.rows, f.err
}

func TestRemotePathMappingHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RemotePathMappingHandler(&fakeRemotePathLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/remotepathmapping", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestRemotePathMappingHandler_PopulatedShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RemotePathMappingHandler(&fakeRemotePathLister{rows: []remotepathmappings.Mapping{
		{ID: 1, Host: "nas", RemotePath: "/downloads", LocalPath: "/mnt/dl"},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/remotepathmapping", nil))
	want := `[{"id":1,"host":"nas","remotePath":"/downloads","localPath":"/mnt/dl"}]`
	if got := rr.Body.String(); !strings.Contains(got, want) {
		t.Errorf("body: got %q want %q", got, want)
	}
}

func TestRemotePathMappingHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RemotePathMappingHandler(&fakeRemotePathLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/remotepathmapping", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
