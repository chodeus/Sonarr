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
// `Parser.ParseTitle:758-775`: Quality, ReleaseGroup with optional
// subgroup override, ReleaseHash with bracket-trim and 1280x720
// reject. Languages / ReleaseTokens / FullSeason+Special toggle ship
// in a later slice — they need the `lastSeasonEpisodeStringIndex`
// machinery the build closures don't yet expose.
func enrichFromMatch(info *ParsedEpisodeInfo, re *regexp.Regexp, m []int, originalTitle, releaseTitle, simpleTitle string) {
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

// ParseSeriesName mirrors `Parser.ParseSeriesName` at `Parser.cs:800-812`.
// Delegates to ParseTitle and returns the parsed SeriesTitle, falling
// back to CleanSeriesTitle when ParseTitle returns nil.
func ParseSeriesName(title string) string {
	if info := ParseTitle(title); info != nil {
		return info.SeriesTitle
	}
	return CleanSeriesTitle(title)
}
