// Package system ports the v5 SystemController family.
//
// Source: src/Sonarr.Api.V5/System/SystemController.cs (128 LOC) +
// src/Sonarr.Api.V5/System/SystemResource.cs (41 LOC).
//
// 5 endpoints — 3 GET, 2 POST. Two wire-shape divergences from v3:
//
//   - SystemResource omits the v3 `isContainerized` field and adds 3
//     deployment-info fields (`packageVersion`, `packageAuthor`,
//     `packageUpdateMechanismMessage`). v3 Go-port doesn't yet wire
//     IDeploymentInfoProvider, so the 3 added fields emit as empty
//     strings — same Wave 1 deferred-fields chain as J258/J259/J261
//     (Resource widens once the underlying provider port lands).
//
//   - GET /system/routes/duplicate emits `{}` (object) on v5 vs
//     `[]` (array) on v3. The v5 C# returns
//     Dictionary<string, List<string>>; v3 Go-port emits `[]` per
//     an earlier design call (`system_lifecycle.go:64-70`). v5 emits
//     `{}` to match the C# schema.
//
// GET /system/routes is a v3-port gap — neither v3 nor v5 Go-port
// has a DfaGraphWriter equivalent. v5 surface emits empty
// text/plain (J266 pattern: close the gap on the v5 wire only,
// leave v3 untouched). When a route-graph dumper lands, both v3
// and v5 widen via a shared adapter.
//
// POST /system/shutdown + /system/restart are pure path-aliases to
// the v3 handlers (identical wire shape: 200 + `{shuttingDown:true}`
// or `{restarting:true}`).
package system

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/httperr"
)

// StatusContext extends apiv3.SystemStatusContext with the three
// IDeploymentInfoProvider-sourced fields v5 adds.
//
// At boot the host populates V3 from existing wiring and leaves the
// three Package* fields as empty strings — IDeploymentInfoProvider
// isn't yet ported (Wave 1 deferred-fields chain).
type StatusContext struct {
	V3                            apiv3.SystemStatusContext
	PackageVersion                string
	PackageAuthor                 string
	PackageUpdateMechanismMessage string
}

// StatusGetter is the per-request dynamic config slice — identical
// shape to v3 (no new dynamic fields on the v5 surface).
type StatusGetter = apiv3.SystemStatusGetter

// Resource mirrors src/Sonarr.Api.V5/System/SystemResource.cs.
//
// Diff vs v3 Go-port apiv3.SystemStatusResource:
//   - omits isContainerized
//   - adds packageVersion, packageAuthor, packageUpdateMechanismMessage
//
// The v3 Go-port resource itself is a deferred subset of v3 C# —
// fields like sqliteVersion are absent on both v3 and v5 Go-port
// surfaces (same Wave 1 chain).
type Resource struct {
	AppName                       string    `json:"appName"`
	InstanceName                  string    `json:"instanceName"`
	Version                       string    `json:"version"`
	BuildTime                     time.Time `json:"buildTime"`
	IsDebug                       bool      `json:"isDebug"`
	IsProduction                  bool      `json:"isProduction"`
	IsAdmin                       bool      `json:"isAdmin"`
	IsUserInteractive             bool      `json:"isUserInteractive"`
	StartupPath                   string    `json:"startupPath"`
	AppData                       string    `json:"appData"`
	OsName                        string    `json:"osName"`
	OsVersion                     string    `json:"osVersion"`
	IsNetCore                     bool      `json:"isNetCore"`
	IsLinux                       bool      `json:"isLinux"`
	IsOsx                         bool      `json:"isOsx"`
	IsWindows                     bool      `json:"isWindows"`
	IsDocker                      bool      `json:"isDocker"`
	Mode                          string    `json:"mode"`
	Branch                        string    `json:"branch"`
	Authentication                string    `json:"authentication"`
	DatabaseVersion               string    `json:"databaseVersion"`
	DatabaseType                  string    `json:"databaseType"`
	MigrationVersion              int       `json:"migrationVersion"`
	URLBase                       string    `json:"urlBase"`
	RuntimeVersion                string    `json:"runtimeVersion"`
	RuntimeName                   string    `json:"runtimeName"`
	StartTime                     time.Time `json:"startTime"`
	PackageVersion                string    `json:"packageVersion"`
	PackageAuthor                 string    `json:"packageAuthor"`
	PackageUpdateMechanism        string    `json:"packageUpdateMechanism"`
	PackageUpdateMechanismMessage string    `json:"packageUpdateMechanismMessage"`
}

// StatusHandler returns GET /api/v5/system/status. Ports the v3
// handler logic with the v5 Resource shape.
func StatusHandler(static StatusContext, getter StatusGetter) http.Handler {
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
		v3 := static.V3
		res := Resource{
			AppName:                       "Sonarr",
			InstanceName:                  instance,
			Version:                       v3.Version,
			BuildTime:                     v3.BuildTime,
			IsDebug:                       v3.IsDebug,
			IsProduction:                  v3.IsProduction,
			IsAdmin:                       v3.IsAdmin,
			IsUserInteractive:             v3.IsUserInteractive,
			StartupPath:                   v3.StartupPath,
			AppData:                       v3.AppData,
			OsName:                        v3.OsName,
			OsVersion:                     v3.OsVersion,
			IsNetCore:                     true,
			IsLinux:                       v3.IsLinux,
			IsOsx:                         v3.IsOsx,
			IsWindows:                     v3.IsWindows,
			IsDocker:                      v3.IsDocker,
			Mode:                          v3.Mode,
			Branch:                        branch,
			Authentication:                apiv3.CamelCaseEnumName(auth),
			DatabaseVersion:               v3.DatabaseVersion,
			DatabaseType:                  v3.DatabaseType,
			MigrationVersion:              v3.MigrationVersion,
			URLBase:                       urlBase,
			RuntimeVersion:                v3.RuntimeVersion,
			RuntimeName:                   v3.RuntimeName,
			StartTime:                     v3.StartTime,
			PackageVersion:                static.PackageVersion,
			PackageAuthor:                 static.PackageAuthor,
			PackageUpdateMechanism:        apiv3.CamelCaseEnumName(updateMech),
			PackageUpdateMechanismMessage: static.PackageUpdateMechanismMessage,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// LifecycleRunner is re-exported from v3 for wiring symmetry.
type LifecycleRunner = apiv3.SystemLifecycleRunner

// ShutdownHandler aliases the v3 handler verbatim — wire shape
// (200 + `{"shuttingDown":true}`) is byte-identical.
var ShutdownHandler = apiv3.SystemShutdownHandler

// RestartHandler aliases the v3 handler verbatim — wire shape
// (200 + `{"restarting":true}`) is byte-identical.
var RestartHandler = apiv3.SystemRestartHandler

// RoutesDuplicateHandler returns GET /api/v5/system/routes/duplicate.
//
// v5 C# returns Dictionary<string, List<string>> which serializes
// to `{}` when empty. v3 Go-port emits `[]` per a different design
// call — v5 emits `{}` to match the v5 openapi schema.
func RoutesDuplicateHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// RoutesHandler returns GET /api/v5/system/routes. Stub for the
// DFA-graph dumper — neither v3 nor v5 Go-port has a DfaGraphWriter
// equivalent. Emits empty text/plain to pin the wire surface; J266
// pattern (close the v5 gap, leave v3 untouched).
func RoutesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	})
}
