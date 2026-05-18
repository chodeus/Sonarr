package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/validation"
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

// ---------- 5.B13c: PUT ----------

// MediaManagementConfigWriter is the write surface — getter + per-
// field setters. *config.Provider satisfies all 23 setters natively
// (the 24th field, Id, is the singleton constant and isn't
// persisted).
type MediaManagementConfigWriter interface {
	MediaManagementConfigGetter
	SetAutoUnmonitorPreviouslyDownloadedEpisodes(ctx context.Context, v bool) error
	SetChmodFolder(ctx context.Context, v string) error
	SetChownGroup(ctx context.Context, v string) error
	SetCopyUsingHardlinks(ctx context.Context, v bool) error
	SetCreateEmptySeriesFolders(ctx context.Context, v bool) error
	SetDeleteEmptyFolders(ctx context.Context, v bool) error
	SetDownloadPropersAndRepacks(ctx context.Context, v quality.ProperDownloadType) error
	SetEnableMediaInfo(ctx context.Context, v bool) error
	SetEpisodeTitleRequired(ctx context.Context, v string) error
	SetExtraFileExtensions(ctx context.Context, v string) error
	SetFileDate(ctx context.Context, v string) error
	SetImportExtraFiles(ctx context.Context, v bool) error
	SetMinimumFreeSpaceWhenImporting(ctx context.Context, v int) error
	SetRecycleBin(ctx context.Context, v string) error
	SetRecycleBinCleanupDays(ctx context.Context, v int) error
	SetRescanAfterRefresh(ctx context.Context, v string) error
	SetScriptImportPath(ctx context.Context, v string) error
	SetSeasonPackUpgrade(ctx context.Context, v string) error
	SetSeasonPackUpgradeThreshold(ctx context.Context, v float64) error
	SetSetPermissionsLinux(ctx context.Context, v bool) error
	SetSkipFreeSpaceCheckWhenImporting(ctx context.Context, v bool) error
	SetUseScriptImport(ctx context.Context, v bool) error
	SetUserRejectedExtensions(ctx context.Context, v string) error
}

// MediaManagementConfigUpdateHandler returns PUT /api/v3/config/mediamanagement/{id}.
// Mirrors `MediaManagementConfigController` (inherits ConfigController
// .SaveConfig). Returns 202 + body rebuilt from the getter so the
// response carries the persisted shape.
//
// Validation chain ports the cheap subset of the C# SharedValidator
// at controller :29-67:
//   - RecycleBinCleanupDays >= 0
//   - MinimumFreeSpaceWhenImporting >= 100
//   - RecycleBin (when non-empty) must be an absolute path
//   - ScriptImportPath (when UseScriptImport) must be an absolute path
//   - UserRejectedExtensions entries each parse to a leading-dot
//     extension (matches the C# `.Split(',').Insert(0, ".")` shape;
//     a bare "txt" is OK but a comma-separated string with spaces
//     gets stripped before checking)
//
// DEFERRED: the deep filesystem-state validators (folderChmodValidator,
// folderWritableValidator, pathExistsValidator, rootFolderValidator,
// rootFolderAncestorValidator, startupFolderValidator,
// systemFolderValidator, seriesPathValidator) — each runs disk-level
// checks the Go side needs adapter shims to surface. Service-level
// Set* methods don't validate; the gap surfaces at filesystem-write
// time. Follow-up slice once the disk-validator adapters land.
func MediaManagementConfigUpdateHandler(svc MediaManagementConfigWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body MediaManagementConfigResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("config/mediamanagement: malformed body: %v", err))
			return
		}
		if errs := validateMediaManagementConfig(&body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		ctx := r.Context()
		// Decode the enum wire string into the typed value once.
		downloadPropers, err := ProperDownloadTypeFromName(body.DownloadPropersAndRepacks)
		if err != nil {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "DownloadPropersAndRepacks",
				ErrorMessage: err.Error(),
				Severity:     "error",
			}})
			return
		}
		type setter struct {
			name string
			fn   func() error
		}
		setters := []setter{
			{"AutoUnmonitorPreviouslyDownloadedEpisodes", func() error {
				return svc.SetAutoUnmonitorPreviouslyDownloadedEpisodes(ctx, body.AutoUnmonitorPreviouslyDownloadedEpisodes)
			}},
			{"ChmodFolder", func() error { return svc.SetChmodFolder(ctx, body.ChmodFolder) }},
			{"ChownGroup", func() error { return svc.SetChownGroup(ctx, body.ChownGroup) }},
			{"CopyUsingHardlinks", func() error { return svc.SetCopyUsingHardlinks(ctx, body.CopyUsingHardlinks) }},
			{"CreateEmptySeriesFolders", func() error { return svc.SetCreateEmptySeriesFolders(ctx, body.CreateEmptySeriesFolders) }},
			{"DeleteEmptyFolders", func() error { return svc.SetDeleteEmptyFolders(ctx, body.DeleteEmptyFolders) }},
			{"DownloadPropersAndRepacks", func() error { return svc.SetDownloadPropersAndRepacks(ctx, downloadPropers) }},
			{"EnableMediaInfo", func() error { return svc.SetEnableMediaInfo(ctx, body.EnableMediaInfo) }},
			{"EpisodeTitleRequired", func() error { return svc.SetEpisodeTitleRequired(ctx, body.EpisodeTitleRequired) }},
			{"ExtraFileExtensions", func() error { return svc.SetExtraFileExtensions(ctx, body.ExtraFileExtensions) }},
			{"FileDate", func() error { return svc.SetFileDate(ctx, body.FileDate) }},
			{"ImportExtraFiles", func() error { return svc.SetImportExtraFiles(ctx, body.ImportExtraFiles) }},
			{"MinimumFreeSpaceWhenImporting", func() error { return svc.SetMinimumFreeSpaceWhenImporting(ctx, body.MinimumFreeSpaceWhenImporting) }},
			{"RecycleBin", func() error { return svc.SetRecycleBin(ctx, body.RecycleBin) }},
			{"RecycleBinCleanupDays", func() error { return svc.SetRecycleBinCleanupDays(ctx, body.RecycleBinCleanupDays) }},
			{"RescanAfterRefresh", func() error { return svc.SetRescanAfterRefresh(ctx, body.RescanAfterRefresh) }},
			{"ScriptImportPath", func() error { return svc.SetScriptImportPath(ctx, body.ScriptImportPath) }},
			{"SeasonPackUpgrade", func() error { return svc.SetSeasonPackUpgrade(ctx, body.SeasonPackUpgrade) }},
			{"SeasonPackUpgradeThreshold", func() error { return svc.SetSeasonPackUpgradeThreshold(ctx, body.SeasonPackUpgradeThreshold) }},
			{"SetPermissionsLinux", func() error { return svc.SetSetPermissionsLinux(ctx, body.SetPermissionsLinux) }},
			{"SkipFreeSpaceCheckWhenImporting", func() error { return svc.SetSkipFreeSpaceCheckWhenImporting(ctx, body.SkipFreeSpaceCheckWhenImporting) }},
			{"UseScriptImport", func() error { return svc.SetUseScriptImport(ctx, body.UseScriptImport) }},
			{"UserRejectedExtensions", func() error { return svc.SetUserRejectedExtensions(ctx, body.UserRejectedExtensions) }},
		}
		for _, s := range setters {
			if err := s.fn(); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/mediamanagement: set %s: %w", s.name, err), nil)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(buildMediaManagementResource(svc))
		_, _ = w.Write(buf)
	})
}

func validateMediaManagementConfig(res *MediaManagementConfigResource) *validation.Errors {
	errs := validation.New()
	errs.NonNegative("RecycleBinCleanupDays", res.RecycleBinCleanupDays)
	if res.MinimumFreeSpaceWhenImporting < 100 {
		errs.Add("MinimumFreeSpaceWhenImporting", "Minimum Free Space When Importing must be at least 100")
	}
	if res.RecycleBin != "" && !filepath.IsAbs(res.RecycleBin) {
		errs.Add("RecycleBin", "Recycle Bin path must be absolute when provided")
	}
	if res.UseScriptImport && res.ScriptImportPath != "" && !filepath.IsAbs(res.ScriptImportPath) {
		errs.Add("ScriptImportPath", "Script Import Path must be absolute when UseScriptImport is true")
	}
	// UserRejectedExtensions: comma-separated list. Each entry, after
	// stripping spaces + leading dots, must be non-empty so the
	// downstream `.Insert(0, ".")` produces a real extension. Empty
	// list is allowed (no user filter).
	if res.UserRejectedExtensions != "" {
		for _, raw := range strings.Split(res.UserRejectedExtensions, ",") {
			if strings.TrimSpace(strings.Trim(raw, ".")) == "" {
				errs.Add("UserRejectedExtensions", "Each user-rejected extension must be non-empty")
				break
			}
		}
	}
	return errs
}

func buildMediaManagementResource(g MediaManagementConfigGetter) MediaManagementConfigResource {
	return MediaManagementConfigResource{
		ID: 1,
		AutoUnmonitorPreviouslyDownloadedEpisodes: g.AutoUnmonitorPreviouslyDownloadedEpisodes(),
		ChmodFolder:                     g.ChmodFolder(),
		ChownGroup:                      g.ChownGroup(),
		CopyUsingHardlinks:              g.CopyUsingHardlinks(),
		CreateEmptySeriesFolders:        g.CreateEmptySeriesFolders(),
		DeleteEmptyFolders:              g.DeleteEmptyFolders(),
		DownloadPropersAndRepacks:       ProperDownloadTypeName(g.DownloadPropersAndRepacks()),
		EnableMediaInfo:                 g.EnableMediaInfo(),
		EpisodeTitleRequired:            CamelCaseEnumName(g.EpisodeTitleRequired()),
		ExtraFileExtensions:             g.ExtraFileExtensions(),
		FileDate:                        CamelCaseEnumName(g.FileDate()),
		ImportExtraFiles:                g.ImportExtraFiles(),
		MinimumFreeSpaceWhenImporting:   g.MinimumFreeSpaceWhenImporting(),
		RecycleBin:                      g.RecycleBin(),
		RecycleBinCleanupDays:           g.RecycleBinCleanupDays(),
		RescanAfterRefresh:              CamelCaseEnumName(g.RescanAfterRefresh()),
		ScriptImportPath:                g.ScriptImportPath(),
		SeasonPackUpgrade:               CamelCaseEnumName(g.SeasonPackUpgrade()),
		SeasonPackUpgradeThreshold:      g.SeasonPackUpgradeThreshold(),
		SetPermissionsLinux:             g.SetPermissionsLinux(),
		SkipFreeSpaceCheckWhenImporting: g.SkipFreeSpaceCheckWhenImporting(),
		UseScriptImport:                 g.UseScriptImport(),
		UserRejectedExtensions:          g.UserRejectedExtensions(),
	}
}
