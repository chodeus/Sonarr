package parser

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/quality"
)

// ParseTitle ports `Parser.ParseTitle` at `Parser.cs:670-797`.
// Returns a *ParsedEpisodeInfo on the first rule that produces a
// non-nil result; returns nil otherwise.
//
// Pipeline (mirrors C# order):
//
//  1. ValidateBeforeParsing.
//  2. applySimplifyTitle — full SimplifyTitle cleanup pass.
//     Returns (releaseTitle, simpleTitle): releaseTitle is post-
//     PreSubstitution but pre-SimpleTitle/Website/etc., used as the
//     ParseReleaseGroup input; simpleTitle has the full strip
//     applied and is what ReportTitleRegex matches against.
//  3. Walk reportTitleRules; first non-nil result wins.
//  4. Post-match enrichment on the surviving info:
//     - Quality   = QualityParser.ParseQuality(originalTitle)
//     - ReleaseGroup = ReleaseGroupParser.ParseReleaseGroup(releaseTitle)
//     overridden by the matched rule's `<subgroup>` capture if non-empty
//     - ReleaseHash = matched rule's `<hash>` capture, with surrounding
//     `[]()` trimmed and the literal `1280x720` rejected as a false
//     positive (mirrors `Parser.cs:1230-1232`).
//
// Still deferred to subsequent slices: Languages / ReleaseTokens
// (needs `lastSeasonEpisodeStringIndex` from build closures);
// `FullSeason && "Special"` toggle (needs ReleaseTokens);
// `GetSeriesTitleInfo` (year-in-title splitter); `ParsePath`.
//
// Returns nil when ValidateBeforeParsing rejects the input or no
// rule's regex matches.
func ParseTitle(title string) *ParsedEpisodeInfo {
	if !ValidateBeforeParsing(title) {
		return nil
	}

	title = strings.TrimSpace(title)
	releaseTitle, simpleTitle := applySimplifyTitle(title)

	for _, rule := range reportTitleRules {
		m := rule.Re.FindStringSubmatchIndex(simpleTitle)
		if m == nil {
			continue
		}
		info := rule.Build(rule.Re, m, simpleTitle)
		if info == nil {
			continue
		}
		info.ReleaseTitle = title
		enrichFromMatch(info, rule.Re, m, title, releaseTitle, simpleTitle)
		return info
	}
	return nil
}

// enrichFromMatch applies the C# post-match enrichment stages from
// `Parser.ParseTitle:749-775`: ReleaseTokens slice, FullSeason+
// "Special" toggle, Languages, Quality, ReleaseGroup with optional
// subgroup override, ReleaseHash with bracket-trim and 1280x720
// reject. GetSeriesTitleInfo (the year-in-title splitter) ships in
// a later slice.
func enrichFromMatch(info *ParsedEpisodeInfo, re *regexp.Regexp, m []int, originalTitle, releaseTitle, simpleTitle string) {
	info.ReleaseTokens = computeReleaseTokens(re, m, releaseTitle)

	// FullSeason → Special toggle: if a season-pack rule fired and
	// the trailing tokens contain "Special", reinterpret as a
	// special. Mirrors `Parser.cs:749-753`.
	if info.FullSeason && containsCI(info.ReleaseTokens, "Special") {
		info.FullSeason = false
		info.Special = true
	}

	info.Languages = ParseLanguages(info.ReleaseTokens)

	info.Quality = quality.ParseQuality(originalTitle)

	info.ReleaseGroup = ParseReleaseGroup(releaseTitle)
	if subgroup, _ := extractGroupSpan(re, m, "subgroup", simpleTitle); strings.TrimSpace(subgroup) != "" {
		info.ReleaseGroup = strings.TrimSpace(subgroup)
	}

	if hashText, _ := extractGroupSpan(re, m, "hash", simpleTitle); hashText != "" {
		h := strings.Trim(hashText, "[]()")
		if h != "1280x720" {
			info.ReleaseHash = h
		}
	}
}

// computeReleaseTokens mirrors the C# computation of
// `lastSeasonEpisodeStringIndex` at `Parser.cs:929 + 961 + 994 +
// 1006 + 1145-1147` plus the `releaseTitle.Substring(idx)` slice at
// `:1163-1170`.
//
// The C# code starts from the title group's EndIndex and bumps it
// up by Math.Max against the END index of the last episode /
// absoluteEpisode / airyear / airmonth / airday capture. RE2 retains
// only the last capture for a repeated named group, so the Go port
// uses the regex's named groups directly — for repeating groups the
// retained capture IS the last, which is the value the C# pipeline
// also picks.
//
// Important caveat preserved verbatim from C#: the index is
// extracted from a simpleTitle match but applied to releaseTitle.
// When simpleTitle differs in length from releaseTitle (because
// SimpleTitleRegex stripped tokens, or SixDigitAirDate added chars),
// the index is approximate. C# accepts this approximation; the Go
// port matches the behaviour byte-for-byte.
//
// Returns the full releaseTitle when the computed index is past
// the releaseTitle's end (mirrors the `else { ReleaseTokens =
// releaseTitle }` branch at `:1167-1170`).
func computeReleaseTokens(re *regexp.Regexp, m []int, releaseTitle string) string {
	idx := -1
	for _, name := range []string{"title", "episode", "absoluteepisode", "airyear", "airmonth", "airday"} {
		gIdx := re.SubexpIndex(name)
		if gIdx <= 0 || 2*gIdx+1 >= len(m) {
			continue
		}
		end := m[2*gIdx+1]
		if end > idx {
			idx = end
		}
	}
	if idx >= 0 && idx < len(releaseTitle) {
		return releaseTitle[idx:]
	}
	return releaseTitle
}

// containsCI reports whether substr appears in s ignoring case.
// Mirrors C# `string.ContainsIgnoreCase`.
func containsCI(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// ParseSeriesName mirrors `Parser.ParseSeriesName` at `Parser.cs:800-812`.
// Delegates to ParseTitle and returns the parsed SeriesTitle, falling
// back to CleanSeriesTitle when ParseTitle returns nil.
func ParseSeriesName(title string) string {
	if info := ParseTitle(title); info != nil {
		return info.SeriesTitle
	}
	return CleanSeriesTitle(title)
}
