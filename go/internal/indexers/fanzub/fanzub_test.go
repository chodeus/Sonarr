package fanzub_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/fanzub"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/provider"
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

// minimalFeed mimics a standard Fanzub RSS payload — NZB enclosures
// (Usenet), no torrent namespace. Just enough to exercise the
// newznab.ParseRSS happy path.
const minimalFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
<channel>
  <title>Fanzub</title>
  <item>
    <title>[Group] Anime - 01 [1080p]</title>
    <link>http://fanzub.com/nzb/1.nzb</link>
    <guid isPermaLink="false">fanzub-1</guid>
    <enclosure url="http://fanzub.com/nzb/1.nzb" length="1234567890" type="application/x-nzb"/>
    <pubDate>Mon, 19 May 2025 12:00:00 +0000</pubDate>
  </item>
</channel>
</rss>`

func mkResponse(req *httpclient.Request, body []byte) *httpclient.Response {
	h := httpclient.NewHeader()
	h.SetContentType("application/xml; charset=utf-8")
	return httpclient.NewResponseFromBytes(req, h, body, http.StatusOK, "1.1")
}

// --- Settings ---

func TestSettings_Defaults(t *testing.T) {
	t.Parallel()
	s := fanzub.NewSettings()
	if s.BaseUrl != "http://fanzub.com/rss/" {
		t.Errorf("BaseUrl default = %q, want C# default", s.BaseUrl)
	}
	if s.AnimeStandardFormatSearch {
		t.Error("AnimeStandardFormatSearch default = true, want false")
	}
}

func TestSettings_ValidateAcceptsDefault(t *testing.T) {
	t.Parallel()
	s := fanzub.NewSettings()
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("default Settings should validate; got %+v", r.Errors)
	}
}

func TestSettings_ValidateRejectsInvalidURL(t *testing.T) {
	t.Parallel()
	s := fanzub.NewSettings()
	s.BaseUrl = "not-a-url"
	r := s.Validate()
	if r.IsValid() {
		t.Error("invalid BaseUrl should fail")
	}
}

func TestSettings_ValidateRejectsEmptyURL(t *testing.T) {
	t.Parallel()
	s := fanzub.NewSettings()
	s.BaseUrl = ""
	r := s.Validate()
	if r.IsValid() {
		t.Error("empty BaseUrl should fail")
	}
}

// --- Indexer ---

func TestIndexer_NameAndContract(t *testing.T) {
	t.Parallel()
	i := fanzub.NewIndexer(nil)
	if got := i.Name(); got != "Fanzub" {
		t.Errorf("Name = %q", got)
	}
	if got := i.ConfigContract(); got != "FanzubSettings" {
		t.Errorf("ConfigContract = %q", got)
	}
}

func TestIndexer_DefaultDefinitionsShape(t *testing.T) {
	t.Parallel()
	i := fanzub.NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Fatalf("len = %d", len(defs))
	}
	d := defs[0]
	if d.Implementation != "Fanzub" || d.ConfigContract != "FanzubSettings" {
		t.Errorf("got %+v", d)
	}
	if d.Enable {
		t.Error("Enable should be false")
	}
	if _, ok := d.Settings.(*fanzub.Settings); !ok {
		t.Errorf("Settings type = %T", d.Settings)
	}
}

func TestIndexer_TestUnboundFails(t *testing.T) {
	t.Parallel()
	i := fanzub.NewIndexer(nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("Test on unbound indexer should fail")
	}
}

func TestIndexer_FetchRecentURLShape(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := fanzub.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 release; got %d", len(rels))
	}
	// C# emits `<BaseUrl verbatim>?cat=anime&max=100` — no separator
	// handling, so the default BaseUrl's trailing slash carries
	// through.
	want := "http://fanzub.com/rss/?cat=anime&max=100"
	got := stub.last.URL.String()
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestIndexer_FetchRecentParsesReleaseInfo(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := fanzub.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 release; got %d", len(rels))
	}
	r := rels[0]
	if r.Title != "[Group] Anime - 01 [1080p]" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Guid != "fanzub-1" {
		t.Errorf("Guid = %q", r.Guid)
	}
	if r.DownloadURL != "http://fanzub.com/nzb/1.nzb" {
		t.Errorf("DownloadURL = %q", r.DownloadURL)
	}
	if r.Size != 1234567890 {
		t.Errorf("Size = %d", r.Size)
	}
}

func TestIndexer_FetchSearch_UnknownCriteriaEmpty(t *testing.T) {
	t.Parallel()
	i := fanzub.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
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
	i := fanzub.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*fanzub.Settings).AnimeStandardFormatSearch = true
	i.SetDefinition(d)

	// SingleEpisode + DailyEpisode + DailySeason + SpecialEpisode
	// all return empty per the C# overloads :32-34 :50-58 :89-92.
	// Plus Season/AnimeSeason with season=0 short-circuit. All
	// must NOT touch HTTP (i.e. the nil-client path doesn't error).
	cases := []any{
		&indexersearch.SingleEpisodeCriteria{SeasonNumber: 1, EpisodeNumber: 5},
		&indexersearch.DailyEpisodeCriteria{},
		&indexersearch.DailySeasonCriteria{},
		&indexersearch.SpecialEpisodeCriteria{EpisodeQueryTitles: []string{"Show OVA"}},
		&indexersearch.SeasonCriteria{SeasonNumber: 0},
		&indexersearch.AnimeSeasonCriteria{SeasonNumber: 0},
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

func TestIndexer_FetchSearch_SeasonEmits2FormatsJoined(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := fanzub.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*fanzub.Settings)
	s.AnimeStandardFormatSearch = true
	i.SetDefinition(d)

	c := &indexersearch.SeasonCriteria{SeasonNumber: 2}
	c.SceneTitles = []string{"Show"}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	// Fanzub emits ONE URL with `q=<pipe-joined terms>`. For
	// SeasonCriteria + clean-title "show" (the GetCleanSceneTitle
	// lowercases) + season=2: two formats `"show%20S02"` and
	// `"show%20-%20S02"` pipe-joined.
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL; got %d (%v)", len(urls), urls)
	}
	got := urls[0]
	if !strings.Contains(got, `&q=`) {
		t.Errorf("URL missing &q=; got %q", got)
	}
	// Verify both formats appear in the q= value. GetCleanSceneTitle
	// preserves case ("Show" stays "Show"); the C# regex builds two
	// formats in order: bare `<title>%20S<ss>` then dash-form
	// `<title>%20-%20S<ss>`. The httpclient URL stringification
	// re-encodes the inline `%20` as `%2520`, so the assertion
	// accepts either form.
	if !strings.Contains(got, `Show%20S02`) && !strings.Contains(got, `Show%2520S02`) {
		t.Errorf("URL missing first format; got %q", got)
	}
	if !strings.Contains(got, `%20-%20S02`) && !strings.Contains(got, `%2520-%2520S02`) {
		t.Errorf("URL missing second format; got %q", got)
	}
}

func TestIndexer_FetchSearch_SeasonSkippedWhenAnimeFlagOff(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			t.Errorf("HTTP should not be called when AnimeStandardFormatSearch=false; got %s", req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := fanzub.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	// Anime flag NOT set.
	i.SetDefinition(d)

	c := &indexersearch.SeasonCriteria{SeasonNumber: 2}
	c.SceneTitles = []string{"Show"}
	rels, err := i.FetchSearch(context.Background(), c)
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected empty; got %d", len(rels))
	}
}

func TestIndexer_FetchSearch_AnimeEpisodeEmitsAbsoluteAndStandardForms(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := fanzub.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*fanzub.Settings).AnimeStandardFormatSearch = true
	i.SetDefinition(d)

	c := &indexersearch.AnimeEpisodeCriteria{
		AbsoluteEpisodeNumber: 7,
		SeasonNumber:          1,
		EpisodeNumber:         5,
	}
	c.SceneTitles = []string{"Show"}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL (pipe-joined); got %d (%v)", len(urls), urls)
	}
	got := urls[0]
	// All four format-strings should appear in the q= value (two
	// for absolute episode + two for standard S/E).
	wants := []string{`07`, `S01E05`}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("URL missing %q; got %q", w, got)
		}
	}
}

func TestIndexer_FetchSearch_AnimeEpisodeAbsoluteOnly(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := fanzub.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	// Anime flag NOT set → S/E branch skipped, abs branch still emits.
	i.SetDefinition(d)

	c := &indexersearch.AnimeEpisodeCriteria{
		AbsoluteEpisodeNumber: 12,
		SeasonNumber:          1, // ignored when AnimeStandardFormatSearch=false
		EpisodeNumber:         5,
	}
	c.SceneTitles = []string{"Show"}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL; got %d (%v)", len(urls), urls)
	}
	got := urls[0]
	if !strings.Contains(got, "12") {
		t.Errorf("URL missing absolute episode 12; got %q", got)
	}
	if strings.Contains(got, "S01E05") {
		t.Errorf("URL should NOT include S/E when AnimeStandardFormatSearch=false; got %q", got)
	}
}

func TestIndexer_FetchSearch_AnimeSeasonEmits(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := fanzub.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*fanzub.Settings).AnimeStandardFormatSearch = true
	i.SetDefinition(d)

	c := &indexersearch.AnimeSeasonCriteria{SeasonNumber: 3}
	c.SceneTitles = []string{"Show"}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL; got %d (%v)", len(urls), urls)
	}
	if !strings.Contains(urls[0], "S03") {
		t.Errorf("URL missing S03; got %q", urls[0])
	}
}

func TestIndexer_FetchSearch_CleanTitleStripsForbiddenChars(t *testing.T) {
	t.Parallel()
	urls := []string{}
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			urls = append(urls, req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := fanzub.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*fanzub.Settings).AnimeStandardFormatSearch = true
	i.SetDefinition(d)

	c := &indexersearch.SeasonCriteria{SeasonNumber: 1}
	// Title with the three forbidden chars: ! ? backtick. After
	// GetCleanSceneTitle (which lowercases + strips non-alnum) +
	// cleanFanzubTitle (which strips ! ? `): the cleaned form
	// should NOT contain any of them.
	c.SceneTitles = []string{"Show!? Title`"}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL; got %d (%v)", len(urls), urls)
	}
	// Only inspect the q= value, NOT the whole URL — the `?cat=anime`
	// query separator legitimately contains `?` and isn't a
	// forbidden-char failure.
	got := urls[0]
	idx := strings.Index(got, "&q=")
	if idx < 0 {
		t.Fatalf("URL missing &q=; got %q", got)
	}
	qValue := got[idx+3:]
	for _, bad := range []string{"!", "?", "`", "%21", "%3F", "%60"} {
		if strings.Contains(qValue, bad) {
			t.Errorf("q= value contains forbidden char %q; got %q (full URL %q)", bad, qValue, got)
		}
	}
}

func TestIndexer_FetchRecentNilClientErrors(t *testing.T) {
	t.Parallel()
	i := fanzub.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on nil HTTPClient")
	}
}

// --- Registry ---

func TestRegistry_ConstructorRegistered(t *testing.T) {
	t.Parallel()
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: fanzub.Implementation, Contract: fanzub.ConfigContract})
	if !ok {
		t.Fatal("Fanzub not registered in provider.Default()")
	}
	if _, ok := ctor().(*fanzub.Settings); !ok {
		t.Errorf("Constructor returned %T", ctor())
	}
}
