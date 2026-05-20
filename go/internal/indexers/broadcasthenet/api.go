package broadcasthenet

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/parser"
)

// jsonRPCRequest is the JSON-RPC 2.0 outer envelope BroadcastheNet
// expects. Mirrors what the C# `JsonRpcRequestBuilder.Call(...)`
// emits — a `getTorrents` method call with positional params
// [apiKey, query, pageSize, offset].
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

// jsonRPCResponse is the wrapped response envelope. Error is non-nil
// when the API returned a JSON-RPC error; Result is non-nil when the
// call succeeded.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
	ID      int             `json:"id"`
}

// jsonRPCError is the JSON-RPC fault object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) String() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("code=%d message=%s", e.Code, e.Message)
}

// torrentQuery is the per-request filter shape. Mirrors C#
// `BroadcastheNetTorrentQuery` at `BroadcastheNetTorrentQuery.cs:6-39`.
// All fields are omitempty so unset fields don't ride into the JSON
// body (matches C# `DefaultValueHandling.Ignore`).
type torrentQuery struct {
	ID         string   `json:"id,omitempty"`
	Category   string   `json:"category,omitempty"`
	Name       string   `json:"name,omitempty"`
	Search     string   `json:"search,omitempty"`
	Codec      []string `json:"codec,omitempty"`
	Container  []string `json:"container,omitempty"`
	Source     []string `json:"source,omitempty"`
	Resolution []string `json:"resolution,omitempty"`
	Origin     []string `json:"origin,omitempty"`
	Hash       string   `json:"hash,omitempty"`
	Tvdb       string   `json:"tvdb,omitempty"`
	Tvrage     string   `json:"tvrage,omitempty"`
	Age        string   `json:"age,omitempty"`
}

// torrentsResult is the inner `result` payload. Mirrors C#
// `BroadcastheNetTorrents` at `BroadcastheNetTorrents.cs:5-10`.
// Torrents is a map keyed by torrent ID — the C# `Dictionary<int,T>`
// shape — and Go's json decoder handles the keyed dict naturally.
type torrentsResult struct {
	Results  int                    `json:"results"`
	Torrents map[string]*btnTorrent `json:"torrents"`
}

// btnTorrent mirrors C# `BroadcastheNetTorrent.cs:5-33`. Pointer
// fields use Go's *int / *int64 nullable shape to match the C#
// `int?` / `long?` nullable semantics that surface on Seeders /
// Leechers / Snatched / TvdbID / TvrageID.
type btnTorrent struct {
	GroupName      string   `json:"GroupName"`
	GroupID        int      `json:"GroupID"`
	TorrentID      int      `json:"TorrentID"`
	SeriesID       int      `json:"SeriesID"`
	Series         string   `json:"Series"`
	SeriesBanner   string   `json:"SeriesBanner"`
	SeriesPoster   string   `json:"SeriesPoster"`
	YoutubeTrailer string   `json:"YoutubeTrailer"`
	Category       string   `json:"Category"`
	Snatched       *int     `json:"Snatched"`
	Seeders        *int     `json:"Seeders"`
	Leechers       *int     `json:"Leechers"`
	Source         string   `json:"Source"`
	Container      string   `json:"Container"`
	Codec          string   `json:"Codec"`
	Resolution     string   `json:"Resolution"`
	Origin         string   `json:"Origin"`
	ReleaseName    string   `json:"ReleaseName"`
	Size           int64    `json:"Size"`
	Time           int64    `json:"Time"`
	TvdbID         *int     `json:"TvdbID"`
	TvrageID       *int     `json:"TvrageID"`
	ImdbID         string   `json:"ImdbID"`
	InfoHash       string   `json:"InfoHash"`
	Tags           []string `json:"Tags"`
	DownloadURL    string   `json:"DownloadURL"`
}

// callLimitMarker is the case-insensitive substring the C# parser
// at `:43-46` matches to detect rate-limit responses. Pre-lowercased.
const callLimitMarker = "call limit exceeded"

// regexProtocol mirrors C# `BroadcastheNetParser.cs:14`. Used to
// rewrite the DownloadURL scheme to match the request's scheme —
// the C# tree forces https on a request-side https URL even when
// the API returns http URLs.
var regexProtocol = regexp.MustCompile(`^https?:`)

// parseResponse ports `BroadcastheNetParser.cs:16-107` — validates
// the HTTP status code (passed in by caller), checks for the
// rate-limit / internal-error sentinel strings in the body, decodes
// the JSON-RPC envelope, walks the torrents map.
//
// reqScheme is the scheme of the original request URL ("http" or
// "https"); used to rewrite returned DownloadURLs via regexProtocol
// (the C# parser at `:65-73` does this to keep the scheme consistent
// across the grab-side request chain).
//
// HTTP-level failures (401, 404, 503, !200) return typed errors so
// callers can distinguish "API key invalid" vs "unexpected status".
// JSON-RPC-level Error envelopes likewise propagate as errors.
func parseResponse(
	body []byte,
	statusCode int,
	contentType string,
	reqScheme string,
	settings *Settings,
	indexerName string,
	indexerID int,
) ([]*parser.TorrentInfo, error) {
	switch statusCode {
	case 401:
		return nil, fmt.Errorf("broadcasthenet: API key invalid or not authorized")
	case 404:
		return nil, fmt.Errorf("broadcasthenet: API endpoint returned NotFound — API may have changed")
	case 503:
		return nil, fmt.Errorf("broadcasthenet: rate limit reached (150 requests/hour)")
	}
	if statusCode != 200 {
		return nil, fmt.Errorf("broadcasthenet: unexpected HTTP status %d", statusCode)
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return nil, fmt.Errorf("broadcasthenet: response is HTML — site likely blocked or unavailable")
	}
	if strings.Contains(strings.ToLower(string(body)), callLimitMarker) {
		return nil, fmt.Errorf("broadcasthenet: rate limit reached (150 requests/hour)")
	}
	if strings.TrimSpace(string(body)) == "Query execution was interrupted" {
		return nil, fmt.Errorf("broadcasthenet: API internal server error")
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("broadcasthenet: parse JSON-RPC envelope: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("broadcasthenet: API error: %s", resp.Error.String())
	}
	if len(resp.Result) == 0 {
		return []*parser.TorrentInfo{}, nil
	}
	var result torrentsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("broadcasthenet: parse result: %w", err)
	}
	if result.Results == 0 || result.Torrents == nil {
		return []*parser.TorrentInfo{}, nil
	}

	protocolReplacement := reqScheme + ":"
	out := make([]*parser.TorrentInfo, 0, len(result.Torrents))
	for _, t := range result.Torrents {
		if t == nil {
			continue
		}
		out = append(out, torrentToInfo(t, protocolReplacement, settings, indexerName, indexerID))
	}
	return out, nil
}

// torrentToInfo maps one btnTorrent to a parser.TorrentInfo. Mirrors
// `BroadcastheNetParser.cs:67-103`.
func torrentToInfo(t *btnTorrent, protocolReplacement string, _ *Settings, indexerName string, indexerID int) *parser.TorrentInfo {
	peers := 0
	if t.Seeders != nil {
		peers += *t.Seeders
	}
	if t.Leechers != nil {
		peers += *t.Leechers
	}
	infoURL := fmt.Sprintf("%s//broadcasthe.net/torrents.php?id=%d&torrentid=%d",
		protocolReplacement, t.GroupID, t.TorrentID)
	downloadURL := regexProtocol.ReplaceAllString(t.DownloadURL, protocolReplacement)

	tvdbID := 0
	if t.TvdbID != nil && *t.TvdbID > 0 {
		tvdbID = *t.TvdbID
	}
	tvrageID := 0
	if t.TvrageID != nil && *t.TvrageID > 0 {
		tvrageID = *t.TvrageID
	}

	imdbID := ""
	if t.ImdbID != "" {
		if n, err := strconv.Atoi(t.ImdbID); err == nil && n > 0 {
			imdbID = fmt.Sprintf("tt%07d", n)
		}
	}

	seedersCopy := 0
	if t.Seeders != nil {
		seedersCopy = *t.Seeders
	}
	peersCopy := peers

	return &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Guid:         fmt.Sprintf("BTN-%d", t.TorrentID),
			InfoURL:      infoURL,
			DownloadURL:  downloadURL,
			Title:        cleanReleaseName(t.ReleaseName),
			Size:         t.Size,
			PublishDate:  time.Unix(t.Time, 0).UTC(),
			IndexerID:    indexerID,
			Indexer:      indexerName,
			TvdbID:       tvdbID,
			TvRageID:     tvrageID,
			ImdbID:       imdbID,
			IndexerFlags: indexerFlagsFor(t),
		},
		InfoHash: t.InfoHash,
		Seeders:  &seedersCopy,
		Peers:    &peersCopy,
	}
}

// indexerFlagsFor mirrors C# `BroadcastheNetParser.cs:109-130`. BTN
// is a freeleech tracker — every torrent gets the Freeleech flag.
// Origin maps to Internal / Scene; the Subtitles tag becomes a flag.
func indexerFlagsFor(t *btnTorrent) parser.IndexerFlags {
	flags := parser.IndexerFlagFreeleech
	switch strings.ToUpper(t.Origin) {
	case "INTERNAL":
		flags |= parser.IndexerFlagInternal
	case "SCENE":
		flags |= parser.IndexerFlagScene
	}
	for _, tag := range t.Tags {
		if tag == "Subtitles" {
			// IndexerFlagSubtitles isn't enumerated in the Go parser
			// today — match the C# bit value via raw OR. Documented
			// as a known divergence: future port slice that adds
			// the named constant should swap to it.
			flags |= 1 << 4 // C# IndexerFlags.Subtitles
		}
	}
	return flags
}

// cleanReleaseName mirrors C# `BroadcastheNetParser.cs:132-135` —
// strips backslash escapes.
func cleanReleaseName(name string) string {
	return strings.ReplaceAll(name, `\`, "")
}
