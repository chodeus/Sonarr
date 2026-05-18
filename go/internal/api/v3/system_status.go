package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sonarr.tv/sonarr/internal/httperr"
)

// SystemStatusResource mirrors
// `src/Sonarr.Api.V3/System/SystemStatusResource.cs`. 29 fields
// covering build / runtime / platform / database state.
//
// Fields divide into three sources: boot-static (build + runtime
// detection — captured once into SystemStatusContext), cfg-dynamic
// (read per-request via SystemStatusGetter so live config changes
// surface immediately), and constants (appName="Sonarr",
// isNetCore=true).
//
// Production routing for this endpoint is DEFERRED — the platform-
// detection helpers (isContainerized, isDocker, runtimeName/Version,
// startupPath, appData, mode) need cmd/sonarr boot-time wiring that
// is its own slice. The handler ships here so the wire shape is
// pinned to the recorded golden; the route lands in main.go once
// the boot-time context construction is in place.
type SystemStatusResource struct {
	AppName                string    `json:"appName"`
	InstanceName           string    `json:"instanceName"`
	Version                string    `json:"version"`
	BuildTime              time.Time `json:"buildTime"`
	IsDebug                bool      `json:"isDebug"`
	IsProduction           bool      `json:"isProduction"`
	IsAdmin                bool      `json:"isAdmin"`
	IsUserInteractive      bool      `json:"isUserInteractive"`
	StartupPath            string    `json:"startupPath"`
	AppData                string    `json:"appData"`
	OsName                 string    `json:"osName"`
	OsVersion              string    `json:"osVersion"`
	IsNetCore              bool      `json:"isNetCore"`
	IsLinux                bool      `json:"isLinux"`
	IsOsx                  bool      `json:"isOsx"`
	IsWindows              bool      `json:"isWindows"`
	IsDocker               bool      `json:"isDocker"`
	IsContainerized        bool      `json:"isContainerized"`
	Mode                   string    `json:"mode"`
	Branch                 string    `json:"branch"`
	Authentication         string    `json:"authentication"`
	DatabaseVersion        string    `json:"databaseVersion"`
	DatabaseType           string    `json:"databaseType"`
	MigrationVersion       int       `json:"migrationVersion"`
	URLBase                string    `json:"urlBase"`
	RuntimeVersion         string    `json:"runtimeVersion"`
	RuntimeName            string    `json:"runtimeName"`
	StartTime              time.Time `json:"startTime"`
	PackageUpdateMechanism string    `json:"packageUpdateMechanism"`
}

// SystemStatusContext carries the boot-static fields. Built once at
// host start and reused per request.
type SystemStatusContext struct {
	Version           string
	BuildTime         time.Time
	StartTime         time.Time
	StartupPath       string
	AppData           string
	OsName            string
	OsVersion         string
	RuntimeName       string
	RuntimeVersion    string
	DatabaseType      string
	DatabaseVersion   string
	MigrationVersion  int
	IsDebug           bool
	IsProduction      bool
	IsAdmin           bool
	IsUserInteractive bool
	IsLinux           bool
	IsOsx             bool
	IsWindows         bool
	IsDocker          bool
	IsContainerized   bool
	Mode              string
}

// SystemStatusGetter narrows the per-request cfg surface to the five
// fields that can change at runtime. *configfile.Provider satisfies
// it via existing accessors (with one rename: PackageUpdateMechanism
// → UpdateMechanism upstream).
type SystemStatusGetter interface {
	AuthenticationMethod(ctx context.Context) (string, error)
	Branch(ctx context.Context) (string, error)
	InstanceName(ctx context.Context) (string, error)
	URLBase(ctx context.Context) (string, error)
	UpdateMechanism(ctx context.Context) (string, error)
}

// SystemStatusHandler returns the http.Handler for
// GET /api/v3/system/status. Mirrors `SystemController.GetStatus`.
// The handler bakes the boot-static context into every response and
// re-reads the dynamic getter for cfg-driven fields. Both
// AuthenticationMethod / UpdateMechanism camelCase at the API edge.
//
// AppName is the constant "Sonarr"; IsNetCore is constant `true` —
// the Go port emits true to match what the React frontend expects
// from a modern runtime.
func SystemStatusHandler(static SystemStatusContext, getter SystemStatusGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		read := func(s string, err error) (string, bool) {
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("system/status: %w", err), nil)
				return "", false
			}
			return s, true
		}
		auth, ok := read(getter.AuthenticationMethod(ctx))
		if !ok {
			return
		}
		branch, ok := read(getter.Branch(ctx))
		if !ok {
			return
		}
		instance, ok := read(getter.InstanceName(ctx))
		if !ok {
			return
		}
		urlBase, ok := read(getter.URLBase(ctx))
		if !ok {
			return
		}
		updateMech, ok := read(getter.UpdateMechanism(ctx))
		if !ok {
			return
		}
		res := SystemStatusResource{
			AppName:                "Sonarr",
			InstanceName:           instance,
			Version:                static.Version,
			BuildTime:              static.BuildTime,
			IsDebug:                static.IsDebug,
			IsProduction:           static.IsProduction,
			IsAdmin:                static.IsAdmin,
			IsUserInteractive:      static.IsUserInteractive,
			StartupPath:            static.StartupPath,
			AppData:                static.AppData,
			OsName:                 static.OsName,
			OsVersion:              static.OsVersion,
			IsNetCore:              true,
			IsLinux:                static.IsLinux,
			IsOsx:                  static.IsOsx,
			IsWindows:              static.IsWindows,
			IsDocker:               static.IsDocker,
			IsContainerized:        static.IsContainerized,
			Mode:                   static.Mode,
			Branch:                 branch,
			Authentication:         CamelCaseEnumName(auth),
			DatabaseVersion:        static.DatabaseVersion,
			DatabaseType:           static.DatabaseType,
			MigrationVersion:       static.MigrationVersion,
			URLBase:                urlBase,
			RuntimeVersion:         static.RuntimeVersion,
			RuntimeName:            static.RuntimeName,
			StartTime:              static.StartTime,
			PackageUpdateMechanism: CamelCaseEnumName(updateMech),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}
