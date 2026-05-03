package parser

import (
	"regexp"
	"strings"
)

// First slice of the C# `ReportTitleRegex[]` array port at
// `Parser.cs:21-452`. The full array is 122 patterns categorised by
// the C# inline comments — this slice ships the most common shapes
// (title-bearing single + multi episode, no-title single + multi
// episode) so the broader ParseTitle pipeline has working coverage of
// scene-standard release names. Subsequent slices add anime, daily,
// special, and miniseries patterns.
//
// Each rule pairs a compiled regex with a `Build` function that
// extracts capture groups into a *ParsedEpisodeInfo. Rules are tried
// in declaration order; first match wins (mirrors the C# "first
// regex with a match" foreach in `Parser.ParseTitle`).

// rgRule is one entry in the ReportTitleRegex table. The C# array is
// homogeneous (all entries are `Regex`), but the Go port pairs each
// regex with its capture-extraction logic so the table stays
// declarative and self-contained. Mirrors the C# pattern of "one
// regex per parse-title shape, dispatched by leftmost match".
type rgRule struct {
	Re    *regexp.Regexp
	Build func(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo
}

// reportTitleRules is the Go-side equivalent of
// `Parser.ReportTitleRegex[]`. Order matches the C# source.
//
// What's shipped in this slice (4 of 122 patterns):
//
//   - Title + season/episode (canonical scene release).
//   - Title + season/multi-episode.
//   - No-title single episode (raw `S01E05` filename).
//   - No-title multi-episode (raw `S01E05E06` filename).
//
// What's deferred to subsequent slices: anime patterns (~25), daily
// patterns (~5), special patterns (~5), miniseries patterns (~3),
// hashed/foreign-format/edge-case patterns (~80).
//
// The C# inline `(?<!\d+)` / `(?!\d+)` digit-boundary lookarounds
// are dropped here — the surrounding regex shape (literal `S` /
// `E` / `x` separators around the digit runs) already enforces the
// "exactly N digits at this position" constraint.
var reportTitleRules = []rgRule{
	// `Parser.cs:45` — Episodes without a title, Multi (`S01E04E05`,
	// `1x04x05`). Listed BEFORE the no-title single rule so the
	// multi-episode form wins over the single-episode form's prefix.
	// Mirrors the C# array ordering at `Parser.cs:44-46` (multi at
	// index 5) preceding the single at index 7.
	//
	// `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_]|[ex]){1,2}(?<episode>\d{2,3}(?!\d+))){2,})`
	{
		Re: regexp.MustCompile(
			`(?i)^(?:S?(?P<season>\d{4}|\d{1,2})(?:(?:[-_]|[ex]){1,2}\d{2,3}){2,})`,
		),
		Build: buildNoTitleMultiEpisode,
	},

	// `Parser.cs:53` — Episodes without a title, Single (`S01E05`, `1x05`).
	// `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_ ]?[ex])(?<episode>\d{2,3}(?!\d+))))`
	{
		Re: regexp.MustCompile(
			`(?i)^(?:S?(?P<season>\d{4}|\d{1,2})(?:[-_ ]?[ex]\d{2,3}))`,
		),
		Build: buildNoTitleEpisode,
	},

	// `Parser.cs:188` — Episodes with a title, single + multi.
	// `^(?<title>.+?)(?:[-_. ]+S(?<season>(?<!\d+)\d{1,2}(?!\d+))
	//   (?:(?:(?:[-_. ]+E(?<episode>\d{2,3}(?!\d+)))+)
	//   |(?:E(?<episode>\d{2,3}(?!\d+))(?:[-_. ]?(?:E?(?<episode>\d{2,3}(?!\d+))))*)))`
	//
	// Go RE2 has no lookbehind; the `\d{1,2}` digit-count constraint is
	// preserved by the surrounding `S<digits>E<digits>` shape (both
	// flanking literals reject extra digits). The C# regex uses the
	// same `<episode>` group name for two duplicate alternation arms;
	// RE2 rejects duplicates so the duplicate-name aliases are dropped
	// and episode tokens are merged via a separate `findAllEpisodes`
	// scan over the matched span.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)(?:[-_. ]+S(?P<season>\d{1,2}))(?:(?:[-_. ]+E\d{2,3})+|(?:E\d{2,3}(?:[-_. ]?(?:E?\d{2,3}))*))`,
		),
		Build: buildTitleSeasonEpisode,
	},
}

// episodeNumberRe is the post-match scanner for `E\d{2,3}` /
// `\d{2,3}` runs inside an already-anchored season+episode region.
// Used by Build functions to recover ALL episode numbers (Go RE2
// `(...)*` retains only the LAST capture per named group, so the C#
// `Captures` collection's per-iteration values don't transfer
// directly). See QualityParser slice 2.10c for the equivalent
// per-pattern fixup.
var episodeNumberRe = regexp.MustCompile(`(?i)[ex](\d{2,3})|(?:[-_]\s?)(\d{2,3})`)

// findAllEpisodes scans a season+episode region for every episode
// digit run, returning the integer values in match order. Mirrors the
// C# `match.Groups["episode"].Captures` iteration in
// `Parser.ParseMatchCollection` at `:944`.
func findAllEpisodes(region string) []int {
	matches := episodeNumberRe.FindAllStringSubmatch(region, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		var raw string
		switch {
		case len(m) >= 2 && m[1] != "":
			raw = m[1]
		case len(m) >= 3 && m[2] != "":
			raw = m[2]
		default:
			continue
		}
		if n, ok := ParseNumber(raw); ok {
			out = append(out, n)
		}
	}
	return out
}

// extractGroupSpan returns (text, end) of the named group, or
// ("", -1) if absent. Used by Build functions to pull out captured
// substrings + their end position for downstream "scan from here"
// passes.
func extractGroupSpan(re *regexp.Regexp, m []int, name, title string) (string, int) {
	idx := re.SubexpIndex(name)
	if idx <= 0 || 2*idx+1 >= len(m) {
		return "", -1
	}
	start, end := m[2*idx], m[2*idx+1]
	if start == -1 {
		return "", -1
	}
	return title[start:end], end
}

func buildTitleSeasonEpisode(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	seasonText, seasonEnd := extractGroupSpan(re, m, "season", title)
	season, ok := ParseNumber(seasonText)
	if !ok {
		return nil
	}
	if seasonEnd < 0 {
		return nil
	}

	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	// The full match span is [m[0], m[1]); episodes live AFTER the
	// season group's end. Re-scan the remainder of the match for
	// every `E<digits>` / `<sep><digits>` token.
	episodeRegion := title[seasonEnd:m[1]]
	episodes := findAllEpisodes(episodeRegion)

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = season
	info.EpisodeNumbers = episodes
	return info
}

func buildNoTitleEpisode(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	seasonText, seasonEnd := extractGroupSpan(re, m, "season", title)
	season, ok := ParseNumber(seasonText)
	if !ok {
		return nil
	}
	episodes := findAllEpisodes(title[seasonEnd:m[1]])

	info := NewParsedEpisodeInfo()
	info.SeasonNumber = season
	info.EpisodeNumbers = episodes
	return info
}

func buildNoTitleMultiEpisode(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	return buildNoTitleEpisode(re, m, title)
}
