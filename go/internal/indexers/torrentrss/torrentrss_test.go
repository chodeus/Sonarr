package torrentrss

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/provider"
)

// stubClient is a tiny test double for httpclient.Client.
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

func mkResponse(t *testing.T, req *httpclient.Request, body []byte) *httpclient.Response {
	t.Helper()
	h := httpclient.NewHeader()
	h.SetContentType("application/xml; charset=utf-8")
	return httpclient.NewResponseFromBytes(req, h, body, http.StatusOK, "1.1")
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- Settings ---

func TestSettings_Defaults(t *testing.T) {
	s := NewSettings()
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d want 1", s.MinimumSeeders)
	}
	if s.AllowZeroSize {
		t.Error("AllowZeroSize default true")
	}
}

func TestSettings_ValidateRequiresBaseURL(t *testing.T) {
	s := NewSettings()
	r := s.Validate()
	if r.IsValid() {
		t.Error("expected invalid for empty BaseUrl")
	}
	if !hasFailure(r, "BaseUrl") {
		t.Errorf("expected BaseUrl failure: %v", r)
	}
}

func TestSettings_ValidateAcceptsValidURL(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "https://feed.example/rss"
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("expected valid: %v", r)
	}
}

// --- ParseSize ---

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1024", 1024},
		{"1.5 MB", 1572864},                     // 1.5*1024^2 (binary default)
		{"1.5 MiB", 1572864},                    // explicit MiB
		{"938.49 MB", 984078090},                // ImmortalSeed: 938.49 * 1024^2 = 984078090.36 (truncated)
		{"1,234 MB", int64(1234 * 1024 * 1024)}, // comma-thousands
		{"", 0},
		{"not a size", 0},
	}
	for _, c := range cases {
		got, ok := ParseSize(c.in, true)
		if c.want == 0 && !ok {
			continue
		}
		if !ok {
			t.Errorf("ParseSize(%q) returned !ok", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// --- Parser ---

func TestParseRSS_LimeTorrentsFixture(t *testing.T) {
	body := loadFixture(t, "LimeTorrents.xml")
	releases, err := ParseRSS(body, &ParseOptions{IndexerName: "LimeTorrents", IndexerID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 5 {
		t.Fatalf("expected 5 items, got %d", len(releases))
	}
	first := releases[0]
	if first.Title != "The Expanse 2x04 (720p-HDTV-x264-SVA)[VTV]" {
		t.Errorf("title = %q", first.Title)
	}
	// Enclosure length is 880496711.
	if first.Size != 880496711 {
		t.Errorf("size = %d want 880496711", first.Size)
	}
	if first.IndexerID != 5 || first.Indexer != "LimeTorrents" {
		t.Errorf("indexer = %q/%d", first.Indexer, first.IndexerID)
	}
}

func TestParseRSS_LimeTorrentsWithSizeElementName(t *testing.T) {
	// LimeTorrents has a top-level <size>NNN</size> element with bytes.
	// Setting SizeElementName="size" should pick it up directly.
	body := loadFixture(t, "LimeTorrents.xml")
	releases, _ := ParseRSS(body, &ParseOptions{
		ParserSettings: ParserSettings{SizeElementName: "size"},
	})
	if releases[0].Size != 880496711 {
		t.Errorf("size = %d", releases[0].Size)
	}
}

func TestParseRSS_ImmortalSeedDescriptionParsing(t *testing.T) {
	// ImmortalSeed feed has no torrent enclosure or size element — size +
	// seeders/leechers must be parsed out of the description.
	body := loadFixture(t, "ImmortalSeed.xml")
	releases, err := ParseRSS(body, &ParseOptions{
		ParserSettings: ParserSettings{
			ParseSeedersInDescription: true,
			ParseSizeInDescription:    true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) == 0 {
		t.Fatal("expected releases")
	}
	first := releases[0]
	// Description: "Seeders: 6 / Leechers: 2 / Size: 938.49 MB"
	wantSize := int64(984078090) // 938.49 MB → 938.49 * 1024^2 = 984078090.36 truncated
	if first.Size != wantSize {
		t.Errorf("size = %d want %d (parsed from description)", first.Size, wantSize)
	}
	if first.Seeders == nil || *first.Seeders != 6 {
		t.Errorf("seeders = %v want 6", first.Seeders)
	}
	if first.Peers == nil || *first.Peers != 8 {
		// peers fallback = seeders + leechers = 6 + 2 = 8
		t.Errorf("peers = %v want 8 (seeders+leechers fallback)", first.Peers)
	}
}

func TestParseRSS_AwesomeHDFixture(t *testing.T) {
	body := loadFixture(t, "AwesomeHD.xml")
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 2 {
		t.Errorf("expected 2 items, got %d", len(releases))
	}
}

func TestParseRSS_ShowRSSFixture(t *testing.T) {
	body := loadFixture(t, "ShowRSS.info.xml")
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 5 {
		t.Errorf("expected 5 items, got %d", len(releases))
	}
}

func TestParseRSS_MagnetURLFromLink(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>Magnet</title><guid>1</guid>
			<pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<link>magnet:?xt=urn:btih:abc123</link>
		</item>
	</channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if releases[0].MagnetURL != "magnet:?xt=urn:btih:abc123" {
		t.Errorf("MagnetURL = %q", releases[0].MagnetURL)
	}
	if releases[0].InfoHash != "abc123" {
		t.Errorf("InfoHash = %q (expected lowercased btih)", releases[0].InfoHash)
	}
}

func TestParseRSS_CustomElementNames(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>x</title><guid>1</guid>
			<pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="0" type="application/x-bittorrent"/>
			<seeds>42</seeds>
			<peers>13</peers>
			<infohash>DEADBEEF</infohash>
			<bytes>1048576</bytes>
		</item>
	</channel></rss>`)
	releases, err := ParseRSS(body, &ParseOptions{
		ParserSettings: ParserSettings{
			SizeElementName:     "bytes",
			SeedsElementName:    "seeds",
			PeersElementName:    "peers",
			InfoHashElementName: "infohash",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := releases[0]
	if r.Size != 1048576 {
		t.Errorf("Size = %d", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 42 {
		t.Errorf("Seeders = %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 13 {
		t.Errorf("Peers = %v", r.Peers)
	}
	if r.InfoHash != "DEADBEEF" {
		t.Errorf("InfoHash = %q", r.InfoHash)
	}
}

func TestParseRSS_CalculatePeersAsSum(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>x</title><guid>1</guid>
			<pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-bittorrent"/>
			<seeds>10</seeds>
			<peers>3</peers>
		</item>
	</channel></rss>`)
	releases, _ := ParseRSS(body, &ParseOptions{
		ParserSettings: ParserSettings{
			SeedsElementName:    "seeds",
			PeersElementName:    "peers",
			CalculatePeersAsSum: true,
		},
	})
	if releases[0].Peers == nil || *releases[0].Peers != 13 {
		t.Errorf("Peers = %v want 13 (3+10)", releases[0].Peers)
	}
}

func TestParseRSS_RelativeURLResolution(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>x</title><guid>1</guid>
			<pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<link>/torrents/abc.torrent</link>
		</item>
	</channel></rss>`)
	releases, _ := ParseRSS(body, &ParseOptions{FeedURL: "https://feed.example/rss"})
	if releases[0].DownloadURL != "https://feed.example/torrents/abc.torrent" {
		t.Errorf("DownloadURL = %q", releases[0].DownloadURL)
	}
}

func TestParseRSS_EmptyChannel(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel></channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Errorf("expected 0 items, got %d", len(releases))
	}
}

func TestParseRSS_MalformedXML(t *testing.T) {
	_, err := ParseRSS([]byte("not xml"), nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

// --- Indexer ---

func TestIndexer_NameAndConfigContract(t *testing.T) {
	i := NewIndexer(nil)
	if i.Name() != "TorrentRssIndexer" {
		t.Errorf("Name = %q", i.Name())
	}
	if i.ConfigContract() != "TorrentRssIndexerSettings" {
		t.Errorf("ConfigContract = %q", i.ConfigContract())
	}
}

func TestIndexer_DefaultDefinitions(t *testing.T) {
	i := NewIndexer(nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Errorf("DefaultDefinitions count = %d", len(defs))
	}
	if defs[0].Implementation != "TorrentRssIndexer" {
		t.Errorf("def impl = %q", defs[0].Implementation)
	}
}

func TestIndexer_Test_UnboundFails(t *testing.T) {
	i := NewIndexer(nil)
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("expected invalid when unbound")
	}
}

func TestIndexer_Test_InvalidSettings(t *testing.T) {
	settings := NewSettings()
	settings.BaseUrl = "" // invalid
	i := NewIndexer(nil)
	i.SetDefinition(&provider.Definition{Settings: settings})
	r := i.Test(context.Background())
	if r.IsValid() {
		t.Error("expected invalid for empty BaseUrl")
	}
}

func TestIndexer_Test_ValidSettings(t *testing.T) {
	settings := NewSettings()
	settings.BaseUrl = "https://feed.example/rss"
	i := NewIndexer(nil)
	i.SetDefinition(&provider.Definition{Settings: settings})
	r := i.Test(context.Background())
	if !r.IsValid() {
		t.Errorf("expected valid: %v", r)
	}
}

func TestIndexer_FetchRecent_HappyPath(t *testing.T) {
	feedBody := loadFixture(t, "LimeTorrents.xml")
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		return mkResponse(t, req, feedBody), nil
	}}

	settings := NewSettings()
	settings.BaseUrl = "https://feed.example/rss"
	i := NewIndexer(stub)
	i.SetDefinition(&provider.Definition{
		ID:             5,
		Name:           "Test Feed",
		Implementation: "TorrentRssIndexer",
		ConfigContract: "TorrentRssIndexerSettings",
		Settings:       settings,
	})

	releases, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) == 0 {
		t.Error("expected releases")
	}
	if releases[0].Indexer != "Test Feed" {
		t.Errorf("Indexer name = %q", releases[0].Indexer)
	}
}

func TestIndexer_FetchRecent_CookieAttached(t *testing.T) {
	feedBody := loadFixture(t, "LimeTorrents.xml")
	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		return mkResponse(t, req, feedBody), nil
	}}

	settings := NewSettings()
	settings.BaseUrl = "https://feed.example/rss"
	settings.Cookie = "session=abc; csrf=xyz"
	i := NewIndexer(stub)
	i.SetDefinition(&provider.Definition{Settings: settings})
	if _, err := i.FetchRecent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stub.last == nil {
		t.Fatal("no request captured")
	}
	if stub.last.Cookies["session"] != "abc" {
		t.Errorf("session cookie = %q", stub.last.Cookies["session"])
	}
	if stub.last.Cookies["csrf"] != "xyz" {
		t.Errorf("csrf cookie = %q", stub.last.Cookies["csrf"])
	}
}

func TestIndexer_FetchRecent_UnboundFails(t *testing.T) {
	i := NewIndexer(&stubClient{})
	_, err := i.FetchRecent(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIndexer_FetchRecent_NoHTTPClientFails(t *testing.T) {
	settings := NewSettings()
	settings.BaseUrl = "https://feed.example/rss"
	i := NewIndexer(nil)
	i.SetDefinition(&provider.Definition{Settings: settings})
	_, err := i.FetchRecent(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "HTTPClient") {
		t.Errorf("err = %v", err)
	}
}

func TestIndexer_FetchSearch_AlwaysEmpty(t *testing.T) {
	i := NewIndexer(&stubClient{})
	releases, err := i.FetchSearch(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Errorf("expected empty, got %d", len(releases))
	}
}

func TestIndexer_RegistryConstructorWired(t *testing.T) {
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: "TorrentRssIndexer", Contract: "TorrentRssIndexerSettings"})
	if !ok {
		t.Fatal("TorrentRssIndexer not registered")
	}
	if ctor == nil {
		t.Fatal("nil constructor")
	}
	if _, ok := ctor().(*Settings); !ok {
		t.Errorf("Constructor returned %T", ctor())
	}
}

func TestIndexer_ImplementsProviderInterface(t *testing.T) {
	var _ provider.Provider = (*Indexer)(nil)
}

// hasFailure helper for ValidationResult assertions.
func hasFailure(r provider.ValidationResult, prop string) bool {
	for _, f := range r.Errors {
		if f.PropertyName == prop {
			return true
		}
	}
	return false
}
