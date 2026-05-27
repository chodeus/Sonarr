package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
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
