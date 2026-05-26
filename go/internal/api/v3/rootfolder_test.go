package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/rootfolders"
)

type fakeRootFolderLister struct {
	rows []rootfolders.Folder
	err  error
}

func (f *fakeRootFolderLister) AllWithUnmappedFolders(context.Context) ([]rootfolders.Folder, error) {
	return f.rows, f.err
}

func TestRootFolderHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RootFolderHandler(&fakeRootFolderLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestRootFolderHandler_PopulatedShape(t *testing.T) {
	t.Parallel()
	free := int64(1_000_000_000)
	total := int64(2_000_000_000)
	rows := []rootfolders.Folder{
		{
			ID: 1, Path: "/tv/", Accessible: true,
			FreeSpace: &free, TotalSpace: &total,
			UnmappedFolders: []rootfolders.UnmappedFolder{{Name: "Show", Path: "/tv/Show", RelativePath: "Show"}},
		},
		{
			ID: 2, Path: "/movies/", Accessible: false,
			// FreeSpace + TotalSpace nil → omitted
			UnmappedFolders: nil, // → []
		},
	}
	rr := httptest.NewRecorder()
	RootFolderHandler(&fakeRootFolderLister{rows: rows}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`, `"id":2`,
		`"accessible":true`, `"accessible":false`,
		`"freeSpace":1000000000`, `"totalSpace":2000000000`,
		`"unmappedFolders":[{"name":"Show","path":"/tv/Show","relativePath":"Show"}]`,
		`"unmappedFolders":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
	// Path should be cleaned (trailing slash dropped per CleanPath).
	var out []RootFolderResource
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out) != 2 || out[0].Path == "/tv/" {
		t.Errorf("path not cleaned: %+v", out)
	}
	// IsEmpty must NOT appear in body.
	if strings.Contains(body, "isEmpty") {
		t.Errorf("body unexpectedly contains isEmpty: %s", body)
	}
}

func TestRootFolderHandler_NilFreeSpaceOmitted(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RootFolderHandler(&fakeRootFolderLister{rows: []rootfolders.Folder{
		{ID: 1, Path: "/tv", Accessible: true},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder", nil))
	body := rr.Body.String()
	for _, gone := range []string{"freeSpace", "totalSpace"} {
		if strings.Contains(body, gone) {
			t.Errorf("nil %s should be omitted: %s", gone, body)
		}
	}
}

func TestRootFolderHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RootFolderHandler(&fakeRootFolderLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.B2: CRUD tests ----------

type fakeRootFolderWriter struct {
	folder    *rootfolders.Folder
	getErr    error
	addReturn *rootfolders.Folder
	addErr    error
	removeErr error

	addCalledWith  *rootfolders.RootFolder
	removeCalledID int
}

func (f *fakeRootFolderWriter) Get(_ context.Context, id int, _ bool) (*rootfolders.Folder, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.folder, nil
}

func (f *fakeRootFolderWriter) Add(_ context.Context, rf *rootfolders.RootFolder) (*rootfolders.Folder, error) {
	f.addCalledWith = rf
	if f.addErr != nil {
		return nil, f.addErr
	}
	if f.addReturn != nil {
		return f.addReturn, nil
	}
	return &rootfolders.Folder{ID: 9, Path: rf.Path, Accessible: true, UnmappedFolders: []rootfolders.UnmappedFolder{}}, nil
}

func (f *fakeRootFolderWriter) Remove(_ context.Context, id int) error {
	f.removeCalledID = id
	return f.removeErr
}

type recordingRootFolderEmitter struct {
	createdWith *RootFolderResource
	deletedWith *RootFolderResource
}

func (e *recordingRootFolderEmitter) Created(_ context.Context, r RootFolderResource) error {
	c := r
	e.createdWith = &c
	return nil
}

func (e *recordingRootFolderEmitter) Deleted(_ context.Context, r RootFolderResource) error {
	c := r
	e.deletedWith = &c
	return nil
}

// ----- GET /rootfolder/{id} -----

func TestRootFolderByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeRootFolderWriter{folder: &rootfolders.Folder{ID: 3, Path: "/tv", Accessible: true}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder/3", nil)
	req.SetPathValue("id", "3")
	RootFolderByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":3`) || !strings.Contains(rr.Body.String(), `"path":"/tv"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRootFolderByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	w := &fakeRootFolderWriter{getErr: rootfolders.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder/99", nil)
	req.SetPathValue("id", "99")
	RootFolderByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestRootFolderByIDHandler_BadID(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v3/rootfolder/"+raw, nil)
		req.SetPathValue("id", raw)
		RootFolderByIDHandler(&fakeRootFolderWriter{}).ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("id=%q: got %d want 400", raw, rr.Code)
		}
	}
}

// ----- POST /rootfolder -----

func TestRootFolderCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeRootFolderWriter{}
	em := &recordingRootFolderEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/rootfolder", strings.NewReader(`{"path":"/data/tv"}`))
	RootFolderCreateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201; body=%s", rr.Code, rr.Body.String())
	}
	if w.addCalledWith == nil || w.addCalledWith.Path != "/data/tv" {
		t.Errorf("svc.Add called with %v", w.addCalledWith)
	}
	if em.createdWith == nil || em.createdWith.Path != "/data/tv" {
		t.Errorf("emitter.Created not fired with new resource: %v", em.createdWith)
	}
}

func TestRootFolderCreateHandler_EmptyPath400Validation(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/rootfolder", strings.NewReader(`{"path":""}`))
	RootFolderCreateHandler(&fakeRootFolderWriter{}, &recordingRootFolderEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rr.Code)
	}
	if !strings.HasPrefix(rr.Body.String(), "[") {
		t.Errorf("body should be validation array: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Path"`) {
		t.Errorf("body should fault Path: %s", rr.Body.String())
	}
}

func TestRootFolderCreateHandler_RelativePath400Validation(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/rootfolder", strings.NewReader(`{"path":"relative/tv"}`))
	RootFolderCreateHandler(&fakeRootFolderWriter{}, &recordingRootFolderEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Path must be absolute") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRootFolderCreateHandler_DuplicateIs409(t *testing.T) {
	t.Parallel()
	w := &fakeRootFolderWriter{addErr: rootfolders.ErrDuplicatePath}
	em := &recordingRootFolderEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/rootfolder", strings.NewReader(`{"path":"/data/tv"}`))
	RootFolderCreateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("status: got %d want 409; body=%s", rr.Code, rr.Body.String())
	}
	if em.createdWith != nil {
		t.Errorf("emitter must NOT fire on conflict")
	}
}

func TestRootFolderCreateHandler_FolderNotFound400(t *testing.T) {
	t.Parallel()
	w := &fakeRootFolderWriter{addErr: rootfolders.ErrFolderNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/rootfolder", strings.NewReader(`{"path":"/data/tv"}`))
	RootFolderCreateHandler(w, &recordingRootFolderEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Folder does not exist") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestRootFolderCreateHandler_NotWritable400(t *testing.T) {
	t.Parallel()
	w := &fakeRootFolderWriter{addErr: rootfolders.ErrFolderNotWritable}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/rootfolder", strings.NewReader(`{"path":"/data/tv"}`))
	RootFolderCreateHandler(w, &recordingRootFolderEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not writable") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// ----- DELETE /rootfolder/{id} -----

func TestRootFolderDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeRootFolderWriter{}
	em := &recordingRootFolderEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/rootfolder/3", nil)
	req.SetPathValue("id", "3")
	RootFolderDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	if w.removeCalledID != 3 {
		t.Errorf("svc.Remove called with %d want 3", w.removeCalledID)
	}
	if em.deletedWith == nil || em.deletedWith.ID != 3 {
		t.Errorf("emitter.Deleted not fired: %v", em.deletedWith)
	}
}

// TestRootFolderDeleteHandler_Idempotent — C.1.i-follow-4 (J232). 200 on missing id.
// SignalR Deleted emitter must NOT fire when the missing-id branch swallows ErrNotFound.
func TestRootFolderDeleteHandler_Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeRootFolderWriter{removeErr: rootfolders.ErrNotFound}
	em := &recordingRootFolderEmitter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/rootfolder/99", nil)
	req.SetPathValue("id", "99")
	RootFolderDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (idempotent)", rr.Code)
	}
	if em.deletedWith != nil {
		t.Errorf("emitter must NOT fire on missing-id swallow")
	}
}
