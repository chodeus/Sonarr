package importlistsettings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ----- fake config provider (satisfies both Getter and Writer) -----

type fakeCfg struct {
	syncLevel string
	syncTag   int

	setLevelCalled string
	setTagCalled   int
}

func (f *fakeCfg) ListSyncLevel() string { return f.syncLevel }
func (f *fakeCfg) ListSyncTag() int      { return f.syncTag }
func (f *fakeCfg) SetListSyncLevel(_ context.Context, v string) error {
	f.setLevelCalled = v
	f.syncLevel = v
	return nil
}
func (f *fakeCfg) SetListSyncTag(_ context.Context, v int) error {
	f.setTagCalled = v
	f.syncTag = v
	return nil
}

// ----- Re-exported handlers smoke -----

func TestReexportedHandlers_NonNil(t *testing.T) {
	t.Parallel()
	cfg := &fakeCfg{syncLevel: "Disabled", syncTag: 0}
	if GetHandler(cfg) == nil {
		t.Errorf("GetHandler was nil — v3 re-export broken")
	}
	if UpdateHandler(cfg) == nil {
		t.Errorf("UpdateHandler was nil — v3 re-export broken")
	}
}

// ----- GET integration (pins same wire shape as v3) -----

func TestGet_ReturnsConfigJSON(t *testing.T) {
	t.Parallel()
	cfg := &fakeCfg{syncLevel: "KeepAndTag", syncTag: 7}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/settings/importlist", nil)
	GetHandler(cfg).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`,
		`"listSyncLevel":"keepAndTag"`, // CamelCaseEnumName lowercases the leading char.
		`"listSyncTag":7`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

// ----- PUT integration (pins same wire shape + 202 Accepted) -----

func TestUpdate_Accepted202(t *testing.T) {
	t.Parallel()
	cfg := &fakeCfg{syncLevel: "Disabled", syncTag: 0}
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"id":1,"listSyncLevel":"keepAndTag","listSyncTag":5}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/importlist/1", body)
	req.SetPathValue("id", "1")
	UpdateHandler(cfg).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rr.Code, rr.Body.String())
	}
	if cfg.setLevelCalled != "KeepAndTag" {
		t.Errorf("SetListSyncLevel got %q want KeepAndTag", cfg.setLevelCalled)
	}
	if cfg.setTagCalled != 5 {
		t.Errorf("SetListSyncTag got %d want 5", cfg.setTagCalled)
	}
	respBody := rr.Body.String()
	for _, want := range []string{
		`"id":1`,
		`"listSyncLevel":"keepAndTag"`,
		`"listSyncTag":5`,
	} {
		if !strings.Contains(respBody, want) {
			t.Errorf("body missing %s: %s", want, respBody)
		}
	}
}

func TestUpdate_KeepAndTag_RejectsZeroTag(t *testing.T) {
	t.Parallel()
	// C# validator at ImportListSettingsController.cs:16-19 rejects
	// ListSyncTag <= 0 when ListSyncLevel == KeepAndTag with the
	// verbatim message "Tag must be specified". The v3 handler
	// preserves this; v5 inherits it via the re-export.
	cfg := &fakeCfg{}
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"id":1,"listSyncLevel":"keepAndTag","listSyncTag":0}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/importlist/1", body)
	req.SetPathValue("id", "1")
	UpdateHandler(cfg).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Tag must be specified") {
		t.Errorf("body should fault ListSyncTag: %s", rr.Body.String())
	}
}
