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

// ---------- 5.B9: CRUD tests ----------

type fakeRemotePathMappingWriter struct {
	row       *remotepathmappings.Mapping
	getErr    error
	addErr    error
	updateErr error
	removeErr error

	addCalledWith    *remotepathmappings.Mapping
	updateCalledWith *remotepathmappings.Mapping
	removeCalledID   int
}

func (f *fakeRemotePathMappingWriter) Get(_ context.Context, id int) (*remotepathmappings.Mapping, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.row == nil {
		return nil, remotepathmappings.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeRemotePathMappingWriter) Add(_ context.Context, m *remotepathmappings.Mapping) (*remotepathmappings.Mapping, error) {
	f.addCalledWith = m
	if f.addErr != nil {
		return nil, f.addErr
	}
	out := *m
	out.ID = 4
	return &out, nil
}

func (f *fakeRemotePathMappingWriter) Update(_ context.Context, m *remotepathmappings.Mapping) (*remotepathmappings.Mapping, error) {
	f.updateCalledWith = m
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	out := *m
	return &out, nil
}

func (f *fakeRemotePathMappingWriter) Remove(_ context.Context, id int) error {
	f.removeCalledID = id
	return f.removeErr
}

func TestRemotePathMappingByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeRemotePathMappingWriter{row: &remotepathmappings.Mapping{ID: 4, Host: "nas", RemotePath: "/downloads/", LocalPath: "/tv"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/remotepathmapping/4", nil)
	req.SetPathValue("id", "4")
	RemotePathMappingByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"host":"nas"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRemotePathMappingByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/remotepathmapping/99", nil)
	req.SetPathValue("id", "99")
	RemotePathMappingByIDHandler(&fakeRemotePathMappingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRemotePathMappingCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeRemotePathMappingWriter{}
	rr := httptest.NewRecorder()
	body := `{"host":"nas","remotePath":"/downloads/","localPath":"/data/tv"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/remotepathmapping", strings.NewReader(body))
	RemotePathMappingCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.addCalledWith == nil || w.addCalledWith.Host != "nas" {
		t.Errorf("svc.Add called with %+v", w.addCalledWith)
	}
	if !strings.Contains(rr.Body.String(), `"id":4`) {
		t.Errorf("response should carry id 4: %s", rr.Body.String())
	}
}

func TestRemotePathMappingCreateHandler_EmptyHost400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"host":"","remotePath":"/r","localPath":"/l"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/remotepathmapping", strings.NewReader(body))
	RemotePathMappingCreateHandler(&fakeRemotePathMappingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Host"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRemotePathMappingCreateHandler_RemotePathStartsWithSpace400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"host":"nas","remotePath":" /leading-space","localPath":"/tv"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/remotepathmapping", strings.NewReader(body))
	RemotePathMappingCreateHandler(&fakeRemotePathMappingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "must not start with a space") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRemotePathMappingCreateHandler_RemotePathEndsWithSpace400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"host":"nas","remotePath":"/trailing-space ","localPath":"/tv"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/remotepathmapping", strings.NewReader(body))
	RemotePathMappingCreateHandler(&fakeRemotePathMappingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "must not end with a space") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRemotePathMappingCreateHandler_LocalPathRelative400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"host":"nas","remotePath":"/r","localPath":"relative/tv"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/remotepathmapping", strings.NewReader(body))
	RemotePathMappingCreateHandler(&fakeRemotePathMappingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Local Path must be absolute") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRemotePathMappingCreateHandler_LocalPathSlashOnly400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"host":"nas","remotePath":"/r","localPath":"/"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/remotepathmapping", strings.NewReader(body))
	RemotePathMappingCreateHandler(&fakeRemotePathMappingWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Cannot be set to '/'") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRemotePathMappingUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeRemotePathMappingWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":4,"host":"nas","remotePath":"/d/","localPath":"/tv/renamed"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/remotepathmapping/4", strings.NewReader(body))
	req.SetPathValue("id", "4")
	RemotePathMappingUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updateCalledWith.ID != 4 || w.updateCalledWith.LocalPath != "/tv/renamed" {
		t.Errorf("svc.Update called with %+v", w.updateCalledWith)
	}
}

func TestRemotePathMappingUpdateHandler_NotFound404(t *testing.T) {
	t.Parallel()
	w := &fakeRemotePathMappingWriter{updateErr: remotepathmappings.ErrNotFound}
	rr := httptest.NewRecorder()
	body := `{"host":"nas","remotePath":"/r","localPath":"/tv"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/remotepathmapping/99", strings.NewReader(body))
	req.SetPathValue("id", "99")
	RemotePathMappingUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRemotePathMappingDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeRemotePathMappingWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/remotepathmapping/4", nil)
	req.SetPathValue("id", "4")
	RemotePathMappingDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if w.removeCalledID != 4 {
		t.Errorf("Remove called with %d", w.removeCalledID)
	}
}

// TestRemotePathMappingDeleteHandler_Idempotent — C.1.i-follow-4 (J232). 200 on missing id.
func TestRemotePathMappingDeleteHandler_Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeRemotePathMappingWriter{removeErr: remotepathmappings.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/remotepathmapping/99", nil)
	req.SetPathValue("id", "99")
	RemotePathMappingDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (idempotent)", rr.Code)
	}
}
