package fanzub_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/fanzub"
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

func TestIndexer_FetchSearchAlwaysEmpty(t *testing.T) {
	t.Parallel()
	i := fanzub.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	i.SetDefinition(d)
	rels, err := i.FetchSearch(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("FetchSearch: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("FetchSearch (J150) always returns empty; got %d", len(rels))
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
