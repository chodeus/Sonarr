package autotagging

import "encoding/json"

// parseGenres decodes the raw JSON-array Genres field on
// series.Series into a []string. Invalid JSON or empty input
// returns nil, which yields a no-match in GenreSpec. Mirrors the
// C# behaviour where `series.Genres` is a `List<string>` already
// hydrated by the persistence layer.
//
// When the typed Genre wrapper ships in a future series-services
// slice, GenreSpec can drop this helper and read a parsed slice
// directly. Until then, parsing JSON each call is acceptable
// because AutoTag matching runs in series-CRUD-event flow rather
// than in any hot path.
func parseGenres(raw string) []string {
	if raw == "" {
		return nil
	}
	var genres []string
	if err := json.Unmarshal([]byte(raw), &genres); err != nil {
		return nil
	}
	return genres
}
