package parser

import (
	"strings"
)

// ParseTitle ports `Parser.ParseTitle` at `Parser.cs:670-797`.
// Returns a *ParsedEpisodeInfo on the first rule that produces a
// non-nil result; returns nil otherwise.
//
// Pipeline (mirrors C# order):
//
//  1. ValidateBeforeParsing.
//  2. applySimplifyTitle — full SimplifyTitle cleanup pass
//     (ReversedTitleRegex, RemoveFileExtension, 【】→[],
//     PreSubstitutionRegex, SimpleTitleRegex, WebsitePrefix /
//     WebsitePostfix / CleanTorrentSuffix, CleanQualityBracketsRegex,
//     SixDigitAirDate).
//  3. Walk reportTitleRules; first non-nil result wins.
//
// The post-match enrichment stages from C# (Languages, Quality,
// ReleaseGroup, ReleaseHash, GetSubGroup, GetSeriesTitleInfo,
// FullSeason+Special toggle) are deferred to subsequent slices —
// ParseTitle currently sets only SeriesTitle / SeasonNumber /
// EpisodeNumbers / ReleaseTitle plus whatever the matched build
// closure populates.
//
// Returns nil when ValidateBeforeParsing rejects the input or no
// rule's regex matches.
func ParseTitle(title string) *ParsedEpisodeInfo {
	if !ValidateBeforeParsing(title) {
		return nil
	}

	title = strings.TrimSpace(title)
	simpleTitle := applySimplifyTitle(title)

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
		return info
	}
	return nil
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
