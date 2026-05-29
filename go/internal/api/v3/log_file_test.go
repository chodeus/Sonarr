package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeLogFileLister struct {
	folder       string
	updateFolder string
	urlBase      string
}

func (f fakeLogFileLister) LogFolder() string       { return f.folder }
func (f fakeLogFileLister) UpdateLogFolder() string { return f.updateFolder }
func (f fakeLogFileLister) URLBase() string         { return f.urlBase }

func TestLogFileHandler_MissingFolderReturnsEmpty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	LogFileHandler(fakeLogFileLister{folder: "/nonexistent/path/sonarr-test", urlBase: ""}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log/file", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("empty body: %q", rr.Body.String())
	}
}

func TestLogFileHandler_ListsTxtFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "sonarr.txt", "log line 1\n")
	time.Sleep(10 * time.Millisecond) // ensure ModTime ordering
	writeFile(t, dir, "sonarr.1.txt", "older log\n")
	writeFile(t, dir, "skip.txt", "no")
	writeFile(t, dir, "notalog.bin", "binary")
	rr := httptest.NewRecorder()
	LogFileHandler(fakeLogFileLister{folder: dir, urlBase: "/sonarr"}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log/file", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []LogFileResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 *.txt entries, got %d", len(got))
	}
	for _, r := range got {
		if r.Filename == "notalog.bin" {
			t.Errorf(".bin should be filtered: %+v", r)
		}
	}
	if got[0].ContentsURL != "/sonarr/api/v1/log/file/"+got[0].Filename {
		t.Errorf("ContentsURL: %q", got[0].ContentsURL)
	}
	if got[0].DownloadURL != "/sonarr/logfile/"+got[0].Filename {
		t.Errorf("DownloadURL: %q", got[0].DownloadURL)
	}
}

func TestLogFileByNameHandler_Found(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "sonarr.txt", "line1\nline2\n")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/log/file/sonarr.txt", nil)
	req.SetPathValue("filename", "sonarr.txt")
	LogFileByNameHandler(fakeLogFileLister{folder: dir}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type: %q", rr.Header().Get("Content-Type"))
	}
	if rr.Body.String() != "line1\nline2\n" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestLogFileByNameHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/log/file/missing.txt", nil)
	req.SetPathValue("filename", "missing.txt")
	LogFileByNameHandler(fakeLogFileLister{folder: dir}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestLogFileByNameHandler_PathTraversal_400(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/log/file/..%2Fetc%2Fpasswd", nil)
	req.SetPathValue("filename", "../etc/passwd")
	LogFileByNameHandler(fakeLogFileLister{folder: dir}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestLogFileByNameHandler_InvalidExtension_400(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "sonarr.bin", "no")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/log/file/sonarr.bin", nil)
	req.SetPathValue("filename", "sonarr.bin")
	LogFileByNameHandler(fakeLogFileLister{folder: dir}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestLogHandler_NilListerEmptyPagedResponse(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	LogHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log?pageSize=10", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got PagingResource[LogResource]
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TotalRecords != 0 || len(got.Records) != 0 {
		t.Errorf("expected empty page; got total=%d len=%d", got.TotalRecords, len(got.Records))
	}
	if got.SortKey != "time" {
		t.Errorf("default SortKey: got %q want time", got.SortKey)
	}
}

// ---------- 5.X2: /log/file/update tests ----------

func TestUpdateLogFileHandler_ListsUpdateLogs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "update.txt", "ok")
	rr := httptest.NewRecorder()
	UpdateLogFileHandler(fakeLogFileLister{updateFolder: dir, urlBase: "/sonarr"}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log/file/update", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []LogFileResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Filename != "update.txt" {
		t.Errorf("got: %+v", got)
	}
	if got[0].ContentsURL != "/sonarr/api/v1/log/file/update/update.txt" {
		t.Errorf("contentsURL: %q", got[0].ContentsURL)
	}
	if got[0].DownloadURL != "/sonarr/updatelogfile/update.txt" {
		t.Errorf("downloadURL: %q", got[0].DownloadURL)
	}
}

func TestUpdateLogFileHandler_MissingFolderReturnsEmpty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	UpdateLogFileHandler(fakeLogFileLister{updateFolder: "/does/not/exist/update", urlBase: ""}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/log/file/update", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestUpdateLogFileByNameHandler_Found(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "update.txt", "log content\n")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/log/file/update/update.txt", nil)
	req.SetPathValue("filename", "update.txt")
	UpdateLogFileByNameHandler(fakeLogFileLister{updateFolder: dir}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "log content\n" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestUpdateLogFileByNameHandler_PathTraversal_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/log/file/update/x", nil)
	req.SetPathValue("filename", "../etc/passwd")
	UpdateLogFileByNameHandler(fakeLogFileLister{updateFolder: "/tmp"}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestUpdateLogFileByNameHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/log/file/update/missing.txt", nil)
	req.SetPathValue("filename", "missing.txt")
	UpdateLogFileByNameHandler(fakeLogFileLister{updateFolder: dir}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}
