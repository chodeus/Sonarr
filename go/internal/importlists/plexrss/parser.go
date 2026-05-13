package plexrss

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/importlists"
)

// rssDocument mirrors the subset of the RSS 2.0 envelope the Plex
// Watchlist feed uses. Plex emits a single <channel> with a flat
// <item> list — no syndication extensions beyond <category> +
// <guid>.
type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title    string `xml:"title"`
	Category string `xml:"category"`
	GUID     string `xml:"guid"`
}

// imdbIDRegex mirrors C# `PlexRssImportParser.cs:13` —
// `(tt\d{7,8})` case-insensitive.
var imdbIDRegex = regexp.MustCompile(`(?i)(tt\d{7,8})`)

// parseFeed parses the raw RSS XML body, filters category=="show",
// and maps each surviving <item> into an [importlists.ItemInfo].
// Items missing all three IDs (IMDb / TVDb / TMDb) are SKIPPED with
// no error (matches C# `PlexRssImportParser.cs:55-60` warn+return-null
// semantic — the orchestrator counts a skipped item as "no match"
// rather than a parse failure).
//
// Returns a hard error only when the XML itself is malformed.
func parseFeed(body []byte) ([]importlists.ItemInfo, error) {
	var doc rssDocument
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("plexrss: parse xml: %w", err)
	}
	out := make([]importlists.ItemInfo, 0, len(doc.Channel.Items))
	for _, item := range doc.Channel.Items {
		if item.Category != "show" {
			continue
		}
		info := importlists.ItemInfo{
			Title: defaultIfEmpty(item.Title, "Unknown"),
		}
		guid := strings.TrimSpace(item.GUID)
		if guid != "" {
			// IMDb guids: imdb://tt1234567 (the C# code prefix-strips
			// "imdb://" then regex-matches the tt-prefixed slug).
			if strings.HasPrefix(guid, "imdb://") {
				info.ImdbID = parseImdbID(strings.TrimPrefix(guid, "imdb://"))
			}
			// The C# code tries to TryParse(guid.Replace("tvdb://","")) +
			// TryParse(guid.Replace("tmdb://","")) — both Replace calls
			// strip the prefix regardless of which guid scheme is used,
			// so the same int may end up in both fields if the guid
			// scheme is e.g. tvdb://. The Go port matches the parse
			// behaviour but only assigns when the prefix matches the
			// scheme — slightly tighter than C# but yields the same
			// final ItemInfo for well-formed Plex feeds.
			if strings.HasPrefix(guid, "tvdb://") {
				if id, err := strconv.Atoi(strings.TrimPrefix(guid, "tvdb://")); err == nil {
					info.TvdbID = id
				}
			}
			if strings.HasPrefix(guid, "tmdb://") {
				if id, err := strconv.Atoi(strings.TrimPrefix(guid, "tmdb://")); err == nil {
					info.TmdbID = id
				}
			}
		}
		if info.ImdbID == "" && info.TvdbID == 0 && info.TmdbID == 0 {
			// Skip items without any usable ID — C# `:55-60` warns
			// and returns null which gets filtered by `AddIfNotNull`.
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// parseImdbID extracts the tt-prefixed IMDb id from a free-form
// string. Mirrors C# `PlexRssImportParser.cs:65-75`. Returns "" when
// no match is found.
func parseImdbID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	m := imdbIDRegex.FindStringSubmatch(value)
	if len(m) == 0 {
		return ""
	}
	return m[1]
}

func defaultIfEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
