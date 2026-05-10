package torznab

import (
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
)

// RSS parser for Torznab indexers. C# cite:
// src/NzbDrone.Core/Indexers/Torznab/TorznabRssParser.cs:13-321 plus the
// shared TorrentRssParser base.
//
// Like the newznab.ParseRSS function, this is a single self-contained
// entry point that produces typed *parser.TorrentInfo values rather than
// the C# inheritance ladder (TorrentRssParser → TorznabRssParser).
// Output uses parser.TorrentInfo (embeds parser.ReleaseInfo with
// MagnetURL/InfoHash/Seeders/Peers extras).
//
// Torznab MIME types: enclosure type is application/x-bittorrent or the
// magnet variant. Newznab feeds containing only torrent enclosures get
// the "did you mean Torznab?" gate ported here in reverse — a Torznab
// feed containing only nzb enclosures returns empty.
const (
	torrentEnclosureMimeType = "application/x-bittorrent"
	magnetEnclosureMimeType  = "application/x-bittorrent;x-scheme-handler/magnet"
	nzbEnclosureMimeType     = "application/x-nzb"
)

var (
	preferredEnclosureMimeTypes = []string{torrentEnclosureMimeType, magnetEnclosureMimeType}
	usenetEnclosureMimeTypes    = []string{nzbEnclosureMimeType}
)

// ParseError mirrors the API-error classification at
// TorznabRssParser.PreProcess :22-57.
type ParseError struct {
	Code    int
	Message string
	Kind    ErrorKind
}

// ErrorKind classifies a Torznab API error.
type ErrorKind int

// Error kinds.
const (
	ErrGeneric ErrorKind = iota
	ErrAPIKey
	ErrRateLimit
)

func (e *ParseError) Error() string {
	switch e.Kind {
	case ErrAPIKey:
		return "torznab API key error: " + e.Message
	case ErrRateLimit:
		return "torznab rate limit reached: " + e.Message
	default:
		return fmt.Sprintf("torznab API error %d: %s", e.Code, e.Message)
	}
}

// IsAPIKey reports whether err is a *ParseError of ErrAPIKey kind.
func IsAPIKey(err error) bool {
	var p *ParseError
	if errors.As(err, &p) {
		return p.Kind == ErrAPIKey
	}
	return false
}

// IsRateLimit reports whether err is a *ParseError of ErrRateLimit kind.
func IsRateLimit(err error) bool {
	var p *ParseError
	if errors.As(err, &p) {
		return p.Kind == ErrRateLimit
	}
	return false
}

// xmlRSS is the wire shape. Torznab uses a different namespace from Newznab
// for its `<torznab:attr>` elements: `http://torznab.com/schemas/2015/feed`.
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
	Languages   []string       `xml:"language"` // fallback when torznab:language attr absent
	Attrs       []xmlAttr      `xml:"http://torznab.com/schemas/2015/feed attr"`
}

type xmlEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type xmlAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlTopError struct {
	XMLName     xml.Name `xml:"error"`
	Code        int      `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

// ParseOptions tunes ParseRSS — same shape as newznab.ParseOptions.
type ParseOptions struct {
	IndexerName string
	IndexerID   int
	FeedURL     string
	HasAPIKey   bool
}

// ParseRSS turns an XML body into a slice of *parser.TorrentInfo.
// C# cite: TorznabRssParser.PreProcess + ProcessItem + PostProcess at
// :22-91.
//
// Returns (nil, *ParseError) on auth / rate-limit / API errors.
// Returns ([], nil) for empty channels OR for the inverse "did you mean
// Newznab?" gate (only nzb enclosures present).
func ParseRSS(body []byte, opts *ParseOptions) ([]*parser.TorrentInfo, error) {
	if opts == nil {
		opts = &ParseOptions{}
	}

	// Top-level error?
	var topErr xmlTopError
	if err := xml.Unmarshal(body, &topErr); err == nil && topErr.XMLName.Local == "error" {
		return nil, classifyError(topErr.Code, topErr.Description, opts.HasAPIKey)
	}

	var doc xmlRSS
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("torznab: parse RSS: %w", err)
	}
	if doc.Channel == nil {
		return []*parser.TorrentInfo{}, nil
	}

	if onlyNzbEnclosures(doc.Channel.Items) {
		// "Did you mean Newznab?" — Torznab feed contains only nzb mimes.
		// C# logs a warning and returns empty (PostProcess returns false).
		return []*parser.TorrentInfo{}, nil
	}

	out := make([]*parser.TorrentInfo, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		ti, err := processItem(it, opts)
		if err != nil {
			continue // per-item parse failures don't abort the feed (mirrors C# catch+continue)
		}
		out = append(out, ti)
	}
	return out, nil
}

// classifyError ports TorznabRssParser.cs:38-57 — three-branch decision:
//   - code 100-199 → APIKey ("Invalid API key")
//   - "Missing parameter" without apikey in request → APIKey ("Indexer requires an API key")
//   - "Request limit reached" → RateLimit ("API limit reached")
//   - else → Generic
func classifyError(code int, message string, hasAPIKey bool) *ParseError {
	if code >= 100 && code <= 199 {
		return &ParseError{Code: code, Message: "Invalid API key", Kind: ErrAPIKey}
	}
	if !hasAPIKey && message == "Missing parameter" {
		return &ParseError{Code: code, Message: "Indexer requires an API key", Kind: ErrAPIKey}
	}
	if message == "Request limit reached" {
		return &ParseError{Code: code, Message: "API limit reached", Kind: ErrRateLimit}
	}
	return &ParseError{Code: code, Message: message, Kind: ErrGeneric}
}

// processItem turns one xmlItem into a *parser.TorrentInfo. C# cite:
// TorznabRssParser.ProcessItem at :78-91 + the TorrentRssParser base.
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
		Languages:   getLanguages(it),
	}
	ri.Size = getSize(it)
	ri.TvdbID = getIntAttr(it, "tvdbid")
	ri.TvRageID = getIntAttr(it, "rageid")
	ri.ImdbID = getImdbID(it)
	ri.IndexerFlags = getFlags(it)

	t := &parser.TorrentInfo{
		ReleaseInfo: ri,
		MagnetURL:   tryGetTorznabAttribute(it, "magneturl"),
		InfoHash:    tryGetTorznabAttribute(it, "infohash"),
	}
	if seeders, ok := getSeeders(it); ok {
		t.Seeders = &seeders
	}
	if peers, ok := getPeers(it); ok {
		t.Peers = &peers
	}
	return t, nil
}

// parsePubDate ports TorrentRssParser.GetPublishDate via base behaviour —
// pubDate is required.
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

// tryGetTorznabAttribute returns the value of the first <torznab:attr>
// with the given (case-insensitive) name. Mirrors :282-294.
func tryGetTorznabAttribute(it xmlItem, key string) string {
	for _, a := range it.Attrs {
		if strings.EqualFold(a.Name, key) {
			return a.Value
		}
	}
	return ""
}

// tryGetMultipleTorznabAttributes returns every value for the named attr.
// Mirrors :304-319.
func tryGetMultipleTorznabAttributes(it xmlItem, key string) []string {
	out := make([]string, 0, 2)
	for _, a := range it.Attrs {
		if strings.EqualFold(a.Name, key) {
			out = append(out, a.Value)
		}
	}
	return out
}

// tryGetFloatTorznabAttribute parses a torznab attr as float64; returns
// dflt when absent or unparseable. Mirrors :297-302.
func tryGetFloatTorznabAttribute(it xmlItem, key string, dflt float64) float64 {
	v := tryGetTorznabAttribute(it, key)
	if v == "" {
		return dflt
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return dflt
	}
	return f
}

func getIntAttr(it xmlItem, key string) int {
	v := tryGetTorznabAttribute(it, key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// getImdbID ports :181-191 — render as `tt%07d` only when value > 0.
func getImdbID(it xmlItem) string {
	v := tryGetTorznabAttribute(it, "imdb")
	if v == "" {
		return ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// getFlags ports :235-280 — DownloadVolumeFactor + UploadVolumeFactor +
// tags map onto IndexerFlags bits.
func getFlags(it xmlItem) parser.IndexerFlags {
	var flags parser.IndexerFlags
	dl := tryGetFloatTorznabAttribute(it, "downloadvolumefactor", 1)
	ul := tryGetFloatTorznabAttribute(it, "uploadvolumefactor", 1)

	switch dl {
	case 0.0:
		flags |= parser.IndexerFlagFreeleech
	case 0.25:
		flags |= parser.IndexerFlagFreeleech75
	case 0.5:
		flags |= parser.IndexerFlagHalfleech
	case 0.75:
		flags |= parser.IndexerFlagFreeleech25
	}
	if ul == 2.0 {
		flags |= parser.IndexerFlagDoubleUpload
	}

	tags := tryGetMultipleTorznabAttributes(it, "tag")
	for _, t := range tags {
		if strings.EqualFold(t, "internal") {
			flags |= parser.IndexerFlagInternal
		}
		if strings.EqualFold(t, "scene") {
			flags |= parser.IndexerFlagScene
		}
	}
	return flags
}

// getLanguages ports :103-125. Tries torznab:language attrs first, then
// falls back to <language> elements.
func getLanguages(it xmlItem) []language.Language {
	values := tryGetMultipleTorznabAttributes(it, "language")
	if len(values) == 0 {
		values = it.Languages
	}
	out := make([]language.Language, 0, 2)
	for _, raw := range values {
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if lang, ok := language.FindByName(name); ok {
				out = append(out, lang)
			}
		}
	}
	return out
}

// getSize ports :127-138 — torznab:size attr preferred over enclosure length.
func getSize(it xmlItem) int64 {
	v := tryGetTorznabAttribute(it, "size")
	if v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	if e := preferredEnclosure(it.Enclosures); e != nil {
		return e.Length
	}
	return 0
}

// getSeeders ports :203-213 — torznab:seeders preferred; no fallback because
// without the attr we can't know.
func getSeeders(it xmlItem) (int, bool) {
	v := tryGetTorznabAttribute(it, "seeders")
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// getPeers ports :215-233 — torznab:peers preferred; if absent compute
// from seeders+leechers.
func getPeers(it xmlItem) (int, bool) {
	if v := tryGetTorznabAttribute(it, "peers"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n, true
		}
	}
	seeders := tryGetTorznabAttribute(it, "seeders")
	leechers := tryGetTorznabAttribute(it, "leechers")
	if seeders != "" && leechers != "" {
		s, err1 := strconv.Atoi(seeders)
		l, err2 := strconv.Atoi(leechers)
		if err1 == nil && err2 == nil {
			return s + l, true
		}
	}
	return 0, false
}

// preferredEnclosure picks the first torrent/magnet enclosure.
func preferredEnclosure(enclosures []xmlEnclosure) *xmlEnclosure {
	for _, mime := range preferredEnclosureMimeTypes {
		for i := range enclosures {
			if enclosures[i].Type == mime {
				return &enclosures[i]
			}
		}
	}
	return nil
}

// onlyNzbEnclosures returns true when every encountered enclosure is an
// nzb mime AND none is a torrent/magnet mime. Used by the inverse
// "did you mean Newznab?" gate at :63-70.
func onlyNzbEnclosures(items []xmlItem) bool {
	sawAny := false
	sawPreferred := false
	allNzb := true
	for _, it := range items {
		for _, e := range it.Enclosures {
			sawAny = true
			if hasMime(preferredEnclosureMimeTypes, e.Type) {
				sawPreferred = true
			}
			if !hasMime(usenetEnclosureMimeTypes, e.Type) {
				allNzb = false
			}
		}
	}
	return sawAny && !sawPreferred && allNzb
}

func hasMime(set []string, t string) bool {
	for _, s := range set {
		if s == t {
			return true
		}
	}
	return false
}

// getDownloadURL ports the C# GetDownloadUrl at :145-155 — prefer the
// enclosure URL; if it's not a well-formed absolute URL, fall back to
// re-parsing the same enclosure URL through the relative-resolver.
//
// We simplify by always using the relative-resolver path since it
// already passes through absolute URLs unchanged.
func getDownloadURL(it xmlItem, opts *ParseOptions) string {
	if e := preferredEnclosure(it.Enclosures); e != nil {
		return parseURL(e.URL, opts.FeedURL)
	}
	return parseURL(it.Link, opts.FeedURL)
}

// getInfoURL strips `#comments` per :93-96.
func getInfoURL(it xmlItem, opts *ParseOptions) string {
	c := strings.TrimSuffix(it.Comments, "#comments")
	return parseURL(c, opts.FeedURL)
}

// getCommentURL returns the raw comments URL.
func getCommentURL(it xmlItem, opts *ParseOptions) string {
	return parseURL(it.Comments, opts.FeedURL)
}

func parseURL(value, feedURL string) string {
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
