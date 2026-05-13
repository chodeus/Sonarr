package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
)

func TestQualitySourceName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   quality.QualitySource
		want string
	}{
		{quality.SourceUnknown, "unknown"},
		{quality.SourceTelevision, "television"},
		{quality.SourceTelevisionRaw, "televisionRaw"},
		{quality.SourceWeb, "web"},
		{quality.SourceWebRip, "webRip"},
		{quality.SourceDVD, "dvd"},
		{quality.SourceBluray, "bluray"},
		{quality.SourceBlurayRaw, "blurayRaw"},
		{quality.QualitySource(99), "unknown"}, // unknown enum → fallback
	}
	for _, tc := range cases {
		if got := QualitySourceName(tc.in); got != tc.want {
			t.Errorf("QualitySourceName(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeLister returns canned rows or an error. Used in handler tests.
type fakeLister struct {
	rows []qualitydefinitions.Decorated
	err  error
}

func (f *fakeLister) All(context.Context) ([]qualitydefinitions.Decorated, error) {
	return f.rows, f.err
}

func TestQualityDefinitionHandler_OK(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{rows: []qualitydefinitions.Decorated{
		{ID: 1, Quality: quality.Unknown, Title: "Unknown", Weight: 1, GroupName: ""},
		{ID: 2, Quality: quality.SDTV, Title: "SDTV", Weight: 2, GroupName: ""},
	}}
	rr := httptest.NewRecorder()
	QualityDefinitionHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}

	var got []QualityDefinitionResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Quality.Source != "unknown" {
		t.Errorf("first entry source: got %q want unknown", got[0].Quality.Source)
	}
	if got[1].Quality.Source != "television" {
		t.Errorf("second entry source: got %q want television", got[1].Quality.Source)
	}
	// GroupName must not be emitted on the wire — the resource lacks
	// the field, so a successful round-trip into QualityDefinitionResource
	// already proves this. Belt-and-braces: confirm the raw body has
	// no "groupName" key.
	if strings.Contains(rr.Body.String(), "groupName") {
		t.Errorf("body unexpectedly contains groupName key: %s", rr.Body.String())
	}
}

func TestQualityDefinitionHandler_Error(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{err: errors.New("db down")}
	rr := httptest.NewRecorder()
	QualityDefinitionHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestQualityDefinitionHandler_OmitsNilSizeFields(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{rows: []qualitydefinitions.Decorated{
		{ID: 1, Quality: quality.Unknown, Title: "Unknown", Weight: 1},
	}}
	rr := httptest.NewRecorder()
	QualityDefinitionHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition", nil))
	body := rr.Body.String()
	for _, key := range []string{"minSize", "maxSize", "preferredSize"} {
		if strings.Contains(body, key) {
			t.Errorf("body unexpectedly contains %q: %s", key, body)
		}
	}
}
