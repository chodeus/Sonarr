package filelist

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/parser"
)

// torrent is the API response shape. Ports `FileListTorrent.cs:6-25`.
// Tags match the C# JsonProperty attributes where they diverge from
// PascalCase defaults.
type torrent struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Size           int64        `json:"size"`
	Leechers       int          `json:"leechers"`
	Seeders        int          `json:"seeders"`
	TimesCompleted uint32       `json:"times_completed"`
	Comments       uint32       `json:"comments"`
	Files          uint32       `json:"files"`
	ImdbID         string       `json:"imdb"`
	Internal       bool         `json:"internal"`
	FreeLeech      bool         `json:"freeleech"`
	UploadDate     filelistTime `json:"upload_date"`
}

// filelistTime accepts the FileList API's TZ-less timestamp form
// (`2025-05-19T12:00:00`) as well as standard RFC3339. The C# tree
// uses JSON.NET's default DateTime parser which is permissive about
// the TZ; the Go port matches by trying both layouts.
//
// The C# `FileListParser.cs:50` calls `.ToUniversalTime()` — Go's
// time.Parse returns UTC unless the layout has a TZ offset, so the
// TZ-less branch already produces a UTC time. The RFC3339 branch
// passes through to .UTC() to match.
type filelistTime time.Time

// UnmarshalJSON tries the FileList TZ-less layout first, then RFC3339
// variants. Empty / null leaves the receiver at its zero value.
func (t *filelistTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05", // FileList's TZ-less wire form
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if parsed, err := time.Parse(layout, s); err == nil {
			*t = filelistTime(parsed.UTC())
			return nil
		}
	}
	return fmt.Errorf("filelist: unparseable upload_date %q", s)
}

// parseResponse ports `FileListParser.cs:19-63` — turns the JSON
// response body into a slice of TorrentInfo. Each row gets its
// Guid + Title + Size + DownloadURL + InfoURL + Seeders + Peers +
// PublishDate + IndexerFlags + (optional) ImdbID computed.
//
// indexerName / indexerID are stamped onto every TorrentInfo so
// downstream specs can identify provenance (matches the
// torrentrss / newznab parser surfaces). settings is used to build
// the download / info URLs from the torrent ID.
func parseResponse(body []byte, settings *Settings, indexerName string, indexerID int) ([]*parser.TorrentInfo, error) {
	var rows []torrent
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("filelist: parse JSON: %w", err)
	}
	out := make([]*parser.TorrentInfo, 0, len(rows))
	for _, r := range rows {
		seeders := r.Seeders
		peers := r.Leechers + r.Seeders
		ti := &parser.TorrentInfo{
			ReleaseInfo: parser.ReleaseInfo{
				Guid:         "FileList-" + r.ID,
				Title:        r.Name,
				Size:         r.Size,
				DownloadURL:  buildDownloadURL(settings, r.ID),
				InfoURL:      buildInfoURL(settings, r.ID),
				PublishDate:  time.Time(r.UploadDate),
				IndexerID:    indexerID,
				Indexer:      indexerName,
				ImdbID:       normaliseImdbID(r.ImdbID),
				IndexerFlags: indexerFlagsFor(r),
			},
			Seeders: &seeders,
			Peers:   &peers,
		}
		out = append(out, ti)
	}
	return out, nil
}

// indexerFlagsFor mirrors C# `FileListParser.cs:65-80`. The two flag
// bits FileList carries — FreeLeech and Internal — fold into the
// shared parser.IndexerFlags bitfield.
func indexerFlagsFor(t torrent) parser.IndexerFlags {
	var flags parser.IndexerFlags
	if t.FreeLeech {
		flags |= parser.IndexerFlagFreeleech
	}
	if t.Internal {
		flags |= parser.IndexerFlagInternal
	}
	return flags
}

// normaliseImdbID mirrors `FileListParser.cs:54-57`. FileList stores
// the IMDB id with or without the `tt` prefix; we strip + re-format
// as `tt<7-digit>` when the numeric part parses. Returns empty
// string for unparseable / too-short ids.
func normaliseImdbID(raw string) string {
	if len(raw) <= 2 {
		return ""
	}
	stripped := strings.TrimPrefix(raw, "tt")
	n, err := strconv.Atoi(stripped)
	if err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// buildDownloadURL mirrors `FileListParser.cs:82-90`. Composes
// `<BaseUrl>/download.php?id=<id>&passkey=<...>`.
func buildDownloadURL(s *Settings, torrentID string) string {
	if s == nil {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(s.BaseUrl), "/")
	q := url.Values{}
	q.Set("id", torrentID)
	q.Set("passkey", strings.TrimSpace(s.Passkey))
	return base + "/download.php?" + q.Encode()
}

// buildInfoURL mirrors `FileListParser.cs:92-99`. Composes
// `<BaseUrl>/details.php?id=<id>`.
func buildInfoURL(s *Settings, torrentID string) string {
	if s == nil {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(s.BaseUrl), "/")
	q := url.Values{}
	q.Set("id", torrentID)
	return base + "/details.php?" + q.Encode()
}
