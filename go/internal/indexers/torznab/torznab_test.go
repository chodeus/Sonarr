package torznab

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers/newznab"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// stubClient is a tiny test double for httpclient.Client.
type stubClient struct {
	getFn func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error)
}

func (s *stubClient) Execute(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	return s.getFn(ctx, req)
}
func (s *stubClient) Get(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
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

// hasFailure helper for ValidationResult assertions.
func hasFailure(r provider.ValidationResult, prop string) bool {
	for _, f := range r.Errors {
		if f.PropertyName == prop {
			return true
		}
	}
	return false
}

// --- Settings tests ---

func TestSettings_Defaults(t *testing.T) {
	s := NewSettings()
	if s.MinimumSeeders != 1 {
		t.Errorf("MinimumSeeders = %d want 1", s.MinimumSeeders)
	}
	if s.ApiPath != "/api" {
		t.Errorf("inherited ApiPath = %q", s.ApiPath)
	}
	if len(s.Categories) != 2 {
		t.Errorf("inherited Categories count = %d", len(s.Categories))
	}
}

func TestSettings_Validate_InheritsNewznabRules(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "" // invalid
	r := s.Validate()
	if r.IsValid() {
		t.Error("expected invalid for empty BaseUrl")
	}
	if !hasFailure(r, "BaseUrl") {
		t.Errorf("expected BaseUrl failure: %v", r)
	}
}

func TestSettings_Validate_NoApiKeyAllowList(t *testing.T) {
	// Torznab has Array.Empty<string> for ApiKeyAllowList — no host
	// requires ApiKey by default.
	s := NewSettings()
	s.BaseUrl = "http://nzbs.org" // would be on Newznab's allowlist
	s.ApiKey = ""
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("Torznab should not require ApiKey for any host: %v", r)
	}
}

func TestSettings_Validate_SeedCriteriaWarnings(t *testing.T) {
	s := NewSettings()
	s.BaseUrl = "http://x"
	zero := 0
	zeroF := 0.0
	s.SeedCriteria.SeedTime = &zero
	s.SeedCriteria.SeedRatio = &zeroF
	r := s.Validate()
	if !r.IsValid() {
		t.Errorf("warnings shouldn't block validation: %v", r)
	}
	if !r.HasWarnings() {
		t.Error("expected warnings for non-positive seed criteria")
	}
}

// --- Parser tests ---

func TestParseRSS_HDAccessNetFixture(t *testing.T) {
	body := loadFixture(t, "torznab_hdaccess_net.xml")
	releases, err := ParseRSS(body, &ParseOptions{IndexerName: "Torznab", IndexerID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 5 {
		t.Fatalf("expected 5 items, got %d", len(releases))
	}

	first := releases[0]
	if first.Title != "Better Call Saul S01E05 Alpine Shepherd 1080p NF WEBRip DD5.1 x264" {
		t.Errorf("title = %q", first.Title)
	}
	if first.DownloadURL != "https://hdaccess.net/download.php?torrent=11515&passkey=123456" {
		t.Errorf("downloadURL = %q", first.DownloadURL)
	}
	if first.InfoURL != "https://hdaccess.net/details.php?id=11515&hit=1" {
		t.Errorf("infoURL = %q (expected #comments stripped)", first.InfoURL)
	}
	if first.CommentURL != "https://hdaccess.net/details.php?id=11515&hit=1#comments" {
		t.Errorf("commentURL = %q", first.CommentURL)
	}
	wantPub := time.Date(2015, 3, 14, 21, 10, 42, 0, time.UTC)
	if !first.PublishDate.Equal(wantPub) {
		t.Errorf("pubDate = %v want %v", first.PublishDate, wantPub)
	}
	if first.Size != 2538463390 {
		t.Errorf("size = %d (expected 2538463390 from enclosure length fallback)", first.Size)
	}
	if first.TvdbID != 273181 {
		t.Errorf("TvdbID = %d", first.TvdbID)
	}
	if first.TvRageID != 37780 {
		t.Errorf("TvRageID = %d", first.TvRageID)
	}
	if first.InfoHash != "63e07ff523710ca268567dad344ce1e0e6b7e8a3" {
		t.Errorf("InfoHash = %q", first.InfoHash)
	}
	if first.Seeders == nil || *first.Seeders != 7 {
		t.Errorf("Seeders = %v", first.Seeders)
	}
	if first.Peers == nil || *first.Peers != 7 {
		t.Errorf("Peers = %v", first.Peers)
	}
	if first.Indexer != "Torznab" {
		t.Errorf("Indexer = %q", first.Indexer)
	}
	if first.IndexerID != 7 {
		t.Errorf("IndexerID = %d", first.IndexerID)
	}
}

func TestParseRSS_TpbFixture(t *testing.T) {
	body := loadFixture(t, "torznab_tpb.xml")
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 5 {
		t.Fatalf("expected 5 items, got %d", len(releases))
	}
	for _, r := range releases {
		if r.Title == "" {
			t.Error("empty title")
		}
		if r.PublishDate.IsZero() {
			t.Error("zero pubDate")
		}
	}
}

func TestParseRSS_AnimetoshoFixture(t *testing.T) {
	body := loadFixture(t, "torznab_animetosho.xml")
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 2 {
		t.Fatalf("expected 2 items, got %d", len(releases))
	}
}

func TestParseRSS_OnlyNzbEnclosuresReturnsEmpty(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>nzb only</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-nzb"/>
		</item>
	</channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Errorf("nzb-only feed should be empty (did-you-mean-Newznab gate), got %d", len(releases))
	}
}

func TestParseRSS_TorznabSizeAttrPreferredOverEnclosureLength(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
		<item>
			<title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-bittorrent"/>
			<torznab:attr name="size" value="9999999"/>
		</item>
	</channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Size != 9999999 {
		t.Errorf("size = %d (attr should win)", releases[0].Size)
	}
}

func TestParseRSS_PeersFromSeedersPlusLeechers(t *testing.T) {
	// peers attr absent but seeders + leechers present → peers = sum.
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
		<item>
			<title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="1" type="application/x-bittorrent"/>
			<torznab:attr name="seeders" value="3"/>
			<torznab:attr name="leechers" value="4"/>
		</item>
	</channel></rss>`)
	releases, _ := ParseRSS(body, nil)
	if len(releases) != 1 {
		t.Fatal("expected 1 release")
	}
	if releases[0].Peers == nil || *releases[0].Peers != 7 {
		t.Errorf("Peers = %v want 7 (3+4)", releases[0].Peers)
	}
}

func TestParseRSS_FreeleechFlagsFromVolumeFactors(t *testing.T) {
	cases := []struct {
		dl, ul float64
		want   parser.IndexerFlags
	}{
		{0.0, 1.0, parser.IndexerFlagFreeleech},
		{0.25, 1.0, parser.IndexerFlagFreeleech75},
		{0.5, 1.0, parser.IndexerFlagHalfleech},
		{0.75, 1.0, parser.IndexerFlagFreeleech25},
		{1.0, 2.0, parser.IndexerFlagDoubleUpload},
		{0.0, 2.0, parser.IndexerFlagFreeleech | parser.IndexerFlagDoubleUpload},
	}
	for _, c := range cases {
		body := []byte(`<?xml version="1.0"?>
			<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
			<item><title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
				<enclosure url="x" length="1" type="application/x-bittorrent"/>
				<torznab:attr name="downloadvolumefactor" value="` + floatStr(c.dl) + `"/>
				<torznab:attr name="uploadvolumefactor" value="` + floatStr(c.ul) + `"/>
			</item></channel></rss>`)
		releases, _ := ParseRSS(body, nil)
		if len(releases) != 1 {
			t.Fatalf("case %v: expected 1 release", c)
			continue
		}
		if releases[0].IndexerFlags != c.want {
			t.Errorf("dl=%v ul=%v: flags = %d, want %d", c.dl, c.ul, releases[0].IndexerFlags, c.want)
		}
	}
}

func TestParseRSS_TagsAddSceneAndInternalFlags(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
		<item><title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="1" type="application/x-bittorrent"/>
			<torznab:attr name="tag" value="internal"/>
			<torznab:attr name="tag" value="scene"/>
		</item></channel></rss>`)
	releases, _ := ParseRSS(body, nil)
	want := parser.IndexerFlagInternal | parser.IndexerFlagScene
	if releases[0].IndexerFlags != want {
		t.Errorf("flags = %d want %d", releases[0].IndexerFlags, want)
	}
}

func TestParseRSS_MagnetURL(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
		<item><title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="1" type="application/x-bittorrent"/>
			<torznab:attr name="magneturl" value="magnet:?xt=urn:btih:abc"/>
		</item></channel></rss>`)
	releases, _ := ParseRSS(body, nil)
	if releases[0].MagnetURL != "magnet:?xt=urn:btih:abc" {
		t.Errorf("MagnetURL = %q", releases[0].MagnetURL)
	}
}

func TestParseRSS_APIKeyError(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><error code="100" description="bad"/>`)
	_, err := ParseRSS(body, &ParseOptions{HasAPIKey: false})
	if !IsAPIKey(err) {
		t.Errorf("expected APIKey error, got %v", err)
	}
}

func TestParseRSS_RateLimit(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><error code="500" description="Request limit reached"/>`)
	_, err := ParseRSS(body, nil)
	if !IsRateLimit(err) {
		t.Errorf("expected RateLimit, got %v", err)
	}
}

func TestParseRSS_MissingParameterAPIKey(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><error code="200" description="Missing parameter"/>`)
	_, err := ParseRSS(body, &ParseOptions{HasAPIKey: false})
	if !IsAPIKey(err) {
		t.Errorf("expected APIKey, got %v", err)
	}
}

// --- Indexer Provider tests ---

func TestIndexer_NameAndConfigContract(t *testing.T) {
	i := NewIndexer(nil, nil)
	if i.Name() != "Torznab" {
		t.Errorf("Name = %q", i.Name())
	}
	if i.ConfigContract() != "TorznabSettings" {
		t.Errorf("ConfigContract = %q", i.ConfigContract())
	}
}

func TestIndexer_DefaultDefinitions(t *testing.T) {
	i := NewIndexer(nil, nil)
	defs := i.DefaultDefinitions()
	if len(defs) != 1 {
		t.Errorf("DefaultDefinitions count = %d, want 1", len(defs))
	}
	d := defs[0]
	if d.Implementation != "Torznab" || d.ConfigContract != "TorznabSettings" {
		t.Errorf("def impl/contract: %q/%q", d.Implementation, d.ConfigContract)
	}
	if d.Enable {
		t.Error("Enable should default false")
	}
}

func TestIndexer_Test_JackettAllWarning(t *testing.T) {
	settings := NewSettings()
	settings.BaseUrl = "http://localhost/torznab/all"
	settings.ApiPath = "/api"
	i := NewIndexer(nil, nil)
	i.SetDefinition(&provider.Definition{Settings: settings})
	r := i.Test(context.Background())
	if !r.HasWarnings() {
		t.Error("expected JackettAll warning")
	}
}

func TestIndexer_Test_NoCapsProviderWarns(t *testing.T) {
	settings := NewSettings()
	settings.BaseUrl = "http://nzbs2.org"
	i := NewIndexer(nil, nil)
	i.SetDefinition(&provider.Definition{Settings: settings})
	r := i.Test(context.Background())
	if !r.IsValid() {
		t.Errorf("expected valid (warnings only): %v", r)
	}
	if !r.HasWarnings() {
		t.Error("expected caps-provider warning")
	}
}

func TestIndexer_RequestAction_NewznabCategoriesNoCaps(t *testing.T) {
	settings := NewSettings()
	settings.BaseUrl = "http://nzbs2.org"
	i := NewIndexer(nil, nil)
	i.SetDefinition(&provider.Definition{Settings: settings})
	got, err := i.RequestAction("newznabCategories", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if opts, ok := m["options"].([]any); !ok || len(opts) != 0 {
		t.Errorf("expected empty options, got %v", m["options"])
	}
}

func TestIndexer_RequestAction_UnknownStageNil(t *testing.T) {
	i := NewIndexer(nil, nil)
	got, _ := i.RequestAction("unknown", nil)
	if got != nil {
		t.Errorf("expected nil for unknown stage, got %v", got)
	}
}

func TestIndexer_FetchRecent_HappyPath(t *testing.T) {
	feedBody := loadFixture(t, "torznab_hdaccess_net.xml")
	capsBody := loadFixture(t, "torznab_hdaccess_net.xml") // any XML body suffices for the caps stub
	_ = capsBody

	stub := &stubClient{getFn: func(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
		if strings.Contains(req.URL.String(), "?t=caps") {
			// Use a minimal caps body so the request generator gets supportedTvSearchParameters.
			caps := []byte(`<caps><limits default="100" max="100"/>
				<searching>
					<search available="yes"/>
					<tv-search available="yes" supportedParams="q,rid,tvdbid,season,ep"/>
				</searching>
				<categories><category id="1" name="cat"/></categories>
			</caps>`)
			return mkResponse(t, req, caps), nil
		}
		return mkResponse(t, req, feedBody), nil
	}}
	caps := newznab.NewCapabilitiesProvider(stub)

	settings := NewSettings()
	settings.BaseUrl = "http://x/"
	settings.ApiKey = "key"
	i := NewIndexer(stub, caps)
	i.SetDefinition(&provider.Definition{
		ID:             7,
		Name:           "Test Torznab",
		Implementation: "Torznab",
		ConfigContract: "TorznabSettings",
		Settings:       settings,
	})

	releases, err := i.FetchRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) == 0 {
		t.Error("expected releases, got none")
	}
	if releases[0].Indexer != "Test Torznab" {
		t.Errorf("Indexer name = %q", releases[0].Indexer)
	}
}

func TestIndexer_RegistryConstructorWired(t *testing.T) {
	ctor, ok := provider.Default().Lookup(provider.FamilyIndexer,
		provider.Key{Impl: "Torznab", Contract: "TorznabSettings"})
	if !ok {
		t.Fatal("Torznab not registered")
	}
	if ctor == nil {
		t.Fatal("Torznab constructor is nil")
	}
	got := ctor()
	if _, ok := got.(*Settings); !ok {
		t.Errorf("Constructor returned %T, want *Settings", got)
	}
}

func TestIndexer_ImplementsProviderInterface(t *testing.T) {
	var _ provider.Provider = (*Indexer)(nil)
}

func TestValidateSearchModeSupported(t *testing.T) {
	cases := []struct {
		caps *newznab.Capabilities
		want bool
	}{
		{&newznab.Capabilities{SupportedSearchParameters: []string{"q"}}, true},
		{&newznab.Capabilities{SupportedTvSearchParameters: []string{"q", "season", "ep"}}, true},
		{&newznab.Capabilities{SupportedTvSearchParameters: []string{"rid"}}, false},
		{&newznab.Capabilities{}, false},
	}
	for i, c := range cases {
		if got := validateSearchModeSupported(c.caps); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

// floatStr renders a float without trailing zeros for compact XML test bodies.
func floatStr(f float64) string {
	switch f {
	case 0.0:
		return "0.0"
	case 0.25:
		return "0.25"
	case 0.5:
		return "0.5"
	case 0.75:
		return "0.75"
	case 1.0:
		return "1.0"
	case 2.0:
		return "2.0"
	default:
		return "1.0"
	}
}
