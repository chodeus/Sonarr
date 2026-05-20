package hdbits

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/parser"
)

// StatusCode mirrors C# `HDBitsApi.cs:117-129`. Non-zero status
// values indicate an error; the API's `message` field carries the
// human-readable detail.
type StatusCode int

// StatusCode values. Wire-stable; the API returns these as the
// `status` field of every response.
const (
	StatusSuccess                   StatusCode = 0
	StatusFailure                   StatusCode = 1
	StatusSSLRequired               StatusCode = 2
	StatusJSONMalformed             StatusCode = 3
	StatusAuthDataMissing           StatusCode = 4
	StatusAuthFailed                StatusCode = 5
	StatusMissingRequiredParameters StatusCode = 6
	StatusInvalidParameter          StatusCode = 7
	StatusImdbImportFail            StatusCode = 8
	StatusImdbTvNotAllowed          StatusCode = 9
)

// torrentQuery is the JSON request shape. Ports C# `TorrentQuery`
// at `HDBitsApi.cs:7-41`. omitempty on the pointer / optional
// fields produces the same on-wire shape as the C# nullable
// properties (absent when zero, present when set).
type torrentQuery struct {
	Username string `json:"username"`
	Passkey  string `json:"passkey"`

	Hash     string `json:"hash,omitempty"`
	Search   string `json:"search,omitempty"`
	Category []int  `json:"category,omitempty"`
	Codec    []int  `json:"codec,omitempty"`
	Medium   []int  `json:"medium,omitempty"`
	Origin   *int   `json:"origin,omitempty"`

	ImdbInfo *imdbInfo `json:"imdb,omitempty"`
	TvdbInfo *tvdbInfo `json:"tvdb,omitempty"`

	FileInTorrent string `json:"file_in_torrent,omitempty"`
	SnatchedOnly  *bool  `json:"snatched_only,omitempty"`

	Limit *int `json:"limit,omitempty"`
	Page  *int `json:"page,omitempty"`
}

// imdbInfo mirrors C# `HDBitsApi.cs:100-108`. All fields optional.
type imdbInfo struct {
	ID            *int     `json:"id,omitempty"`
	EnglishTitle  string   `json:"english_title,omitempty"`
	OriginalTitle string   `json:"original_title,omitempty"`
	Year          *int     `json:"year,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	Rating        *float64 `json:"rating,omitempty"`
}

// tvdbInfo mirrors C# `HDBitsApi.cs:110-115`. Used by the search-
// routing slice for the criteria-typed dispatch.
type tvdbInfo struct {
	ID      *int `json:"id,omitempty"`
	Season  *int `json:"season,omitempty"`
	Episode *int `json:"episode,omitempty"`
}

// hdbitsResponse mirrors C# `HDBitsApi.cs:43-49` — the wrapped
// `{status, message, data}` shape. data is parsed lazily as
// json.RawMessage so error responses don't fail the outer decode.
type hdbitsResponse struct {
	Status  StatusCode      `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// torrentQueryResponse mirrors C# `HDBitsApi.cs:51-98`.
type torrentQueryResponse struct {
	ID             string     `json:"id"`
	Hash           string     `json:"hash"`
	Leechers       int        `json:"leechers"`
	Seeders        int        `json:"seeders"`
	Name           string     `json:"name"`
	TimesCompleted uint32     `json:"times_completed"`
	Size           int64      `json:"size"`
	UtAdded        int64      `json:"utadded"`
	Added          hdbitsTime `json:"added"`
	Comments       uint32     `json:"comments"`
	NumFiles       uint32     `json:"numfiles"`
	FileName       string     `json:"filename"`
	FreeLeech      string     `json:"freeleech"`
	TypeCategory   int        `json:"type_category"`
	TypeCodec      int        `json:"type_codec"`
	TypeMedium     int        `json:"type_medium"`
	TypeOrigin     int        `json:"type_origin"`
	ImdbInfo       *imdbInfo  `json:"imdb,omitempty"`
	TvdbInfo       *tvdbInfo  `json:"tvdb,omitempty"`
}

// hdbitsTime accepts the HDBits TZ-less timestamp wire form
// (`2025-05-19 12:00:00`) as well as RFC3339. Same pattern as the
// J152 filelistTime — Go's default time.Time JSON parser is strict
// while JSON.NET (C# side) is permissive.
type hdbitsTime time.Time

// UnmarshalJSON tries the HDBits TZ-less + space-separated layout
// first, then standard RFC3339. Empty / null leaves the receiver
// at its zero value.
func (t *hdbitsTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05", // HDBits wire form
		"2006-01-02T15:04:05", // ISO without TZ
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if parsed, err := time.Parse(layout, s); err == nil {
			*t = hdbitsTime(parsed.UTC())
			return nil
		}
	}
	return fmt.Errorf("hdbits: unparseable added timestamp %q", s)
}

// parseResponse ports `HDBitsParser.cs:20-70`. Decodes the wrapped
// response, validates Status==Success, walks the `data` array and
// maps each row to a parser.TorrentInfo. Status / decode errors
// return errors so callers can surface them; the FetchRecent path
// otherwise returns the populated slice.
func parseResponse(body []byte, settings *Settings, indexerName string, indexerID int) ([]*parser.TorrentInfo, error) {
	var resp hdbitsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("hdbits: parse JSON: %w", err)
	}
	if resp.Status != StatusSuccess {
		return nil, fmt.Errorf("hdbits: API status %d: %s", resp.Status, resp.Message)
	}
	var rows []torrentQueryResponse
	if err := json.Unmarshal(resp.Data, &rows); err != nil {
		return nil, fmt.Errorf("hdbits: parse data array: %w", err)
	}
	out := make([]*parser.TorrentInfo, 0, len(rows))
	for _, r := range rows {
		seeders := r.Seeders
		peers := r.Leechers + r.Seeders
		tvdbID := 0
		if r.TvdbInfo != nil && r.TvdbInfo.ID != nil {
			tvdbID = *r.TvdbInfo.ID
		}
		ti := &parser.TorrentInfo{
			ReleaseInfo: parser.ReleaseInfo{
				Guid:         "HDBits-" + r.ID,
				Title:        r.Name,
				Size:         r.Size,
				DownloadURL:  buildDownloadURL(settings, r.ID),
				InfoURL:      buildInfoURL(settings, r.ID),
				PublishDate:  time.Time(r.Added),
				IndexerID:    indexerID,
				Indexer:      indexerName,
				TvdbID:       tvdbID,
				IndexerFlags: indexerFlagsFor(r),
			},
			InfoHash: r.Hash,
			Seeders:  &seeders,
			Peers:    &peers,
		}
		out = append(out, ti)
	}
	return out, nil
}

// indexerFlagsFor mirrors C# `HDBitsParser.cs:72-87`. FreeLeech is
// a string ("yes" / anything else); TypeOrigin == 1 → Internal.
func indexerFlagsFor(r torrentQueryResponse) parser.IndexerFlags {
	var flags parser.IndexerFlags
	if r.FreeLeech == "yes" {
		flags |= parser.IndexerFlagFreeleech
	}
	if r.TypeOrigin == 1 {
		flags |= parser.IndexerFlagInternal
	}
	return flags
}

// buildDownloadURL mirrors C# `HDBitsParser.cs:89-97`. Composes
// `<BaseUrl>/download.php?id=<id>&passkey=<ApiKey>`.
func buildDownloadURL(s *Settings, torrentID string) string {
	if s == nil {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(s.BaseUrl), "/")
	q := url.Values{}
	q.Set("id", torrentID)
	q.Set("passkey", strings.TrimSpace(s.ApiKey))
	return base + "/download.php?" + q.Encode()
}

// buildInfoURL mirrors C# `HDBitsParser.cs:99-105`.
func buildInfoURL(s *Settings, torrentID string) string {
	if s == nil {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(s.BaseUrl), "/")
	q := url.Values{}
	q.Set("id", torrentID)
	return base + "/details.php?" + q.Encode()
}
