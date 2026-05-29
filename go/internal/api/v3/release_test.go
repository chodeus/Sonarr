package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/scenemappings"
	"sonarr.tv/sonarr/internal/series"
)

type fakeReleaseSearcher struct {
	episodeCalls []int
	seasonCalls  []struct{ Series, Season int }
	decisions    []decisionengine.Decision
	err          error
}

func (f *fakeReleaseSearcher) EpisodeSearch(_ context.Context, episodeID int, _ indexersearch.SearchSettings) ([]decisionengine.Decision, error) {
	f.episodeCalls = append(f.episodeCalls, episodeID)
	return f.decisions, f.err
}

func (f *fakeReleaseSearcher) SeasonSearch(_ context.Context, seriesID, seasonNumber int, _ bool, _ indexersearch.SearchSettings) ([]decisionengine.Decision, error) {
	f.seasonCalls = append(f.seasonCalls, struct{ Series, Season int }{seriesID, seasonNumber})
	return f.decisions, f.err
}

func TestReleaseHandler_EmptyArrayWhenNoParams(t *testing.T) {
	t.Parallel()
	searcher := &fakeReleaseSearcher{}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(searcher.episodeCalls) != 0 || len(searcher.seasonCalls) != 0 {
		t.Errorf("no params should not invoke search; episode=%v season=%v",
			searcher.episodeCalls, searcher.seasonCalls)
	}
}

func TestReleaseHandler_InvalidInt_400(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"seriesId", "episodeId", "seasonNumber"} {
		rr := httptest.NewRecorder()
		ReleaseHandler(&fakeReleaseSearcher{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?"+key+"=abc", nil))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s=abc: status %d", key, rr.Code)
		}
	}
}

func TestReleaseHandler_DispatchesEpisodeSearch(t *testing.T) {
	t.Parallel()
	searcher := &fakeReleaseSearcher{
		decisions: []decisionengine.Decision{
			decisionengine.NewDecision(&decisionengine.Subject{
				Release: &parser.ReleaseInfo{
					Title:   "Show.S01E01.HDTV-GROUP",
					Guid:    "g1",
					Indexer: "Indexer-X",
				},
				ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
					Quality:        quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()},
					ReleaseGroup:   "GROUP",
					EpisodeNumbers: []int{1},
					SeasonNumber:   1,
				},
			}),
		},
	}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?episodeId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body)
	}
	if len(searcher.episodeCalls) != 1 || searcher.episodeCalls[0] != 42 {
		t.Errorf("episode search not invoked with 42; got %v", searcher.episodeCalls)
	}
	var got []ReleaseResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Title != "Show.S01E01.HDTV-GROUP" {
		t.Errorf("Title = %q", got[0].Title)
	}
	if got[0].Guid != "g1" {
		t.Errorf("Guid = %q", got[0].Guid)
	}
	if got[0].Indexer != "Indexer-X" {
		t.Errorf("Indexer = %q", got[0].Indexer)
	}
	if !got[0].Approved {
		t.Errorf("Approved should be true for empty rejection slice")
	}
}

// J335 wire-shape pin: ReleaseResource.Quality widened from
// json.RawMessage (which embedded the persistence-reduced form
// `{quality:int,revision:{...}}` via the quality.Model custom
// MarshalJSON) to *QualityModelResource (the J325 expanded wire
// form `{quality:{id,name,source,resolution},revision:{...}}`
// matching C# v3+v5 default serialization).
func TestReleaseHandler_QualityTypedShape_J335(t *testing.T) {
	t.Parallel()
	searcher := &fakeReleaseSearcher{
		decisions: []decisionengine.Decision{
			decisionengine.NewDecision(&decisionengine.Subject{
				Release: &parser.ReleaseInfo{Title: "Show.S01E01.HDTV-720p", Guid: "g1", Indexer: "X"},
				ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
					Quality:        quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()},
					EpisodeNumbers: []int{1},
					SeasonNumber:   1,
				},
			}),
		},
	}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?episodeId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	// Positive: typed expanded form present.
	mustContain := []string{
		`"quality":{`,
		`"id":4`,
		`"name":"HDTV-720p"`,
		`"source":"television"`,
		`"resolution":720`,
		`"revision":{`,
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("populated Quality typed shape missing %q in %s", want, body)
		}
	}
	// Negative: the persistence-reduced form `"quality":4` (bare int)
	// must NOT appear — would be the pre-J335 shape.
	if strings.Contains(body, `"quality":4`) {
		t.Errorf("populated Quality must emit expanded form, not the reduced `\"quality\":4`; got %s", body)
	}
}

// J334 wire-shape pin: ReleaseResource.Languages widened from []int
// to []LanguageRef (J314 typed list form matching C# `List<Language>`
// wire). Populated rows emit typed `[{id,name}]` entries via the
// ToLanguageRef registry lookup; empty subject Languages still emits
// `[]` (not null) via the toReleaseResource fallback at release.go:251.
func TestReleaseHandler_LanguagesTypedShape_J334(t *testing.T) {
	t.Parallel()
	searcher := &fakeReleaseSearcher{
		decisions: []decisionengine.Decision{
			decisionengine.NewDecision(&decisionengine.Subject{
				Release: &parser.ReleaseInfo{Title: "Show.S01E01.HDTV-GROUP", Guid: "g1", Indexer: "X"},
				ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
					Quality:        quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()},
					EpisodeNumbers: []int{1},
					SeasonNumber:   1,
				},
				Languages: []language.Language{language.English, language.French},
			}),
		},
	}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?episodeId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"languages":[{"id":1,"name":"English"},{"id":2,"name":"French"}]`) {
		t.Errorf("populated Languages typed shape missing in response; got %s", body)
	}
	// Decode-through round-trip pin.
	var got []ReleaseResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || len(got[0].Languages) != 2 {
		t.Fatalf("len: %d / langs: %+v", len(got), got)
	}
	if got[0].Languages[0].ID != 1 || got[0].Languages[0].Name != "English" {
		t.Errorf("Languages[0] round-trip mismatch: %+v", got[0].Languages[0])
	}
	if got[0].Languages[1].ID != 2 || got[0].Languages[1].Name != "French" {
		t.Errorf("Languages[1] round-trip mismatch: %+v", got[0].Languages[1])
	}
}

func TestReleaseHandler_DispatchesSeasonSearch(t *testing.T) {
	t.Parallel()
	searcher := &fakeReleaseSearcher{}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?seriesId=7&seasonNumber=3", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(searcher.seasonCalls) != 1 || searcher.seasonCalls[0].Series != 7 || searcher.seasonCalls[0].Season != 3 {
		t.Errorf("season search not invoked correctly; got %v", searcher.seasonCalls)
	}
}

func TestReleaseHandler_PropagatesRejections(t *testing.T) {
	t.Parallel()
	searcher := &fakeReleaseSearcher{
		decisions: []decisionengine.Decision{
			decisionengine.NewRejectedDecision(
				&decisionengine.Subject{
					Release: &parser.ReleaseInfo{Title: "Rejected.Title"},
				},
				decisionengine.NewRejection(0, "Quality too low"),
				decisionengine.NewRejection(0, "Wrong language"),
			),
		},
	}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?episodeId=1", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "Quality too low") || !strings.Contains(body, "Wrong language") {
		t.Errorf("rejection messages missing: %q", body)
	}
	var got []ReleaseResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) == 0 || got[0].Approved {
		t.Errorf("rejected decision should not be Approved")
	}
}

func TestReleaseHandler_SearcherErrorPropagates(t *testing.T) {
	t.Parallel()
	searcher := &fakeReleaseSearcher{err: errors.New("indexer offline")}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?episodeId=1", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: %d", rr.Code)
	}
}

// TestReleaseHandler_FullDecisionProjection_J436 pins the Tier-2
// widening: the mapped-episode info, decision-output flags, and
// custom-format fields the C# DownloadDecision mapper populates.
func TestReleaseHandler_FullDecisionProjection_J436(t *testing.T) {
	t.Parallel()
	abs := 101
	searcher := &fakeReleaseSearcher{
		decisions: []decisionengine.Decision{
			decisionengine.NewDecision(&decisionengine.Subject{
				Release:           &parser.ReleaseInfo{Title: "Show.S01E01", Guid: "g1", Indexer: "X"},
				ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{Quality: quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()}, EpisodeNumbers: []int{1}, SeasonNumber: 1},
				Series:            &series.Series{ID: 77},
				Episodes: []episodes.Episode{
					{ID: 501, SeasonNumber: 1, EpisodeNumber: 1, AbsoluteEpisodeNumber: &abs, Title: "Pilot"},
					{ID: 502, SeasonNumber: 1, EpisodeNumber: 2, Title: "Second"},
				},
				EpisodeRequested:  true,
				DownloadAllowed:   true,
				CustomFormatScore: 42,
				CustomFormats:     []customformats.CustomFormat{{ID: 3, Name: "x265"}},
			}),
		},
	}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?episodeId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var got []ReleaseResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	r := got[0]
	if r.MappedSeriesID == nil || *r.MappedSeriesID != 77 {
		t.Errorf("MappedSeriesID = %v, want 77", r.MappedSeriesID)
	}
	if len(r.MappedEpisodeInfo) != 2 || r.MappedEpisodeInfo[0].ID != 501 || r.MappedEpisodeInfo[0].Title != "Pilot" {
		t.Errorf("MappedEpisodeInfo = %+v", r.MappedEpisodeInfo)
	}
	if r.MappedEpisodeInfo[0].AbsoluteEpisodeNumber == nil || *r.MappedEpisodeInfo[0].AbsoluteEpisodeNumber != 101 {
		t.Errorf("ep0 abs = %v, want 101", r.MappedEpisodeInfo[0].AbsoluteEpisodeNumber)
	}
	if r.MappedEpisodeInfo[1].AbsoluteEpisodeNumber != nil {
		t.Errorf("ep1 abs should be nil (omitted), got %v", *r.MappedEpisodeInfo[1].AbsoluteEpisodeNumber)
	}
	// Only ep0 had an absolute number → MappedAbsoluteEpisodeNumbers has 1 entry.
	if len(r.MappedAbsoluteEpisodeNumbers) != 1 || r.MappedAbsoluteEpisodeNumbers[0] != 101 {
		t.Errorf("MappedAbsoluteEpisodeNumbers = %v, want [101]", r.MappedAbsoluteEpisodeNumbers)
	}
	if !r.EpisodeRequested || !r.DownloadAllowed {
		t.Errorf("flags: requested=%v allowed=%v, want true/true", r.EpisodeRequested, r.DownloadAllowed)
	}
	if r.CustomFormatScore != 42 {
		t.Errorf("CustomFormatScore = %d, want 42", r.CustomFormatScore)
	}
	if len(r.CustomFormats) != 1 || r.CustomFormats[0].ID != 3 || r.CustomFormats[0].Name != "x265" {
		t.Errorf("CustomFormats = %+v", r.CustomFormats)
	}
	// Lite custom-format projection: no specifications key on the spec.
	body := rr.Body.String()
	if strings.Contains(body, `"specifications"`) {
		t.Errorf("release CustomFormats must be lite (no specifications): %s", body)
	}
	// Weight fields emit their C# defaults (0).
	for _, want := range []string{`"qualityWeight":0`, `"languageWeight":0`, `"releaseWeight":0`, `"sceneSource":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing default field %s in %s", want, body)
		}
	}
}

// TestSceneMappingToAlternateTitle_J437 pins the SceneMapping →
// AlternateTitleResource projection incl. the "Limited matching"
// comment fallback (C# AlternateTitleResource.cs:24-28).
func TestSceneMappingToAlternateTitle_J437(t *testing.T) {
	t.Parallel()
	if sceneMappingToAlternateTitle(nil) != nil {
		t.Fatal("nil mapping must project to nil")
	}
	season := 3
	scene := 1
	got := sceneMappingToAlternateTitle(&scenemappings.Mapping{
		Title:             "Show (Scene)",
		SeasonNumber:      &season,
		SceneSeasonNumber: &scene,
		SceneOrigin:       "tvdb",
		Comment:           "hand-mapped",
	})
	if got == nil || got.Title != "Show (Scene)" || got.SceneOrigin != "tvdb" || got.Comment != "hand-mapped" {
		t.Fatalf("projection mismatch: %+v", got)
	}
	if got.SeasonNumber == nil || *got.SeasonNumber != 3 || got.SceneSeasonNumber == nil || *got.SceneSeasonNumber != 1 {
		t.Errorf("season fields: %+v", got)
	}
	// Blank comment + non-empty FilterRegex → "Limited matching".
	lim := sceneMappingToAlternateTitle(&scenemappings.Mapping{Title: "T", Comment: "  ", FilterRegex: "S0[12]"})
	if lim.Comment != "Limited matching" {
		t.Errorf("FilterRegex fallback comment = %q, want 'Limited matching'", lim.Comment)
	}
	// Blank comment + blank FilterRegex → stays blank.
	blank := sceneMappingToAlternateTitle(&scenemappings.Mapping{Title: "T"})
	if blank.Comment != "" {
		t.Errorf("comment should stay blank, got %q", blank.Comment)
	}
}

// TestReleaseHandler_SceneMappingProjected_J437 verifies the field
// flows through the full release projection.
func TestReleaseHandler_SceneMappingProjected_J437(t *testing.T) {
	t.Parallel()
	season := 2
	searcher := &fakeReleaseSearcher{
		decisions: []decisionengine.Decision{
			decisionengine.NewDecision(&decisionengine.Subject{
				Release:           &parser.ReleaseInfo{Title: "X", Guid: "g", Indexer: "I"},
				ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{Quality: quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()}, EpisodeNumbers: []int{1}, SeasonNumber: 1},
				SceneMapping:      &scenemappings.Mapping{Title: "Alt Title", SeasonNumber: &season, SceneOrigin: "tvdb"},
			}),
		},
	}
	rr := httptest.NewRecorder()
	ReleaseHandler(searcher).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/release?episodeId=1", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `"sceneMapping":{`) || !strings.Contains(body, `"title":"Alt Title"`) || !strings.Contains(body, `"sceneOrigin":"tvdb"`) {
		t.Errorf("sceneMapping not projected: %s", body)
	}
}
