package parser

import (
	"regexp"
	"strconv"
)

// Ports `Parser.GetSeriesTitleInfo` at `Parser.cs:891-921`. Splits a
// parsed SeriesTitle into title / titleWithoutYear / year and (when
// the title contains paren / pipe / AKA components) populates AllTitles.

// yearInTitleRe ports `Parser.YearInTitleRegex` at
// `Parser.cs:533-534`. Anchored at start; lazy title group consumes
// up to the first separator+`<year>` token. The bracket characters
// `(`/`[`/`)`/`]` around the year are optional.
//
// RE2-clean.
var yearInTitleRe = regexp.MustCompile(`(?i)^(?P<title>.+?)[-_. ]+?[\(\[]?(?P<year>\d{4})[\]\)]?`)

// titleComponents{Paren,Pipe,AKA} port `Parser.TitleComponentsRegex`
// at `Parser.cs:536-537`. The C# original is one regex with three
// alternatives, each containing two `(?<title>...)` groups with the
// SAME name. RE2 rejects duplicate named groups, so the Go port
// splits into three separate regexes; each consumer captures the
// two title components into bare positional groups and the caller
// reads `m[1]` and `m[2]`.
//
// RE2-clean.
var (
	titleComponentsParenRe = regexp.MustCompile(`^(.+?) \((.+?)\)$`)
	titleComponentsPipeRe  = regexp.MustCompile(`^(.+?) \| (.+?)$`)
	titleComponentsAKARe   = regexp.MustCompile(`^(.+?) AKA (.+?)$`)
)

// GetSeriesTitleInfo mirrors `Parser.GetSeriesTitleInfo`. The C#
// version takes the `MatchCollection` so it can read multiple
// captures of the matched ReportTitleRegex's `<title>` group;
// however RE2's named groups can't repeat, so a Go ReportTitleRegex
// rule never produces multiple `<title>` captures. The Go port
// drops the matchCollection arg and falls through to the
// TitleComponents fallback, which is the path that actually fires
// for paren / pipe / AKA forms in real fixtures.
//
// Returns a non-nil *SeriesTitleInfo. Year is 0 (matches the C#
// `int` zero value) when the title has no year.
func GetSeriesTitleInfo(title string) *SeriesTitleInfo {
	info := &SeriesTitleInfo{Title: title}

	if m := yearInTitleRe.FindStringSubmatch(title); m != nil {
		info.TitleWithoutYear = m[yearInTitleRe.SubexpIndex("title")]
		// Year regex group is `\d{4}`; Atoi can't fail.
		yr, _ := strconv.Atoi(m[yearInTitleRe.SubexpIndex("year")])
		info.Year = yr
	} else {
		info.TitleWithoutYear = title
	}

	switch {
	case titleComponentsParenRe.MatchString(title):
		m := titleComponentsParenRe.FindStringSubmatch(title)
		info.AllTitles = []string{m[1], m[2]}
	case titleComponentsPipeRe.MatchString(title):
		m := titleComponentsPipeRe.FindStringSubmatch(title)
		info.AllTitles = []string{m[1], m[2]}
	case titleComponentsAKARe.MatchString(title):
		m := titleComponentsAKARe.FindStringSubmatch(title)
		info.AllTitles = []string{m[1], m[2]}
	}

	return info
}
