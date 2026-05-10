package torrentrss

import (
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/parser"
)

// RSS parser for the generic Torrent RSS Feed indexer. C# cite:
// src/NzbDrone.Core/Indexers/TorrentRssParser.cs:11-197 + the shared
// Indexers/RssParser.cs base.
//
// Driven by ParserSettings (per-feed knobs). Returns *parser.TorrentInfo
// the same shape as torznab.ParseRSS, but without the torznab namespace —
// items consume top-level RSS-2.0 elements + optional custom-named
// elements for size/seeds/peers/magnet/infohash.

const (
	torrentEnclosureMimeType = "application/x-bittorrent"
	magnetEnclosureMimeType  = "application/x-bittorrent;x-scheme-handler/magnet"
)

var preferredEnclosureMimeTypes = []string{torrentEnclosureMimeType, magnetEnclosureMimeType}

// ParseError is the typed error wrapper. C# cite: TorrentRssParser does
// not classify auth/rate-limit errors itself (the generic RssParser
// handles HTTP-level status codes upstream). We surface only generic
// XML-parse failures here.
type ParseError struct {
	Message string
}

func (e *ParseError) Error() string {
	return "torrentrss: " + e.Message
}

// xmlRSS is the wire shape — generic RSS-2.0 with arbitrary extra
// elements per item (the user-configured *ElementName settings).
type xmlRSS struct {
	XMLName xml.Name    `xml:"rss"`
	Channel *xmlChannel `xml:"channel"`
}

type xmlChannel struct {
	Items []xmlItem `xml:"item"`
}

type xmlItem struct {
	Title       string         `xml:"title"`
	Guid        string         `xml:"guid"`
	Link        string         `xml:"link"`
	Comments    string         `xml:"comments"`
	PubDate     string         `xml:"pubDate"`
	Description string         `xml:"description"`
	Enclosures  []xmlEnclosure `xml:"enclosure"`

	// Extra holds every non-standard child element so the per-feed
	// "<sizeElementName>", "<seedsElementName>" etc. can be looked up
	// at parse time. Captured via xml:",any".
	Extra []xmlExtra `xml:",any"`
}

type xmlExtra struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

type xmlEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// ParseOptions tunes ParseRSS. Mirrors the shape of torznab.ParseOptions
// + a Settings reference for per-feed format knobs.
type ParseOptions struct {
	IndexerName    string
	IndexerID      int
	FeedURL        string
	ParserSettings ParserSettings
}

// ParseRSS turns an XML body into a slice of *parser.TorrentInfo.
//
// Returns ([], nil) for empty channels. Returns (*ParseError, nil) for
// XML parse failures. Per-item parse failures silently skip + continue
// (matches RssParser.cs:78-83 catch+continue).
func ParseRSS(body []byte, opts *ParseOptions) ([]*parser.TorrentInfo, error) {
	if opts == nil {
		opts = &ParseOptions{}
	}

	var doc xmlRSS
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("torrentrss: parse RSS: %w", err)
	}
	if doc.Channel == nil {
		return []*parser.TorrentInfo{}, nil
	}

	out := make([]*parser.TorrentInfo, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		ti, err := processItem(it, opts)
		if err != nil {
			continue
		}
		out = append(out, ti)
	}
	return out, nil
}

func processItem(it xmlItem, opts *ParseOptions) (*parser.TorrentInfo, error) {
	pubDate, err := parsePubDate(it)
	if err != nil {
		return nil, err
	}

	ri := parser.ReleaseInfo{
		Title:       html.UnescapeString(strings.TrimSpace(it.Title)),
		Guid:        valueOr(it.Guid, ""),
		PublishDate: pubDate,
		IndexerID:   opts.IndexerID,
		Indexer:     opts.IndexerName,
		DownloadURL: getDownloadURL(it, opts),
		InfoURL:     getInfoURL(it, opts),
		CommentURL:  getCommentURL(it, opts),
	}
	ri.Size = getSize(it, &opts.ParserSettings)

	t := &parser.TorrentInfo{
		ReleaseInfo: ri,
		MagnetURL:   getMagnetURL(it, &opts.ParserSettings),
		InfoHash:    getInfoHash(it, &opts.ParserSettings),
	}

	if seeders, ok := getSeeders(it, &opts.ParserSettings); ok {
		t.Seeders = &seeders
	}
	if peers, ok := getPeers(it, t.Seeders, &opts.ParserSettings); ok {
		t.Peers = &peers
	}
	return t, nil
}

func parsePubDate(it xmlItem) (time.Time, error) {
	if strings.TrimSpace(it.PubDate) == "" {
		return time.Time{}, errors.New("missing pubDate")
	}
	return parseDate(it.PubDate)
}

var dateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	// Some RSS feeds (LimeTorrents) emit pubDate without the day-of-week
	// prefix.
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006/01/02 15:04:05",
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date format: %q", s)
}

// extra returns the value of the first non-standard child element with
// the given (case-insensitive) local name, or "" when absent.
func extra(it xmlItem, name string) string {
	if name == "" {
		return ""
	}
	for _, e := range it.Extra {
		if strings.EqualFold(e.XMLName.Local, name) {
			return e.Value
		}
	}
	return ""
}

// getSize ports TorrentRssParser.GetSize at :178-191. Tries enclosure
// length first; falls back to a configurable element name OR a
// description-regex parse.
func getSize(it xmlItem, p *ParserSettings) int64 {
	// 1. Configurable size element.
	if p.SizeElementName != "" {
		v := extra(it, p.SizeElementName)
		if v != "" {
			if n, ok := ParseSize(v, true); ok {
				return n
			}
		}
	}
	// 2. Enclosure length (always tried).
	if e := preferredEnclosure(it.Enclosures); e != nil && e.Length > 0 {
		return e.Length
	}
	// 3. Description-regex fallback (when ParseSizeInDescription is on).
	if p.ParseSizeInDescription && it.Description != "" {
		if n, ok := ParseSize(it.Description, true); ok {
			return n
		}
	}
	return 0
}

// getDownloadURL prefers the enclosure URL when UseEnclosureUrl is set
// (mirrors TorrentRssParser.cs:36 default), else uses <link>.
func getDownloadURL(it xmlItem, opts *ParseOptions) string {
	if opts.ParserSettings.UseEnclosureUrl {
		if e := preferredEnclosure(it.Enclosures); e != nil {
			return parseRelative(e.URL, opts.FeedURL)
		}
	}
	return parseRelative(it.Link, opts.FeedURL)
}

// getMagnetURL mirrors :95-115. Either reads from a configured element
// name OR detects when the download URL is a magnet:.
func getMagnetURL(it xmlItem, p *ParserSettings) string {
	if p.MagnetElementName != "" {
		v := extra(it, p.MagnetElementName)
		if strings.HasPrefix(v, "magnet:") {
			return v
		}
		return ""
	}
	if strings.HasPrefix(it.Link, "magnet:") {
		return it.Link
	}
	return ""
}

// getInfoHash mirrors :72-93. Either reads from a configured element name
// OR extracts from the magnet URL's `xt=urn:btih:` fragment.
func getInfoHash(it xmlItem, p *ParserSettings) string {
	if p.InfoHashElementName != "" {
		return extra(it, p.InfoHashElementName)
	}
	magnet := getMagnetURL(it, p)
	if magnet == "" {
		return ""
	}
	return extractInfoHashFromMagnet(magnet)
}

// extractInfoHashFromMagnet pulls the v1 BTIH from a magnet URI's xt
// parameter. Best-effort — the full C# code uses MonoTorrent.MagnetLink
// which handles v2 hashes etc.; the Go port handles the common v1 case
// inline. C# divergence flagged at the package level.
func extractInfoHashFromMagnet(magnet string) string {
	idx := strings.Index(magnet, "?")
	if idx < 0 {
		return ""
	}
	query, err := url.ParseQuery(magnet[idx+1:])
	if err != nil {
		return ""
	}
	for _, xt := range query["xt"] {
		const prefix = "urn:btih:"
		if strings.HasPrefix(xt, prefix) {
			return strings.ToLower(xt[len(prefix):])
		}
	}
	return ""
}

// getSeeders ports :117-147. Tries description regex (when enabled),
// then a configured element name.
func getSeeders(it xmlItem, p *ParserSettings) (int, bool) {
	if p.ParseSeedersInDescription && it.Description != "" {
		if v, ok := matchInt(parseSeedersRegex, it.Description); ok {
			return v, true
		}
		// Peers-minus-leechers fallback per :131-137.
		peers, peersOK := matchInt(parsePeersRegex, it.Description)
		leechers, leechersOK := matchInt(parseLeechersRegex, it.Description)
		if peersOK && leechersOK {
			return peers - leechers, true
		}
	}
	if p.SeedsElementName != "" {
		v := extra(it, p.SeedsElementName)
		if v != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// getPeers ports :149-176. Tries description regex (when enabled), then
// a configured element name. Optionally sums seeders+peers.
func getPeers(it xmlItem, seeders *int, p *ParserSettings) (int, bool) {
	if p.ParseSeedersInDescription && it.Description != "" {
		if v, ok := matchInt(parsePeersRegex, it.Description); ok {
			return v, true
		}
		// Seeders+leechers fallback per :160-166.
		seedersDesc, seedersOK := matchInt(parseSeedersRegex, it.Description)
		leechers, leechersOK := matchInt(parseLeechersRegex, it.Description)
		if seedersOK && leechersOK {
			return seedersDesc + leechers, true
		}
	}
	if p.PeersElementName != "" {
		v := extra(it, p.PeersElementName)
		if v != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				if p.CalculatePeersAsSum && seeders != nil {
					return n + *seeders, true
				}
				return n, true
			}
		}
	}
	return 0, false
}

// parseSeedersRegex / parseLeechersRegex / parsePeersRegex port the regexes
// at TorrentRssParser.cs:193-195. The patterns match either "Seeders: N"
// or "N seeders" (case-insensitive).
var (
	parseSeedersRegex  = regexp.MustCompile(`(?i)(?:Seeder)s?:\s+(?P<value>\d+)|(?P<value2>\d+)\s+(?:seeder)s?`)
	parseLeechersRegex = regexp.MustCompile(`(?i)(?:Leecher)s?:\s+(?P<value>\d+)|(?P<value2>\d+)\s+(?:leecher)s?`)
	parsePeersRegex    = regexp.MustCompile(`(?i)(?:Peer)s?:\s+(?P<value>\d+)|(?P<value2>\d+)\s+(?:peer)s?`)
)

// matchInt finds the first capture group named "value" or "value2"
// (alternation in the regex above) and returns its int value.
func matchInt(re *regexp.Regexp, s string) (int, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	for i, name := range re.SubexpNames() {
		if (name == "value" || name == "value2") && i < len(m) && m[i] != "" {
			n, err := strconv.Atoi(m[i])
			if err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// preferredEnclosure picks the first torrent / magnet enclosure.
func preferredEnclosure(enclosures []xmlEnclosure) *xmlEnclosure {
	for _, mime := range preferredEnclosureMimeTypes {
		for i := range enclosures {
			if enclosures[i].Type == mime {
				return &enclosures[i]
			}
		}
	}
	if len(enclosures) > 0 {
		// Fall through to first enclosure when no mime matches — many
		// generic torrent feeds set type="" or "application/octet-stream".
		// C# RssParser.GetEnclosure with enforceMimeType=false has the
		// same fallback at :297-322.
		return &enclosures[0]
	}
	return nil
}

func getInfoURL(it xmlItem, opts *ParseOptions) string {
	c := strings.TrimSuffix(it.Comments, "#comments")
	return parseRelative(c, opts.FeedURL)
}

func getCommentURL(it xmlItem, opts *ParseOptions) string {
	return parseRelative(it.Comments, opts.FeedURL)
}

func parseRelative(value, feedURL string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	if feedURL == "" {
		return value
	}
	base, err := url.Parse(feedURL)
	if err != nil {
		return value
	}
	rel, err := url.Parse(value)
	if err != nil {
		return value
	}
	return base.ResolveReference(rel).String()
}

func valueOr(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}

// parseURL is a tiny wrapper used by settings.go. Defined here so the
// settings file doesn't need its own net/url import.
func parseURL(s string) (*url.URL, error) {
	return url.Parse(s)
}

// ParseSize parses a size string like "1.3 GB" / "523 MiB" / "12345" into
// bytes. C# cite: src/NzbDrone.Core/Indexers/RssParser.cs:357-403.
//
// defaultBinaryPrefix=true uses 1024-based scaling (KiB/MiB/GiB) when the
// unit is ambiguous; false uses 1000-based. Sonarr passes true here per
// the C# call site at TorrentRssParser.cs:186.
//
// Returns (n, true) on success. Returns (0, false) when the input isn't
// recognisable as a size.
func ParseSize(s string, defaultBinaryPrefix bool) (int64, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	// Pure-digits → raw bytes.
	if isAllDigits(strings.TrimSpace(s)) {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	m := parseSizeRegex.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	rawValue := strings.ReplaceAll(m[1], ",", "")
	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil {
		return 0, false
	}
	unit := strings.ToLower(m[2])
	switch unit {
	case "kb":
		return convertSize(value, 1, defaultBinaryPrefix), true
	case "mb":
		return convertSize(value, 2, defaultBinaryPrefix), true
	case "gb":
		return convertSize(value, 3, defaultBinaryPrefix), true
	case "kib":
		return convertSize(value, 1, true), true
	case "mib":
		return convertSize(value, 2, true), true
	case "gib":
		return convertSize(value, 3, true), true
	}
	return int64(value), true
}

var parseSizeRegex = regexp.MustCompile(`(?i)((?:\d+,)*\d+(?:\.\d{1,3})?)\W?([KMG]i?B)`)

func convertSize(value float64, power int, binary bool) int64 {
	prefix := 1000.0
	if binary {
		prefix = 1024.0
	}
	mult := 1.0
	for i := 0; i < power; i++ {
		mult *= prefix
	}
	return int64(value * mult)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
