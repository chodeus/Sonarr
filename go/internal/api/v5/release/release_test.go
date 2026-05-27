package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/indexersearch"
)

// stubSearcher implements Searcher (alias of apiv3.ReleaseSearcher).
// Empty branches → empty decision slice; the handler then emits
// 200 + `[]` matching the C# v5 deferred-branch shape.
type stubSearcher struct{}

func (stubSearcher) EpisodeSearch(_ context.Context, _ int, _ indexersearch.SearchSettings) ([]decisionengine.Decision, error) {
	return nil, nil
}

func (stubSearcher) SeasonSearch(_ context.Context, _ int, _ int, _ bool, _ indexersearch.SearchSettings) ([]decisionengine.Decision, error) {
	return nil, nil
}

func TestListHandler_EpisodeID_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/release?episodeId=42", nil)
	ListHandler(stubSearcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	// Stub returns nil decisions → empty array on the wire. Matches the
	// C# v5 deferred branch (no decisions to map yet — RSS fetcher +
	// search service ports gate the populated branch).
	if rr.Body.String() != "[]" {
		t.Errorf("body: got %q want []", rr.Body.String())
	}
}

func TestListHandler_SeriesIDAndSeasonNumber_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/release?seriesId=1&seasonNumber=2", nil)
	ListHandler(stubSearcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: got %q want []", rr.Body.String())
	}
}

func TestListHandler_NoQueryParams_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	// RSS-fetch branch — DEFERRED on both surfaces. v3 handler returns
	// 200 + `[]` per the J255-era doc-comment ("`GetRss` branch not
	// ported"); v5 inherits the same shape via the alias.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/release", nil)
	ListHandler(stubSearcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: got %q want []", rr.Body.String())
	}
}

func TestListHandler_InvalidEpisodeID_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/release?episodeId=abc", nil)
	ListHandler(stubSearcher{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (passthrough from v3 parseOptionalInt)", rr.Code)
	}
}

// ----- type aliases (compile-time invariant guard) -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.ReleaseResource{}
	var _ apiv3.ReleaseResource = Resource{}
	var _ Searcher = (apiv3.ReleaseSearcher)(nil)
}
