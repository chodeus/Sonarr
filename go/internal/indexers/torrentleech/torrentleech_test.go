package torrentleech_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/torrentleech"
	"sonarr.tv/sonarr/internal/provider"
)

// stubClient is a minimal httpclient.Client test double.
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

// minimalFeed is a 1-item RSS body just enough to round-trip through
// torrentrss.ParseRSS without producing a parse error. The detailed
// parser tests live in torrentrss/parser_test.go; this slice cares
// about the URL-shape + binding behaviour, not the parse output.
const minimalFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
<channel>
  <title>TorrentLeech</title>
  <item>
    <title>Show.S01E01.720p.HDTV-x264</title>
    <link>https://www.torrentleech.org/download/1234567/Show.S01E01.720p.HDTV-x264.torrent</link>
    <guid>https://www.torrentleech.org/torrent/1234567</guid>
    <enclosure url="https://www.torrentleech.org/download/1234567/Show.S01E01.720p.HDTV-x264.torrent" length="1234567890" type="application/x-bittorrent"/>
    <description>Size: 1.15 GB - Seeders: 42 - Leechers: 3</description>
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
	s := torrentleech.NewSettings()
	if s.BaseUrl != "http://rss.torrentleech.org" {
		t.Errorf("BaseUrl = %q, want C# default", s.BaseUrl)
	}
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d, want 1", s.MinimumSeeders)
	}
	if s.ApiKey != "" {
		t.Errorf("ApiKey default = %q, want empty (user must populate)", s.ApiKey)
	}
}

func TestSettings_ValidateRequiresApiKey(t *testing.T) {
	t.Parallel()
	s := torrentleech.NewSettings()
	r := s.Validate()
	if r.IsValid() {
		t.Error("default Settings (empty ApiKey) should not validate")
	}
	if !hasFailure(r, "ApiKey") {
		t.Errorf("expected ApiKey failure; got %+v", r.Errors)
	}
}

func TestSettings_ValidateRequiresBaseURL(t *testing.T) {
	t.Parallel()
	s := torrentleech.NewSettings()
	s.BaseUrl = "not-a-url"
	s.ApiKey = "abc"
	r := s.Validate()
	if !hasFailure(r, "BaseUrl") {
		t.Errorf("expected BaseUrl failure; got %+v", r.Errors)
	}
}

func TestSettings_ValidateAcceptsValidConfig(t *testing.T) {
	t.Parallel()
	s := torrentleech.NewSettings()
	s.ApiKey = "abc"
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("expected valid; got %+v", r.Errors)
	}
}

// --- Indexer ---

func TestIndexer_NameAndContract(t *testing.T) {
	t.Parallel()
	i := torrentleech.NewIndexer(nil)
	if got := i.Name(); got != "TorrentLeech" {
		t.Errorf("Name = %q, want TorrentLeech (display form with capital L)", got)
	}
	if got := i.ConfigContract(); got != "TorrentleechSettings" {
		t.Errorf("ConfigContract = %q, want TorrentleechSettings", got)
	}
}

func TestIndexer_DefaultDefinitionsShape(t *testing.T) {
	t.Parallel()
	i := torrentleech.NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Fatalf("DefaultDefinitions length = %d, want 1", len(defs))
	}
	d := defs[0]
	if d.Implementation != "Torrentleech" {
		t.Errorf("Implementation = %q, want Torrentleech (wire-stable lowercase l)", d.Implementation)
	}
	if d.ConfigContract != "TorrentleechSettings" {
		t.Errorf("ConfigContract = %q", d.ConfigContract)
	}
	if d.Enable {
		t.Error("DefaultDefinition Enable should be false")
	}
	if _, ok := d.Settings.(*torrentleech.Settings); !ok {
		t.Errorf("Settings type = %T, want *torrentleech.Settings", d.Settings)
	}
}

func TestIndexer_TestUnboundFails(t *testing.T) {
	t.Parallel()
	i := torrentleech.NewIndexer(nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("Test on unbound indexer should fail")
	}
}

func TestIndexer_TestRunsSettingsValidate(t *testing.T) {
	t.Parallel()
	i := torrentleech.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*torrentleech.Settings).ApiKey = "key"
	i.SetDefinition(d)
	r := i.Test(context.Background())
	if !r.IsValid() {
		t.Errorf("Test should pass for valid Settings; got %+v", r.Errors)
	}
}

func TestIndexer_FetchRecentEmbedsApiKeyInURL(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := torrentleech.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	s := d.Settings.(*torrentleech.Settings)
	s.ApiKey = "myKey123"
	s.BaseUrl = "http://rss.torrentleech.org/"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 release; got %d", len(rels))
	}
	if stub.last == nil {
		t.Fatal("HTTPClient.Get was not called")
	}
	got := stub.last.URL.String()
	// C# emits `<BaseUrl trimmed>/<ApiKey>` with no other suffix.
	if !strings.HasSuffix(got, "/myKey123") {
		t.Errorf("URL = %q, want suffix /myKey123 (ApiKey at the path tail)", got)
	}
	if strings.Contains(got, "//myKey123") {
		t.Errorf("URL = %q, BaseUrl trailing-slash should not produce a double slash", got)
	}
}

func TestIndexer_FetchSearchAlwaysEmpty(t *testing.T) {
	t.Parallel()
	i := torrentleech.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*torrentleech.Settings).ApiKey = "k"
	i.SetDefinition(d)
	rels, err := i.FetchSearch(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("FetchSearch should always return empty; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentNilClientErrors(t *testing.T) {
	t.Parallel()
	i := torrentleech.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*torrentleech.Settings).ApiKey = "k"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on nil HTTPClient")
	}
}

func TestIndexer_FetchRecentUnboundErrors(t *testing.T) {
	t.Parallel()
	i := torrentleech.NewIndexer(&stubClient{})
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on unbound indexer")
	}
}

// --- Registry ---

func TestRegistry_ConstructorRegistered(t *testing.T) {
	t.Parallel()
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: torrentleech.Implementation, Contract: torrentleech.ConfigContract})
	if !ok {
		t.Fatal("Torrentleech not registered in provider.Default()")
	}
	if got := ctor(); got == nil {
		t.Error("Constructor returned nil")
	}
	if _, ok := ctor().(*torrentleech.Settings); !ok {
		t.Errorf("Constructor returned %T, want *torrentleech.Settings", ctor())
	}
}

// --- helpers ---

func hasFailure(r provider.ValidationResult, field string) bool {
	for _, f := range r.Errors {
		if f.PropertyName == field {
			return true
		}
	}
	return false
}
