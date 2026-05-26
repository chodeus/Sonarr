package localization

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/language"
)

// fakeStringsLister implements StringsLister.
type fakeStringsLister struct {
	dict map[string]string
}

func (f *fakeStringsLister) GetLocalizationDictionary() map[string]string {
	return f.dict
}

// ----- list/dict path-alias smoke (regression guard, not behaviour) -----

func TestLanguageHandler_PathAlias_Returns200(t *testing.T) {
	t.Parallel()
	langs := []language.Language{language.English, language.French}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/language", nil)
	LanguageHandler(langs).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	// Both rows present + nameLower computed by ListAll.
	body := rr.Body.String()
	if !strings.Contains(body, `"name":"English"`) || !strings.Contains(body, `"nameLower":"french"`) {
		t.Errorf("body should contain both languages with nameLower; got %q", body)
	}
}

func TestLocalizationHandler_PathAlias_Returns200(t *testing.T) {
	t.Parallel()
	lister := &fakeStringsLister{dict: map[string]string{"hello": "Hi"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/localization", nil)
	LocalizationHandler(lister).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"strings":{"hello":"Hi"}`) {
		t.Errorf("body should wrap dict under `strings`; got %q", rr.Body.String())
	}
}

func TestLocalizationLanguageHandler_PathAlias_Returns200(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/localization/language", nil)
	LocalizationLanguageHandler(func() int { return 1 }).ServeHTTP(rr, req) // English
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"identifier":"en"`) {
		t.Errorf("body should contain BCP-47 identifier for English; got %q", rr.Body.String())
	}
}

// ----- LanguageByID (new v5-specific) -----

func TestLanguageByIDHandler_Found_Returns200(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/language/1", nil)
	req.SetPathValue("id", "1")
	LanguageByIDHandler(language.All()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	var got LanguageResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != 1 || got.Name != "English" || got.NameLower != "english" {
		t.Errorf("unexpected resource: got %+v", got)
	}
}

func TestLanguageByIDHandler_Unknown_Returns404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/language/9999", nil)
	req.SetPathValue("id", "9999")
	LanguageByIDHandler(language.All()).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404; body=%q", rr.Code, rr.Body.String())
	}
}

func TestLanguageByIDHandler_Zero_ReturnsUnknown(t *testing.T) {
	t.Parallel()
	// C# parity: `(Language)0` returns `Language.Unknown` (per
	// Language.cs:196-199); v3 Go-port FindByID matches.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/language/0", nil)
	req.SetPathValue("id", "0")
	LanguageByIDHandler(language.All()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"name":"Unknown"`) {
		t.Errorf("body should be Language.Unknown; got %q", rr.Body.String())
	}
}

func TestLanguageByIDHandler_InvalidIDFormat_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/language/abc", nil)
	req.SetPathValue("id", "abc")
	LanguageByIDHandler(language.All()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- LocalizationByID (new v5-specific) -----

func TestLocalizationByIDHandler_AnyID_ReturnsDict(t *testing.T) {
	t.Parallel()
	// C# parity: `LocalizationController.GetResourceById(int id)` at
	// `:23-26` IGNORES the id and returns the same dict as the list
	// endpoint. Verify by calling with two different ids.
	lister := &fakeStringsLister{dict: map[string]string{"k": "v"}}
	for _, id := range []string{"1", "999"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v5/localization/"+id, nil)
		req.SetPathValue("id", id)
		LocalizationByIDHandler(lister).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("id=%s status: got %d want 200", id, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), `"strings":{"k":"v"}`) {
			t.Errorf("id=%s body should be the dict; got %q", id, rr.Body.String())
		}
	}
}

func TestLocalizationByIDHandler_InvalidID_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/localization/abc", nil)
	req.SetPathValue("id", "abc")
	LocalizationByIDHandler(&fakeStringsLister{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- alias guards -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ LanguageResource = apiv3.LanguageResource{}
	var _ LocalizationResource = apiv3.LocalizationResource{}
	var _ LocalizationLanguageResource = apiv3.LocalizationLanguageResource{}
	var _ apiv3.LanguageResource = LanguageResource{}
}

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if LanguageHandler == nil || LocalizationHandler == nil || LocalizationLanguageHandler == nil {
		t.Error("path-alias handler vars must be non-nil")
	}
}
