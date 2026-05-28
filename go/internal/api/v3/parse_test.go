package v3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

func TestParseHandler_EmptyTitleAndPath_ReturnsNull(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ParseHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse", nil))
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
	ParseHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
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
	ParseHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=randomgibberish", nil))
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
	ParseHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
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

// ----- J344 — ParseMapper wire-through -----

type fakeParseMapper struct {
	got       *parser.ParsedEpisodeInfo
	returnSub *decisionengine.Subject
	err       error
	calls     int
}

func (f *fakeParseMapper) Map(_ context.Context, p *parser.ParsedEpisodeInfo, _ int, _ int, _ string, _ *decisionengine.SearchCriteria) (*decisionengine.Subject, error) {
	f.got = p
	f.calls++
	return f.returnSub, f.err
}

func TestParseHandler_MapperNil_BackwardsCompat_J344(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ParseHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
	body := rr.Body.String()
	// Nil mapper → series/episodes/languages absent (omitempty).
	if strings.Contains(body, `"series"`) {
		t.Errorf("nil-mapper body must NOT contain `series`: %s", body)
	}
	if strings.Contains(body, `"episodes"`) {
		t.Errorf("nil-mapper body must NOT contain `episodes`: %s", body)
	}
	if strings.Contains(body, `"languages"`) {
		t.Errorf("nil-mapper body must NOT contain `languages`: %s", body)
	}
}

func TestParseHandler_MapperPopulatesSeriesEpisodesLanguages_J344(t *testing.T) {
	t.Parallel()
	mapper := &fakeParseMapper{
		returnSub: &decisionengine.Subject{
			Series:    &series.Series{ID: 7, Title: "ResolvedShow"},
			Episodes:  []episodes.Episode{{ID: 11, SeriesID: 7, SeasonNumber: 1, EpisodeNumber: 2}},
			Languages: []language.Language{{ID: 1, Name: "English"}},
		},
	}
	rr := httptest.NewRecorder()
	ParseHandler(mapper).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if mapper.calls != 1 {
		t.Errorf("mapper.Map should fire once; got calls=%d", mapper.calls)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"series":{`) {
		t.Errorf("body should carry series envelope: %s", body)
	}
	if !strings.Contains(body, `"title":"ResolvedShow"`) {
		t.Errorf("body should carry resolved series title: %s", body)
	}
	if !strings.Contains(body, `"episodes":[{`) {
		t.Errorf("body should carry episodes envelope: %s", body)
	}
	if !strings.Contains(body, `"languages":[`) {
		t.Errorf("body should carry languages envelope: %s", body)
	}
}

func TestParseHandler_MapperReturnsNilSubject_FallsBackToMinimalShape_J344(t *testing.T) {
	t.Parallel()
	// Mapper returns (nil, nil) — couldn't resolve. C# `:51` else branch.
	mapper := &fakeParseMapper{returnSub: nil}
	rr := httptest.NewRecorder()
	ParseHandler(mapper).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
	body := rr.Body.String()
	if strings.Contains(body, `"series"`) {
		t.Errorf("nil-subject body must NOT contain `series`: %s", body)
	}
}

func TestParseHandler_MapperReturnsSubjectWithNilSeries_FallsBackToMinimalShape_J344(t *testing.T) {
	t.Parallel()
	// Subject returned but Series unresolved → C# behaviour also falls
	// through the else branch (the C# Map returns null when series is
	// unresolved, our equivalent is non-nil Subject with nil Series).
	mapper := &fakeParseMapper{returnSub: &decisionengine.Subject{}}
	rr := httptest.NewRecorder()
	ParseHandler(mapper).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
	body := rr.Body.String()
	if strings.Contains(body, `"series"`) {
		t.Errorf("nil-series body must NOT contain `series`: %s", body)
	}
}

func TestParseHandler_MapperErrors_SilentlyFallsBack_J344(t *testing.T) {
	t.Parallel()
	mapper := &fakeParseMapper{err: errParseMapper}
	rr := httptest.NewRecorder()
	ParseHandler(mapper).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("mapper error should degrade silently to 200 minimal shape; got %d", rr.Code)
	}
}

var errParseMapper = &parseMapperErr{}

type parseMapperErr struct{}

func (e *parseMapperErr) Error() string { return "mapper boom" }

// ----- J345 — ParsePopulator optional-extension wire-through -----

// fakeParseMapperWithPopulator implements BOTH ParseMapper AND
// ParsePopulator. The handler's type-assertion path picks up the
// populator extension and emits populated customFormats[] +
// customFormatScore.
type fakeParseMapperWithPopulator struct {
	returnSub *decisionengine.Subject
	cfReturn  []CustomFormatResource
	scoreRet  int
	cfCalls   int
}

func (f *fakeParseMapperWithPopulator) Map(_ context.Context, _ *parser.ParsedEpisodeInfo, _ int, _ int, _ string, _ *decisionengine.SearchCriteria) (*decisionengine.Subject, error) {
	return f.returnSub, nil
}

func (f *fakeParseMapperWithPopulator) PopulateParseCustomFormats(_ context.Context, _ *decisionengine.Subject) ([]CustomFormatResource, int) {
	f.cfCalls++
	return f.cfReturn, f.scoreRet
}

func TestParseHandler_PopulatorOptionalExtension_J345(t *testing.T) {
	t.Parallel()
	// Mapper-without-populator path: existing fakeParseMapper doesn't
	// implement ParsePopulator → CustomFormats stays nil + customFormatScore=0.
	t.Run("MapperWithoutPopulator_EmptyCustomFormats", func(t *testing.T) {
		t.Parallel()
		mapper := &fakeParseMapper{
			returnSub: &decisionengine.Subject{
				Series:    &series.Series{ID: 7, Title: "ResolvedShow"},
				Episodes:  []episodes.Episode{{ID: 11}},
				Languages: []language.Language{{ID: 1, Name: "English"}},
			},
		}
		rr := httptest.NewRecorder()
		ParseHandler(mapper).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
		body := rr.Body.String()
		if strings.Contains(body, `"customFormats"`) {
			t.Errorf("mapper-without-populator body must NOT contain `customFormats`; got %s", body)
		}
		if !strings.Contains(body, `"customFormatScore":0`) {
			t.Errorf("mapper-without-populator must emit customFormatScore:0; got %s", body)
		}
	})
	// Mapper-with-populator path: populated customFormats[] +
	// customFormatScore in the wire.
	t.Run("MapperWithPopulator_PopulatedCustomFormatsAndScore", func(t *testing.T) {
		t.Parallel()
		mapper := &fakeParseMapperWithPopulator{
			returnSub: &decisionengine.Subject{
				Series:    &series.Series{ID: 7, Title: "ResolvedShow"},
				Episodes:  []episodes.Episode{{ID: 11}},
				Languages: []language.Language{{ID: 1, Name: "English"}},
			},
			cfReturn: []CustomFormatResource{{ID: 5, Name: "x265"}},
			scoreRet: 42,
		}
		rr := httptest.NewRecorder()
		ParseHandler(mapper).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
		body := rr.Body.String()
		if !strings.Contains(body, `"customFormats":[{"id":5`) {
			t.Errorf("populator body must emit customFormats[] envelope; got %s", body)
		}
		if !strings.Contains(body, `"customFormatScore":42`) {
			t.Errorf("populator body must emit customFormatScore:42; got %s", body)
		}
		if mapper.cfCalls != 1 {
			t.Errorf("PopulateParseCustomFormats should fire once; got calls=%d", mapper.cfCalls)
		}
	})
	// Subject-with-nil-series → handler short-circuits BEFORE the
	// populator branch (matches the J344 fallback contract).
	t.Run("MapperWithPopulator_NilSeries_NoPopulatorCall", func(t *testing.T) {
		t.Parallel()
		mapper := &fakeParseMapperWithPopulator{returnSub: &decisionengine.Subject{}}
		rr := httptest.NewRecorder()
		ParseHandler(mapper).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/parse?title=The.Show.S01E02.720p.WEB-DL", nil))
		if mapper.cfCalls != 0 {
			t.Errorf("PopulateParseCustomFormats must NOT fire when Subject.Series is nil; got calls=%d", mapper.cfCalls)
		}
	})
}
