package updatesettings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/updatesettings"
)

type fakeProv struct {
	mu       sync.Mutex
	branch   string
	auto     bool
	mech     string
	script   string
	setCalls []string
	failNext string // simulate provider error from this field name
}

func newProv() *fakeProv { return &fakeProv{branch: "main", mech: "BuiltIn"} }

func (f *fakeProv) Branch(context.Context) (string, error) {
	if f.failNext == "Branch" {
		return "", errors.New("boom")
	}
	return f.branch, nil
}
func (f *fakeProv) SetBranch(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "Branch="+v)
	f.branch = v
	return nil
}
func (f *fakeProv) UpdateAutomatically(context.Context) (bool, error) { return f.auto, nil }
func (f *fakeProv) SetUpdateAutomatically(_ context.Context, v bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "UpdateAutomatically")
	f.auto = v
	return nil
}
func (f *fakeProv) UpdateMechanism(context.Context) (string, error) { return f.mech, nil }
func (f *fakeProv) SetUpdateMechanism(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "UpdateMechanism="+v)
	f.mech = v
	return nil
}
func (f *fakeProv) UpdateScriptPath(context.Context) (string, error) { return f.script, nil }
func (f *fakeProv) SetUpdateScriptPath(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "UpdateScriptPath="+v)
	f.script = v
	return nil
}

func TestGetHandler_DefaultsID1(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	updatesettings.GetHandler(newProv()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/settings/update", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got updatesettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 1 {
		t.Errorf("ID: got %d want 1", got.ID)
	}
	if got.Branch != "main" {
		t.Errorf("branch: %q want main", got.Branch)
	}
	if got.UpdateMechanism != "builtIn" {
		t.Errorf("updateMechanism: %q want builtIn (camelCase enum)", got.UpdateMechanism)
	}
}

func TestGetHandler_ByID_HonorsPathParam(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/settings/update/7", nil)
	req.SetPathValue("id", "7")
	updatesettings.GetHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got updatesettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("ID: got %d want 7", got.ID)
	}
}

func TestUpdateHandler_Accepted_Persists(t *testing.T) {
	t.Parallel()
	p := newProv()
	body := mustEncode(t, updatesettings.Resource{Branch: "develop", UpdateMechanism: "script", UpdateScriptPath: "/usr/local/bin/sonarr-update.sh", UpdateAutomatically: true})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/update/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	updatesettings.UpdateHandler(p).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rr.Code, rr.Body.String())
	}
	var got updatesettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Branch != "develop" || got.UpdateMechanism != "script" || got.UpdateScriptPath != "/usr/local/bin/sonarr-update.sh" || !got.UpdateAutomatically {
		t.Errorf("got %+v", got)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Persisted UpdateMechanism uses PascalCase ("Script") at the store.
	if p.mech != "Script" {
		t.Errorf("persisted mechanism: %q want Script (PascalCase)", p.mech)
	}
}

func TestUpdateHandler_ScriptMechanismRequiresAbsolutePath(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, updatesettings.Resource{Branch: "main", UpdateMechanism: "script", UpdateScriptPath: "relative/path.sh"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/update/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	updatesettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (script mechanism + relative path)", rr.Code)
	}
}

func TestUpdateHandler_ScriptMechanismRequiresNonEmptyPath(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, updatesettings.Resource{Branch: "main", UpdateMechanism: "script", UpdateScriptPath: ""})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/update/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	updatesettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (script mechanism + empty path)", rr.Code)
	}
}

func TestUpdateHandler_InvalidMechanism(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, updatesettings.Resource{Branch: "main", UpdateMechanism: "ZZZ"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/update/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	updatesettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "UpdateMechanism") {
		t.Errorf("body should mention UpdateMechanism: %s", rr.Body.String())
	}
}

func TestUpdateHandler_NonScriptMechanism_PathNotValidated(t *testing.T) {
	t.Parallel()
	// builtIn with empty path → 202, no validation fires.
	body := mustEncode(t, updatesettings.Resource{Branch: "main", UpdateMechanism: "builtIn", UpdateScriptPath: ""})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/update/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	updatesettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202", rr.Code)
	}
}

func TestUpdateHandler_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	body := []byte(`{"branch":"main","updateMechanism":"builtIn","wat":"unknown"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/update/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	updatesettings.UpdateHandler(newProv()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (DisallowUnknownFields)", rr.Code)
	}
}

func mustEncode(t *testing.T, r updatesettings.Resource) []byte {
	t.Helper()
	buf, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}
