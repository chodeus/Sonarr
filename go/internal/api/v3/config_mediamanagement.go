package v3

import (
	"encoding/json"
	"net/http"

	"sonarr.tv/sonarr/internal/quality"
)

// MediaManagementConfigResource mirrors
// `src/Sonarr.Api.V3/Config/MediaManagementConfigResource.cs`. 24
// fields covering naming/storage/import behaviour. Most map directly
// from config.Provider getters; the four C# enum fields render as
// camelCase strings on the wire (downloadPropersAndRepacks,
// episodeTitleRequired, fileDate, rescanAfterRefresh, seasonPackUpgrade)
// — the mapping is done via ProperDownloadTypeName + CamelCaseEnumName
// at the API edge so the config package stays decoupled from the
// REST serialiser quirks.
//
// Field-source mapping (C# → Go):
//
//	id                                        → constant 1
//	autoUnmonitorPreviouslyDownloadedEpisodes → Provider.AutoUnmonitorPreviouslyDownloadedEpisodes
//	chmodFolder                               → Provider.ChmodFolder
//	chownGroup                                → Provider.ChownGroup
//	copyUsingHardlinks                        → Provider.CopyUsingHardlinks
//	createEmptySeriesFolders                  → Provider.CreateEmptySeriesFolders
//	deleteEmptyFolders                        → Provider.DeleteEmptyFolders
//	downloadPropersAndRepacks                 → ProperDownloadTypeName(Provider.DownloadPropersAndRepacks)
//	enableMediaInfo                           → Provider.EnableMediaInfo
//	episodeTitleRequired                      → CamelCaseEnumName(Provider.EpisodeTitleRequired)
//	extraFileExtensions                       → Provider.ExtraFileExtensions
//	fileDate                                  → CamelCaseEnumName(Provider.FileDate)
//	importExtraFiles                          → Provider.ImportExtraFiles
//	minimumFreeSpaceWhenImporting             → Provider.MinimumFreeSpaceWhenImporting
//	recycleBin                                → Provider.RecycleBin
//	recycleBinCleanupDays                     → Provider.RecycleBinCleanupDays
//	rescanAfterRefresh                        → CamelCaseEnumName(Provider.RescanAfterRefresh)
//	scriptImportPath                          → Provider.ScriptImportPath
//	seasonPackUpgrade                         → CamelCaseEnumName(Provider.SeasonPackUpgrade)
//	seasonPackUpgradeThreshold                → Provider.SeasonPackUpgradeThreshold
//	setPermissionsLinux                       → Provider.SetPermissionsLinux
//	skipFreeSpaceCheckWhenImporting           → Provider.SkipFreeSpaceCheckWhenImporting
//	useScriptImport                           → Provider.UseScriptImport
//	userRejectedExtensions                    → Provider.UserRejectedExtensions
type MediaManagementConfigResource struct {
	ID                                        int     `json:"id"`
	AutoUnmonitorPreviouslyDownloadedEpisodes bool    `json:"autoUnmonitorPreviouslyDownloadedEpisodes"`
	ChmodFolder                               string  `json:"chmodFolder"`
	ChownGroup                                string  `json:"chownGroup"`
	CopyUsingHardlinks                        bool    `json:"copyUsingHardlinks"`
	CreateEmptySeriesFolders                  bool    `json:"createEmptySeriesFolders"`
	DeleteEmptyFolders                        bool    `json:"deleteEmptyFolders"`
	DownloadPropersAndRepacks                 string  `json:"downloadPropersAndRepacks"`
	EnableMediaInfo                           bool    `json:"enableMediaInfo"`
	EpisodeTitleRequired                      string  `json:"episodeTitleRequired"`
	ExtraFileExtensions                       string  `json:"extraFileExtensions"`
	FileDate                                  string  `json:"fileDate"`
	ImportExtraFiles                          bool    `json:"importExtraFiles"`
	MinimumFreeSpaceWhenImporting             int     `json:"minimumFreeSpaceWhenImporting"`
	RecycleBin                                string  `json:"recycleBin"`
	RecycleBinCleanupDays                     int     `json:"recycleBinCleanupDays"`
	RescanAfterRefresh                        string  `json:"rescanAfterRefresh"`
	ScriptImportPath                          string  `json:"scriptImportPath"`
	SeasonPackUpgrade                         string  `json:"seasonPackUpgrade"`
	SeasonPackUpgradeThreshold                float64 `json:"seasonPackUpgradeThreshold"`
	SetPermissionsLinux                       bool    `json:"setPermissionsLinux"`
	SkipFreeSpaceCheckWhenImporting           bool    `json:"skipFreeSpaceCheckWhenImporting"`
	UseScriptImport                           bool    `json:"useScriptImport"`
	UserRejectedExtensions                    string  `json:"userRejectedExtensions"`
}

// MediaManagementConfigGetter is the read surface the handler depends
// on. *config.Provider satisfies it directly via existing typed
// getters.
type MediaManagementConfigGetter interface {
	AutoUnmonitorPreviouslyDownloadedEpisodes() bool
	ChmodFolder() string
	ChownGroup() string
	CopyUsingHardlinks() bool
	CreateEmptySeriesFolders() bool
	DeleteEmptyFolders() bool
	DownloadPropersAndRepacks() quality.ProperDownloadType
	EnableMediaInfo() bool
	EpisodeTitleRequired() string
	ExtraFileExtensions() string
	FileDate() string
	ImportExtraFiles() bool
	MinimumFreeSpaceWhenImporting() int
	RecycleBin() string
	RecycleBinCleanupDays() int
	RescanAfterRefresh() string
	ScriptImportPath() string
	SeasonPackUpgrade() string
	SeasonPackUpgradeThreshold() float64
	SetPermissionsLinux() bool
	SkipFreeSpaceCheckWhenImporting() bool
	UseScriptImport() bool
	UserRejectedExtensions() string
}

// MediaManagementConfigHandler returns the http.Handler for
// GET /api/v3/config/mediamanagement. Mirrors
// `MediaManagementConfigController` — singleton with Id=1.
func MediaManagementConfigHandler(getter MediaManagementConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := MediaManagementConfigResource{
			ID: 1,
			AutoUnmonitorPreviouslyDownloadedEpisodes: getter.AutoUnmonitorPreviouslyDownloadedEpisodes(),
			ChmodFolder:                     getter.ChmodFolder(),
			ChownGroup:                      getter.ChownGroup(),
			CopyUsingHardlinks:              getter.CopyUsingHardlinks(),
			CreateEmptySeriesFolders:        getter.CreateEmptySeriesFolders(),
			DeleteEmptyFolders:              getter.DeleteEmptyFolders(),
			DownloadPropersAndRepacks:       ProperDownloadTypeName(getter.DownloadPropersAndRepacks()),
			EnableMediaInfo:                 getter.EnableMediaInfo(),
			EpisodeTitleRequired:            CamelCaseEnumName(getter.EpisodeTitleRequired()),
			ExtraFileExtensions:             getter.ExtraFileExtensions(),
			FileDate:                        CamelCaseEnumName(getter.FileDate()),
			ImportExtraFiles:                getter.ImportExtraFiles(),
			MinimumFreeSpaceWhenImporting:   getter.MinimumFreeSpaceWhenImporting(),
			RecycleBin:                      getter.RecycleBin(),
			RecycleBinCleanupDays:           getter.RecycleBinCleanupDays(),
			RescanAfterRefresh:              CamelCaseEnumName(getter.RescanAfterRefresh()),
			ScriptImportPath:                getter.ScriptImportPath(),
			SeasonPackUpgrade:               CamelCaseEnumName(getter.SeasonPackUpgrade()),
			SeasonPackUpgradeThreshold:      getter.SeasonPackUpgradeThreshold(),
			SetPermissionsLinux:             getter.SetPermissionsLinux(),
			SkipFreeSpaceCheckWhenImporting: getter.SkipFreeSpaceCheckWhenImporting(),
			UseScriptImport:                 getter.UseScriptImport(),
			UserRejectedExtensions:          getter.UserRejectedExtensions(),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}
