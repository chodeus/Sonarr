package newznab

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

// RSS parser for Newznab indexers. C# cite:
// src/NzbDrone.Core/Indexers/Newznab/NewznabRssParser.cs:13-253 plus the
// shared base at src/NzbDrone.Core/Indexers/RssParser.cs:20-419.
//
// The Go port collapses the inheritance hierarchy (RssParser → NewznabRssParser)
// into a single ParseRSS function plus small helpers. The shared
// RSS-2.0 walking logic that the Torznab + RSS-fallback indexer slices
// will need lives here for now and will be lifted to a shared package
// when sub-arc 5 (Torznab) lands.

// Newznab XML namespace for `<newznab:attr name="X" value="Y"/>` elements.
// C# cite: NewznabRssParser.cs:15. The namespace is referenced inline in
// the xmlItem.Attrs struct tag — keeping a separate const would just
// duplicate the literal, so the URL lives only in that tag.

// MIME types Newznab feeds emit on `<enclosure>` elements. C# cite:
// RssParser.cs:24-28. Newznab is Usenet-only so we only enforce the
// nzb mime; Torznab will reuse these constants when sub-arc 5 ships.
const (
	NzbEnclosureMimeType     = "application/x-nzb"
	TorrentEnclosureMimeType = "application/x-bittorrent"
	MagnetEnclosureMimeType  = "application/x-bittorrent;x-scheme-handler/magnet"
)

// preferredEnclosureMimeTypes for Newznab. C# cite:
// NewznabRssParser.cs:19 (UsenetEnclosureMimeTypes from RssParser.cs:26).
var preferredEnclosureMimeTypes = []string{NzbEnclosureMimeType}

// torrentEnclosureMimeTypes — used to surface a "did you mean Torznab?"
// warning when a Newznab feed contains torrent enclosures only. C# cite:
// RssParser.cs:27 + NewznabRssParser.cs:74-78.
var torrentEnclosureMimeTypes = []string{TorrentEnclosureMimeType, MagnetEnclosureMimeType}

// ParseError is the typed Newznab API-error wrapper. C# cite:
// NewznabRssParser.CheckError at NewznabRssParser.cs:23-51 — three classes:
//   - APIKey (code 100-199, or "Missing parameter" / contains "apikey")
//   - RateLimit ("Request limit reached")
//   - Generic (anything else)
type ParseError struct {
	Code    int
	Message string
	Kind    ErrorKind
}

// ErrorKind classifies a Newznab API error.
type ErrorKind int

// Error kinds.
const (
	ErrGeneric   ErrorKind = iota // Anything not specifically classified.
	ErrAPIKey                     // Code 100-199 or "Missing parameter" / message contains "apikey".
	ErrRateLimit                  // "Request limit reached".
)

func (e *ParseError) Error() string {
	switch e.Kind {
	case ErrAPIKey:
		return "newznab API key error: " + e.Message
	case ErrRateLimit:
		return "newznab rate limit reached: " + e.Message
	default:
		return fmt.Sprintf("newznab API error %d: %s", e.Code, e.Message)
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

// xmlRSS is the wire shape of a Newznab RSS feed. The top-level may also
// be an `<error>` element (auth-failure path) — we attempt both.
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
	Languages   []string       `xml:"language"` // fallback when newznab:language attr absent
	Attrs       []xmlAttr      `xml:"http://www.newznab.com/DTD/2010/feeds/attributes/ attr"`
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

// xmlTopError is the top-level `<error code="..." description="..."/>` shape.
type xmlTopError struct {
	XMLName     xml.Name `xml:"error"`
	Code        int      `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

// ParseOptions tunes ParseRSS for tests / non-default invocations. The
// fields mirror the C# protected properties UseEnclosureLength,
// ParseSizeInDescription, ScopedURL (used by ParseUrl). When the zero
// value is fine the caller can pass a nil *ParseOptions.
type ParseOptions struct {
	// IndexerName is the human-readable indexer label written into
	// ReleaseInfo.Indexer. Mirrors the C# IndexerBase.Name passthrough.
	IndexerName string

	// IndexerID is the persisted Indexers row ID (or 0 for unsaved
	// providers). Stamped onto every ReleaseInfo so downstream specs
	// can identify provenance.
	IndexerID int

	// FeedURL is the URL the feed was fetched from. Used to resolve
	// relative URLs inside the items via *url.URL.ResolveReference.
	// May be empty (relative URLs then pass through unchanged).
	FeedURL string

	// HasAPIKey indicates whether the original request URL carried an
	// `apikey=` query param. Mirrors the check at NewznabRssParser.cs:40.
	HasAPIKey bool
}

// ParseRSS turns an XML body into a slice of *parser.ReleaseInfo.
// C# cite: NewznabRssParser.PreProcess + ProcessItem + PostProcess at
// NewznabRssParser.cs:53-97.
//
// Returns (nil, *ParseError) on auth/rate-limit/API errors so callers can
// type-switch via IsAPIKey / IsRateLimit / errors.As. Returns ([], nil)
// when the document is well-formed but contains no items.
//
// The C# parser surfaces a "did you mean Torznab?" warning when a
// Newznab feed contains only torrent enclosures; the Go port returns
// the empty slice in that case (PostProcess returns false →
// ParseResponse returns an empty array per RssParser.cs:88-89). Callers
// should treat zero-results as a non-fatal mismatch.
func ParseRSS(body []byte, opts *ParseOptions) ([]*parser.ReleaseInfo, error) {
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
		return nil, fmt.Errorf("newznab: parse RSS: %w", err)
	}
	if doc.Channel == nil {
		return []*parser.ReleaseInfo{}, nil
	}

	// "Did you mean Torznab?" detection (NewznabRssParser.cs:68-85): if
	// every enclosure type across all items is a torrent type and none
	// is the preferred Newznab type, return empty.
	if onlyTorrentEnclosures(doc.Channel.Items) {
		return []*parser.ReleaseInfo{}, nil
	}

	out := make([]*parser.ReleaseInfo, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		ri, err := processItem(it, opts)
		if err != nil {
			// Per-item parse failures do NOT abort the whole feed; the
			// C# code logs and continues at RssParser.cs:78-83. We
			// silently skip in the Go port (logging will be added in
			// the Service slice when the slog instance is wired).
			continue
		}
		out = append(out, ri)
	}
	return out, nil
}

// classifyError ports the three-branch decision at NewznabRssParser.cs:35-50.
func classifyError(code int, message string, hasAPIKey bool) *ParseError {
	lower := strings.ToLower(message)

	if code >= 100 && code <= 199 {
		return &ParseError{Code: code, Message: message, Kind: ErrAPIKey}
	}
	// "Missing parameter" or message mentions apikey when the request didn't
	// carry one → API-key required.
	if !hasAPIKey && (message == "Missing parameter" || strings.Contains(lower, "apikey")) {
		return &ParseError{Code: code, Message: "Indexer requires an API key", Kind: ErrAPIKey}
	}
	if message == "Request limit reached" {
		return &ParseError{Code: code, Message: "API limit reached", Kind: ErrRateLimit}
	}
	return &ParseError{Code: code, Message: message, Kind: ErrGeneric}
}

// processItem turns one xmlItem into a *parser.ReleaseInfo. C# cite:
// NewznabRssParser.ProcessItem at :87-97 plus the base ProcessItem at
// RssParser.cs:147-176.
func processItem(it xmlItem, opts *ParseOptions) (*parser.ReleaseInfo, error) {
	pubDate, err := parsePubDate(it, opts)
	if err != nil {
		return nil, err
	}

	r := &parser.ReleaseInfo{
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
	r.Size = getSize(it)
	r.TvdbID = getIntAttr(it, "tvdbid")
	r.TvRageID = getIntAttr(it, "rageid")
	r.ImdbID = getImdbID(it)
	r.IndexerFlags = getFlags(it)
	return r, nil
}

// parsePubDate ports NewznabRssParser.GetPublishDate at :152-161 (prefer
// `newznab:usenetdate` over `<pubDate>`).
func parsePubDate(it xmlItem, opts *ParseOptions) (time.Time, error) {
	if v := tryGetNewznabAttribute(it, "usenetdate"); v != "" {
		if t, err := parseDate(v); err == nil {
			return t, nil
		}
	}
	if strings.TrimSpace(it.PubDate) == "" {
		// Mirrors RssParser.GetPublishDate at :194-202 which throws
		// UnsupportedFeedException. We surface as a normal error so the
		// per-item skip catches it.
		return time.Time{}, errors.New("missing pubDate")
	}
	t, err := parseDate(it.PubDate)
	if err != nil {
		_ = opts // reserved for future logging hookup
		return time.Time{}, err
	}
	return t, nil
}

// parseDate accepts the date formats the C# XElementExtensions.ParseDate
// helper handles: RFC822 with named offsets ("GMT") and numeric ("+0300"),
// plus the common Newznab variants. The C# DateTime parser is permissive;
// we approximate by trying the most common formats in order.
var dateLayouts = []string{
	time.RFC1123Z, // "Mon, 27 Feb 2012 11:09:39 -0500"
	time.RFC1123,  // "Mon, 27 Feb 2012 11:09:39 GMT"
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
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

// tryGetNewznabAttribute returns the value of the first <newznab:attr> with
// the given (case-insensitive) name. Mirrors NewznabRssParser.cs:221-234.
func tryGetNewznabAttribute(it xmlItem, key string) string {
	for _, a := range it.Attrs {
		if strings.EqualFold(a.Name, key) {
			return a.Value
		}
	}
	return ""
}

// tryGetMultipleNewznabAttributes returns every value for the named attr.
// Mirrors NewznabRssParser.cs:236-251.
func tryGetMultipleNewznabAttributes(it xmlItem, key string) []string {
	out := make([]string, 0, 2)
	for _, a := range it.Attrs {
		if strings.EqualFold(a.Name, key) {
			out = append(out, a.Value)
		}
	}
	return out
}

// getIntAttr parses an integer newznab attr; returns 0 when missing or
// unparseable. Mirrors GetTvdbId / GetTvRageId at NewznabRssParser.cs:163-185.
func getIntAttr(it xmlItem, key string) int {
	v := tryGetNewznabAttribute(it, key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// getImdbID renders the ImdbID as `tt%07d` per NewznabRssParser.cs:187-197.
// Returns "" when the attr is missing, unparseable, or non-positive.
func getImdbID(it xmlItem) string {
	v := tryGetNewznabAttribute(it, "imdb")
	if v == "" {
		return ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// getFlags ports NewznabRssParser.GetFlags at :199-219.
//
//	prematch=1 OR haspretime=1 → Scene
//	nuked=1                    → Nuked
//	subs!=""                   → Subtitles
func getFlags(it xmlItem) parser.IndexerFlags {
	var flags parser.IndexerFlags
	if tryGetNewznabAttribute(it, "prematch") == "1" || tryGetNewznabAttribute(it, "haspretime") == "1" {
		flags |= parser.IndexerFlagScene
	}
	if tryGetNewznabAttribute(it, "nuked") == "1" {
		flags |= parser.IndexerFlagNuked
	}
	if strings.TrimSpace(tryGetNewznabAttribute(it, "subs")) != "" {
		flags |= parser.IndexerFlagSubtitles
	}
	return flags
}

// getLanguages ports NewznabRssParser.GetLanguages at :109-137. Tries the
// newznab:language attr first (comma-separated), then falls back to the
// `<language>` elements at the item level. Each language name maps via
// language.FindByName.
func getLanguages(it xmlItem) []language.Language {
	values := tryGetMultipleNewznabAttributes(it, "language")
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

// getSize ports NewznabRssParser.GetSize at :139-150. Prefer the
// newznab:size attr; fall back to the enclosure length.
func getSize(it xmlItem) int64 {
	v := tryGetNewznabAttribute(it, "size")
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

// preferredEnclosure picks the first enclosure matching the preferred
// MIME type (Newznab → application/x-nzb). Mirrors RssParser.GetEnclosure
// at :297-322 with enforceMimeType=true.
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

// onlyTorrentEnclosures returns true when every encountered enclosure
// type is a torrent type AND none is the preferred Newznab type. Used
// by the "did you mean Torznab?" gate at NewznabRssParser.cs:68-85.
func onlyTorrentEnclosures(items []xmlItem) bool {
	sawAny := false
	sawPreferred := false
	allTorrent := true
	for _, it := range items {
		for _, e := range it.Enclosures {
			sawAny = true
			if hasMime(preferredEnclosureMimeTypes, e.Type) {
				sawPreferred = true
			}
			if !hasMime(torrentEnclosureMimeTypes, e.Type) {
				allTorrent = false
			}
		}
	}
	return sawAny && !sawPreferred && allTorrent
}

func hasMime(set []string, t string) bool {
	for _, s := range set {
		if s == t {
			return true
		}
	}
	return false
}

// getDownloadURL prefers the enclosure URL (Newznab sets UseEnclosureUrl=true
// at NewznabRssParser.cs:20), falling back to the <link> element.
func getDownloadURL(it xmlItem, opts *ParseOptions) string {
	if e := preferredEnclosure(it.Enclosures); e != nil {
		return parseURL(e.URL, opts.FeedURL)
	}
	return parseURL(it.Link, opts.FeedURL)
}

// getInfoURL strips the trailing `#comments` per NewznabRssParser.cs:99-102.
func getInfoURL(it xmlItem, opts *ParseOptions) string {
	c := strings.TrimSuffix(it.Comments, "#comments")
	return parseURL(c, opts.FeedURL)
}

// getCommentURL returns the raw comments URL.
func getCommentURL(it xmlItem, opts *ParseOptions) string {
	return parseURL(it.Comments, opts.FeedURL)
}

// parseURL returns "" for empty values, otherwise resolves the URL
// against the feed URL when relative. Mirrors RssParser.ParseUrl at :340-356.
func parseURL(value, feedURL string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	// Relative-resolve. The C# code uses HttpUri's custom join; for HTTP
	// resolution we use the standard *url.URL.ResolveReference which
	// matches RFC 3986 — appropriate here since the input is a real
	// URL coming off the wire.
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
