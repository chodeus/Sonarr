package series

import (
	"strings"

	"sonarr.tv/sonarr/internal/parser"
)

// preComputedNormalizedTitles mirrors the C#
// `SeriesTitleNormalizer.PreComputedTitles` table at
// `SeriesTitleNormalizer.cs:7-10`. Each TVDB id maps to the canonical
// normalized form, used to override `parser.NormalizeTitle` for series
// whose normal form would be empty or otherwise ambiguous.
//
// Currently a single entry: TVDB 281588 ("A to Z") would normalize to
// "" under the standard rules (single-letter words "a" / "z" are
// stripped along with the connecting "to"). Hardcoding "a to z"
// preserves a non-empty match key.
//
// Don't add entries lightly — every entry is a workaround for the
// normalizer producing a degenerate output for that specific series.
var preComputedNormalizedTitles = map[int]string{
	281588: "a to z",
}

// NormalizeTitle ports `Tv/SeriesTitleNormalizer.Normalize(title, tvdbId)`
// at `:12-20`. Returns the canonical lowercased normalized form Sonarr
// uses as a series-title match key. Looks up `tvdbID` in the
// pre-computed override table first; otherwise calls
// `parser.NormalizeTitle` and lowercases.
//
// Used as the join key between parser output and the series table for
// title-based series matching (RSS feeds, scene mappings, manual
// imports). The normalized form must be stable across Sonarr versions
// because it's compared against persisted CleanTitle values; any
// change to the normalization algorithm would invalidate caches and
// require a backfill migration.
func NormalizeTitle(title string, tvdbID int) string {
	if v, ok := preComputedNormalizedTitles[tvdbID]; ok {
		return v
	}
	return strings.ToLower(parser.NormalizeTitle(title))
}
