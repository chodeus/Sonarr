package systembackup_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/api/v5/systembackup"
	"sonarr.tv/sonarr/internal/backup"
)

type fakeAdmin struct {
	rows       []backup.Backup
	exists     map[string]bool
	deletedAt  []string
	restoredAt []string
}

func (f *fakeAdmin) GetBackups() ([]backup.Backup, error) { return f.rows, nil }
func (f *fakeAdmin) GetBackupFolderForType(t backup.Type) string {
	return "/var/lib/sonarr/Backups/" + t.String()
}
func (f *fakeAdmin) Restore(p string) error {
	f.restoredAt = append(f.restoredAt, p)
	return nil
}
func (f *fakeAdmin) FileExists(path string) bool {
	if f.exists == nil {
		return true
	}
	return f.exists[path]
}
func (f *fakeAdmin) DeleteFile(path string) error {
	f.deletedAt = append(f.deletedAt, path)
	return nil
}

type fakeUploader struct {
	tempPath  string
	savedAt   []string
	restored  []string
	deletedAt []string
}

func (f *fakeUploader) TempPath() string { return f.tempPath }
func (f *fakeUploader) SaveStream(r io.Reader, path string) error {
	_, _ = io.Copy(io.Discard, r)
	f.savedAt = append(f.savedAt, path)
	return nil
}
func (f *fakeUploader) Restore(path string) error {
	f.restored = append(f.restored, path)
	return nil
}
func (f *fakeUploader) DeleteFile(path string) error {
	f.deletedAt = append(f.deletedAt, path)
	return nil
}

func sampleBackup() backup.Backup {
	return backup.Backup{
		Name: "sonarr_backup_2026.05.18_12.00.00.zip",
		Type: backup.TypeScheduled,
		Size: 1024,
		Time: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
}

func TestListHandlerAlias_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	systembackup.ListHandler(&fakeAdmin{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/backup", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `[]` {
		t.Errorf("body: %q want []", rr.Body.String())
	}
}

func TestListHandlerAlias_Populated(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	systembackup.ListHandler(&fakeAdmin{rows: []backup.Backup{sampleBackup()}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/backup", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []systembackup.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name == "" || got[0].Path == "" || got[0].Type == "" {
		t.Errorf("got %+v", got)
	}
}

func findID(t *testing.T, admin *fakeAdmin) int {
	t.Helper()
	rr := httptest.NewRecorder()
	systembackup.ListHandler(admin).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/backup", nil))
	var rows []systembackup.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("no backups")
	}
	return rows[0].ID
}

func TestDeleteHandler_RewritesTo204(t *testing.T) {
	t.Parallel()
	admin := &fakeAdmin{rows: []backup.Backup{sampleBackup()}}
	id := findID(t, admin)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/system/backup/"+itoa(id), nil)
	req.SetPathValue("id", itoa(id))
	systembackup.DeleteHandler(admin).ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body must be empty for 204: %q", rr.Body.String())
	}
	if len(admin.deletedAt) != 1 {
		t.Errorf("DeleteFile not invoked: %v", admin.deletedAt)
	}
}

func TestDeleteHandler_MissingID_PassesThrough404(t *testing.T) {
	t.Parallel()
	admin := &fakeAdmin{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/system/backup/999", nil)
	req.SetPathValue("id", "999")
	systembackup.DeleteHandler(admin).ServeHTTP(rr, req)
	// Wrapper predicate only matches 200; 404 passes through.
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestRestoreHandlerAlias_Found(t *testing.T) {
	t.Parallel()
	admin := &fakeAdmin{rows: []backup.Backup{sampleBackup()}}
	id := findID(t, admin)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/system/backup/restore/"+itoa(id), nil)
	req.SetPathValue("id", itoa(id))
	systembackup.RestoreHandler(admin).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	var got struct {
		RestartRequired bool `json:"restartRequired"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.RestartRequired {
		t.Errorf("expected restartRequired=true")
	}
	if len(admin.restoredAt) != 1 {
		t.Errorf("Restore not invoked: %v", admin.restoredAt)
	}
}

func TestRestoreHandlerAlias_NotFound(t *testing.T) {
	t.Parallel()
	admin := &fakeAdmin{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/system/backup/restore/999", nil)
	req.SetPathValue("id", "999")
	systembackup.RestoreHandler(admin).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestRestoreUploadHandler_HappyPath(t *testing.T) {
	t.Parallel()
	body, contentType := multipartBody(t, "backup.zip", []byte("ZIP\x00"))
	up := &fakeUploader{tempPath: "/tmp"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/system/backup/restore/upload", body)
	req.Header.Set("Content-Type", contentType)
	systembackup.RestoreUploadHandler(up).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"restartRequired":true}` {
		t.Errorf("body: %q", got)
	}
	if len(up.savedAt) != 1 || len(up.restored) != 1 || len(up.deletedAt) != 1 {
		t.Errorf("upload pipeline not fully invoked: saved=%v restored=%v deleted=%v", up.savedAt, up.restored, up.deletedAt)
	}
}

func TestRestoreUploadHandler_BadExtension_V5ShapeIs400(t *testing.T) {
	t.Parallel()
	body, contentType := multipartBody(t, "backup.exe", []byte("MZ"))
	up := &fakeUploader{tempPath: "/tmp"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/system/backup/restore/upload", body)
	req.Header.Set("Content-Type", contentType)
	systembackup.RestoreUploadHandler(up).ServeHTTP(rr, req)

	// v5 wire-shape: 400 + `{"error":"..."}` — NOT v3's 415 + envelope.
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (v5 demotes v3's 415)", rr.Code)
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(got.Error, "Invalid extension") {
		t.Errorf("error body: %q", got.Error)
	}
	if len(up.savedAt) != 0 || len(up.restored) != 0 {
		t.Errorf("upload pipeline must not run on bad extension")
	}
}

func TestRestoreUploadHandler_MissingFile_400(t *testing.T) {
	t.Parallel()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("other", "value")
	_ = mw.Close()

	up := &fakeUploader{tempPath: "/tmp"}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/system/backup/restore/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	systembackup.RestoreUploadHandler(up).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// TestTypeAlias pins compile-time equivalence with v3.
func TestTypeAliases(t *testing.T) {
	t.Parallel()
	var r systembackup.Resource = systembackup.Resource{ID: 1, Name: "x", Path: "/x", Size: 1, Type: "scheduled", Time: time.Now()}
	if r.Name != "x" {
		t.Errorf("Resource not addressable")
	}
}

// helpers

func multipartBody(t *testing.T, filename string, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return body, mw.FormDataContentType()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
