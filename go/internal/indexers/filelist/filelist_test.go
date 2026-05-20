package filelist_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/filelist"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
	"sonarr.tv/sonarr/internal/series"
)

type stubClient struct {
	getFn func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error)
	last  *httpclient.Request
}

func (s *stubClient) Execute(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	s.last = req
	return s.getFn(ctx, req)
}
func (s *stubClient) Get(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	s.last = req
	return s.getFn(ctx, req)
}
func (s *stubClient) Head(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.getFn(ctx, req)
}
func (s *stubClient) Post(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.getFn(ctx, req)
}
func (s *stubClient) DownloadFile(_ context.Context, _, _ string) error {
	return errors.New("not implemented")
}

// minimalFeed is one FileList torrent record — exercises every
// field the parser maps (id/name/size/leechers/seeders/imdb/
// freeleech/internal/upload_date).
const minimalFeed = `[{
  "id": "1234567",
  "name": "Show.S01E01.1080p.WEB-DL-GROUP",
  "size": 1234567890,
  "leechers": 5,
  "seeders": 42,
  "times_completed": 100,
  "comments": 0,
  "files": 1,
  "imdb": "tt0903747",
  "internal": true,
  "freeleech": false,
  "upload_date": "2025-05-19T12:00:00"
}]`

func mkResponse(req *httpclient.Request, body []byte) *httpclient.Response {
	h := httpclient.NewHeader()
	h.SetContentType("application/json; charset=utf-8")
	return httpclient.NewResponseFromBytes(req, h, body, http.StatusOK, "1.1")
}

// --- Settings ---

func TestSettings_Defaults(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	if s.BaseUrl != "https://filelist.io" {
		t.Errorf("BaseUrl = %q", s.BaseUrl)
	}
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d", s.MinimumSeeders)
	}
	wantCats := []int{filelist.CategoryTVSD, filelist.CategoryTVHD, filelist.CategoryTV4K}
	if len(s.Categories) != 3 {
		t.Errorf("Categories = %v, want %v", s.Categories, wantCats)
	}
	for i, c := range wantCats {
		if s.Categories[i] != c {
			t.Errorf("Categories[%d] = %d, want %d", i, s.Categories[i], c)
		}
	}
}

func TestSettings_ValidateRequiresUsernameAndPasskey(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	r := s.Validate()
	if r.IsValid() {
		t.Error("default Settings (empty Username/Passkey) should not validate")
	}
	usernameFail, passkeyFail := false, false
	for _, f := range r.Errors {
		if f.PropertyName == "Username" {
			usernameFail = true
		}
		if f.PropertyName == "Passkey" {
			passkeyFail = true
		}
	}
	if !usernameFail || !passkeyFail {
		t.Errorf("expected Username AND Passkey failures; got %+v", r.Errors)
	}
}

func TestSettings_ValidateRequiresCategories(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	s.Username = "u"
	s.Passkey = "p"
	s.Categories = []int{}
	s.AnimeCategories = []int{}
	r := s.Validate()
	if r.IsValid() {
		t.Error("empty Categories AND AnimeCategories should fail")
	}
}

func TestSettings_ValidateAnimeCategoriesAlone(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	s.Username = "u"
	s.Passkey = "p"
	s.Categories = []int{}
	s.AnimeCategories = []int{filelist.CategoryAnime}
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("anime-only categories should validate; got %+v", r.Errors)
	}
}

func TestSettings_ValidateAcceptsValidConfig(t *testing.T) {
	t.Parallel()
	s := filelist.NewSettings()
	s.Username = "u"
	s.Passkey = "p"
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("default + user/passkey should validate; got %+v", r.Errors)
	}
}

// --- Indexer ---

func TestIndexer_NameAndContract(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	if got := i.Name(); got != "FileList" {
		t.Errorf("Name = %q", got)
	}
	if got := i.ConfigContract(); got != "FileListSettings" {
		t.Errorf("ConfigContract = %q", got)
	}
}

func TestIndexer_DefaultDefinitionsShape(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Fatalf("len = %d", len(defs))
	}
	d := defs[0]
	if d.Implementation != "FileList" || d.ConfigContract != "FileListSettings" {
		t.Errorf("got %+v", d)
	}
}

func TestIndexer_TestUnboundFails(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("Test on unbound indexer should fail")
	}
}

func TestIndexer_FetchRecentURLAndAuth(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "alice"
	s.Passkey = "secret"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 release; got %d", len(rels))
	}
	// URL shape: <BaseUrl>/api.php?action=latest-torrents&category=<csv>
	got := stub.last.URL.String()
	want := "https://filelist.io/api.php?action=latest-torrents&category=23,21,27"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	// Basic auth credentials threaded through.
	if stub.last.BasicAuthUser != "alice" || stub.last.BasicAuthPass != "secret" {
		t.Errorf("basic auth = (%q,%q), want (alice,secret)", stub.last.BasicAuthUser, stub.last.BasicAuthPass)
	}
}

func TestIndexer_FetchRecentParsesAllFields(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "secret"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 release; got %d", len(rels))
	}
	r := rels[0]
	if r.Guid != "FileList-1234567" {
		t.Errorf("Guid = %q", r.Guid)
	}
	if r.Title != "Show.S01E01.1080p.WEB-DL-GROUP" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Size != 1234567890 {
		t.Errorf("Size = %d", r.Size)
	}
	// DownloadURL composes <BaseUrl>/download.php?id=<id>&passkey=<...>.
	if !strings.Contains(r.DownloadURL, "/download.php?") ||
		!strings.Contains(r.DownloadURL, "id=1234567") ||
		!strings.Contains(r.DownloadURL, "passkey=secret") {
		t.Errorf("DownloadURL = %q", r.DownloadURL)
	}
	if !strings.Contains(r.InfoURL, "/details.php?") ||
		!strings.Contains(r.InfoURL, "id=1234567") {
		t.Errorf("InfoURL = %q", r.InfoURL)
	}
	if r.Seeders == nil || *r.Seeders != 42 {
		t.Errorf("Seeders = %v, want 42", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 47 {
		t.Errorf("Peers = %v, want 47 (leechers+seeders)", r.Peers)
	}
	// IMDB-id normaliser produces `tt<7-digit>`.
	if r.ImdbID != "tt0903747" {
		t.Errorf("ImdbID = %q, want tt0903747", r.ImdbID)
	}
	// IndexerFlags = Internal (FreeLeech=false in fixture).
	if r.IndexerFlags != parser.IndexerFlagInternal {
		t.Errorf("IndexerFlags = %v, want Internal-only", r.IndexerFlags)
	}
}

func TestIndexer_FetchRecentEmptyCategoriesSkipsHTTP(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			t.Errorf("HTTP should not be called when categories empty; got %s", req.URL.String())
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	s.Categories = []int{}
	s.AnimeCategories = []int{}
	i.SetDefinition(d)
	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("empty categories should yield empty; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentCategoryUnionDedupes(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	s.Categories = []int{filelist.CategoryTVHD, filelist.CategoryAnime}
	s.AnimeCategories = []int{filelist.CategoryAnime, filelist.CategoryAnimation}
	i.SetDefinition(d)

	if _, err := i.FetchRecent(context.Background()); err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	got := stub.last.URL.String()
	// 21,24 from Categories then 15 from AnimeCategories (24 deduped).
	want := "https://filelist.io/api.php?action=latest-torrents&category=21,24,15"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestIndexer_FetchRecentInvalidJSONErrors(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(`{not-json`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected JSON parse error")
	}
}

func TestIndexer_FetchSearch_UnknownCriteriaEmpty(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)
	rels, err := i.FetchSearch(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("Unknown criteria should yield empty; got %d", len(rels))
	}
}

func TestIndexer_FetchSearch_EmptyCriteriaShape(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)
	cases := []any{
		&indexersearch.DailyEpisodeCriteria{},
		&indexersearch.DailySeasonCriteria{},
		&indexersearch.SpecialEpisodeCriteria{EpisodeQueryTitles: []string{"Show OVA"}},
	}
	for ci, c := range cases {
		rels, err := i.FetchSearch(context.Background(), c)
		if err != nil {
			t.Errorf("case %d (%T): %v", ci, c, err)
		}
		if len(rels) != 0 {
			t.Errorf("case %d (%T): expected empty; got %d", ci, c, len(rels))
		}
	}
}

func TestIndexer_FetchSearch_SingleEpisodeDefaultEmitsIMDBThenNameFallback(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			// Return empty so tier 0 doesn't short-circuit -- the
			// test wants to verify tier 1 fires too.
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SingleEpisodeCriteria{SeasonNumber: 1, EpisodeNumber: 5}
	c.Series = &series.Series{ImdbID: "tt1234567"}
	c.SceneTitles = []string{"Show"}
	// SearchMode=Default (zero value).
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	// Tier 0: IMDB request. Tier 1: Name request (Default fallback).
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs across 2 tiers (IMDB + Name fallback); got %d (%v)", len(urls), urls)
	}
	if !strings.Contains(urls[0], "type=imdb") || !strings.Contains(urls[0], "query=tt1234567") {
		t.Errorf("tier 0 URL should be IMDB; got %q", urls[0])
	}
	if !strings.Contains(urls[1], "type=name") || !strings.Contains(urls[1], "query=Show") {
		t.Errorf("tier 1 URL should be Name; got %q", urls[1])
	}
}

func TestIndexer_FetchSearch_TierShortCircuitsOnFirstNonEmpty(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			// Tier 0 returns one result → tier 1 should NOT fire.
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SingleEpisodeCriteria{SeasonNumber: 1, EpisodeNumber: 5}
	c.Series = &series.Series{ImdbID: "tt1234567"}
	c.SceneTitles = []string{"Show"}
	rels, err := i.FetchSearch(context.Background(), c)
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(urls) != 1 {
		t.Errorf("expected 1 URL (tier 0 short-circuit); got %d", len(urls))
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 release; got %d", len(rels))
	}
}

func TestIndexer_FetchSearch_SearchTitleModeOnly(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SingleEpisodeCriteria{SeasonNumber: 1, EpisodeNumber: 5}
	c.Series = &series.Series{ImdbID: "tt1234567"}
	c.SceneTitles = []string{"Show"}
	c.SearchMode = decisionengine.SearchModeSearchTitle
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	// Only Name search; no IMDB (SearchID flag not set, not Default).
	// No fallback tier (not Default).
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL (Name-only); got %d (%v)", len(urls), urls)
	}
	if !strings.Contains(urls[0], "type=name") {
		t.Errorf("URL should be Name; got %q", urls[0])
	}
}

func TestIndexer_FetchSearch_SearchIDModeOnly(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SingleEpisodeCriteria{SeasonNumber: 1, EpisodeNumber: 5}
	c.Series = &series.Series{ImdbID: "tt1234567"}
	c.SceneTitles = []string{"Show"}
	c.SearchMode = decisionengine.SearchModeSearchID
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL (IMDB-only); got %d (%v)", len(urls), urls)
	}
	if !strings.Contains(urls[0], "type=imdb") {
		t.Errorf("URL should be IMDB; got %q", urls[0])
	}
}

func TestIndexer_FetchSearch_AnimeEpisodeFourTiers(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			// Return empty → all 4 tiers fire.
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	s.AnimeCategories = []int{filelist.CategoryAnime}
	i.SetDefinition(d)

	c := &indexersearch.AnimeEpisodeCriteria{AbsoluteEpisodeNumber: 12}
	c.Series = &series.Series{ImdbID: "tt9999999"}
	c.SceneTitles = []string{"AnimeShow"}
	c.Episodes = []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 12},
	}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	// 4 tiers: IMDB-abs, IMDB-per-episode, Name-abs, Name-per-episode.
	if len(urls) != 4 {
		t.Fatalf("expected 4 URLs across 4 tiers; got %d (%v)", len(urls), urls)
	}
	// First two are IMDB; last two are Name.
	for i, u := range urls[:2] {
		if !strings.Contains(u, "type=imdb") {
			t.Errorf("urls[%d] (tier 0/1) should be IMDB; got %q", i, u)
		}
	}
	for i, u := range urls[2:] {
		if !strings.Contains(u, "type=name") {
			t.Errorf("urls[%d] (tier 2/3) should be Name; got %q", i+2, u)
		}
	}
	// First URL uses abs (season=0&episode=12); second uses per-episode (season=1&episode=12).
	if !strings.Contains(urls[0], "season=0&episode=12") {
		t.Errorf("tier 0 (IMDB abs) should have season=0&episode=12; got %q", urls[0])
	}
	if !strings.Contains(urls[1], "season=1&episode=12") {
		t.Errorf("tier 1 (IMDB per-episode) should have season=1&episode=12; got %q", urls[1])
	}
}

func TestIndexer_FetchSearch_AnimeSeasonTwoTiers(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	s.AnimeCategories = []int{filelist.CategoryAnime}
	i.SetDefinition(d)

	c := &indexersearch.AnimeSeasonCriteria{SeasonNumber: 2}
	c.Series = &series.Series{ImdbID: "tt1234567"}
	c.SceneTitles = []string{"AnimeShow"}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs (IMDB + Name tiers); got %d", len(urls))
	}
	if !strings.Contains(urls[0], "type=imdb") {
		t.Errorf("tier 0 should be IMDB; got %q", urls[0])
	}
	if !strings.Contains(urls[1], "type=name") {
		t.Errorf("tier 1 should be Name; got %q", urls[1])
	}
}

func TestIndexer_FetchSearch_NoImdbSkipsIMDBTier(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SingleEpisodeCriteria{SeasonNumber: 1, EpisodeNumber: 5}
	// NO ImdbID → IMDB tier should be skipped.
	c.Series = &series.Series{}
	c.SceneTitles = []string{"Show"}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	// With Default mode + no ImdbId: tier 0 was IMDB (skipped),
	// tier 1 is Name fallback.
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL (Name fallback only); got %d (%v)", len(urls), urls)
	}
	if !strings.Contains(urls[0], "type=name") {
		t.Errorf("URL should be Name; got %q", urls[0])
	}
}

func TestIndexer_FetchSearch_SearchModeSearchIDSkippedWithoutImdb(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			t.Errorf("HTTP should not be called when SearchID-only mode has no ImdbId; got %s", req.URL.String())
			return mkResponse(req, []byte(`[]`)), nil
		},
	}
	i := filelist.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SingleEpisodeCriteria{SeasonNumber: 1, EpisodeNumber: 5}
	c.Series = &series.Series{} // no ImdbId
	c.SceneTitles = []string{"Show"}
	c.SearchMode = decisionengine.SearchModeSearchID
	rels, err := i.FetchSearch(context.Background(), c)
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected empty; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentNilClientErrors(t *testing.T) {
	t.Parallel()
	i := filelist.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*filelist.Settings)
	s.Username = "u"
	s.Passkey = "p"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on nil HTTPClient")
	}
}

// --- Registry ---

func TestRegistry_ConstructorRegistered(t *testing.T) {
	t.Parallel()
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: filelist.Implementation, Contract: filelist.ConfigContract})
	if !ok {
		t.Fatal("FileList not registered in provider.Default()")
	}
	if _, ok := ctor().(*filelist.Settings); !ok {
		t.Errorf("Constructor returned %T", ctor())
	}
}
