package nyaa_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/nyaa"
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

const minimalFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:nyaa="https://nyaa.si/xmlns/nyaa">
<channel>
  <title>Nyaa</title>
  <item>
    <title>[Group] Anime - 01 [1080p]</title>
    <link>https://nyaa.si/download/1234567.torrent</link>
    <guid>https://nyaa.si/view/1234567</guid>
    <nyaa:size>1.2 GiB</nyaa:size>
    <nyaa:seeders>42</nyaa:seeders>
    <nyaa:leechers>3</nyaa:leechers>
    <nyaa:infoHash>deadbeef</nyaa:infoHash>
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
	s := nyaa.NewSettings()
	if s.AdditionalParameters != "&cats=1_0&filter=1" {
		t.Errorf("AdditionalParameters default = %q, want C# default", s.AdditionalParameters)
	}
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d, want 1", s.MinimumSeeders)
	}
	if s.AnimeStandardFormatSearch {
		t.Error("AnimeStandardFormatSearch default = true, want false")
	}
}

func TestSettings_ValidateRequiresBaseURL(t *testing.T) {
	t.Parallel()
	s := nyaa.NewSettings()
	r := s.Validate()
	if r.IsValid() {
		t.Error("default Settings (empty BaseUrl) should not validate")
	}
}

func TestSettings_ValidateAdditionalParametersShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		ap    string
		valid bool
	}{
		{"empty", "", true},
		{"C# default", "&cats=1_0&filter=1", true},
		{"multiple", "&a=1&b=2_3", true},
		{"missing-ampersand", "cats=1_0", false},
		{"missing-key", "&=1", false},
		{"missing-value", "&cats=", false},
		{"trailing-text", "&cats=1_0?", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := nyaa.NewSettings()
			s.BaseUrl = "https://nyaa.si"
			s.AdditionalParameters = c.ap
			r := s.Validate()
			if r.IsValid() != c.valid {
				t.Errorf("Validate(%q) IsValid=%v, want %v; failures=%+v", c.ap, r.IsValid(), c.valid, r.Errors)
			}
		})
	}
}

// --- Indexer ---

func TestIndexer_NameAndContract(t *testing.T) {
	t.Parallel()
	i := nyaa.NewIndexer(nil)
	if got := i.Name(); got != "Nyaa" {
		t.Errorf("Name = %q", got)
	}
	if got := i.ConfigContract(); got != "NyaaSettings" {
		t.Errorf("ConfigContract = %q", got)
	}
}

func TestIndexer_DefaultDefinitionsShape(t *testing.T) {
	t.Parallel()
	i := nyaa.NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Fatalf("len = %d", len(defs))
	}
	d := defs[0]
	if d.Implementation != "Nyaa" || d.ConfigContract != "NyaaSettings" {
		t.Errorf("got %+v", d)
	}
	if d.Enable {
		t.Error("Enable should be false")
	}
	if _, ok := d.Settings.(*nyaa.Settings); !ok {
		t.Errorf("Settings type = %T", d.Settings)
	}
}

func TestIndexer_TestUnboundFails(t *testing.T) {
	t.Parallel()
	i := nyaa.NewIndexer(nil)
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
	i := nyaa.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*nyaa.Settings).BaseUrl = "https://nyaa.si/"
	i.SetDefinition(d)

	rels, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 release; got %d", len(rels))
	}
	got := stub.last.URL.String()
	// C# emits `<BaseUrl trimmed>/?page=rss<AdditionalParameters>`.
	want := "https://nyaa.si/?page=rss&cats=1_0&filter=1"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestIndexer_FetchRecentURLNoDoubleSlash(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := nyaa.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	// BaseUrl with NO trailing slash — TrimRight should still leave
	// a clean `/?page=rss` prefix.
	d.Settings.(*nyaa.Settings).BaseUrl = "https://nyaa.si"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err != nil {
		t.Fatalf("FetchRecent: %v", err)
	}
	got := stub.last.URL.String()
	if strings.HasPrefix(got, "https://nyaa.si//") {
		t.Errorf("URL = %q has double-slash", got)
	}
}

func TestIndexer_FetchSearchAlwaysEmpty(t *testing.T) {
	t.Parallel()
	i := nyaa.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*nyaa.Settings).BaseUrl = "https://nyaa.si"
	i.SetDefinition(d)
	rels, err := i.FetchSearch(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("FetchSearch (J148) always returns empty; got %d", len(rels))
	}
}

func TestIndexer_FetchRecentNilClientErrors(t *testing.T) {
	t.Parallel()
	i := nyaa.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*nyaa.Settings).BaseUrl = "https://nyaa.si"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on nil HTTPClient")
	}
}

// --- Registry ---

func TestRegistry_ConstructorRegistered(t *testing.T) {
	t.Parallel()
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: nyaa.Implementation, Contract: nyaa.ConfigContract})
	if !ok {
		t.Fatal("Nyaa not registered in provider.Default()")
	}
	if _, ok := ctor().(*nyaa.Settings); !ok {
		t.Errorf("Constructor returned %T", ctor())
	}
}
