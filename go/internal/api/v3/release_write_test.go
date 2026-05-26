package v3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/releasecache"
	"sonarr.tv/sonarr/internal/series"
)

// fakeReleaseDispatcher implements ReleaseDispatcher for both release
// write handlers (cache-hit dispatch + push dispatch).
type fakeReleaseDispatcher struct {
	calls [][]decisionengine.Decision
}

func (f *fakeReleaseDispatcher) ProcessDecisions(_ context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions {
	cp := make([]decisionengine.Decision, len(decisions))
	copy(cp, decisions)
	f.calls = append(f.calls, cp)
	return download.NewProcessedDecisions()
}

// fakeDecisionMaker implements ReleaseDecisionMaker for the push handler.
type fakeDecisionMaker struct {
	returnDecisions []decisionengine.Decision
}

func (f *fakeDecisionMaker) GetRssDecision(_ context.Context, _ []any, _ bool) []decisionengine.Decision {
	return f.returnDecisions
}

// ----- POST /api/v3/release — cache-lookup path -----

func TestReleaseDownloadHandler_CacheMiss404(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	disp := &fakeReleaseDispatcher{}
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"indexerId":42,"guid":"abc"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", body)
	ReleaseDownloadHandler(cache, disp, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404 (cache miss); body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Couldn't find requested release in cache") {
		t.Errorf("body should carry the C# cache-miss message: %s", rr.Body.String())
	}
	if len(disp.calls) != 0 {
		t.Errorf("dispatcher must not be called on cache miss")
	}
}

func TestReleaseDownloadHandler_CacheHitDispatches(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	disp := &fakeReleaseDispatcher{}
	cached := decisionengine.NewDecision(&decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "X.S01E01", DownloadURL: "https://example.com/x.torrent"},
	})
	cache.Put(releasecache.Key(42, "abc"), cached)

	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"indexerId":42,"guid":"abc"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", body)
	ReleaseDownloadHandler(cache, disp, nil, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if len(disp.calls) != 1 || len(disp.calls[0]) != 1 {
		t.Fatalf("dispatcher: got %d calls", len(disp.calls))
	}
}

func TestReleaseDownloadHandler_ValidatorIndexerIdRequired(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"indexerId":0,"guid":"abc"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", body)
	ReleaseDownloadHandler(releasecache.New(0), &fakeReleaseDispatcher{}, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (IndexerId must be > 0)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "IndexerId") {
		t.Errorf("body should fault IndexerId: %s", rr.Body.String())
	}
}

func TestReleaseDownloadHandler_ValidatorGuidRequired(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"indexerId":42,"guid":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", body)
	ReleaseDownloadHandler(releasecache.New(0), &fakeReleaseDispatcher{}, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (Guid must not be empty)", rr.Code)
	}
}

func TestReleaseDownloadHandler_MalformedBody400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(`not-json`))
	ReleaseDownloadHandler(releasecache.New(0), &fakeReleaseDispatcher{}, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- POST /api/v3/release/push -----

func TestReleasePushHandler_HappyPath(t *testing.T) {
	t.Parallel()
	dm := &fakeDecisionMaker{returnDecisions: []decisionengine.Decision{
		decisionengine.NewDecision(&decisionengine.Subject{
			Release:           &parser.ReleaseInfo{Title: "Show.S01E01", DownloadURL: "https://x/r.torrent"},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{SeasonNumber: 1, EpisodeNumbers: []int{1}, ReleaseTitle: "Show.S01E01"},
		}),
	}}
	disp := &fakeReleaseDispatcher{}
	cache := releasecache.New(0)
	bodyJSON := `{
		"title":"Show.S01E01.720p",
		"downloadUrl":"https://example.com/show.torrent",
		"protocol":2,
		"publishDate":"2026-01-15T00:00:00Z",
		"indexerId":42,
		"indexer":"TestIndexer",
		"size":1048576
	}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", strings.NewReader(bodyJSON))
	ReleasePushHandler(dm, disp, cache).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if len(disp.calls) != 1 || len(disp.calls[0]) != 1 {
		t.Fatalf("dispatcher: got %d calls", len(disp.calls))
	}
	// Verify cache was seeded under the PUSH-prefixed key.
	pushKey := releasecache.Key(42, "PUSH-https://example.com/show.torrent")
	if _, ok := cache.Get(pushKey); !ok {
		t.Errorf("cache should be seeded under push key %q", pushKey)
	}
}

func TestReleasePushHandler_ValidatorTitleRequired(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"title":"","downloadUrl":"x","protocol":2,"publishDate":"2026-01-15T00:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0)).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestReleasePushHandler_ValidatorDownloadOrMagnetRequired(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"title":"X","downloadUrl":"","magnetUrl":"","protocol":2,"publishDate":"2026-01-15T00:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0)).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (neither url provided)", rr.Code)
	}
}

func TestReleasePushHandler_MagnetOnlyOK(t *testing.T) {
	t.Parallel()
	dm := &fakeDecisionMaker{returnDecisions: []decisionengine.Decision{
		decisionengine.NewDecision(&decisionengine.Subject{
			Release:           &parser.ReleaseInfo{Title: "Y"},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Y"},
		}),
	}}
	body := strings.NewReader(`{"title":"Y","magnetUrl":"magnet:?xt=urn:btih:abc","protocol":2,"publishDate":"2026-01-15T00:00:00Z","indexerId":1}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0)).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (magnet-only allowed)", rr.Code)
	}
}

func TestReleasePushHandler_ValidatorProtocolRequired(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"title":"X","downloadUrl":"y","protocol":0,"publishDate":"2026-01-15T00:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0)).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (protocol=Unknown rejected)", rr.Code)
	}
}

func TestReleasePushHandler_ValidatorPublishDateRequired(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	// publishDate omitted → IsZero
	body := strings.NewReader(`{"title":"X","downloadUrl":"y","protocol":2}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0)).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (publishDate IsZero rejected)", rr.Code)
	}
}

func TestReleasePushHandler_UnableToParse400(t *testing.T) {
	t.Parallel()
	// Decision maker returns a decision but Subject.ParsedEpisodeInfo is nil
	// (matches the C# `:76-79` "Unable to parse" guard).
	dm := &fakeDecisionMaker{returnDecisions: []decisionengine.Decision{
		decisionengine.NewDecision(&decisionengine.Subject{
			Release: &parser.ReleaseInfo{Title: "X"},
			// ParsedEpisodeInfo intentionally nil
		}),
	}}
	body := strings.NewReader(`{"title":"X","downloadUrl":"https://e/x.torrent","protocol":2,"publishDate":"2026-01-15T00:00:00Z","indexerId":1}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0)).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Unable to parse") {
		t.Errorf("body should say 'Unable to parse': %s", rr.Body.String())
	}
}

func TestReleasePushHandler_EmptyDecisionList400(t *testing.T) {
	t.Parallel()
	// Decision maker returns empty list — release rejected entirely.
	dm := &fakeDecisionMaker{returnDecisions: nil}
	body := strings.NewReader(`{"title":"X","downloadUrl":"https://e/x.torrent","protocol":2,"publishDate":"2026-01-15T00:00:00Z","indexerId":1}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0)).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (empty decisions)", rr.Code)
	}
}

func TestReleasePushHandler_TorrentProtocolTorrentInfoBuilt(t *testing.T) {
	t.Parallel()
	dm := &fakeDecisionMaker{returnDecisions: []decisionengine.Decision{
		decisionengine.NewDecision(&decisionengine.Subject{
			Release:           &parser.ReleaseInfo{Title: "Z"},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Z"},
		}),
	}}
	// Protocol=2 = Torrent. Handler should route through TorrentInfo branch.
	body := strings.NewReader(`{"title":"Z","downloadUrl":"https://e/z.torrent","magnetUrl":"magnet:?xt=urn:btih:zzz","protocol":2,"publishDate":"2026-01-15T00:00:00Z","indexerId":1}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0)).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

// guard against unused-package lint
var _ = time.Now
var _ = indexers.DownloadProtocolUnknown

// ----- POST /api/v3/release ShouldOverride=true (J249) -----

type fakeOverrideSeriesLookup struct {
	got     int
	returnS *series.Series
	err     error
}

func (f *fakeOverrideSeriesLookup) GetSeries(_ context.Context, id int) (*series.Series, error) {
	f.got = id
	return f.returnS, f.err
}

type fakeOverrideEpisodeLookup struct {
	got      []int
	returnEp []episodes.Episode
	err      error
}

func (f *fakeOverrideEpisodeLookup) GetEpisodes(_ context.Context, ids []int) ([]episodes.Episode, error) {
	f.got = ids
	return f.returnEp, f.err
}

func TestReleaseDownloadHandler_OverrideHappyPath_OverridesSubjectAndDispatches(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	disp := &fakeReleaseDispatcher{}
	// Cache holds a Subject with a stub series (id 1) + parsedinfo
	// (quality SDTV). Override body swaps series→42, episodes→[10,11],
	// quality→HDTV-1080p, languages→[English].
	origParsed := &parser.ParsedEpisodeInfo{SeasonNumber: 1, EpisodeNumbers: []int{1}}
	cached := decisionengine.NewDecision(&decisionengine.Subject{
		Release:           &parser.ReleaseInfo{Title: "Show.S01E01", DownloadURL: "https://x/r.torrent"},
		Series:            &series.Series{ID: 1, Title: "Original"},
		Episodes:          []episodes.Episode{{ID: 99}},
		ParsedEpisodeInfo: origParsed,
	})
	cache.Put(releasecache.Key(42, "abc"), cached)

	sl := &fakeOverrideSeriesLookup{returnS: &series.Series{ID: 42, Title: "Overridden"}}
	el := &fakeOverrideEpisodeLookup{returnEp: []episodes.Episode{{ID: 10}, {ID: 11}}}

	// quality id 1 = SDTV (any valid registry id works for marshal/unmarshal round-trip).
	bodyJSON := `{"indexerId":42,"guid":"abc","shouldOverride":true,"seriesId":42,"episodeIds":[10,11],"quality":{"quality":1,"revision":{"version":1,"real":0,"isRepack":false}},"languages":[{"id":1,"name":"English"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, disp, sl, el).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if sl.got != 42 {
		t.Errorf("seriesLookup got id=%d, want 42", sl.got)
	}
	if len(el.got) != 2 || el.got[0] != 10 || el.got[1] != 11 {
		t.Errorf("episodeLookup got ids=%v, want [10 11]", el.got)
	}
	if len(disp.calls) != 1 || len(disp.calls[0]) != 1 {
		t.Fatalf("dispatcher: got %d calls", len(disp.calls))
	}
	// Inspect the dispatched Subject — it should be the OVERRIDDEN clone.
	dispatched := disp.calls[0][0]
	subj, ok := dispatched.Subject.(*decisionengine.Subject)
	if !ok {
		t.Fatalf("dispatched Subject type unexpected: %T", dispatched.Subject)
	}
	if subj.Series == nil || subj.Series.ID != 42 {
		t.Errorf("Subject.Series.ID got %v, want 42", subj.Series)
	}
	if len(subj.Episodes) != 2 || subj.Episodes[0].ID != 10 {
		t.Errorf("Subject.Episodes got %v, want [{10}, {11}]", subj.Episodes)
	}
	if len(subj.Languages) != 1 || subj.Languages[0].ID != 1 {
		t.Errorf("Subject.Languages got %+v, want [{ID:1}]", subj.Languages)
	}
	if subj.ParsedEpisodeInfo == nil || subj.ParsedEpisodeInfo.Quality.Quality.ID != 1 {
		t.Errorf("ParsedEpisodeInfo.Quality.Quality.ID got %v, want 1", subj.ParsedEpisodeInfo)
	}
	// Crucially: the cached Subject must NOT have been mutated — it's a
	// shared in-memory cache entry. The cloned ParsedEpisodeInfo
	// pointer should differ from the original.
	if subj.ParsedEpisodeInfo == origParsed {
		t.Errorf("override mutated the cached ParsedEpisodeInfo in place; expected a shallow clone")
	}
}

func TestReleaseDownloadHandler_OverrideMissingRequiredFields_400Validation(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cache.Put(releasecache.Key(42, "abc"), decisionengine.NewDecision(&decisionengine.Subject{}))

	cases := []struct {
		name    string
		body    string
		wantTag string
	}{
		{"no SeriesId", `{"indexerId":42,"guid":"abc","shouldOverride":true,"episodeIds":[10],"quality":{"quality":1,"revision":{"version":1}},"languages":[{"id":1}]}`, "SeriesId"},
		{"empty EpisodeIds", `{"indexerId":42,"guid":"abc","shouldOverride":true,"seriesId":42,"episodeIds":[],"quality":{"quality":1,"revision":{"version":1}},"languages":[{"id":1}]}`, "EpisodeIds"},
		{"missing quality", `{"indexerId":42,"guid":"abc","shouldOverride":true,"seriesId":42,"episodeIds":[10],"languages":[{"id":1}]}`, "Quality"},
		{"missing languages", `{"indexerId":42,"guid":"abc","shouldOverride":true,"seriesId":42,"episodeIds":[10],"quality":{"quality":1,"revision":{"version":1}}}`, "Languages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(tc.body))
			ReleaseDownloadHandler(cache, &fakeReleaseDispatcher{}, &fakeOverrideSeriesLookup{}, &fakeOverrideEpisodeLookup{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: got %d want 400; body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.wantTag) {
				t.Errorf("validation envelope should name %q; got body=%s", tc.wantTag, rr.Body.String())
			}
		})
	}
}

func TestReleaseDownloadHandler_OverrideWithNilDeps_503(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cache.Put(releasecache.Key(42, "abc"), decisionengine.NewDecision(&decisionengine.Subject{}))
	bodyJSON := `{"indexerId":42,"guid":"abc","shouldOverride":true,"seriesId":42,"episodeIds":[10],"quality":{"quality":1,"revision":{"version":1}},"languages":[{"id":1}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	disp := &fakeReleaseDispatcher{}
	// Pass nil for both lookups — request asks for override but the
	// server isn't wired to honour it. 503 (Service Unavailable).
	ReleaseDownloadHandler(cache, disp, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d want 503", rr.Code)
	}
	if len(disp.calls) != 0 {
		t.Errorf("dispatcher must not be called when override deps are missing")
	}
}

func TestReleaseDownloadHandler_OverrideWithLookupError_500(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cache.Put(releasecache.Key(42, "abc"), decisionengine.NewDecision(&decisionengine.Subject{}))
	bodyJSON := `{"indexerId":42,"guid":"abc","shouldOverride":true,"seriesId":42,"episodeIds":[10],"quality":{"quality":1,"revision":{"version":1}},"languages":[{"id":1}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	disp := &fakeReleaseDispatcher{}
	sl := &fakeOverrideSeriesLookup{err: errSeriesLookup}
	el := &fakeOverrideEpisodeLookup{}
	ReleaseDownloadHandler(cache, disp, sl, el).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
	if len(disp.calls) != 0 {
		t.Errorf("dispatcher must not be called when series lookup errors")
	}
}

var errSeriesLookup = newErr("series lookup boom")

func newErr(s string) error { return &simpleErr{s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
