package v3

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/backup"
)

type fakeBackupAdmin struct {
	rows       []backup.Backup
	folder     map[backup.Type]string
	exists     map[string]bool
	listErr    error
	deletedAt  []string
	deleteErr  error
	restoredAt []string
	restoreErr error
}

func (f *fakeBackupAdmin) GetBackups() ([]backup.Backup, error) { return f.rows, f.listErr }
func (f *fakeBackupAdmin) GetBackupFolderForType(t backup.Type) string {
	if v, ok := f.folder[t]; ok {
		return v
	}
	return "/var/lib/sonarr/Backups/" + t.String()
}
func (f *fakeBackupAdmin) Restore(p string) error {
	if f.restoreErr != nil {
		return f.restoreErr
	}
	f.restoredAt = append(f.restoredAt, p)
	return nil
}
func (f *fakeBackupAdmin) FileExists(path string) bool {
	if f.exists == nil {
		return true
	}
	return f.exists[path]
}
func (f *fakeBackupAdmin) DeleteFile(path string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
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

func TestBackupListHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	BackupListHandler(&fakeBackupAdmin{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/system/backup", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `[]` {
		t.Errorf("empty body: %q", rr.Body.String())
	}
}

func TestBackupListHandler_OrderingAndProjection(t *testing.T) {
	t.Parallel()
	older := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	rr := httptest.NewRecorder()
	BackupListHandler(&fakeBackupAdmin{rows: []backup.Backup{
		{Name: "a.zip", Type: backup.TypeScheduled, Size: 100, Time: older},
		{Name: "b.zip", Type: backup.TypeManual, Size: 200, Time: newer},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/system/backup", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []BackupResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: %d", len(got))
	}
	// Time desc → newer first.
	if got[0].Name != "b.zip" {
		t.Errorf("ordering: got %q want b.zip", got[0].Name)
	}
	if got[0].Type != "manual" || got[1].Type != "scheduled" {
		t.Errorf("type names: %q %q", got[0].Type, got[1].Type)
	}
	if got[0].Path != "/backup/manual/b.zip" {
		t.Errorf("path: %q", got[0].Path)
	}
	if got[0].ID <= 0 {
		t.Errorf("id should be derived non-negative int31: %d", got[0].ID)
	}
}

func TestBackupID_StableAcrossCalls(t *testing.T) {
	t.Parallel()
	b := sampleBackup()
	id1 := backupID(b)
	id2 := backupID(b)
	if id1 != id2 || id1 <= 0 {
		t.Errorf("ID instability: %d vs %d", id1, id2)
	}
}

func TestBackupDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	b := sampleBackup()
	id := backupID(b)
	admin := &fakeBackupAdmin{rows: []backup.Backup{b}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/system/backup/0", nil)
	req.SetPathValue("id", itoa(id))
	BackupDeleteHandler(admin).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(admin.deletedAt) != 1 {
		t.Errorf("expected 1 DeleteFile call; got %d", len(admin.deletedAt))
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestBackupDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/system/backup/999999", nil)
	req.SetPathValue("id", "999999")
	BackupDeleteHandler(&fakeBackupAdmin{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestBackupDeleteHandler_MissingFile_404(t *testing.T) {
	t.Parallel()
	b := sampleBackup()
	id := backupID(b)
	admin := &fakeBackupAdmin{
		rows:   []backup.Backup{b},
		exists: map[string]bool{}, // every FileExists call returns false.
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/system/backup/0", nil)
	req.SetPathValue("id", itoa(id))
	BackupDeleteHandler(admin).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestBackupRestoreHandler_Success(t *testing.T) {
	t.Parallel()
	b := sampleBackup()
	id := backupID(b)
	admin := &fakeBackupAdmin{rows: []backup.Backup{b}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/system/backup/restore/0", nil)
	req.SetPathValue("id", itoa(id))
	BackupRestoreHandler(admin).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(admin.restoredAt) != 1 {
		t.Errorf("expected 1 Restore call; got %d", len(admin.restoredAt))
	}
	var got struct {
		RestartRequired bool `json:"restartRequired"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.RestartRequired {
		t.Errorf("restartRequired not true; got %+v", got)
	}
}

func TestBackupRestoreHandler_RestoreError_500(t *testing.T) {
	t.Parallel()
	b := sampleBackup()
	id := backupID(b)
	admin := &fakeBackupAdmin{rows: []backup.Backup{b}, restoreErr: errors.New("boom")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/system/backup/restore/0", nil)
	req.SetPathValue("id", itoa(id))
	BackupRestoreHandler(admin).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestBackupRestoreHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/system/backup/restore/123", nil)
	req.SetPathValue("id", "123")
	BackupRestoreHandler(&fakeBackupAdmin{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func itoa(n int) string {
	return fmtInt(n)
}

// fmtInt avoids a strconv import in the test file (already in scope
// at package level, but the helper makes the call sites read cleaner).
func fmtInt(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
