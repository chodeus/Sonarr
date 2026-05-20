package iptorrents_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/iptorrents"
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
<rss version="2.0">
<channel>
  <title>IPTorrents</title>
  <item>
    <title>Show.S01E01.1080p.WEB-DL-x264</title>
    <link>https://iptorrents.com/download.php/1/Show.S01E01.torrent</link>
    <guid>https://iptorrents.com/torrent/1</guid>
    <description>Size: 2.5 GB</description>
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
	s := iptorrents.NewSettings()
	if s.BaseUrl != "" {
		t.Errorf("BaseUrl default = %q, want empty (user must paste their feed URL)", s.BaseUrl)
	}
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d, want 1", s.MinimumSeeders)
	}
}

func TestSettings_ValidateRequiresBaseURL(t *testing.T) {
	t.Parallel()
	s := iptorrents.NewSettings()
	r := s.Validate()
	if r.IsValid() {
		t.Error("default Settings (empty BaseUrl) should not validate")
	}
}

func TestSettings_ValidateRequiresRssPattern(t *testing.T) {
	t.Parallel()
	s := iptorrents.NewSettings()
	s.BaseUrl = "https://iptorrents.com/" // no /rss?... tail
	r := s.Validate()
	if r.IsValid() {
		t.Error("BaseUrl without /rss?... tail should fail")
	}
	found := false
	for _, f := range r.Errors {
		if f.PropertyName == "BaseUrl" && f.Severity == provider.MessageError {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hard BaseUrl failure for missing rss query; got %+v", r.Errors)
	}
}

func TestSettings_ValidateWarnsOnMissingDownloadFlag(t *testing.T) {
	t.Parallel()
	s := iptorrents.NewSettings()
	// rss query present but no ;download segment — warning, not error.
	s.BaseUrl = "https://iptorrents.com/t/rss?u=u;tp=tok;feed"
	r := s.Validate()
	// Hard errors should be empty; warning should be present.
	var hardErr, warnFound bool
	for _, f := range r.Errors {
		if f.PropertyName == "BaseUrl" && f.Severity == provider.MessageError {
			hardErr = true
		}
		if f.PropertyName == "BaseUrl" && f.Severity == provider.MessageWarning {
			warnFound = true
		}
	}
	if hardErr {
		t.Errorf("expected no hard BaseUrl error; got %+v", r.Errors)
	}
	if !warnFound {
		t.Errorf("expected ;download warning; got %+v", r.Errors)
	}
}

func TestSettings_ValidateAcceptsFullURL(t *testing.T) {
	t.Parallel()
	s := iptorrents.NewSettings()
	s.BaseUrl = "https://iptorrents.com/t/rss?u=user;tp=token;feed=dl;download"
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("expected valid; got %+v", r.Errors)
	}
}

// --- Indexer ---

func TestIndexer_NameAndContract(t *testing.T) {
	t.Parallel()
	i := iptorrents.NewIndexer(nil)
	if got := i.Name(); got != "IP Torrents" {
		t.Errorf("Name = %q, want %q (with space)", got, "IP Torrents")
	}
	if got := i.ConfigContract(); got != "IPTorrentsSettings" {
		t.Errorf("ConfigContract = %q", got)
	}
}

func TestIndexer_DefaultDefinitionsShape(t *testing.T) {
	t.Parallel()
	i := iptorrents.NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Fatalf("DefaultDefinitions length = %d, want 1", len(defs))
	}
	d := defs[0]
	if d.Implementation != "IPTorrents" {
		t.Errorf("Implementation = %q, want IPTorrents (wire-stable)", d.Implementation)
	}
	if d.ConfigContract != "IPTorrentsSettings" {
		t.Errorf("ConfigContract = %q", d.ConfigContract)
	}
	if d.Enable {
		t.Error("DefaultDefinition Enable should be false")
	}
	if _, ok := d.Settings.(*iptorrents.Settings); !ok {
		t.Errorf("Settings type = %T, want *iptorrents.Settings", d.Settings)
	}
}

func TestIndexer_TestUnboundFails(t *testing.T) {
	t.Parallel()
	i := iptorrents.NewIndexer(nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("Test on unbound indexer should fail")
	}
}

func TestIndexer_FetchRecentUsesBaseURLVerbatim(t *testing.T) {
	t.Parallel()
	stub := &stubClient{
		getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
			return mkResponse(req, []byte(minimalFeed)), nil
		},
	}
	i := iptorrents.NewIndexer(stub)
	d := i.DefaultDefinitions()[0]
	want := "https://iptorrents.com/t/rss?u=user;tp=token;feed=dl;download"
	d.Settings.(*iptorrents.Settings).BaseUrl = want
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
	if got := stub.last.URL.String(); got != want {
		t.Errorf("URL = %q, want %q (verbatim BaseUrl, no path concat)", got, want)
	}
}

func TestIndexer_FetchSearchAlwaysEmpty(t *testing.T) {
	t.Parallel()
	i := iptorrents.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*iptorrents.Settings).BaseUrl = "https://iptorrents.com/t/rss?x;download"
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
	i := iptorrents.NewIndexer(nil)
	d := i.DefaultDefinitions()[0]
	d.Settings.(*iptorrents.Settings).BaseUrl = "https://iptorrents.com/t/rss?x;download"
	i.SetDefinition(d)
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on nil HTTPClient")
	}
}

func TestIndexer_FetchRecentUnboundErrors(t *testing.T) {
	t.Parallel()
	i := iptorrents.NewIndexer(&stubClient{})
	if _, err := i.FetchRecent(context.Background()); err == nil {
		t.Error("expected error on unbound indexer")
	}
}

// --- Registry ---

func TestRegistry_ConstructorRegistered(t *testing.T) {
	t.Parallel()
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: iptorrents.Implementation, Contract: iptorrents.ConfigContract})
	if !ok {
		t.Fatal("IPTorrents not registered in provider.Default()")
	}
	if _, ok := ctor().(*iptorrents.Settings); !ok {
		t.Errorf("Constructor returned %T, want *iptorrents.Settings", ctor())
	}
}
