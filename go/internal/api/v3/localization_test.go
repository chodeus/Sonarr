package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/language"
)

func TestLanguageIdentifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   int
		want string
	}{
		{"english_default", 1, "en"},
		{"portuguese_brazil_with_country", language.PortugueseBrazil.ID, "pt-BR"},
		{"french_with_country", language.French.ID, "fr-FR"},
		{"chinese_with_country", language.Chinese.ID, "zh-CN"},
		{"unknown_falls_back_to_english", 0, "en"},
		{"out_of_range_falls_back_to_english", 9999, "en"},
		{"negative_falls_back_to_english", -42, "en"},
		// Languages with a registered Iso but no country code
		// produce a bare two-letter identifier.
		{"spanish_no_country", language.Spanish.ID, "es"},
		{"japanese_no_country", language.Japanese.ID, "ja"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LanguageIdentifier(tc.id); got != tc.want {
				t.Errorf("LanguageIdentifier(%d) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestLocalizationLanguageHandler_Body(t *testing.T) {
	t.Parallel()
	handler := LocalizationLanguageHandler(func() int { return 33 }) // PortugueseBrazil
	req := httptest.NewRequest(http.MethodGet, "/api/v3/localization/language", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got LocalizationLanguageResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Identifier != "pt-BR" {
		t.Errorf("identifier: got %q want pt-BR", got.Identifier)
	}
}

func TestLocalizationLanguageHandler_ReadsOnEveryRequest(t *testing.T) {
	t.Parallel()
	// Mutable counter — proves the handler re-reads uiLanguage()
	// rather than capturing the value at handler-build time.
	current := 1
	handler := LocalizationLanguageHandler(func() int { return current })

	hit := func() string {
		req := httptest.NewRequest(http.MethodGet, "/api/v3/localization/language", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		var got LocalizationLanguageResource
		_ = json.Unmarshal(rr.Body.Bytes(), &got)
		return got.Identifier
	}
	if got := hit(); got != "en" {
		t.Errorf("first hit: got %q want en", got)
	}
	current = language.French.ID
	if got := hit(); got != "fr-FR" {
		t.Errorf("after change to French: got %q want fr-FR", got)
	}
}
