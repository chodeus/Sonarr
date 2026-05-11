package plex

import (
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/importlists"
)

// parseWatchlistItems ports `PlexParser.ParseResponse` at
// `src/NzbDrone.Core/ImportLists/Plex/PlexParser.cs:16-53`.
//
// Walks each [WatchlistItem], extracts the tvdb / tmdb / imdb GUID
// values (each carries a scheme prefix like "tvdb://12345"), and
// returns the matching [importlists.ItemInfo] list.
//
// GUID parsing semantics (C# `:35-37` + `:71-75`):
//
//   - findGUID looks for the first guid whose Id starts with
//     "{prefix}://" and returns the suffix.
//   - tvdb and tmdb are parsed via int.TryParse — bad / missing
//     values yield 0.
//   - imdb is kept as a string (typically "tt"-prefixed).
//
// Note: the function intentionally does NOT call importlists.Dedup
// — the caller's Fetch path does that across all pages combined.
func parseWatchlistItems(items []WatchlistItem) []importlists.ItemInfo {
	out := make([]importlists.ItemInfo, 0, len(items))
	for _, item := range items {
		tvdbID, _ := strconv.Atoi(findGUID(item.Guids, "tvdb"))
		tmdbID, _ := strconv.Atoi(findGUID(item.Guids, "tmdb"))
		imdbID := findGUID(item.Guids, "imdb")
		out = append(out, importlists.ItemInfo{
			TvdbID: tvdbID,
			TmdbID: tmdbID,
			ImdbID: imdbID,
			Title:  item.Title,
			Year:   item.Year,
		})
	}
	return out
}

// findGUID ports `PlexParser.FindGuid` at
// `PlexParser.cs:71-76`. Returns the substring after the
// "{prefix}://" scheme or "" when no matching guid is present.
func findGUID(guids []ItemGuid, prefix string) string {
	scheme := prefix + "://"
	for _, g := range guids {
		if strings.HasPrefix(g.ID, scheme) {
			return strings.TrimPrefix(g.ID, scheme)
		}
	}
	return ""
}
