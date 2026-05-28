package wanted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodes"
)

// fakeLister implements EpisodeLister.
type fakeLister struct {
	rows  []apiv3.EpisodeResource
	total int
	err   error
}

func (f *fakeLister) PagedWanted(_ context.Context, _ apiv3.PagingRequest, _ apiv3.WantedScope, _, _ bool) ([]apiv3.EpisodeResource, int, error) {
	return f.rows, f.total, f.err
}

// fakeEpisodeGetter implements EpisodeByIDGetter.
type fakeEpisodeGetter struct {
	found *episodes.Episode
}

func (f *fakeEpisodeGetter) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	if f.found != nil && f.found.ID == id {
		cp := *f.found
		return &cp, nil
	}
	return nil, episodes.ErrNotFound
}

// ----- list (both scopes) -----

func TestListHandler_EmptyCorpus_PathAliasShape(t *testing.T) {
	t.Parallel()
	// Pins the v5 wire envelope at the empty-corpus level matches v3:
	// 200 + paging envelope with default sort `episodes.airDateUtc`
	// + records `[]` (NOT null).
	for _, scope := range []Scope{ScopeMissing, ScopeCutoff} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v5/wanted/x", nil)
		ListHandler(&fakeLister{rows: nil, total: 0}, scope).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("scope=%v status: got %d want 200", scope, rr.Code)
		}
		body := rr.Body.String()
		// Empty records slice must serialize as `[]` per the path-alias
		// contract (apiv3.WantedHandler normalises nil → []).
		if !strings.Contains(body, `"records":[]`) {
			t.Errorf("scope=%v records: body should contain `\"records\":[]`; got %q", scope, body)
		}
		// Default sortKey must match the C# v5 `MapToPagingSpec(...,
		// "episodes.airDateUtc", SortDirection.Ascending)` per the v5
		// openapi paging spec.
		if !strings.Contains(body, `"sortKey":"episodes.airDateUtc"`) {
			t.Errorf("scope=%v sortKey: body should contain default; got %q", scope, body)
		}
	}
}

func TestListHandler_StatusCodeMatchesV3(t *testing.T) {
	t.Parallel()
	// Pin: v5 wanted list returns 200 (same as v3 — no v5util wrapper
	// needed). Path-alias predicate.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/wanted/missing", nil)
	ListHandler(&fakeLister{}, ScopeMissing).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
}

// ----- by-id (both scopes share the same handler) -----

func TestByIDHandler_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/wanted/missing/999", nil)
	req.SetPathValue("id", "999")
	ByIDHandler(&fakeEpisodeGetter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestByIDHandler_Found_Returns200(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/wanted/cutoff/7", nil)
	req.SetPathValue("id", "7")
	ByIDHandler(&fakeEpisodeGetter{found: &episodes.Episode{ID: 7, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 3}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	// Spot-check Resource shape — confirm v3 EpisodeResource alias
	// round-trips cleanly.
	var got Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("id: got %d want 7", got.ID)
	}
}

// ----- type-alias compile-time pins -----

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	// Pure-alias slice: any drift between v5 wanted types and v3
	// types would fail to compile. These statements both type-check
	// and document the alias contract.
	var _ Resource = apiv3.EpisodeResource{}
	var _ Scope = apiv3.WantedMissing
	var _ Scope = apiv3.WantedCutoff
}
