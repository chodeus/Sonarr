package newznab

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseRSS_NzbSuFixture_ParsesItems(t *testing.T) {
	body := loadFixture(t, "newznab_nzb_su.xml")
	releases, err := ParseRSS(body, &ParseOptions{IndexerName: "Nzb.su", IndexerID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 100 {
		t.Fatalf("expected 100 items, got %d", len(releases))
	}

	first := releases[0]
	if first.Title != "White.Collar.S03E05.720p.HDTV.X264-DIMENSION" {
		t.Errorf("title = %q", first.Title)
	}
	// Newznab is usenet-only; every parsed release must carry the usenet
	// protocol so the grab path can match a usenet download client.
	// Without this stamp ProcessDecisions reported "no client available
	// for protocol" (DownloadProtocolUnknown) and never grabbed.
	if first.DownloadProtocol != indexers.DownloadProtocolUsenet {
		t.Errorf("DownloadProtocol = %v, want usenet", first.DownloadProtocol)
	}
	if first.Guid != "http://nzb.su/details/24967ef4c2e26296c65d3bbfa97aa8fe" {
		t.Errorf("guid = %q", first.Guid)
	}
	if first.Indexer != "Nzb.su" {
		t.Errorf("indexer = %q", first.Indexer)
	}
	if first.IndexerID != 7 {
		t.Errorf("indexerID = %d", first.IndexerID)
	}
	if first.Size != 1183105773 {
		t.Errorf("size = %d (expected 1183105773 from newznab:size attr)", first.Size)
	}
	wantPub := time.Date(2012, 2, 27, 16, 9, 39, 0, time.UTC) // -0500 → UTC = +5h
	if !first.PublishDate.Equal(wantPub) {
		t.Errorf("pubDate = %v want %v", first.PublishDate, wantPub)
	}
	if first.DownloadURL != "http://nzb.su/getnzb/24967ef4c2e26296c65d3bbfa97aa8fe.nzb&i=37292&r=xxx" {
		t.Errorf("downloadURL = %q", first.DownloadURL)
	}
	// InfoURL should strip the trailing #comments.
	if first.InfoURL != "http://nzb.su/details/24967ef4c2e26296c65d3bbfa97aa8fe" {
		t.Errorf("infoURL = %q", first.InfoURL)
	}
	if first.CommentURL != "http://nzb.su/details/24967ef4c2e26296c65d3bbfa97aa8fe#comments" {
		t.Errorf("commentURL = %q", first.CommentURL)
	}
}

func TestParseRSS_IndexerFlags_PortFixture(t *testing.T) {
	// The fixture has 11 items keyed by GUID-as-description; iterate and
	// assert the expected flag bits for each.
	body := loadFixture(t, "newznab_indexerflags.xml")
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 11 {
		t.Fatalf("expected 11 items, got %d", len(releases))
	}

	// Build a guid → release index for keyed assertions.
	byGuid := map[string]*parser.ReleaseInfo{}
	for _, r := range releases {
		byGuid[r.Guid] = r
	}

	cases := []struct {
		guid string
		want parser.IndexerFlags
	}{
		{"no custom attributes", 0},
		{"prematch=1 attribute", parser.IndexerFlagScene},
		{"haspretime=1 attribute", parser.IndexerFlagScene},
		{"prematch=0 attribute", 0},
		{"haspretime=0 attribute", 0},
		{"nuked=1 attribute", parser.IndexerFlagNuked},
		{"nuked=0 attribute", 0},
		{"prematch=1 and nuked=1 attributes", parser.IndexerFlagScene | parser.IndexerFlagNuked},
		{"haspretime=0 and nuked=0 attributes", 0},
		{"subs=eng", parser.IndexerFlagSubtitles},
		{"subs=''", 0},
	}
	for _, c := range cases {
		t.Run(c.guid, func(t *testing.T) {
			r, ok := byGuid[c.guid]
			if !ok {
				t.Fatalf("guid %q not found in feed", c.guid)
			}
			if r.IndexerFlags != c.want {
				t.Errorf("flags = %d, want %d", r.IndexerFlags, c.want)
			}
		})
	}
}

func TestParseRSS_LanguageFixture_ExtractsLanguages(t *testing.T) {
	body := loadFixture(t, "newznab_language.xml")
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) == 0 {
		t.Fatal("expected at least one release")
	}
	// At least one item should have a Languages slice with multiple
	// entries — the fixture includes "English, Japanese" comma-split.
	sawMulti := false
	for _, r := range releases {
		if len(r.Languages) >= 2 {
			sawMulti = true
			break
		}
	}
	if !sawMulti {
		t.Errorf("expected at least one item with multiple languages parsed from comma-split attr")
	}
}

func TestParseRSS_UnauthorizedFixture_ReturnsAPIKeyError(t *testing.T) {
	body := loadFixture(t, "unauthorized.xml")
	_, err := ParseRSS(body, &ParseOptions{HasAPIKey: false})
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsAPIKey(err) {
		t.Errorf("expected ErrAPIKey, got %v", err)
	}
	var p *ParseError
	if !errors.As(err, &p) {
		t.Fatal("err should be *ParseError")
	}
	if p.Code != 100 {
		t.Errorf("code = %d want 100", p.Code)
	}
}

func TestParseRSS_RateLimitErrorClassified(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><error code="500" description="Request limit reached"/>`)
	_, err := ParseRSS(body, nil)
	if !IsRateLimit(err) {
		t.Errorf("expected ErrRateLimit, got %v", err)
	}
}

func TestParseRSS_GenericError(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><error code="201" description="Some other error"/>`)
	_, err := ParseRSS(body, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if IsAPIKey(err) || IsRateLimit(err) {
		t.Errorf("misclassified: %v", err)
	}
}

func TestParseRSS_MissingApiKeyMessageWithoutAPIKeyInRequest(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><error code="200" description="Missing parameter"/>`)
	_, err := ParseRSS(body, &ParseOptions{HasAPIKey: false})
	if !IsAPIKey(err) {
		t.Errorf("expected APIKey error, got %v", err)
	}
}

func TestParseRSS_MissingApiKeyMessageButRequestHadAPIKey(t *testing.T) {
	// When the request DID carry apikey=, the "Missing parameter" message
	// is treated as a generic error rather than misclassifying as an API
	// key issue. Mirrors NewznabRssParser.cs:40.
	body := []byte(`<?xml version="1.0"?><error code="200" description="Missing parameter"/>`)
	_, err := ParseRSS(body, &ParseOptions{HasAPIKey: true})
	if IsAPIKey(err) {
		t.Errorf("when apikey is present, Missing parameter should NOT be APIKey-classified: %v", err)
	}
}

func TestParseRSS_EmptyChannelReturnsEmpty(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel></channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Errorf("expected 0 items, got %d", len(releases))
	}
}

func TestParseRSS_OnlyTorrentEnclosures_ReturnsEmpty(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-bittorrent"/>
		</item>
	</channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Errorf("torrent-only feed should return empty (did-you-mean-Torznab gate), got %d", len(releases))
	}
}

func TestParseRSS_NewznabSizeAttrTakesPrecedenceOverEnclosureLength(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
		<item>
			<title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-nzb"/>
			<newznab:attr name="size" value="9999999"/>
		</item>
	</channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Size != 9999999 {
		t.Errorf("size = %d want 9999999 (attr wins)", releases[0].Size)
	}
}

func TestParseRSS_UsenetDateAttrPreferredOverPubDate(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
		<item>
			<title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-nzb"/>
			<newznab:attr name="usenetdate" value="Tue, 28 Feb 2012 12:00:00 -0500"/>
		</item>
	</channel></rss>`)
	releases, _ := ParseRSS(body, nil)
	if len(releases) != 1 {
		t.Fatal("expected 1 release")
	}
	want := time.Date(2012, 2, 28, 17, 0, 0, 0, time.UTC)
	if !releases[0].PublishDate.Equal(want) {
		t.Errorf("pubDate = %v, want %v (usenetdate attr should win)", releases[0].PublishDate, want)
	}
}

func TestParseRSS_TvdbAndImdbAttrs(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
		<item>
			<title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-nzb"/>
			<newznab:attr name="tvdbid" value="12345"/>
			<newznab:attr name="rageid" value="67890"/>
			<newznab:attr name="imdb" value="987654"/>
		</item>
	</channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := releases[0]
	if r.TvdbID != 12345 {
		t.Errorf("TvdbID = %d", r.TvdbID)
	}
	if r.TvRageID != 67890 {
		t.Errorf("TvRageID = %d", r.TvRageID)
	}
	if r.ImdbID != "tt0987654" {
		t.Errorf("ImdbID = %q want tt0987654", r.ImdbID)
	}
}

func TestParseRSS_ImdbZeroOrInvalidYieldsEmpty(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
		<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
		<item>
			<title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="100" type="application/x-nzb"/>
			<newznab:attr name="imdb" value="0"/>
		</item>
		<item>
			<title>y</title><guid>2</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="y" length="100" type="application/x-nzb"/>
			<newznab:attr name="imdb" value="not-an-int"/>
		</item>
	</channel></rss>`)
	releases, _ := ParseRSS(body, nil)
	for _, r := range releases {
		if r.ImdbID != "" {
			t.Errorf("ImdbID for %q = %q, want empty", r.Guid, r.ImdbID)
		}
	}
}

func TestParseRSS_RelativeURLResolvedAgainstFeedURL(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>x</title><guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="/getnzb/abc.nzb" length="100" type="application/x-nzb"/>
			<comments>/details/abc</comments>
		</item>
	</channel></rss>`)
	releases, _ := ParseRSS(body, &ParseOptions{FeedURL: "https://nzb.example/api?t=tvsearch"})
	if len(releases) != 1 {
		t.Fatal("expected 1 release")
	}
	r := releases[0]
	if r.DownloadURL != "https://nzb.example/getnzb/abc.nzb" {
		t.Errorf("DownloadURL = %q", r.DownloadURL)
	}
	if r.CommentURL != "https://nzb.example/details/abc" {
		t.Errorf("CommentURL = %q", r.CommentURL)
	}
}

func TestParseRSS_HTMLTitleDecoded(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>Show &amp; Tell</title>
			<guid>1</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="x" length="1" type="application/x-nzb"/>
		</item>
	</channel></rss>`)
	releases, _ := ParseRSS(body, nil)
	if len(releases) != 1 || releases[0].Title != "Show & Tell" {
		t.Errorf("title not HTML-decoded: %q", releases[0].Title)
	}
}

func TestParseRSS_MissingPubDateSkipsItem(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item>
			<title>x</title><guid>1</guid>
			<enclosure url="x" length="1" type="application/x-nzb"/>
		</item>
		<item>
			<title>y</title><guid>2</guid><pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
			<enclosure url="y" length="1" type="application/x-nzb"/>
		</item>
	</channel></rss>`)
	releases, err := ParseRSS(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 {
		t.Errorf("expected 1 release (item without pubDate skipped), got %d", len(releases))
	}
	if releases[0].Guid != "2" {
		t.Errorf("wrong release survived: %q", releases[0].Guid)
	}
}

func TestParseRSS_MalformedXML(t *testing.T) {
	_, err := ParseRSS([]byte("not xml"), nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseError_String(t *testing.T) {
	cases := []struct {
		err      *ParseError
		contains string
	}{
		{&ParseError{Code: 100, Message: "bad key", Kind: ErrAPIKey}, "API key"},
		{&ParseError{Code: 500, Message: "limit", Kind: ErrRateLimit}, "rate limit"},
		{&ParseError{Code: 200, Message: "boom", Kind: ErrGeneric}, "API error"},
	}
	for _, c := range cases {
		s := c.err.Error()
		if len(s) == 0 || !contains(s, c.contains) {
			t.Errorf("Error() = %q; want substring %q", s, c.contains)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
