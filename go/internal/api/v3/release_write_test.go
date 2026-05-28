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
	ReleaseDownloadHandler(cache, disp, nil, nil, nil, nil).ServeHTTP(rr, req)
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
	// Well-formed cached Subject — Series + Episodes populated so the
	// J343 fallback ladder fast-paths and dispatch proceeds.
	cached := decisionengine.NewDecision(&decisionengine.Subject{
		Release:  &parser.ReleaseInfo{Title: "X.S01E01", DownloadURL: "https://example.com/x.torrent"},
		Series:   &series.Series{ID: 1},
		Episodes: []episodes.Episode{{ID: 99}},
	})
	cache.Put(releasecache.Key(42, "abc"), cached)

	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"indexerId":42,"guid":"abc"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", body)
	ReleaseDownloadHandler(cache, disp, nil, nil, nil, nil).ServeHTTP(rr, req)

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
	ReleaseDownloadHandler(releasecache.New(0), &fakeReleaseDispatcher{}, nil, nil, nil, nil).ServeHTTP(rr, req)
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
	ReleaseDownloadHandler(releasecache.New(0), &fakeReleaseDispatcher{}, nil, nil, nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (Guid must not be empty)", rr.Code)
	}
}

func TestReleaseDownloadHandler_MalformedBody400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(`not-json`))
	ReleaseDownloadHandler(releasecache.New(0), &fakeReleaseDispatcher{}, nil, nil, nil, nil).ServeHTTP(rr, req)
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
	ReleasePushHandler(dm, disp, cache, nil, nil).ServeHTTP(rr, req)
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
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0), nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestReleasePushHandler_ValidatorDownloadOrMagnetRequired(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"title":"X","downloadUrl":"","magnetUrl":"","protocol":2,"publishDate":"2026-01-15T00:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0), nil, nil).ServeHTTP(rr, req)
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
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0), nil, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (magnet-only allowed)", rr.Code)
	}
}

func TestReleasePushHandler_ValidatorProtocolRequired(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := strings.NewReader(`{"title":"X","downloadUrl":"y","protocol":0,"publishDate":"2026-01-15T00:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0), nil, nil).ServeHTTP(rr, req)
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
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0), nil, nil).ServeHTTP(rr, req)
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
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0), nil, nil).ServeHTTP(rr, req)
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
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0), nil, nil).ServeHTTP(rr, req)
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
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0), nil, nil).ServeHTTP(rr, req)
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
	ReleaseDownloadHandler(cache, disp, sl, el, nil, nil).ServeHTTP(rr, req)

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
			ReleaseDownloadHandler(cache, &fakeReleaseDispatcher{}, &fakeOverrideSeriesLookup{}, &fakeOverrideEpisodeLookup{}, nil, nil).ServeHTTP(rr, req)
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
	ReleaseDownloadHandler(cache, disp, nil, nil, nil, nil).ServeHTTP(rr, req)
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
	ReleaseDownloadHandler(cache, disp, sl, el, nil, nil).ServeHTTP(rr, req)
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

// ----- POST /api/v3/release/push indexer/dc resolve (J250) -----

// fakePushIndexerLookup tracks Get + FindByName calls and returns
// configured rows or errors. Both arms can be primed independently.
type fakePushIndexerLookup struct {
	gotID       int
	gotName     string
	returnByID  *indexers.Indexer
	returnName  *indexers.Indexer
	errByID     error
	errByName   error
	callsByID   int
	callsByName int
}

func (f *fakePushIndexerLookup) Get(_ context.Context, id int) (*indexers.Indexer, error) {
	f.gotID = id
	f.callsByID++
	return f.returnByID, f.errByID
}

func (f *fakePushIndexerLookup) FindByName(_ context.Context, name string) (*indexers.Indexer, error) {
	f.gotName = name
	f.callsByName++
	return f.returnName, f.errByName
}

func TestReleasePushHandler_IndexerResolve_ByID_OverwritesNameFromRepo(t *testing.T) {
	t.Parallel()
	dm := &fakeDecisionMaker{returnDecisions: []decisionengine.Decision{
		decisionengine.NewDecision(&decisionengine.Subject{
			Release:           &parser.ReleaseInfo{Title: "Z"},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Z"},
		}),
	}}
	indexerLook := &fakePushIndexerLookup{
		returnByID: &indexers.Indexer{ID: 42, Name: "CanonicalName"},
		errByName:  indexers.ErrNotFound, // not used when name is empty
	}
	// Body sends id-only; name absent. Resolver should look up by id
	// and the dispatched ReleaseInfo should carry the canonical name.
	body := strings.NewReader(`{"title":"Z","downloadUrl":"https://x/y","protocol":1,"publishDate":"2026-01-15T00:00:00Z","indexerId":42}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0), indexerLook, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if indexerLook.callsByID != 1 {
		t.Errorf("expected 1 Get(id=42) call, got %d", indexerLook.callsByID)
	}
	if indexerLook.callsByName != 0 {
		t.Errorf("expected 0 FindByName calls when name is empty, got %d", indexerLook.callsByName)
	}
}

func TestReleasePushHandler_IndexerResolve_NameOnly_LooksUpByName(t *testing.T) {
	t.Parallel()
	dm := &fakeDecisionMaker{returnDecisions: []decisionengine.Decision{
		decisionengine.NewDecision(&decisionengine.Subject{
			Release:           &parser.ReleaseInfo{Title: "Z"},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Z"},
		}),
	}}
	indexerLook := &fakePushIndexerLookup{
		returnName: &indexers.Indexer{ID: 99, Name: "Newznab-A"},
	}
	body := strings.NewReader(`{"title":"Z","downloadUrl":"https://x/y","protocol":1,"publishDate":"2026-01-15T00:00:00Z","indexer":"Newznab-A"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0), indexerLook, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if indexerLook.callsByID != 0 {
		t.Errorf("expected 0 Get calls when id is zero, got %d", indexerLook.callsByID)
	}
	if indexerLook.callsByName != 1 || indexerLook.gotName != "Newznab-A" {
		t.Errorf("expected 1 FindByName('Newznab-A'), got calls=%d name=%q", indexerLook.callsByName, indexerLook.gotName)
	}
}

func TestReleasePushHandler_IndexerResolve_IDNotFound_400(t *testing.T) {
	t.Parallel()
	indexerLook := &fakePushIndexerLookup{errByID: indexers.ErrNotFound}
	body := strings.NewReader(`{"title":"Z","downloadUrl":"https://x/y","protocol":1,"publishDate":"2026-01-15T00:00:00Z","indexerId":9999}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0), indexerLook, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 (IndexerId not found)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "9999") {
		t.Errorf("validation envelope should name the missing id; got body=%s", rr.Body.String())
	}
}

func TestReleasePushHandler_IndexerResolve_IDNameMismatch_400(t *testing.T) {
	t.Parallel()
	indexerLook := &fakePushIndexerLookup{
		returnByID: &indexers.Indexer{ID: 1, Name: "A"},
		returnName: &indexers.Indexer{ID: 2, Name: "B"},
	}
	// Body claims id=1 + name="B" — repo says these are two different rows.
	body := strings.NewReader(`{"title":"Z","downloadUrl":"https://x/y","protocol":1,"publishDate":"2026-01-15T00:00:00Z","indexerId":1,"indexer":"B"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(&fakeDecisionMaker{}, &fakeReleaseDispatcher{}, releasecache.New(0), indexerLook, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 (id/name mismatch)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "does not match") {
		t.Errorf("validation envelope should signal the mismatch; got body=%s", rr.Body.String())
	}
}

func TestReleasePushHandler_IndexerResolve_NeitherSupplied_NullReturn(t *testing.T) {
	t.Parallel()
	// No id, no name → resolver returns null and the push proceeds
	// with body fields verbatim (which are also empty).
	dm := &fakeDecisionMaker{returnDecisions: []decisionengine.Decision{
		decisionengine.NewDecision(&decisionengine.Subject{
			Release:           &parser.ReleaseInfo{Title: "Z"},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Z"},
		}),
	}}
	indexerLook := &fakePushIndexerLookup{}
	body := strings.NewReader(`{"title":"Z","downloadUrl":"https://x/y","protocol":1,"publishDate":"2026-01-15T00:00:00Z"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release/push", body)
	ReleasePushHandler(dm, &fakeReleaseDispatcher{}, releasecache.New(0), indexerLook, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (neither id nor name); body=%s", rr.Code, rr.Body.String())
	}
	if indexerLook.callsByID != 0 || indexerLook.callsByName != 0 {
		t.Errorf("no lookups should fire when both id+name absent; got id=%d name=%d", indexerLook.callsByID, indexerLook.callsByName)
	}
}

// resolvePushIndexer / resolvePushDownloadClient unit tests below
// exercise the package-private helper directly with fake lookups.
// Faster than going through the handler for the table-driven shape.

func TestResolvePushIndexer_IDFoundOnlyReturnsByID(t *testing.T) {
	t.Parallel()
	lk := &fakePushIndexerLookup{returnByID: &indexers.Indexer{ID: 7, Name: "FromRepo"}}
	got, vf := resolvePushIndexer(context.Background(), lk, 7, "")
	if len(vf) > 0 {
		t.Fatalf("unexpected vf: %+v", vf)
	}
	if got == nil || got.ID != 7 {
		t.Errorf("got %+v, want id=7", got)
	}
}

func TestResolvePushIndexer_NameFoundOnly_ReturnsByName(t *testing.T) {
	t.Parallel()
	lk := &fakePushIndexerLookup{returnName: &indexers.Indexer{ID: 3, Name: "Indexed"}}
	got, vf := resolvePushIndexer(context.Background(), lk, 0, "Indexed")
	if len(vf) > 0 {
		t.Fatalf("unexpected vf: %+v", vf)
	}
	if got == nil || got.ID != 3 {
		t.Errorf("got %+v, want id=3", got)
	}
}

func TestResolvePushIndexer_BothMatch_PrefersByID(t *testing.T) {
	t.Parallel()
	// Both lookups return the SAME row id → no mismatch → byID wins.
	lk := &fakePushIndexerLookup{
		returnByID: &indexers.Indexer{ID: 5, Name: "X"},
		returnName: &indexers.Indexer{ID: 5, Name: "X"},
	}
	got, vf := resolvePushIndexer(context.Background(), lk, 5, "X")
	if len(vf) > 0 {
		t.Fatalf("unexpected vf: %+v", vf)
	}
	if got == nil || got.ID != 5 {
		t.Errorf("got %+v, want id=5", got)
	}
}

func TestResolvePushIndexer_NameNotFound_Validation(t *testing.T) {
	t.Parallel()
	lk := &fakePushIndexerLookup{errByName: indexers.ErrNotFound}
	_, vf := resolvePushIndexer(context.Background(), lk, 0, "ghost")
	if len(vf) != 1 || vf[0].PropertyName != "Indexer" {
		t.Errorf("want one Indexer-property failure; got %+v", vf)
	}
}

// ----- POST /api/v3/release searchInfo fallback (J343) -----

// fakeParsingFallback implements ReleaseParsingFallback for the
// fallback-ladder tests. Returns a configured episode slice or error
// when called.
type fakeParsingFallback struct {
	got         *parser.ParsedEpisodeInfo
	gotSeries   *series.Series
	gotSceneSrc bool
	returnEps   []episodes.Episode
	err         error
	calls       int
}

func (f *fakeParsingFallback) GetEpisodes(_ context.Context, p *parser.ParsedEpisodeInfo, ser *series.Series, sceneSource bool) ([]episodes.Episode, error) {
	f.got = p
	f.gotSeries = ser
	f.gotSceneSrc = sceneSource
	f.calls++
	return f.returnEps, f.err
}

// fakeSingleEpisodeLookup implements ReleaseSingleEpisodeLookup.
type fakeSingleEpisodeLookup struct {
	gotID    int
	returnEp *episodes.Episode
	err      error
	calls    int
}

func (f *fakeSingleEpisodeLookup) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	f.gotID = id
	f.calls++
	return f.returnEp, f.err
}

// cachedBareSubject seeds a cache entry for a given indexerId+guid with
// a Subject that has only Release + ParsedEpisodeInfo populated (Series
// nil, Episodes empty) — the shape that triggers the fallback ladder.
func cachedBareSubject(t *testing.T, cache *releasecache.Cache, indexerID int, guid string) {
	t.Helper()
	cache.Put(releasecache.Key(indexerID, guid), decisionengine.NewDecision(&decisionengine.Subject{
		Release:           &parser.ReleaseInfo{Title: "X.S01E01", DownloadURL: "https://example.com/x.torrent"},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{SeasonNumber: 1, EpisodeNumbers: []int{1}, ReleaseTitle: "X.S01E01"},
	}))
}

func TestReleaseDownloadHandler_Fallback_SeriesNil_EpisodeIDLookup(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cachedBareSubject(t, cache, 42, "abc")

	disp := &fakeReleaseDispatcher{}
	sl := &fakeOverrideSeriesLookup{returnS: &series.Series{ID: 7, Title: "Lookup"}}
	singleEp := &fakeSingleEpisodeLookup{returnEp: &episodes.Episode{ID: 10, SeriesID: 7}}
	pf := &fakeParsingFallback{}

	// Body carries episodeId=10 (no seriesId) — Series-nil arm 1.
	bodyJSON := `{"indexerId":42,"guid":"abc","episodeId":10}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, disp, sl, nil, pf, singleEp).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if singleEp.calls != 1 || singleEp.gotID != 10 {
		t.Errorf("single-ep lookup: calls=%d gotID=%d", singleEp.calls, singleEp.gotID)
	}
	if sl.got != 7 {
		t.Errorf("series lookup: got id=%d, want 7 (the episode's SeriesID)", sl.got)
	}
	if pf.calls != 0 {
		t.Errorf("parsing fallback should NOT fire when episodeId arm matches")
	}
	if len(disp.calls) != 1 {
		t.Fatalf("dispatcher: want 1 call, got %d", len(disp.calls))
	}
	subj := disp.calls[0][0].Subject.(*decisionengine.Subject)
	if subj.Series == nil || subj.Series.ID != 7 {
		t.Errorf("dispatched Subject.Series.ID=%v, want 7", subj.Series)
	}
	if len(subj.Episodes) != 1 || subj.Episodes[0].ID != 10 {
		t.Errorf("dispatched Subject.Episodes=%v, want [{10}]", subj.Episodes)
	}
}

func TestReleaseDownloadHandler_Fallback_SeriesNil_SeriesIDLookup(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cachedBareSubject(t, cache, 42, "abc")

	disp := &fakeReleaseDispatcher{}
	sl := &fakeOverrideSeriesLookup{returnS: &series.Series{ID: 99, Title: "ParsedSeries"}}
	pf := &fakeParsingFallback{returnEps: []episodes.Episode{{ID: 11}, {ID: 12}}}

	bodyJSON := `{"indexerId":42,"guid":"abc","seriesId":99}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, disp, sl, nil, pf, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if sl.got != 99 {
		t.Errorf("series lookup id: got %d, want 99", sl.got)
	}
	if pf.calls != 1 || !pf.gotSceneSrc {
		t.Errorf("parsing fallback: calls=%d sceneSource=%v, want 1/true", pf.calls, pf.gotSceneSrc)
	}
	subj := disp.calls[0][0].Subject.(*decisionengine.Subject)
	if subj.Series == nil || subj.Series.ID != 99 {
		t.Errorf("dispatched Subject.Series.ID=%v, want 99", subj.Series)
	}
	if len(subj.Episodes) != 2 {
		t.Errorf("dispatched Subject.Episodes len=%d, want 2", len(subj.Episodes))
	}
}

func TestReleaseDownloadHandler_Fallback_SeriesNil_SeriesIDParsingEmpty_404(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cachedBareSubject(t, cache, 42, "abc")

	sl := &fakeOverrideSeriesLookup{returnS: &series.Series{ID: 99}}
	pf := &fakeParsingFallback{returnEps: nil}

	bodyJSON := `{"indexerId":42,"guid":"abc","seriesId":99}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, &fakeReleaseDispatcher{}, sl, nil, pf, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Unable to parse episodes") {
		t.Errorf("body should match C# `:130` message: %s", rr.Body.String())
	}
}

func TestReleaseDownloadHandler_Fallback_SeriesNil_NeitherID_404(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cachedBareSubject(t, cache, 42, "abc")

	bodyJSON := `{"indexerId":42,"guid":"abc"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, &fakeReleaseDispatcher{}, nil, nil, nil, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Unable to find matching series") {
		t.Errorf("body should match C# `:138` message: %s", rr.Body.String())
	}
}

func TestReleaseDownloadHandler_Fallback_EpisodesEmpty_ParsingPopulates(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	// Series populated, Episodes empty — C# `:141` else-if branch.
	cache.Put(releasecache.Key(42, "abc"), decisionengine.NewDecision(&decisionengine.Subject{
		Release:           &parser.ReleaseInfo{Title: "X.S01E01"},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{SeasonNumber: 1, ReleaseTitle: "X.S01E01"},
		Series:            &series.Series{ID: 7, Title: "Cached"},
	}))

	disp := &fakeReleaseDispatcher{}
	pf := &fakeParsingFallback{returnEps: []episodes.Episode{{ID: 21}, {ID: 22}}}

	bodyJSON := `{"indexerId":42,"guid":"abc"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, disp, nil, nil, pf, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if pf.calls != 1 || pf.gotSeries == nil || pf.gotSeries.ID != 7 {
		t.Errorf("parsing fallback: calls=%d gotSeries=%v", pf.calls, pf.gotSeries)
	}
	subj := disp.calls[0][0].Subject.(*decisionengine.Subject)
	if len(subj.Episodes) != 2 {
		t.Errorf("dispatched Subject.Episodes len=%d, want 2", len(subj.Episodes))
	}
}

func TestReleaseDownloadHandler_Fallback_EpisodesEmpty_ParsingEmpty_EpisodeIDFallback(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cache.Put(releasecache.Key(42, "abc"), decisionengine.NewDecision(&decisionengine.Subject{
		Release:           &parser.ReleaseInfo{Title: "X.S01E01"},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "X.S01E01"},
		Series:            &series.Series{ID: 7},
	}))

	disp := &fakeReleaseDispatcher{}
	pf := &fakeParsingFallback{returnEps: nil}
	singleEp := &fakeSingleEpisodeLookup{returnEp: &episodes.Episode{ID: 33}}

	// Body carries episodeId — C# `:145` inner fallback.
	bodyJSON := `{"indexerId":42,"guid":"abc","episodeId":33}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, disp, nil, nil, pf, singleEp).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if pf.calls != 1 {
		t.Errorf("parsing fallback should fire once first; got calls=%d", pf.calls)
	}
	if singleEp.calls != 1 || singleEp.gotID != 33 {
		t.Errorf("single-ep lookup: calls=%d gotID=%d", singleEp.calls, singleEp.gotID)
	}
	subj := disp.calls[0][0].Subject.(*decisionengine.Subject)
	if len(subj.Episodes) != 1 || subj.Episodes[0].ID != 33 {
		t.Errorf("dispatched Subject.Episodes=%v, want [{33}]", subj.Episodes)
	}
}

func TestReleaseDownloadHandler_Fallback_EpisodesEmpty_NoFallbackAvailable_404(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cache.Put(releasecache.Key(42, "abc"), decisionengine.NewDecision(&decisionengine.Subject{
		Release:           &parser.ReleaseInfo{Title: "X.S01E01"},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "X.S01E01"},
		Series:            &series.Series{ID: 7},
	}))

	pf := &fakeParsingFallback{returnEps: nil}

	bodyJSON := `{"indexerId":42,"guid":"abc"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, &fakeReleaseDispatcher{}, nil, nil, pf, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestReleaseDownloadHandler_Fallback_NilDeps_BrokenSubject_503(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	cachedBareSubject(t, cache, 42, "abc")

	// Body asks for episodeId arm but deps are nil — 503.
	bodyJSON := `{"indexerId":42,"guid":"abc","episodeId":10}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, &fakeReleaseDispatcher{}, nil, nil, nil, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want 503; body=%s", rr.Code, rr.Body.String())
	}
}

func TestReleaseDownloadHandler_Fallback_CachedSubjectNotMutated(t *testing.T) {
	t.Parallel()
	cache := releasecache.New(0)
	origSubj := &decisionengine.Subject{
		Release:           &parser.ReleaseInfo{Title: "X.S01E01"},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "X.S01E01"},
		Series:            &series.Series{ID: 7},
	}
	cache.Put(releasecache.Key(42, "abc"), decisionengine.NewDecision(origSubj))

	pf := &fakeParsingFallback{returnEps: []episodes.Episode{{ID: 5}}}
	bodyJSON := `{"indexerId":42,"guid":"abc"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/release", strings.NewReader(bodyJSON))
	ReleaseDownloadHandler(cache, &fakeReleaseDispatcher{}, nil, nil, pf, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	// Cache invariant — the original Subject must not be mutated; the
	// next replay shouldn't see the fallback's Episodes injection.
	if len(origSubj.Episodes) != 0 {
		t.Errorf("cached Subject was mutated; expected pristine, got Episodes=%v", origSubj.Episodes)
	}
}
