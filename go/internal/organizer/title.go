// Package organizer is the Go port of NzbDrone.Core.Organizer — the
// token-replacement engine that builds episode/series file and folder
// names from user-configured format strings.
//
// This first slice (Phase 2.55) ports the pure title-cleaning helpers
// the rest of the engine builds on. Each helper is a free function
// — no service deps, deterministic output. The integration with
// `BuildFileName` orchestration lands in later slices (2.56-2.60).
package organizer

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"sonarr.tv/sonarr/internal/parser"
)

// titlePrefixRegex matches a leading "The/An/A " title prefix, capturing
// the prefix word (group 1), the rest of the title (group 2), and any
// trailing parenthesised year/qualifier (group 3). Mirrors the C#
// `TitlePrefixRegex` at `FileNameBuilder.cs:82`.
//
// Matches `The Foo (2019)` as `["The Foo (2019)", "The", "Foo", " (2019)"]`.
var titlePrefixRegex = regexp.MustCompile(`(?i)^(The|An|A) (.*?)((?: *\([^)]+\))*)$`)

// yearRegex matches a trailing ` (####)` 4-digit year. Mirrors the C#
// `YearRegex` at `:84`.
var yearRegex = regexp.MustCompile(`\(\d{4}\)$`)

// scenifyReplaceChars matches `/` characters — they get replaced with a
// space rather than removed, matching the C#
// `ScenifyReplaceChars = new Regex(@"[\/]", ...)` at `:75`.
var scenifyReplaceChars = regexp.MustCompile(`[/]`)

// scenifyBracketChars matches the C# regex's third alternative —
// `(\(|\)|\[|\]|\{|\})` at `:74`. Removed without preserving spacing.
var scenifyBracketChars = regexp.MustCompile(`[(){}\[\]]`)

// scenifyMidWordSpaceChars matches the C# regex's first alternative —
// a single special character flanked by whitespace on both sides. The
// C# uses lookbehind/lookahead to avoid consuming the surrounding
// whitespace; the Go port captures and re-emits the spaces (RE2
// doesn't support lookarounds).
//
// Character class lifted from the C# regex: `,<>/\\;:'"|` + backtick +
// `’~!?@$%^*-_=`. (`/` is also matched here in C# but the Go port
// runs scenifyReplaceChars first which converts `/` to space, so by
// the time this regex runs `/` is gone.)
var scenifyMidWordSpaceChars = regexp.MustCompile("(\\s)[,<>;:'\"|`’~!?@$%^*\\-_=\\\\](\\s)")

// scenifyApostropheChars matches the C# regex's second alternative —
// an apostrophe-like character followed by a contraction suffix
// (`'s/'m/'t/'ve/'ll/'d/'re ` then space) OR a space OR end of string.
//
// Example: `Karma's a B*tch!` → first match is `'s ` → emit `s ` (drop
// the apostrophe). Second alternative captures the suffix so we keep
// it in the output.
var scenifyApostropheChars = regexp.MustCompile("['`’:?,]((?:s|m|t|ve|ll|d|re)\\s|\\s|$)")

// CleanTitle strips path-unsafe characters from `title` for use in
// scene-style filenames. Mirrors `FileNameBuilder.CleanTitle(string)` at
// `FileNameBuilder.cs:302-309`. Pure function; no I/O.
//
// Steps (verbatim from C#):
//
//  1. Replace `&` with ` and `.
//  2. Replace `/` with a space (`ScenifyReplaceChars`).
//  3. Apply `ScenifyRemoveChars`:
//     a. Bracket removal (no whitespace preserved).
//     b. Apostrophe + contraction suffix: drop the apostrophe, keep
//     the suffix.
//     c. Space-flanked-special-char: keep both spaces, drop the char.
//  4. Strip diacritics via `parser.RemoveDiacritics`.
//
// The C# regex bundles all three sub-rules into a single alternation
// using lookarounds; RE2 lacks lookbehind/lookahead so the Go port
// applies them sequentially, with the bracket pass first (no
// adjacency concerns), then apostrophes, then space-flanked.
func CleanTitle(title string) string {
	title = strings.ReplaceAll(title, "&", "and")
	title = scenifyReplaceChars.ReplaceAllString(title, " ")
	title = scenifyBracketChars.ReplaceAllString(title, "")
	title = scenifyApostropheChars.ReplaceAllString(title, "$1")
	// Iterate the space-flanked pass to handle adjacency: a special
	// char between two spaces, removed, leaves both spaces in place;
	// a second special char that became space-flanked after the first
	// removal is still matched on the next iteration.
	for {
		next := scenifyMidWordSpaceChars.ReplaceAllString(title, "$1$2")
		if next == title {
			break
		}
		title = next
	}
	return parser.RemoveDiacritics(title)
}

// TitleThe moves a leading "The/An/A " prefix to the end as `, The`.
// Mirrors `FileNameBuilder.TitleThe(string)` at `:311-314`.
//
// Examples:
//
//   - `The Office`         → `Office, The`
//   - `An American Tail`   → `American Tail, An`
//   - `A New Hope (1977)`  → `New Hope, A (1977)`
//   - `Office`             → `Office` (no prefix → unchanged)
func TitleThe(title string) string {
	return titlePrefixRegex.ReplaceAllString(title, "$2, $1$3")
}

// CleanTitleThe combines [TitleThe] + [CleanTitle] for series with a
// leading article. Mirrors `:316-325`.
//
// When the title has a leading "The/An/A " prefix, splits at the
// prefix, cleans the body, and emits `<cleanbody>, <prefix><cleansuffix>`.
// When no prefix matches, falls back to plain CleanTitle.
func CleanTitleThe(title string) string {
	if !titlePrefixRegex.MatchString(title) {
		return CleanTitle(title)
	}
	parts := titlePrefixRegex.FindStringSubmatch(title)
	if len(parts) < 4 {
		return CleanTitle(title)
	}
	body := strings.TrimSpace(CleanTitle(parts[2]))
	suffix := CleanTitle(parts[3])
	return body + ", " + parts[1] + suffix
}

// TitleYear appends ` (year)` to `title` when `year > 0` and `title`
// doesn't already carry a trailing year. Mirrors `:327-342`.
func TitleYear(title string, year int) string {
	if year == 0 {
		return title
	}
	if yearRegex.MatchString(title) {
		return title
	}
	return title + " (" + strconv.Itoa(year) + ")"
}

// CleanTitleTheYear combines [CleanTitleThe] + an appended bare year.
// Mirrors `:344-361`.
//
// When `title` already carries a trailing `(####)` year, that year wins
// over the parameter (matches C# behaviour at `:354-358` — extracts
// the title's year). When `year == 0` and the title has no trailing
// year, falls back to plain CleanTitleThe.
//
// Output shape: `<cleanedTitleThe> <year>` (year as bare digits, no
// parens — different from [TitleYear]).
func CleanTitleTheYear(title string, year int) string {
	if year == 0 {
		if yearRegex.MatchString(title) {
			match := yearRegex.FindString(title)
			body := strings.TrimSpace(yearRegex.ReplaceAllString(title, ""))
			return CleanTitleThe(body) + " " + match[1:5]
		}
		return CleanTitleThe(title)
	}
	if yearRegex.MatchString(title) {
		match := yearRegex.FindString(title)
		body := strings.TrimSpace(yearRegex.ReplaceAllString(title, ""))
		return CleanTitleThe(body) + " " + match[1:5]
	}
	return CleanTitleThe(title) + " " + strconv.Itoa(year)
}

// CleanTitleYear is the [CleanTitle]+year analogue of [CleanTitleTheYear]
// without the article move. Sonarr uses this when the user prefers
// `Office 2005` to `Office, The 2005`.
//
// Not in the C# `FileNameBuilder` directly — it's the body of the
// `{Series CleanTitleYear}` token resolver. Lifted out here for
// reuse + testability.
func CleanTitleYear(title string, year int) string {
	if yearRegex.MatchString(title) {
		match := yearRegex.FindString(title)
		body := strings.TrimSpace(yearRegex.ReplaceAllString(title, ""))
		return CleanTitle(body) + " " + match[1:5]
	}
	if year == 0 {
		return CleanTitle(title)
	}
	return CleanTitle(title) + " " + strconv.Itoa(year)
}

// CleanTitleWithoutYear strips a trailing `(####)` year and applies
// CleanTitle to the remainder. Used by the
// `{Series CleanTitleWithoutYear}` token.
func CleanTitleWithoutYear(title string) string {
	return CleanTitle(strings.TrimSpace(yearRegex.ReplaceAllString(title, "")))
}

// CleanTitleTheWithoutYear strips a trailing year then applies
// [CleanTitleThe]. Used by `{Series CleanTitleTheWithoutYear}`.
func CleanTitleTheWithoutYear(title string) string {
	return CleanTitleThe(strings.TrimSpace(yearRegex.ReplaceAllString(title, "")))
}

// TitleWithoutYear strips a trailing `(####)` year. Mirrors `:363-368`.
func TitleWithoutYear(title string) string {
	return yearRegex.ReplaceAllString(title, "")
}

// TitleTheWithoutYear is the article-moved + year-stripped variant.
// Used by `{Series TitleTheWithoutYear}`.
func TitleTheWithoutYear(title string) string {
	return TitleThe(strings.TrimSpace(yearRegex.ReplaceAllString(title, "")))
}

// TitleTheYear is `TitleThe` + appended year. The C# tree expresses
// this via the `{Series TitleTheYear}` token resolver inline — extracted
// here for reuse.
func TitleTheYear(title string, year int) string {
	return TitleYear(TitleThe(title), year)
}

// TitleFirstCharacter returns the first letter or digit of `title`,
// uppercased and diacritic-stripped. Falls back to the second character
// when the first is non-alphanumeric, then to `_` if neither slot has
// a usable character. Mirrors `:370-385`.
func TitleFirstCharacter(title string) string {
	if title == "" {
		return "_"
	}
	runes := []rune(title)
	if isLetterOrDigit(runes[0]) {
		return parser.RemoveDiacritics(strings.ToUpper(string(runes[0])))
	}
	if len(runes) > 1 && isLetterOrDigit(runes[1]) {
		return parser.RemoveDiacritics(strings.ToUpper(string(runes[1])))
	}
	return "_"
}

func isLetterOrDigit(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
