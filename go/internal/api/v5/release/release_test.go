package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/releasecache"
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

// ----- POST /api/v5/release — body-translation adapter -----

type fakeDispatcher struct {
	calls [][]decisionengine.Decision
}

func (f *fakeDispatcher) ProcessDecisions(_ context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions {
	cp := make([]decisionengine.Decision, len(decisions))
	copy(cp, decisions)
	f.calls = append(f.calls, cp)
	return download.NewProcessedDecisions()
}

func TestDownloadHandler_CacheMissReturns404(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	disp := &fakeDispatcher{}
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"guid":"abc","indexerId":42}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v5/release", body)
	DownloadHandler(cache, disp, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404 (cache miss); body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Couldn't find requested release in cache") {
		t.Errorf("body should carry the C# cache-miss message; got %s", rr.Body.String())
	}
	if len(disp.calls) != 0 {
		t.Errorf("dispatcher must not be called on cache miss")
	}
}

func TestDownloadHandler_CacheHit_EchoesV5BodyAndDispatches(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	disp := &fakeDispatcher{}
	cached := decisionengine.NewDecision(&decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "X.S01E01", DownloadURL: "https://example.com/x.torrent"},
	})
	cache.Put(releasecache.Key(42, "abc"), cached)

	// V5 body shape with NO override + NO searchInfo (pure cache lookup path).
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"guid":"abc","indexerId":42}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v5/release", body)
	DownloadHandler(cache, disp, nil, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	// Response body must be the v5 shape (the request body verbatim).
	got := rr.Body.String()
	if !strings.Contains(got, `"guid":"abc"`) || !strings.Contains(got, `"indexerId":42`) {
		t.Errorf("response body must echo v5 body; got %q", got)
	}
	// Negative pin: v3-flattened-shape fields must NOT leak through.
	if strings.Contains(got, `"shouldOverride"`) {
		t.Errorf("v5 response must NOT carry v3-flattened-shape fields like shouldOverride; got %q", got)
	}
	if strings.Contains(got, `"episodeIds"`) {
		t.Errorf("v5 response must NOT have root-level episodeIds (would be under .override); got %q", got)
	}
	if len(disp.calls) != 1 || len(disp.calls[0]) != 1 {
		t.Errorf("dispatcher: got %d calls; want 1 dispatch", len(disp.calls))
	}
}

func TestDownloadHandler_ValidatorGuidRequired_PassesThrough400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"guid":"","indexerId":42}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v5/release", body)
	DownloadHandler(releasecache.New(0), &fakeDispatcher{}, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (Guid must not be empty)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Guid") {
		t.Errorf("body should fault Guid: %s", rr.Body.String())
	}
}

func TestDownloadHandler_ValidatorIndexerIDRequired_PassesThrough400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"guid":"abc","indexerId":0}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v5/release", body)
	DownloadHandler(releasecache.New(0), &fakeDispatcher{}, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (IndexerId must be > 0)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "IndexerId") {
		t.Errorf("body should fault IndexerId: %s", rr.Body.String())
	}
}

func TestDownloadHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/release", strings.NewReader(`{not-json`))
	DownloadHandler(releasecache.New(0), &fakeDispatcher{}, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (malformed body)", rr.Code)
	}
}

func TestDownloadHandler_UnknownField_RejectedAt400(t *testing.T) {
	t.Parallel()
	// The v5 body decoder uses DisallowUnknownFields. A field that
	// belongs to the v3 flat-shape (e.g. shouldOverride on the root)
	// MUST be rejected — defends the v5 wire contract against client
	// confusion between the two shapes.
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"guid":"abc","indexerId":42,"shouldOverride":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v5/release", body)
	DownloadHandler(releasecache.New(0), &fakeDispatcher{}, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (unknown field shouldOverride should be rejected)", rr.Code)
	}
}

func TestDownloadHandler_OverrideRequiredFieldsValidated_400(t *testing.T) {
	t.Parallel()
	// Override with missing required fields — v3 applyShouldOverride
	// surfaces 400 with per-field validation. Passes through unchanged.
	cache := releasecache.New(0)
	cached := decisionengine.NewDecision(&decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "X.S01E01"},
	})
	cache.Put(releasecache.Key(42, "abc"), cached)
	rr := httptest.NewRecorder()
	// Override with empty episodeIds + null quality + null languages → 400.
	body := strings.NewReader(`{"guid":"abc","indexerId":42,"override":{"seriesId":7,"episodeIds":[]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v5/release", body)
	DownloadHandler(cache, &fakeDispatcher{}, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (incomplete override should fail validation)", rr.Code)
	}
}

// ----- type aliases (compile-time invariant guard) -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.ReleaseResource{}
	var _ apiv3.ReleaseResource = Resource{}
	var _ Searcher = (apiv3.ReleaseSearcher)(nil)
}
