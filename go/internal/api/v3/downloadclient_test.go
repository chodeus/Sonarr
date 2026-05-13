package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
)

type fakeDownloadClientLister struct {
	rows []downloadclients.DownloadClient
	err  error
}

func (f *fakeDownloadClientLister) All(context.Context) ([]downloadclients.DownloadClient, error) {
	return f.rows, f.err
}

func TestDownloadClientHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientHandler(&fakeDownloadClientLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestDownloadClientHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientHandler(&fakeDownloadClientLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
