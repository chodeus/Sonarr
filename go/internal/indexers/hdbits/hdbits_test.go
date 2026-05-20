package hdbits_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/hdbits"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
	"sonarr.tv/sonarr/internal/series"
)

type stubClient struct {
	postFn   func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error)
	last     *httpclient.Request
	lastBody []byte
}

func (s *stubClient) Execute(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	s.last = req
	s.lastBody = append([]byte(nil), req.ContentData...)
	return s.postFn(ctx, req)
}
func (s *stubClient) Get(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	s.last = req
	s.lastBody = append([]byte(nil), req.ContentData...)
	return s.postFn(ctx, req)
}
func (s *stubClient) Head(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.postFn(ctx, req)
}
func (s *stubClient) Post(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	s.last = req
	s.lastBody = append([]byte(nil), req.ContentData...)
	return s.postFn(ctx, req)
}
func (s *stubClient) DownloadFile(_ context.Context, _, _ string) error {
	return errors.New("not implemented")
}

// minimalFeed is one HDBits success response with a single torrent.
const minimalFeed = `{
  "status": 0,
  "message": "",
  "data": [
    {
      "id": "1234567",
      "hash": "deadbeef",
      "leechers": 5,
      "seeders": 42,
      "name": "Show.S01E01.1080p.WEB-DL-GROUP",
      "size": 1234567890,
      "added": "2025-05-19 12:00:00",
      "freeleech": "yes",
      "type_origin": 1,
      "tvdb": {"id": 999, "season": 1, "episode": 1}
    }
  ]
}`

const errorFeed = `{
  "status": 5,
  "message": "Auth failed",
  "data": null
}`

func mkResponse(req *httpclient.Request, body []byte) *httpclient.Response {
	h := httpclient.NewHeader()
	h.SetContentType("application/json; charset=utf-8")
	return httpclient.NewResponseFromBytes(req, h, body, http.StatusOK, "1.1")
}

// --- Settings ---

func TestSettings_Defaults(t *testing.T) {
	t.Parallel()
	s := hdbits.NewSettings()
	if s.BaseUrl != "https://hdbits.org" {
		t.Errorf("BaseUrl = %q", s.BaseUrl)
	}
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d", s.MinimumSeeders)
	}
	wantCats := []int{hdbits.CategoryTV, hdbits.CategoryDocumentary}
	if len(s.Categories) != 2 || s.Categories[0] != wantCats[0] || s.Categories[1] != wantCats[1] {
		t.Errorf("Categories = %v, want %v", s.Categories, wantCats)
	}
}

func TestSettings_ValidateRequiresApiKey(t *testing.T) {
	t.Parallel()
	s := hdbits.NewSettings()
	r := s.Validate()
	if r.IsValid() {
		t.Error("default Settings (empty ApiKey) should not validate")
	}
	for _, f := range r.Errors {
		if f.PropertyName == "ApiKey" {
			return
		}
	}
	t.Errorf("expected ApiKey failure; got %+v", r.Errors)
}

func TestSettings_ValidateAcceptsValidConfig(t *testing.T) {
	t.Parallel()
	s := hdbits.NewSettings()
	s.ApiKey = "secret"
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("expected valid; got %+v", r.Errors)
	}
}

// --- Indexer ---

func TestIndexer_NameAndContract(t *testing.T) {
	t.Parallel()
	i := hdbits.NewIndexer(nil)
	if got := i.Name(); got != "HDBits" {
		t.Errorf("Name = %q", got)
	}
	if got := i.ConfigContract(); got != "HDBitsSettings" {
		t.Errorf("ConfigContract = %q", got)
	}
}

func TestIndexer_DefaultDefinitionsShape(t *testing.T) {
	t.Parallel()
	i := hdbits.NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Fatalf("len = %d", len(defs))
	}
	d := defs[0]
	if d.Implementation != "HDBits" || d.ConfigContract != "HDBitsSettings" {
		t.Errorf("got %+v", d)
	}
}

func TestIndexer_TestUnboundFails(t *testing.T) {
	t.Parallel()
	i := hdbits.NewIndexer(nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("Test on unbound indexer should fail")
	}
}

func TestIndexer_FetchRecentPOSTsJSONBody(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "alice"
	s.ApiKey = "secret"
	s.Codecs = []int{hdbits.CodecH264}
	s.Mediums = []int{hdbits.MediumWebDL}
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 release; got %d", len(rels))
	}
	// Method should be POST.
	if stub.last.Method != "POST" {
		t.Errorf("Method = %q, want POST", stub.last.Method)
	}
	// URL is /api/torrents (no query string).
	got := stub.last.URL.String()
	want := "https://hdbits.org/api/torrents"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	// Body should be JSON with auth + filter params.
	var body map[string]any
	if err := json.Unmarshal(stub.lastBody, &body); err != nil {
		t.Fatalf("body did not parse as JSON: %v (raw=%q)", err, stub.lastBody)
	}
	if body["username"] != "alice" {
		t.Errorf("body.username = %v", body["username"])
	}
	if body["passkey"] != "secret" {
		t.Errorf("body.passkey = %v", body["passkey"])
	}
	if v, ok := body["limit"].(float64); !ok || int(v) != 100 {
		t.Errorf("body.limit = %v, want 100", body["limit"])
	}
	// category / codec / medium come through as JSON arrays.
	if cats, ok := body["category"].([]any); !ok || len(cats) != 2 {
		t.Errorf("body.category = %v", body["category"])
	}
	if codecs, ok := body["codec"].([]any); !ok || len(codecs) != 1 {
		t.Errorf("body.codec = %v", body["codec"])
	}
}

func TestIndexer_FetchRecentParsesResponseFields(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "secret"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	r := rels[0]
	if r.Guid != "HDBits-1234567" {
		t.Errorf("Guid = %q", r.Guid)
	}
	if r.Title != "Show.S01E01.1080p.WEB-DL-GROUP" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Size != 1234567890 {
		t.Errorf("Size = %d", r.Size)
	}
	if r.InfoHash != "deadbeef" {
		t.Errorf("InfoHash = %q", r.InfoHash)
	}
	if !strings.Contains(r.DownloadURL, "passkey=secret") {
		t.Errorf("DownloadURL missing passkey: %q", r.DownloadURL)
	}
	if r.Seeders == nil || *r.Seeders != 42 {
		t.Errorf("Seeders = %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 47 {
		t.Errorf("Peers = %v, want 47", r.Peers)
	}
	if r.TvdbID != 999 {
		t.Errorf("TvdbID = %d, want 999", r.TvdbID)
	}
	// FreeLeech="yes" + TypeOrigin=1 → both flags.
	wantFlags := parser.IndexerFlagFreeleech | parser.IndexerFlagInternal
	if r.IndexerFlags != wantFlags {
		t.Errorf("IndexerFlags = %v, want %v", r.IndexerFlags, wantFlags)
	}
}

func TestIndexer_FetchRecentErrorResponse(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(errorFeed)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)
	_, err := i.FetchRecent(context.Background())
	if err == nil {
		t.Fatal("expected error on status != Success")
	}
	if !strings.Contains(err.Error(), "Auth failed") {
		t.Errorf("error should include message; got %v", err)
	}
}

func TestIndexer_FetchRecentInvalidJSONErrors(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(`{not-json`)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected JSON parse error")
	}
}

func TestIndexer_FetchSearch_UnknownCriteriaEmpty(t *testing.T) {
	t.Parallel()
	i := hdbits.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)
	rels, err := i.FetchSearch(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("Unknown criteria should yield empty; got %d", len(rels))
	}
}

func TestIndexer_FetchSearch_NoTvdbIDSkipsHTTP(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			t.Errorf("HTTP should not be called without TvdbID; got %s", req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SingleEpisodeCriteria{
		SeasonNumber:  1,
		EpisodeNumber: 5,
	}
	c.Episodes = []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 5}}
	c.Series = &series.Series{} // TvdbID=0
	rels, err := i.FetchSearch(context.Background(), c)
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected empty; got %d", len(rels))
	}
}

func TestIndexer_FetchSearch_SingleEpisodeFanout(t *testing.T) {
	t.Parallel()
	bodies := []map[string]any{}
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			var body map[string]any
			_ = json.Unmarshal(req.ContentData, &body)
			bodies = append(bodies, body)
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SingleEpisodeCriteria{
		SeasonNumber:  1,
		EpisodeNumber: 5,
	}
	c.Episodes = []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 5},
		{SeasonNumber: 1, EpisodeNumber: 6},
	}
	c.Series = &series.Series{TvdbID: 12345}
	rels, err := i.FetchSearch(context.Background(), c)
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 POSTs (1 per episode); got %d", len(bodies))
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 deduped release; got %d", len(rels))
	}
	for i, want := range []float64{5, 6} {
		tvdb, _ := bodies[i]["tvdb"].(map[string]any)
		if tvdb["id"].(float64) != 12345 {
			t.Errorf("body[%d].tvdb.id = %v", i, tvdb["id"])
		}
		if tvdb["episode"].(float64) != want {
			t.Errorf("body[%d].tvdb.episode = %v, want %v", i, tvdb["episode"], want)
		}
	}
}

func TestIndexer_FetchSearch_SeasonFanoutDistinct(t *testing.T) {
	t.Parallel()
	bodies := []map[string]any{}
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			var body map[string]any
			_ = json.Unmarshal(req.ContentData, &body)
			bodies = append(bodies, body)
			return mkResponse(req, []byte(`{"status":0,"data":[]}`)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SeasonCriteria{SeasonNumber: 1}
	c.Series = &series.Series{TvdbID: 42}
	c.Episodes = []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1},
		{SeasonNumber: 1, EpisodeNumber: 2},
		{SeasonNumber: 2, EpisodeNumber: 1},
	}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 POSTs (1 per distinct season); got %d", len(bodies))
	}
	seasons := []float64{
		bodies[0]["tvdb"].(map[string]any)["season"].(float64),
		bodies[1]["tvdb"].(map[string]any)["season"].(float64),
	}
	if seasons[0] != 1 || seasons[1] != 2 {
		t.Errorf("seasons = %v, want [1 2]", seasons)
	}
}

func TestIndexer_FetchSearch_DailyEpisodeUsesAirdateSearch(t *testing.T) {
	t.Parallel()
	var body map[string]any
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			_ = json.Unmarshal(req.ContentData, &body)
			return mkResponse(req, []byte(`{"status":0,"data":[]}`)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)

	c := &indexersearch.DailyEpisodeCriteria{
		AirDate: time.Date(2025, 5, 19, 0, 0, 0, 0, time.UTC),
	}
	c.Series = &series.Series{TvdbID: 42}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if body["search"] != "2025-05-19" {
		t.Errorf("body.search = %v, want 2025-05-19", body["search"])
	}
}

func TestIndexer_FetchSearch_DailySeasonUsesYearSearch(t *testing.T) {
	t.Parallel()
	var body map[string]any
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			_ = json.Unmarshal(req.ContentData, &body)
			return mkResponse(req, []byte(`{"status":0,"data":[]}`)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)

	c := &indexersearch.DailySeasonCriteria{Year: 2025}
	c.Series = &series.Series{TvdbID: 42}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if body["search"] != "2025-" {
		t.Errorf("body.search = %v, want \"2025-\"", body["search"])
	}
}

func TestIndexer_FetchSearch_AnimeEpisodePreservesCSharpBug(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			t.Errorf("HTTP should not fire for AnimeEpisode (C# bug preserved); got %s", req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)

	c := &indexersearch.AnimeEpisodeCriteria{
		AbsoluteEpisodeNumber: 7,
		SeasonNumber:          1,
		EpisodeNumber:         5,
	}
	c.Series = &series.Series{TvdbID: 42}
	c.Episodes = []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 5}}
	rels, err := i.FetchSearch(context.Background(), c)
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected empty (C# bug); got %d", len(rels))
	}
}

func TestIndexer_FetchSearch_AnimeSeasonFanout(t *testing.T) {
	t.Parallel()
	bodies := []map[string]any{}
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			var body map[string]any
			_ = json.Unmarshal(req.ContentData, &body)
			bodies = append(bodies, body)
			return mkResponse(req, []byte(`{"status":0,"data":[]}`)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)

	c := &indexersearch.AnimeSeasonCriteria{SeasonNumber: 1}
	c.Series = &series.Series{TvdbID: 42}
	c.Episodes = []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1},
		{SeasonNumber: 1, EpisodeNumber: 2},
	}
	if _, err := i.FetchSearch(context.Background(), c); err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(bodies) != 1 {
		t.Errorf("expected 1 POST (1 distinct season); got %d", len(bodies))
	}
}

func TestIndexer_FetchSearch_SpecialEpisodeEmpty(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		postFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			t.Errorf("HTTP should not fire for SpecialEpisode; got %s", req.URL.String())
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := hdbits.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)

	c := &indexersearch.SpecialEpisodeCriteria{EpisodeQueryTitles: []string{"Show OVA"}}
	c.Series = &series.Series{TvdbID: 42}
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
	i := hdbits.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*hdbits.Settings)
	s.Username = "u"
	s.ApiKey = "p"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on nil HTTPClient")
	}
}

func TestRegistry_ConstructorRegistered(t *testing.T) {
	t.Parallel()
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: hdbits.Implementation, Contract: hdbits.ConfigContract})
	if !ok {
		t.Fatal("HDBits not registered in provider.Default()")
	}
	if _, ok := ctor().(*hdbits.Settings); !ok {
		t.Errorf("Constructor returned %T", ctor())
	}
}
