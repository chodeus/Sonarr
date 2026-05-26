package parse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestGetHandler_EmptyInput_ReturnsObjectNotNull pins the
// v5-specific divergence from v3. v3 emits literal `null` on empty
// title+path; v5 emits a ParseResource object with `title` set
// (here to empty string, which JSON-marshals as `"title": ""`).
//
// This is the SOLE wire reason this slice can't path-alias to v3.
// The test is the canonical regression guard.
func TestGetHandler_EmptyInput_ReturnsObjectNotNull(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/parse", nil)
	GetHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if body == "null" {
		t.Errorf("v5 must NOT emit literal `null` on empty input; got %q (this is the v3 contract)", body)
	}
	// Should be a JSON object with `title` field.
	if !strings.Contains(body, `"title":""`) {
		t.Errorf("body should contain `\"title\":\"\"`; got %q", body)
	}
	// Decode round-trip should succeed (object, not null).
	var got Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Errorf("body should decode as Resource (not null): %v", err)
	}
}

// TestGetHandler_TitleWithoutEpisodeInfo — title supplied but Parser
// returns nil; result: ParseResource with Title set + ParsedEpisodeInfo
// nil (omitempty drops it).
func TestGetHandler_TitleWithoutEpisodeInfo(t *testing.T) {
	t.Parallel()
	// Use a title that won't parse to ParsedEpisodeInfo (random words).
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/parse?title=random+gibberish+nothing+to+see", nil)
	GetHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var got Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if got.Title != "random gibberish nothing to see" {
		t.Errorf("title: got %q", got.Title)
	}
	// ParsedEpisodeInfo may or may not be nil depending on the parser's
	// tolerance; we don't assert either way. The important contract is
	// that the response was a Resource object, not null.
}

// TestGetHandler_TitleParses — title that does parse to an episode
// info; result carries ParsedEpisodeInfo non-nil.
func TestGetHandler_TitleParses(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/parse?title=Breaking.Bad.S01E01.720p.HDTV.x264", nil)
	GetHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var got Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if got.ParsedEpisodeInfo == nil {
		t.Errorf("ParsedEpisodeInfo should be non-nil for a parseable title; got body=%s", rr.Body.String())
	}
}

// TestGetHandler_PathTakesPrecedence — when both title + path are
// supplied, the parser uses ParsePath, not ParseTitle. We don't pin
// the exact parser output (varies with filesystem behaviour); we
// just confirm the handler accepts the path-only query path without
// failing.
func TestGetHandler_PathTakesPrecedence(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/parse?title=ignored&path=Breaking.Bad.S01E01.720p.HDTV.x264.mkv", nil)
	GetHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
}

// TestTypeAliasMatchesV3 — compile-time invariant guard per the
// J251 pattern.
func TestTypeAliasMatchesV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.ParseResource{}
	var _ apiv3.ParseResource = Resource{}
}
