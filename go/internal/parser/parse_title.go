package parser

import (
	"strings"
)

// ParseTitle is the first slice of the C# `Parser.ParseTitle` pipeline
// at `Parser.cs:670-797`. Returns a *ParsedEpisodeInfo on the first
// rule that produces a non-nil result; returns nil otherwise.
//
// What's shipped in this slice: the rule-walking shell + the four
// reportTitleRules ported in 2.12.1. The C# pipeline's downstream
// stages (SimplifyTitle, language detection, quality detection,
// release-group detection, year-in-title parsing, daily/anime
// branches) are deferred — ParseTitle currently sets only
// SeriesTitle / SeasonNumber / EpisodeNumbers / ReleaseTitle.
//
// Returns nil when ValidateBeforeParsing rejects the input or no
// rule's regex matches.
func ParseTitle(title string) *ParsedEpisodeInfo {
	if !ValidateBeforeParsing(title) {
		return nil
	}

	title = strings.TrimSpace(title)

	// Strip the file extension before regex matching — mirrors
	// `Parser.cs:1193` `var titleWithoutExtension =
	// FileExtensions.RemoveFileExtension(title)`. (The C# code does
	// the strip inside ValidateBeforeParsing for the hash-rejection
	// pass and again as a side-effect of the SimplifyTitle path; the
	// Go port pre-strips here so all rule regexes see consistent input.)
	titleNoExt := RemoveFileExtension(title)

	for _, rule := range reportTitleRules {
		m := rule.Re.FindStringSubmatchIndex(titleNoExt)
		if m == nil {
			continue
		}
		info := rule.Build(rule.Re, m, titleNoExt)
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
