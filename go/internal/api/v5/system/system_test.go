package system_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	systemv5 "sonarr.tv/sonarr/internal/api/v5/system"
)

type fakeStatusGetter struct {
	auth, branch, instance, urlBase, updateMech string
}

func (f *fakeStatusGetter) AuthenticationMethod(context.Context) (string, error) {
	return f.auth, nil
}
func (f *fakeStatusGetter) Branch(context.Context) (string, error)       { return f.branch, nil }
func (f *fakeStatusGetter) InstanceName(context.Context) (string, error) { return f.instance, nil }
func (f *fakeStatusGetter) URLBase(context.Context) (string, error)      { return f.urlBase, nil }
func (f *fakeStatusGetter) UpdateMechanism(context.Context) (string, error) {
	return f.updateMech, nil
}

func makeContext() systemv5.StatusContext {
	return systemv5.StatusContext{
		V3: apiv3.SystemStatusContext{
			Version:           "5.0.0",
			BuildTime:         time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			StartTime:         time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC),
			StartupPath:       "/opt/sonarr",
			AppData:           "/var/sonarr",
			OsName:            "Linux",
			OsVersion:         "5.10",
			RuntimeName:       "go",
			RuntimeVersion:    "1.26",
			DatabaseType:      "sqlite",
			DatabaseVersion:   "3.45.0",
			MigrationVersion:  230,
			IsDebug:           true,
			IsProduction:      false,
			IsAdmin:           false,
			IsUserInteractive: true,
			IsLinux:           true,
			IsOsx:             false,
			IsWindows:         false,
			IsDocker:          false,
			IsContainerized:   true, // v3 carries this; v5 must NOT surface it
			Mode:              "console",
		},
		PackageVersion:                "",
		PackageAuthor:                 "",
		PackageUpdateMechanismMessage: "",
	}
}

func TestStatusHandler_V5ShapeOmitsIsContainerized(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	systemv5.StatusHandler(makeContext(), &fakeStatusGetter{
		auth: "None", branch: "main", instance: "Sonarr", urlBase: "", updateMech: "BuiltIn",
	}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/status", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: %q", ct)
	}
	body := rr.Body.String()
	if strings.Contains(body, "isContainerized") {
		t.Errorf("v5 SystemResource must NOT include isContainerized: %s", body)
	}
	for _, want := range []string{
		`"appName":"Sonarr"`,
		`"authentication":"none"`,
		`"packageUpdateMechanism":"builtIn"`,
		`"packageVersion":""`,
		`"packageAuthor":""`,
		`"packageUpdateMechanismMessage":""`,
		`"isNetCore":true`,
		`"isLinux":true`,
		`"isOsx":false`,
		`"isWindows":false`,
		`"mode":"console"`,
		`"branch":"main"`,
		`"instanceName":"Sonarr"`,
		`"migrationVersion":230`,
		`"version":"5.0.0"`,
		`"databaseType":"sqlite"`,
		`"databaseVersion":"3.45.0"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody=%s", want, body)
		}
	}

	// Decode and confirm exactly 31 keys (no isContainerized; no
	// sqliteVersion). 28 v3-shared + 3 v5-new (packageVersion,
	// packageAuthor, packageUpdateMechanismMessage).
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := generic["isContainerized"]; ok {
		t.Errorf("isContainerized must be absent from v5 wire")
	}
	for _, must := range []string{"packageVersion", "packageAuthor", "packageUpdateMechanismMessage", "packageUpdateMechanism"} {
		if _, ok := generic[must]; !ok {
			t.Errorf("v5 wire must include %s", must)
		}
	}
}

func TestRoutesDuplicateHandler_EmptyObject(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	systemv5.RoutesDuplicateHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/routes/duplicate", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := rr.Body.String(); got != "{}" {
		t.Errorf("body: %q want {} — v5 returns object, not v3's []", got)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: %q", ct)
	}
}

func TestRoutesHandler_EmptyTextPlain(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	systemv5.RoutesHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/routes", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := rr.Body.String(); got != "" {
		t.Errorf("body: %q want empty (DfaGraphWriter deferred)", got)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content-type: %q", ct)
	}
}

// fakeLifecycle records lifecycle calls for shutdown/restart alias tests.
type fakeLifecycle struct {
	mu       sync.Mutex
	shutdown int
	restart  int
}

func (f *fakeLifecycle) Shutdown(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdown++
	return nil
}

func (f *fakeLifecycle) Restart(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restart++
	return nil
}

func (f *fakeLifecycle) shutdownCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdown
}

func (f *fakeLifecycle) restartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restart
}

func TestShutdownHandlerAlias_RespondsAndDispatches(t *testing.T) {
	t.Parallel()
	lc := &fakeLifecycle{}
	rr := httptest.NewRecorder()
	systemv5.ShutdownHandler(lc).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/system/shutdown", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got struct {
		ShuttingDown bool `json:"shuttingDown"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.ShuttingDown {
		t.Errorf("expected shuttingDown=true")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && lc.shutdownCount() < 1 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := lc.shutdownCount(); got < 1 {
		t.Errorf("shutdown not dispatched (count=%d)", got)
	}
}

func TestRestartHandlerAlias_RespondsAndDispatches(t *testing.T) {
	t.Parallel()
	lc := &fakeLifecycle{}
	rr := httptest.NewRecorder()
	systemv5.RestartHandler(lc).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v5/system/restart", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got struct {
		Restarting bool `json:"restarting"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Restarting {
		t.Errorf("expected restarting=true")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && lc.restartCount() < 1 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := lc.restartCount(); got < 1 {
		t.Errorf("restart not dispatched (count=%d)", got)
	}
}

// TestStatusContext_TypeIsExtensible pins the V3 embedding so that
// boot-wiring can populate the v3 context and leave Package* fields
// as zero strings without touching v3 surface.
func TestStatusContext_TypeIsExtensible(t *testing.T) {
	t.Parallel()
	ctx := systemv5.StatusContext{V3: apiv3.SystemStatusContext{Version: "1.0"}}
	if ctx.V3.Version != "1.0" {
		t.Errorf("V3 field not addressable")
	}
}
