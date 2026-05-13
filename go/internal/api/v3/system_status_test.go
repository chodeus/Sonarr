package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeStatusGetter struct {
	auth, branch, instance, urlBase, updateMech string
	err                                         error
}

func (f *fakeStatusGetter) AuthenticationMethod(context.Context) (string, error) {
	return f.auth, f.err
}
func (f *fakeStatusGetter) Branch(context.Context) (string, error)       { return f.branch, f.err }
func (f *fakeStatusGetter) InstanceName(context.Context) (string, error) { return f.instance, f.err }
func (f *fakeStatusGetter) URLBase(context.Context) (string, error)      { return f.urlBase, f.err }
func (f *fakeStatusGetter) UpdateMechanism(context.Context) (string, error) {
	return f.updateMech, f.err
}

func TestSystemStatusHandler_RecordedShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	static := SystemStatusContext{
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
		IsLinux:           false,
		IsOsx:             true,
		IsWindows:         false,
		IsDocker:          false,
		IsContainerized:   false,
		Mode:              "console",
	}
	SystemStatusHandler(static, &fakeStatusGetter{
		auth:       "None",
		branch:     "main",
		instance:   "Sonarr",
		urlBase:    "",
		updateMech: "BuiltIn",
	}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/system/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"appName":"Sonarr"`,
		`"authentication":"none"`,
		`"packageUpdateMechanism":"builtIn"`,
		`"branch":"main"`,
		`"instanceName":"Sonarr"`,
		`"isNetCore":true`,
		`"isOsx":true`,
		`"isLinux":false`,
		`"isWindows":false`,
		`"isDebug":true`,
		`"isProduction":false`,
		`"migrationVersion":230`,
		`"mode":"console"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
	var got SystemStatusResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.IsNetCore {
		t.Error("isNetCore should be true (Go port emits true to match modern .NET wire shape)")
	}
}

func TestSystemStatusHandler_GetterError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SystemStatusHandler(SystemStatusContext{}, &fakeStatusGetter{err: errors.New("boom")}).ServeHTTP(
		rr, httptest.NewRequest(http.MethodGet, "/api/v3/system/status", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
