// Package plexrss ports the C# `NzbDrone.Core.ImportLists.Rss.Plex.PlexRssImport`
// concrete import-list provider — the Plex Watchlist RSS feed. Slice
// 4.CP (sub-arc C4b of queue [C4]).
//
// The PlexRss import list polls the user-configured RSS feed URL
// (https://rss.plex.tv/{token}) and converts each <item> with
// category="show" into an [importlists.ItemInfo]. Items with a guid
// of the form imdb://, tvdb://, or tmdb:// are recognised; items
// missing any of these three IDs are SKIPPED (matches C#
// `PlexRssImportParser.cs:55-60` which warns + returns null).
//
// Slice scope: Settings + Validate + Parser (XML) + Provider (Fetch
// + Test + RequestAction no-op) + tests.
package plexrss

import (
	"strings"

	"sonarr.tv/sonarr/internal/importlists"
)

// Implementation is the value stored in the ImportLists.Implementation
// column for rows wired to this provider. Mirrors C# `PlexRssImport.cs:10`.
const Implementation = "PlexRssImport"

// ConfigContract is the value stored in the ImportLists.ConfigContract
// column. Mirrors C# `PlexRssImportSettings.cs:15`.
const ConfigContract = "PlexRssImportSettings"

// Settings ports `PlexRssImportSettings` at
// `PlexRssImportSettings.cs:15-26`.
type Settings struct {
	// URL is the Plex Watchlist RSS feed URL — REQUIRED. Mirrors C#
	// `:20`.
	URL string `json:"url"`
}

// DefaultSettings returns the zero-valued shape.
func DefaultSettings() Settings { return Settings{} }

// Validate runs the C# `PlexRssImportSettingsValidator` at `:7-13`.
// Single rule: URL non-empty.
func (s *Settings) Validate() []importlists.TestFailure {
	if strings.TrimSpace(s.URL) == "" {
		return []importlists.TestFailure{{
			PropertyName: "Url",
			ErrorMessage: "'Url' must not be empty.",
		}}
	}
	return nil
}
