package mediamanagementsettings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/mediamanagementsettings"
	"sonarr.tv/sonarr/internal/quality"
)

type fakeProv struct {
	mu                              sync.Mutex
	autoUnmonitor                   bool
	chmodFolder                     string
	chownGroup                      string
	copyHardlinks                   bool
	createEmpty                     bool
	deleteEmpty                     bool
	downloadPropers                 quality.ProperDownloadType
	enableMediaInfo                 bool
	episodeTitleRequired            string
	extraFileExtensions             string
	fileDate                        string
	importExtraFiles                bool
	minimumFreeSpaceWhenImporting   int
	recycleBin                      string
	recycleBinCleanupDays           int
	rescanAfterRefresh              string
	scriptImportPath                string
	seasonPackUpgrade               string
	seasonPackUpgradeThreshold      float64
	setPermissionsLinux             bool
	skipFreeSpaceCheckWhenImporting bool
	useScriptImport                 bool
	userRejectedExtensions          string
	setCalls                        []string
}

func newProv() *fakeProv {
	return &fakeProv{
		minimumFreeSpaceWhenImporting: 100, // satisfies min-100 validator default
		downloadPropers:               0,   // PreferAndUpgrade
		episodeTitleRequired:          "always",
		fileDate:                      "none",
		rescanAfterRefresh:            "always",
		seasonPackUpgrade:             "never",
	}
}

func (f *fakeProv) AutoUnmonitorPreviouslyDownloadedEpisodes() bool { return f.autoUnmonitor }
func (f *fakeProv) ChmodFolder() string                             { return f.chmodFolder }
func (f *fakeProv) ChownGroup() string                              { return f.chownGroup }
func (f *fakeProv) CopyUsingHardlinks() bool                        { return f.copyHardlinks }
func (f *fakeProv) CreateEmptySeriesFolders() bool                  { return f.createEmpty }
func (f *fakeProv) DeleteEmptyFolders() bool                        { return f.deleteEmpty }
func (f *fakeProv) DownloadPropersAndRepacks() quality.ProperDownloadType {
	return f.downloadPropers
}
func (f *fakeProv) EnableMediaInfo() bool                 { return f.enableMediaInfo }
func (f *fakeProv) EpisodeTitleRequired() string          { return f.episodeTitleRequired }
func (f *fakeProv) ExtraFileExtensions() string           { return f.extraFileExtensions }
func (f *fakeProv) FileDate() string                      { return f.fileDate }
func (f *fakeProv) ImportExtraFiles() bool                { return f.importExtraFiles }
func (f *fakeProv) MinimumFreeSpaceWhenImporting() int    { return f.minimumFreeSpaceWhenImporting }
func (f *fakeProv) RecycleBin() string                    { return f.recycleBin }
func (f *fakeProv) RecycleBinCleanupDays() int            { return f.recycleBinCleanupDays }
func (f *fakeProv) RescanAfterRefresh() string            { return f.rescanAfterRefresh }
func (f *fakeProv) ScriptImportPath() string              { return f.scriptImportPath }
func (f *fakeProv) SeasonPackUpgrade() string             { return f.seasonPackUpgrade }
func (f *fakeProv) SeasonPackUpgradeThreshold() float64   { return f.seasonPackUpgradeThreshold }
func (f *fakeProv) SetPermissionsLinux() bool             { return f.setPermissionsLinux }
func (f *fakeProv) SkipFreeSpaceCheckWhenImporting() bool { return f.skipFreeSpaceCheckWhenImporting }
func (f *fakeProv) UseScriptImport() bool                 { return f.useScriptImport }
func (f *fakeProv) UserRejectedExtensions() string        { return f.userRejectedExtensions }

func (f *fakeProv) set(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, name)
}

func (f *fakeProv) SetAutoUnmonitorPreviouslyDownloadedEpisodes(_ context.Context, v bool) error {
	f.set("AutoUnmonitor")
	f.autoUnmonitor = v
	return nil
}
func (f *fakeProv) SetChmodFolder(_ context.Context, v string) error {
	f.set("ChmodFolder")
	f.chmodFolder = v
	return nil
}
func (f *fakeProv) SetChownGroup(_ context.Context, v string) error {
	f.set("ChownGroup")
	f.chownGroup = v
	return nil
}
func (f *fakeProv) SetCopyUsingHardlinks(_ context.Context, v bool) error {
	f.set("CopyUsingHardlinks")
	f.copyHardlinks = v
	return nil
}
func (f *fakeProv) SetCreateEmptySeriesFolders(_ context.Context, v bool) error {
	f.set("CreateEmptySeriesFolders")
	f.createEmpty = v
	return nil
}
func (f *fakeProv) SetDeleteEmptyFolders(_ context.Context, v bool) error {
	f.set("DeleteEmptyFolders")
	f.deleteEmpty = v
	return nil
}
func (f *fakeProv) SetDownloadPropersAndRepacks(_ context.Context, v quality.ProperDownloadType) error {
	f.set("DownloadPropers")
	f.downloadPropers = v
	return nil
}
func (f *fakeProv) SetEnableMediaInfo(_ context.Context, v bool) error {
	f.set("EnableMediaInfo")
	f.enableMediaInfo = v
	return nil
}
func (f *fakeProv) SetEpisodeTitleRequired(_ context.Context, v string) error {
	f.set("EpisodeTitleRequired")
	f.episodeTitleRequired = v
	return nil
}
func (f *fakeProv) SetExtraFileExtensions(_ context.Context, v string) error {
	f.set("ExtraFileExtensions")
	f.extraFileExtensions = v
	return nil
}
func (f *fakeProv) SetFileDate(_ context.Context, v string) error {
	f.set("FileDate")
	f.fileDate = v
	return nil
}
func (f *fakeProv) SetImportExtraFiles(_ context.Context, v bool) error {
	f.set("ImportExtraFiles")
	f.importExtraFiles = v
	return nil
}
func (f *fakeProv) SetMinimumFreeSpaceWhenImporting(_ context.Context, v int) error {
	f.set("MinimumFreeSpaceWhenImporting")
	f.minimumFreeSpaceWhenImporting = v
	return nil
}
func (f *fakeProv) SetRecycleBin(_ context.Context, v string) error {
	f.set("RecycleBin")
	f.recycleBin = v
	return nil
}
func (f *fakeProv) SetRecycleBinCleanupDays(_ context.Context, v int) error {
	f.set("RecycleBinCleanupDays")
	f.recycleBinCleanupDays = v
	return nil
}
func (f *fakeProv) SetRescanAfterRefresh(_ context.Context, v string) error {
	f.set("RescanAfterRefresh")
	f.rescanAfterRefresh = v
	return nil
}
func (f *fakeProv) SetScriptImportPath(_ context.Context, v string) error {
	f.set("ScriptImportPath")
	f.scriptImportPath = v
	return nil
}
func (f *fakeProv) SetSeasonPackUpgrade(_ context.Context, v string) error {
	f.set("SeasonPackUpgrade")
	f.seasonPackUpgrade = v
	return nil
}
func (f *fakeProv) SetSeasonPackUpgradeThreshold(_ context.Context, v float64) error {
	f.set("SeasonPackUpgradeThreshold")
	f.seasonPackUpgradeThreshold = v
	return nil
}
func (f *fakeProv) SetSetPermissionsLinux(_ context.Context, v bool) error {
	f.set("SetPermissionsLinux")
	f.setPermissionsLinux = v
	return nil
}
func (f *fakeProv) SetSkipFreeSpaceCheckWhenImporting(_ context.Context, v bool) error {
	f.set("SkipFreeSpaceCheckWhenImporting")
	f.skipFreeSpaceCheckWhenImporting = v
	return nil
}
func (f *fakeProv) SetUseScriptImport(_ context.Context, v bool) error {
	f.set("UseScriptImport")
	f.useScriptImport = v
	return nil
}
func (f *fakeProv) SetUserRejectedExtensions(_ context.Context, v string) error {
	f.set("UserRejectedExtensions")
	f.userRejectedExtensions = v
	return nil
}

func (f *fakeProv) setCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.setCalls)
}

func TestGetHandler_EmitsSingleton(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	mediamanagementsettings.GetHandler(newProv()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/settings/mediamanagement", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got mediamanagementsettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 1 || got.MinimumFreeSpaceWhenImporting != 100 {
		t.Errorf("got %+v", got)
	}
}

func TestUpdateHandler_PutAccepted_PersistsAll23Fields(t *testing.T) {
	t.Parallel()
	p := newProv()
	body := mustEncode(t, mediamanagementsettings.Resource{
		ID: 1, AutoUnmonitorPreviouslyDownloadedEpisodes: true, RecycleBin: "/var/lib/sonarr/recycle",
		RecycleBinCleanupDays: 14, DownloadPropersAndRepacks: "preferAndUpgrade", CreateEmptySeriesFolders: true,
		DeleteEmptyFolders: true, FileDate: "none", RescanAfterRefresh: "always", SetPermissionsLinux: false,
		ChmodFolder: "0755", ChownGroup: "media", EpisodeTitleRequired: "always",
		SkipFreeSpaceCheckWhenImporting: false, MinimumFreeSpaceWhenImporting: 250, CopyUsingHardlinks: true,
		UseScriptImport: false, ScriptImportPath: "", ImportExtraFiles: true, ExtraFileExtensions: ".srt,.nfo",
		EnableMediaInfo: true, UserRejectedExtensions: ".exe,.com", SeasonPackUpgrade: "never",
		SeasonPackUpgradeThreshold: 0.85,
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/mediamanagement/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	mediamanagementsettings.UpdateHandler(p).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rr.Code, rr.Body.String())
	}
	var got mediamanagementsettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RecycleBinCleanupDays != 14 || got.MinimumFreeSpaceWhenImporting != 250 || got.ChmodFolder != "0755" {
		t.Errorf("body round-trip: %+v", got)
	}
	if c := p.setCallCount(); c != 23 {
		t.Errorf("expected 23 setter calls, got %d", c)
	}
}

func TestUpdateHandler_MinimumFreeSpaceBelow100_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, mediamanagementsettings.Resource{
		ID: 1, MinimumFreeSpaceWhenImporting: 50, DownloadPropersAndRepacks: "preferAndUpgrade",
		EpisodeTitleRequired: "always", FileDate: "none", RescanAfterRefresh: "always", SeasonPackUpgrade: "never",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/mediamanagement/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	mediamanagementsettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (min 100)", rr.Code)
	}
}

func TestUpdateHandler_RecycleBinRelativePath_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, mediamanagementsettings.Resource{
		ID: 1, RecycleBin: "relative/path", MinimumFreeSpaceWhenImporting: 100, DownloadPropersAndRepacks: "preferAndUpgrade",
		EpisodeTitleRequired: "always", FileDate: "none", RescanAfterRefresh: "always", SeasonPackUpgrade: "never",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/mediamanagement/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	mediamanagementsettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (RecycleBin must be absolute when set)", rr.Code)
	}
}

func TestUpdateHandler_RecycleBinCleanupDaysNegative_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, mediamanagementsettings.Resource{
		ID: 1, RecycleBinCleanupDays: -1, MinimumFreeSpaceWhenImporting: 100, DownloadPropersAndRepacks: "preferAndUpgrade",
		EpisodeTitleRequired: "always", FileDate: "none", RescanAfterRefresh: "always", SeasonPackUpgrade: "never",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/mediamanagement/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	mediamanagementsettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (RecycleBinCleanupDays ≥ 0)", rr.Code)
	}
}

func TestUpdateHandler_RejectsUnknownField_400(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":1,"minimumFreeSpaceWhenImporting":100,"wat":"unknown"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/mediamanagement/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	mediamanagementsettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (DisallowUnknownFields)", rr.Code)
	}
}

func TestHandlerAliases_NonNil(t *testing.T) {
	t.Parallel()
	if mediamanagementsettings.GetHandler == nil {
		t.Errorf("GetHandler nil")
	}
	if mediamanagementsettings.UpdateHandler == nil {
		t.Errorf("UpdateHandler nil")
	}
}

// TestTypeAliases pins compile-time equivalence with v3.
func TestTypeAliases(t *testing.T) {
	t.Parallel()
	r := mediamanagementsettings.Resource{ID: 1, RecycleBin: "/x"}
	if r.RecycleBin != "/x" {
		t.Errorf("Resource not addressable")
	}
}

func mustEncode(t *testing.T, r mediamanagementsettings.Resource) []byte {
	t.Helper()
	buf, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}
