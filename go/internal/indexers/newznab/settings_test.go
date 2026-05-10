package newznab

import (
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/provider"
)

// Mirrors src/NzbDrone.Core.Test/IndexerTests/NewznabTests/NewznabSettingFixture.cs.

func TestSettings_RequiresAPIKey_AllowList(t *testing.T) {
	cases := []string{
		"http://nzbs.org",
		"http:///www.nzbplanet.net",
	}
	for _, url := range cases {
		s := NewSettings()
		s.BaseUrl = url
		s.ApiKey = ""
		r := s.Validate()
		if r.IsValid() {
			t.Errorf("%q: expected invalid, got valid", url)
		}
		if !hasFailure(r, "ApiKey") {
			t.Errorf("%q: expected ApiKey failure, got %v", url, r.String())
		}
	}
}

func TestSettings_InvalidURL_DoesNotEmitAPIKeyFailure(t *testing.T) {
	cases := []string{"", "  "}
	for _, url := range cases {
		s := NewSettings()
		s.BaseUrl = url
		s.ApiKey = ""
		r := s.Validate()
		if r.IsValid() {
			t.Errorf("%q: expected invalid", url)
		}
		if hasFailure(r, "ApiKey") {
			t.Errorf("%q: should NOT emit ApiKey failure for invalid URL: %v", url, r)
		}
		if !hasFailure(r, "BaseUrl") {
			t.Errorf("%q: expected BaseUrl failure", url)
		}
	}
}

func TestSettings_NonAllowListDoesNotRequireAPIKey(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.ApiKey = ""
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("expected valid (non-allowlist), got %v", r)
	}
}

func TestSettings_RequiresCategoriesOrAnimeCategories(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.Categories = []int{}
	s.AnimeCategories = []int{}
	r := s.Validate()
	if r.IsValid() {
		t.Error("expected invalid when both Categories and AnimeCategories empty")
	}
	if !strings.Contains(r.String(), "Categories") {
		t.Errorf("expected Categories-mentioning failure, got %v", r)
	}
}

func TestSettings_AnimeCategoriesAloneIsValid(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.Categories = []int{}
	s.AnimeCategories = []int{5070}
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("expected valid with only AnimeCategories, got %v", r)
	}
}

func TestSettings_ApiPath_RejectsAbsoluteURL(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.ApiPath = "https://other.example/api"
	r := s.Validate()
	if !hasFailure(r, "ApiPath") {
		t.Errorf("expected ApiPath failure for absolute URL, got %v", r)
	}
}

func TestSettings_ApiPath_AcceptsRelativePath(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.ApiPath = "/api"
	r := s.Validate()
	if hasFailure(r, "ApiPath") {
		t.Errorf("expected /api ApiPath valid, got %v", r)
	}
}

func TestSettings_AdditionalParameters_AcceptsValid(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.AdditionalParameters = "&foo=bar&baz=qux"
	r := s.Validate()
	if hasFailure(r, "AdditionalParameters") {
		t.Errorf("expected AP valid, got %v", r)
	}
}

func TestSettings_AdditionalParameters_RejectsMalformed(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.AdditionalParameters = "not valid"
	r := s.Validate()
	if !hasFailure(r, "AdditionalParameters") {
		t.Errorf("expected AP failure for malformed, got %v", r)
	}
}

func TestSettings_AdditionalParameters_EmptyOK(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://nzbs2.org"
	s.AdditionalParameters = "  "
	r := s.Validate()
	if hasFailure(r, "AdditionalParameters") {
		t.Errorf("whitespace AP must not fail validation: %v", r)
	}
}

func TestSettings_DefaultsMatchCSharpConstructor(t *testing.T) {
	s := NewSettings()
	if s.ApiPath != "/api" {
		t.Errorf("ApiPath = %q want /api", s.ApiPath)
	}
	if len(s.Categories) != 2 || s.Categories[0] != 5030 || s.Categories[1] != 5040 {
		t.Errorf("Categories = %v want [5030 5040]", s.Categories)
	}
	if len(s.AnimeCategories) != 0 {
		t.Errorf("AnimeCategories not empty: %v", s.AnimeCategories)
	}
}

func hasFailure(r provider.ValidationResult, prop string) bool {
	for _, f := range r.Errors {
		if f.PropertyName == prop {
			return true
		}
	}
	return false
}
