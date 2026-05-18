package v3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/notifications"
)

// ---------- IndexerByIDHandler ----------

type fakeIndexerByIDGetter struct {
	row *indexers.Indexer
	err error
}

func (f *fakeIndexerByIDGetter) Get(_ context.Context, _ int) (*indexers.Indexer, error) {
	return f.row, f.err
}

func TestIndexerByIDHandler_Found(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/indexer/5", nil)
	req.SetPathValue("id", "5")
	IndexerByIDHandler(&fakeIndexerByIDGetter{row: &indexers.Indexer{ID: 5, Name: "x"}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/indexer/999", nil)
	req.SetPathValue("id", "999")
	IndexerByIDHandler(&fakeIndexerByIDGetter{err: indexers.ErrNotFound}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestIndexerByIDHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/indexer/abc", nil)
	req.SetPathValue("id", "abc")
	IndexerByIDHandler(&fakeIndexerByIDGetter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- ImportListByIDHandler ----------

type fakeImportListByIDGetter struct {
	row *importlists.ImportList
	err error
}

func (f *fakeImportListByIDGetter) Get(_ context.Context, _ int) (*importlists.ImportList, error) {
	return f.row, f.err
}

func TestImportListByIDHandler_Found(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/importlist/5", nil)
	req.SetPathValue("id", "5")
	ImportListByIDHandler(&fakeImportListByIDGetter{row: &importlists.ImportList{ID: 5, Name: "x"}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestImportListByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/importlist/999", nil)
	req.SetPathValue("id", "999")
	ImportListByIDHandler(&fakeImportListByIDGetter{err: importlists.ErrNotFound}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- DownloadClientByIDHandler ----------

type fakeDownloadClientByIDGetter struct {
	row *downloadclients.DownloadClient
	err error
}

func (f *fakeDownloadClientByIDGetter) Get(_ context.Context, _ int) (*downloadclients.DownloadClient, error) {
	return f.row, f.err
}

func TestDownloadClientByIDHandler_Found(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient/5", nil)
	req.SetPathValue("id", "5")
	DownloadClientByIDHandler(&fakeDownloadClientByIDGetter{row: &downloadclients.DownloadClient{ID: 5, Name: "x"}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient/999", nil)
	req.SetPathValue("id", "999")
	DownloadClientByIDHandler(&fakeDownloadClientByIDGetter{err: downloadclients.ErrNotFound}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- NotificationByIDHandler ----------

type fakeNotificationByIDGetter struct {
	row *notifications.Notification
	err error
}

func (f *fakeNotificationByIDGetter) Get(_ context.Context, _ int) (*notifications.Notification, error) {
	return f.row, f.err
}

func TestNotificationByIDHandler_Found(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/notification/5", nil)
	req.SetPathValue("id", "5")
	NotificationByIDHandler(&fakeNotificationByIDGetter{row: &notifications.Notification{ID: 5, Name: "x"}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestNotificationByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/notification/999", nil)
	req.SetPathValue("id", "999")
	NotificationByIDHandler(&fakeNotificationByIDGetter{err: notifications.ErrNotFound}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ---------- MetadataByIDHandler ----------

type fakeMetadataByIDGetter struct {
	row *metadata.Metadata
	err error
}

func (f *fakeMetadataByIDGetter) Get(_ context.Context, _ int) (*metadata.Metadata, error) {
	return f.row, f.err
}

func TestMetadataByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/metadata/999", nil)
	req.SetPathValue("id", "999")
	MetadataByIDHandler(&fakeMetadataByIDGetter{err: metadata.ErrNotFound}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataByIDHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/metadata/abc", nil)
	req.SetPathValue("id", "abc")
	MetadataByIDHandler(&fakeMetadataByIDGetter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
