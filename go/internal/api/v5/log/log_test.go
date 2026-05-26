package log

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// fakeFileLister implements both FileLister and UpdateFileLister via
// duck-typing — production logFileLister works the same way.
type fakeFileLister struct {
	folder       string
	updateFolder string
	urlBase      string
}

func (f *fakeFileLister) LogFolder() string       { return f.folder }
func (f *fakeFileLister) UpdateLogFolder() string { return f.updateFolder }
func (f *fakeFileLister) URLBase() string         { return f.urlBase }

// ----- GET /log — empty paged envelope -----

func TestLogHandler_EmptyCorpus_Returns200PagedEmpty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log", nil)
	LogHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// Empty paged envelope: records `[]`, sortKey rewritten to "time"
	// per the v3 handler's `id` → `time` response remap (matches C# v5
	// at LogController.cs:73).
	if !strings.Contains(body, `"records":[]`) {
		t.Errorf("body should contain `\"records\":[]`; got %q", body)
	}
	if !strings.Contains(body, `"sortKey":"time"`) {
		t.Errorf("sortKey should be rewritten to `time`; got %q", body)
	}
}

// ----- GET /log/file — empty folder returns `[]` -----

func TestFileListHandler_NonexistentFolder_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	svc := &fakeFileLister{folder: "/nonexistent/folder/xyz"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log/file", nil)
	FileListHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "[]" {
		t.Errorf("body: got %q want `[]`", body)
	}
}

// ----- GET /log/file/{filename} — 404 for missing file -----

func TestFileByNameHandler_Missing_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeFileLister{folder: "/nonexistent/folder/xyz"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log/file/foo.txt", nil)
	req.SetPathValue("filename", "foo.txt")
	FileByNameHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestFileByNameHandler_InvalidFilename_Returns400(t *testing.T) {
	t.Parallel()
	// Path-traversal protection: filename must match `^[-.a-zA-Z0-9]+\.txt$`.
	svc := &fakeFileLister{folder: "/tmp"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log/file/../etc/passwd", nil)
	req.SetPathValue("filename", "../etc/passwd")
	FileByNameHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (filename regex); got body=%q", rr.Code, rr.Body.String())
	}
}

// ----- GET /log/file/update — empty folder returns `[]` -----

func TestUpdateFileListHandler_NonexistentFolder_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	svc := &fakeFileLister{updateFolder: "/nonexistent/update/folder/xyz"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/log/file/update", nil)
	UpdateFileListHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "[]" {
		t.Errorf("body: got %q want `[]`", body)
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if LogHandler == nil || FileListHandler == nil || FileByNameHandler == nil {
		t.Error("handler aliases must be non-nil")
	}
	if UpdateFileListHandler == nil || UpdateFileByNameHandler == nil {
		t.Error("update handler aliases must be non-nil")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.LogResource{}
	var _ FileResource = apiv3.LogFileResource{}
	var _ apiv3.LogResource = Resource{}
	var _ apiv3.LogFileResource = FileResource{}
	var _ FileLister = (apiv3.LogFileLister)(nil)
	var _ UpdateFileLister = (apiv3.UpdateLogFileLister)(nil)
}
