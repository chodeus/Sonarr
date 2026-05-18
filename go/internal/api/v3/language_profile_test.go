package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/language"
)

func TestLanguageProfileHandler_ReturnsDeprecatedSingleton(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	LanguageProfileHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/languageprofile", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []LanguageProfileResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len: %d", len(got))
	}
	if got[0].Name != "Deprecated" || got[0].ID != 1 || !got[0].UpgradeAllowed {
		t.Errorf("hardcoded shape: %+v", got[0])
	}
	if got[0].Cutoff != language.English {
		t.Errorf("Cutoff: %+v", got[0].Cutoff)
	}
	if len(got[0].Languages) != 1 || !got[0].Languages[0].Allowed {
		t.Errorf("Languages: %+v", got[0].Languages)
	}
}

func TestLanguageProfileByIDHandler_AnyIDReturnsSingleton(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/languageprofile/42", nil)
	req.SetPathValue("id", "42")
	LanguageProfileByIDHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got LanguageProfileResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Name != "Deprecated" {
		t.Errorf("name: %q", got.Name)
	}
}

func TestLanguageProfileByIDHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/languageprofile/abc", nil)
	req.SetPathValue("id", "abc")
	LanguageProfileByIDHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestLanguageProfileSchemaHandler_ReturnsSingleton(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	LanguageProfileSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/languageprofile/schema", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got LanguageProfileResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Name != "Deprecated" {
		t.Errorf("name: %q", got.Name)
	}
}

func TestLanguageProfileWriteAcceptedHandler_PUTEchoesBody(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"id":5,"name":"Custom","upgradeAllowed":false,"languages":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/languageprofile/5", strings.NewReader(body))
	LanguageProfileWriteAcceptedHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"name":"Custom"`) {
		t.Errorf("body should echo input: %s", rr.Body.String())
	}
}

func TestLanguageProfileWriteAcceptedHandler_DELETEReturnsEmptyObject(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/languageprofile/5", nil)
	LanguageProfileWriteAcceptedHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
}
