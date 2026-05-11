package scenemappings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// servicesSceneMappingFixture is a synthetic 3-row response that
// exercises every field of the C# SceneMapping wire shape — including
// the [JsonProperty]-renamed `searchTitle` + `season` fields. NOT
// recorded against the live services.sonarr.tv endpoint; it's a
// minimal contract-shape sample. A future slice with access to the
// Phase-0.5 contract corpus would replace this with goldenfile data.
const servicesSceneMappingFixture = `[
  {
    "mappingId": "tvdb-81189-1",
    "title": "Breaking Bad",
    "parseTerm": "breaking bad",
    "searchTitle": "Breaking Bad",
    "tvdbId": 81189,
    "season": 1,
    "sceneSeasonNumber": null,
    "sceneOrigin": "tvdb",
    "searchMode": null,
    "comment": "primary",
    "filterRegex": "",
    "type": "TvdbSceneMappingProvider"
  },
  {
    "mappingId": "anidb-83-1",
    "title": "Some Anime",
    "parseTerm": "some anime",
    "searchTitle": "Some.Anime",
    "tvdbId": 12345,
    "season": null,
    "sceneSeasonNumber": 2,
    "sceneOrigin": "anidb",
    "searchMode": 1,
    "comment": "",
    "filterRegex": "1080p",
    "type": "AnidbSceneMappingProvider"
  },
  {
    "mappingId": "minimal",
    "title": "Min",
    "searchTitle": "Min",
    "tvdbId": 1,
    "sceneOrigin": "tvdb",
    "type": "TvdbSceneMappingProvider"
  }
]`

func newTestProxy(t *testing.T, srv *httptest.Server) *Proxy {
	t.Helper()
	p, err := NewProxy(srv.Client(), srv.URL, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return p
}

// TestProxy_GetSceneMappings_HappyPath decodes the fixture and asserts
// every field on each of the three rows maps to the expected Mapping
// shape — including the `searchTitle → SearchTerm` and `season →
// SeasonNumber` renames.
func TestProxy_GetSceneMappings_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept header = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(servicesSceneMappingFixture))
	}))
	defer srv.Close()

	p := newTestProxy(t, srv)
	got, err := p.GetSceneMappings(context.Background())
	if err != nil {
		t.Fatalf("GetSceneMappings: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}

	// Row 0 — Breaking Bad, season 1, no sceneSeasonNumber.
	bb := got[0]
	if bb.MappingID != "tvdb-81189-1" || bb.Title != "Breaking Bad" ||
		bb.ParseTerm != "breaking bad" || bb.SearchTerm != "Breaking Bad" ||
		bb.TvdbID != 81189 || bb.SceneOrigin != "tvdb" || bb.Comment != "primary" ||
		bb.FilterRegex != "" || bb.Type != "TvdbSceneMappingProvider" {
		t.Errorf("row 0 scalar mismatch: %+v", bb)
	}
	if bb.SeasonNumber == nil || *bb.SeasonNumber != 1 {
		t.Errorf("row 0 SeasonNumber = %v, want *1 (verifies `season` → SeasonNumber rename)", bb.SeasonNumber)
	}
	if bb.SceneSeasonNumber != nil {
		t.Errorf("row 0 SceneSeasonNumber = %v, want nil", bb.SceneSeasonNumber)
	}
	if bb.SearchMode != nil {
		t.Errorf("row 0 SearchMode = %v, want nil", bb.SearchMode)
	}

	// Row 1 — Anime with sceneSeasonNumber=2, searchMode=1, no season.
	an := got[1]
	if an.MappingID != "anidb-83-1" || an.SearchTerm != "Some.Anime" {
		t.Errorf("row 1 IDs = %+v", an)
	}
	if an.SeasonNumber != nil {
		t.Errorf("row 1 SeasonNumber = %v, want nil", an.SeasonNumber)
	}
	if an.SceneSeasonNumber == nil || *an.SceneSeasonNumber != 2 {
		t.Errorf("row 1 SceneSeasonNumber = %v, want *2", an.SceneSeasonNumber)
	}
	if an.SearchMode == nil || *an.SearchMode != 1 {
		t.Errorf("row 1 SearchMode = %v, want *1", an.SearchMode)
	}
	if an.FilterRegex != "1080p" {
		t.Errorf("row 1 FilterRegex = %q, want 1080p", an.FilterRegex)
	}

	// Row 2 — minimal payload (omitted nullable fields).
	mn := got[2]
	if mn.MappingID != "minimal" || mn.Title != "Min" || mn.SearchTerm != "Min" ||
		mn.TvdbID != 1 || mn.SceneOrigin != "tvdb" {
		t.Errorf("row 2 scalar mismatch: %+v", mn)
	}
	if mn.SeasonNumber != nil || mn.SceneSeasonNumber != nil || mn.SearchMode != nil {
		t.Errorf("row 2 nullables = %v / %v / %v, want all nil",
			mn.SeasonNumber, mn.SceneSeasonNumber, mn.SearchMode)
	}
}

// TestProxy_GetSceneMappings_EmptyArray confirms a successful empty
// response decodes to a non-nil zero-length slice.
func TestProxy_GetSceneMappings_EmptyArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	p := newTestProxy(t, srv)
	got, err := p.GetSceneMappings(context.Background())
	if err != nil {
		t.Fatalf("GetSceneMappings: %v", err)
	}
	if got == nil {
		t.Fatalf("got = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

// TestProxy_GetSceneMappings_Non2xx covers the status-code error
// branch.
func TestProxy_GetSceneMappings_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	p := newTestProxy(t, srv)
	_, err := p.GetSceneMappings(context.Background())
	if err == nil {
		t.Fatal("err = nil, want HTTP-status error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want substring 502", err)
	}
}

// TestProxy_GetSceneMappings_DecodeError covers malformed JSON.
func TestProxy_GetSceneMappings_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"mappingId": "x"`))
	}))
	defer srv.Close()

	p := newTestProxy(t, srv)
	_, err := p.GetSceneMappings(context.Background())
	if err == nil {
		t.Fatal("err = nil, want decode error")
	}
}

// TestProxy_GetSceneMappings_ContextCancel confirms ctx propagation.
func TestProxy_GetSceneMappings_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	p := newTestProxy(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.GetSceneMappings(ctx)
	if err == nil {
		t.Fatal("err = nil, want deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want wraps context.DeadlineExceeded", err)
	}
}

// TestNewProxy_RejectsNil covers the constructor guards.
func TestNewProxy_RejectsNil(t *testing.T) {
	cases := []struct {
		name   string
		client *http.Client
		base   string
	}{
		{"nil_client", nil, "https://services.sonarr.tv/v1/scenemapping"},
		{"empty_base", &http.Client{}, ""},
		{"whitespace_base", &http.Client{}, "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewProxy(tc.client, tc.base, nil); err == nil {
				t.Errorf("NewProxy(%v, %q) = no err, want guard rejection", tc.client, tc.base)
			}
		})
	}
}
