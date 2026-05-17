package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

func TestImportListSchema_PinnedCount(t *testing.T) {
	t.Parallel()
	if got := len(importListSchemas); got != 10 {
		t.Errorf("importListSchemas count: got %d want 10", got)
	}
}

func TestImportListSchema_OrderedByImplementationName(t *testing.T) {
	t.Parallel()
	// ProviderControllerBase.GetTemplates emits providers
	// .OrderBy(d => d.ImplementationName); confirm we agree.
	names := make([]string, len(importListSchemas))
	for i, s := range importListSchemas {
		names[i] = s.ImplementationName
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("schema order not sorted by implementationName: %v", names)
	}
}

func TestImportListSchema_SchemaOmitsNameAndID(t *testing.T) {
	t.Parallel()
	// ProviderResource emits Name/Id as null on schema templates;
	// System.Text.Json WhenWritingNull omits both. Our struct has no
	// Name/ID fields at all so the top-level row body must lack those
	// keys (field-level `name` keys inside fields[] are expected).
	rr := httptest.NewRecorder()
	ImportListSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlist/schema", nil))
	var rows []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, r := range rows {
		if _, ok := r["name"]; ok {
			t.Errorf("%v: top-level name key must be absent", r["implementation"])
		}
		if _, ok := r["id"]; ok {
			t.Errorf("%v: top-level id key must be absent", r["implementation"])
		}
	}
}

func TestImportListSchema_PresetsEmitsEmptyArray(t *testing.T) {
	t.Parallel()
	// Presets is *[]any with omitempty — schema rows always set it to
	// &[]any{} so the key emits as `[]`. List-row callers nil the
	// pointer to drop the key entirely.
	rr := httptest.NewRecorder()
	ImportListSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlist/schema", nil))
	var got []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, s := range got {
		p, ok := s["presets"]
		if !ok {
			t.Errorf("%v: presets key missing on schema row", s["implementation"])
			continue
		}
		arr, isArr := p.([]any)
		if !isArr || len(arr) != 0 {
			t.Errorf("%v: presets want empty array, got %v", s["implementation"], p)
		}
	}
}

func TestImportListSchema_AniListHasAllStatusCheckboxes(t *testing.T) {
	t.Parallel()
	// AniList has the largest field set (16 fields, 11 of which are
	// the "Import List Status" sectioned checkboxes). Smoke-tests the
	// shape: section name + count + ordering.
	var sch ImportListSchemaResource
	for _, s := range importListSchemas {
		if s.Implementation == "AniListImport" {
			sch = s
			break
		}
	}
	if sch.Implementation == "" {
		t.Fatalf("AniListImport missing from schema list")
	}
	if len(sch.Fields) != 16 {
		t.Errorf("AniList fields: got %d want 16", len(sch.Fields))
	}
	sectioned := 0
	for _, f := range sch.Fields {
		if f.Section == importListImportStatusSection {
			sectioned++
		}
	}
	if sectioned != 11 {
		t.Errorf("AniList sectioned-checkbox count: got %d want 11", sectioned)
	}
}

func TestImportListSchema_SonarrUsesSelectOptionsProviders(t *testing.T) {
	t.Parallel()
	// SonarrImport's profileIds/tagIds/rootFolderPaths/languageProfileIds
	// fields lazy-load via selectOptionsProviderAction — verifies the
	// JSON key emits AND that value=[] (the C# `List<int>` default ctor
	// renders as `[]`, not null).
	var sch ImportListSchemaResource
	for _, s := range importListSchemas {
		if s.Implementation == "SonarrImport" {
			sch = s
			break
		}
	}
	if sch.Implementation == "" {
		t.Fatalf("SonarrImport missing")
	}
	wantActions := map[string]string{
		"profileIds":         "getProfiles",
		"tagIds":             "getTags",
		"rootFolderPaths":    "getRootFolders",
		"languageProfileIds": "getLanguageProfiles",
	}
	for _, f := range sch.Fields {
		want, ok := wantActions[f.Name]
		if !ok {
			continue
		}
		if f.SelectOptionsProviderAction != want {
			t.Errorf("Sonarr.%s.selectOptionsProviderAction: got %q want %q", f.Name, f.SelectOptionsProviderAction, want)
		}
		if !f.ValueSet {
			t.Errorf("Sonarr.%s must emit value=[] (ValueSet=true)", f.Name)
		}
		delete(wantActions, f.Name)
	}
	if len(wantActions) != 0 {
		t.Errorf("Sonarr schema missing fields: %v", wantActions)
	}
}

func TestImportListSchema_MyAnimeListSignInOmitsValue(t *testing.T) {
	t.Parallel()
	// MyAnimeList's signIn oAuth field has no `value` in the golden —
	// it differs from every other provider's signIn (which carries
	// value="startOAuth"). Catches accidental copy-paste of WithValue.
	var sch ImportListSchemaResource
	for _, s := range importListSchemas {
		if s.Implementation == "MyAnimeListImport" {
			sch = s
			break
		}
	}
	if sch.Implementation == "" {
		t.Fatalf("MyAnimeListImport missing")
	}
	for _, f := range sch.Fields {
		if f.Name != "signIn" {
			continue
		}
		if f.ValueSet {
			t.Errorf("MyAnimeList.signIn must NOT emit value (golden has no value key); got Value=%v", f.Value)
		}
		return
	}
	t.Errorf("MyAnimeList.signIn missing from schema")
}

func TestImportListSchema_ContentTypeAndStatus(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ImportListSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/importlist/schema", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
}
