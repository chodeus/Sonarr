package series

// MatchType mirrors the C# `SeriesMatchType` enum at
// `src/NzbDrone.Core/Parser/Model/FindSeriesResult.cs:18-23`. Tags
// HOW a release was matched to a known Series.
//
// Lives in `internal/series` rather than `internal/parser` to avoid a
// parser→series import cycle (series already imports parser for
// title-normalisation helpers). The C# tree colocates the enum with
// FindSeriesResult under Parser/Model, but the Go-port package
// boundary makes series the cleaner home — the enum logically tags
// a Series, not a parsed release.
//
// Persisted: flows through the v3 history payload + RemoteEpisode
// JSON, so order is wire-stable.
type MatchType int

// MatchType enumeration. Pinned against C# `:20-23`.
const (
	// MatchTypeUnknown is the zero value — no match attempted, or the
	// match path didn't tag the result.
	MatchTypeUnknown MatchType = 0
	// MatchTypeTitle — matched the parsed series-title string against
	// the persisted Series.Title (or its CleanTitle).
	MatchTypeTitle MatchType = 1
	// MatchTypeAlias — matched against the SceneMappings alias list.
	// The parsed title doesn't match canonical title but is a known
	// scene-release alias.
	MatchTypeAlias MatchType = 2
	// MatchTypeID — matched by an explicit ID token in the release
	// (tvdbId / tvRageId / imdbId).
	MatchTypeID MatchType = 3
)

// FindResult mirrors the C# `FindSeriesResult` at
// `src/NzbDrone.Core/Parser/Model/FindSeriesResult.cs:5-16`. Returned
// by future ParsingService.FindSeries port; carries both the matched
// series and the match-type tag the decision pipeline stamps onto the
// resulting Subject (RemoteEpisode equivalent).
//
// Series is a pointer so a "no match" caller path can return
// `&FindResult{Series: nil}` without splitting into a discriminated
// union.
type FindResult struct {
	Series    *Series
	MatchType MatchType
}

// NewFindResult mirrors the C# constructor at `:9-13`.
func NewFindResult(s *Series, matchType MatchType) *FindResult {
	return &FindResult{Series: s, MatchType: matchType}
}
