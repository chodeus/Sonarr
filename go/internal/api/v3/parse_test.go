package v3

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseHandler_EmptyTitleAndPath_ReturnsNull(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ParseHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "null" {
		t.Errorf("body: %q want null", rr.Body.String())
	}
}

func TestParseHandler_TitleParses(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ParseHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	// Body should contain title + parsedEpisodeInfo populated when
	// the regex matches.
	body := rr.Body.String()
	if !strings.Contains(body, `"title":"The.Show.S01E02.720p.WEB-DL"`) {
		t.Errorf("missing title: %s", body)
	}
}

func TestParseHandler_UnparseableTitle_ReturnsOnlyTitle(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ParseHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=randomgibberish", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"title":"randomgibberish"`) {
		t.Errorf("title field: %s", body)
	}
}

// J299 wire-shape pin: ParseResource must emit `customFormatScore:0`
// always (non-nullable int in C# v3 + v5 — serializes the zero
// value), and customFormats[] is OMITTED at the current deferred
// state (matches C# `JsonIgnore(WhenWritingNull)` on
// `List<CustomFormatResource>?` while the parsing-service Map
// projection isn't running). Once the populated-row pipeline lands,
// customFormats becomes a non-nil slice and serializes to `[]` or a
// filled array; CustomFormatScore is computed via
// `series.QualityProfile.Value.CalculateCustomFormatScore`.
func TestParseHandler_EmitsCustomFormatScoreOmitsCustomFormats(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ParseHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"customFormatScore":0`) {
		t.Errorf("body must contain `\"customFormatScore\":0` always (non-nullable int); got %s", body)
	}
	if strings.Contains(body, `"customFormats"`) {
		t.Errorf("body must NOT contain `customFormats` at deferred state (omitempty + nil slice); got %s", body)
	}
}
