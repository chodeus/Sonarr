package v3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

// fakeMMConfig is the test double for both MediaManagementConfigGetter
// and MediaManagementConfigWriter. Every field has both a getter
// return and a setter capture; setter calls record into the *Set
// field so tests can assert that the handler propagated the body
// values through every per-field writer.
type fakeMMConfig struct {
	// getter return values
	autoUnmonitor, copyHardlinks, createEmpty, deleteEmpty bool
	enableMediaInfo, importExtra                           bool
	setPermsLinux, skipFreeSpace, useScriptImport          bool
	chmod, chown, extraExt, recycleBin                     string
	episodeTitleRequired, fileDate, rescan, seasonPack     string
	scriptImportPath, userRejected                         string
	minFree, recycleBinDays                                int
	seasonPackThresh                                       float64
	properType                                             quality.ProperDownloadType

	// setter records (any non-nil pointer means it was called)
	callCount int
}

func (f *fakeMMConfig) AutoUnmonitorPreviouslyDownloadedEpisodes() bool { return f.autoUnmonitor }
func (f *fakeMMConfig) ChmodFolder() string                             { return f.chmod }
func (f *fakeMMConfig) ChownGroup() string                              { return f.chown }
func (f *fakeMMConfig) CopyUsingHardlinks() bool                        { return f.copyHardlinks }
func (f *fakeMMConfig) CreateEmptySeriesFolders() bool                  { return f.createEmpty }
func (f *fakeMMConfig) DeleteEmptyFolders() bool                        { return f.deleteEmpty }
func (f *fakeMMConfig) DownloadPropersAndRepacks() quality.ProperDownloadType {
	return f.properType
}
func (f *fakeMMConfig) EnableMediaInfo() bool                 { return f.enableMediaInfo }
func (f *fakeMMConfig) EpisodeTitleRequired() string          { return f.episodeTitleRequired }
func (f *fakeMMConfig) ExtraFileExtensions() string           { return f.extraExt }
func (f *fakeMMConfig) FileDate() string                      { return f.fileDate }
func (f *fakeMMConfig) ImportExtraFiles() bool                { return f.importExtra }
func (f *fakeMMConfig) MinimumFreeSpaceWhenImporting() int    { return f.minFree }
func (f *fakeMMConfig) RecycleBin() string                    { return f.recycleBin }
func (f *fakeMMConfig) RecycleBinCleanupDays() int            { return f.recycleBinDays }
func (f *fakeMMConfig) RescanAfterRefresh() string            { return f.rescan }
func (f *fakeMMConfig) ScriptImportPath() string              { return f.scriptImportPath }
func (f *fakeMMConfig) SeasonPackUpgrade() string             { return f.seasonPack }
func (f *fakeMMConfig) SeasonPackUpgradeThreshold() float64   { return f.seasonPackThresh }
func (f *fakeMMConfig) SetPermissionsLinux() bool             { return f.setPermsLinux }
func (f *fakeMMConfig) SkipFreeSpaceCheckWhenImporting() bool { return f.skipFreeSpace }
func (f *fakeMMConfig) UseScriptImport() bool                 { return f.useScriptImport }
func (f *fakeMMConfig) UserRejectedExtensions() string        { return f.userRejected }

// 23 setters — capture the new value into the corresponding getter
// field so the response-body round-trip reflects the persisted state.
func (f *fakeMMConfig) SetAutoUnmonitorPreviouslyDownloadedEpisodes(_ context.Context, v bool) error {
	f.autoUnmonitor = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetChmodFolder(_ context.Context, v string) error {
	f.chmod = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetChownGroup(_ context.Context, v string) error {
	f.chown = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetCopyUsingHardlinks(_ context.Context, v bool) error {
	f.copyHardlinks = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetCreateEmptySeriesFolders(_ context.Context, v bool) error {
	f.createEmpty = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetDeleteEmptyFolders(_ context.Context, v bool) error {
	f.deleteEmpty = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetDownloadPropersAndRepacks(_ context.Context, v quality.ProperDownloadType) error {
	f.properType = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetEnableMediaInfo(_ context.Context, v bool) error {
	f.enableMediaInfo = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetEpisodeTitleRequired(_ context.Context, v string) error {
	f.episodeTitleRequired = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetExtraFileExtensions(_ context.Context, v string) error {
	f.extraExt = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetFileDate(_ context.Context, v string) error {
	f.fileDate = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetImportExtraFiles(_ context.Context, v bool) error {
	f.importExtra = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetMinimumFreeSpaceWhenImporting(_ context.Context, v int) error {
	f.minFree = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetRecycleBin(_ context.Context, v string) error {
	f.recycleBin = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetRecycleBinCleanupDays(_ context.Context, v int) error {
	f.recycleBinDays = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetRescanAfterRefresh(_ context.Context, v string) error {
	f.rescan = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetScriptImportPath(_ context.Context, v string) error {
	f.scriptImportPath = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetSeasonPackUpgrade(_ context.Context, v string) error {
	f.seasonPack = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetSeasonPackUpgradeThreshold(_ context.Context, v float64) error {
	f.seasonPackThresh = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetSetPermissionsLinux(_ context.Context, v bool) error {
	f.setPermsLinux = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetSkipFreeSpaceCheckWhenImporting(_ context.Context, v bool) error {
	f.skipFreeSpace = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetUseScriptImport(_ context.Context, v bool) error {
	f.useScriptImport = v
	f.callCount++
	return nil
}
func (f *fakeMMConfig) SetUserRejectedExtensions(_ context.Context, v string) error {
	f.userRejected = v
	f.callCount++
	return nil
}

func mmBody() string {
	// Minimal valid body with MinimumFreeSpaceWhenImporting=100,
	// RecycleBin empty, UseScriptImport false.
	return `{"id":1,"autoUnmonitorPreviouslyDownloadedEpisodes":false,"chmodFolder":"","chownGroup":"","copyUsingHardlinks":false,"createEmptySeriesFolders":false,"deleteEmptyFolders":false,"downloadPropersAndRepacks":"preferAndUpgrade","enableMediaInfo":true,"episodeTitleRequired":"always","extraFileExtensions":"","fileDate":"none","importExtraFiles":false,"minimumFreeSpaceWhenImporting":100,"recycleBin":"","recycleBinCleanupDays":7,"rescanAfterRefresh":"always","scriptImportPath":"","seasonPackUpgrade":"never","seasonPackUpgradeThreshold":0.95,"setPermissionsLinux":false,"skipFreeSpaceCheckWhenImporting":false,"useScriptImport":false,"userRejectedExtensions":""}`
}

func TestMediaManagementConfigUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeMMConfig{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/mediamanagement/1", strings.NewReader(mmBody()))
	req.SetPathValue("id", "1")
	MediaManagementConfigUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.callCount != 23 {
		t.Errorf("expected 23 setter calls; got %d", w.callCount)
	}
	if w.properType != quality.ProperPreferAndUpgrade {
		t.Errorf("DownloadPropersAndRepacks not decoded: got %d", w.properType)
	}
	if w.minFree != 100 {
		t.Errorf("MinimumFreeSpaceWhenImporting: got %d", w.minFree)
	}
}

func TestMediaManagementConfigUpdateHandler_MinFreeBelow100_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(mmBody(), `"minimumFreeSpaceWhenImporting":100`, `"minimumFreeSpaceWhenImporting":50`, 1)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/mediamanagement/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	MediaManagementConfigUpdateHandler(&fakeMMConfig{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "must be at least 100") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestMediaManagementConfigUpdateHandler_NegativeCleanupDays_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(mmBody(), `"recycleBinCleanupDays":7`, `"recycleBinCleanupDays":-1`, 1)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/mediamanagement/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	MediaManagementConfigUpdateHandler(&fakeMMConfig{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"RecycleBinCleanupDays"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestMediaManagementConfigUpdateHandler_RelativeRecycleBin_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(mmBody(), `"recycleBin":""`, `"recycleBin":"relative/path"`, 1)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/mediamanagement/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	MediaManagementConfigUpdateHandler(&fakeMMConfig{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Recycle Bin path must be absolute") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestMediaManagementConfigUpdateHandler_RelativeScriptImport_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(mmBody(), `"useScriptImport":false`, `"useScriptImport":true`, 1)
	body = strings.Replace(body, `"scriptImportPath":""`, `"scriptImportPath":"rel/path"`, 1)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/mediamanagement/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	MediaManagementConfigUpdateHandler(&fakeMMConfig{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Script Import Path must be absolute") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestMediaManagementConfigUpdateHandler_BadDownloadPropersAndRepacks_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.Replace(mmBody(), `"downloadPropersAndRepacks":"preferAndUpgrade"`, `"downloadPropersAndRepacks":"nope"`, 1)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/mediamanagement/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	MediaManagementConfigUpdateHandler(&fakeMMConfig{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"DownloadPropersAndRepacks"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestMediaManagementConfigUpdateHandler_BadUserRejectedExtensions_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	// Comma surrounded by spaces (one empty entry) — should reject.
	body := strings.Replace(mmBody(), `"userRejectedExtensions":""`, `"userRejectedExtensions":"txt,,bin"`, 1)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/config/mediamanagement/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	MediaManagementConfigUpdateHandler(&fakeMMConfig{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "user-rejected extension must be non-empty") {
		t.Errorf("body: %s", rr.Body.String())
	}
}
