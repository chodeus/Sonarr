package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAutoTaggingSchema_PinnedCount(t *testing.T) {
	t.Parallel()
	if got := len(autoTaggingSchemas); got != 11 {
		t.Errorf("autoTaggingSchemas count: got %d want 11", got)
	}
}

func TestAutoTaggingSchema_NoInfoLinkOrPresets(t *testing.T) {
	t.Parallel()
	// AutoTagSpecificationBase has neither InfoLink nor Presets, so
	// the schema struct must not carry those fields. Round-tripping
	// the JSON output confirms neither key emits.
	rr := httptest.NewRecorder()
	AutoTaggingSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/autotagging/schema", nil))
	body := rr.Body.String()
	if strings.Contains(body, `"infoLink"`) {
		t.Errorf("autotagging schema must omit infoLink: %s", body[:200])
	}
	if strings.Contains(body, `"presets"`) {
		t.Errorf("autotagging schema must omit presets: %s", body[:200])
	}
}

func TestAutoTaggingSchema_MonitoredEmitsEmptyFields(t *testing.T) {
	t.Parallel()
	// MonitoredSpecification has no fields — the wire shape must
	// emit `"fields":[]` (non-null empty), not omit the key.
	var got []AutoTaggingSpecificationSchemaResource
	rr := httptest.NewRecorder()
	AutoTaggingSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/autotagging/schema", nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, s := range got {
		if s.Implementation == "MonitoredSpecification" {
			if s.Fields == nil || len(s.Fields) != 0 {
				t.Errorf("Monitored fields: got %+v, want empty non-nil", s.Fields)
			}
			return
		}
	}
	t.Errorf("MonitoredSpecification missing from schema list")
}

func TestAutoTaggingSchema_OriginalLanguageExcludesOriginal(t *testing.T) {
	t.Parallel()
	// OriginalLanguageSpec must NOT carry the synthetic "Original"
	// (id=-2) language — that value only makes sense as a series-
	// audio sentinel in QualityProfile, not as a source-language
	// classifier.
	for _, s := range autoTaggingSchemas {
		if s.Implementation != "OriginalLanguageSpecification" {
			continue
		}
		if len(s.Fields) != 1 {
			t.Fatalf("OriginalLanguage should have 1 field; got %d", len(s.Fields))
		}
		for _, opt := range s.Fields[0].SelectOptions {
			if opt.Value == -2 {
				t.Errorf("OriginalLanguage must exclude id=-2 (Original): %+v", opt)
			}
		}
		return
	}
	t.Errorf("OriginalLanguageSpecification missing from schema list")
}
