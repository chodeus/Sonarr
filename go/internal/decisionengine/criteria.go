package decisionengine

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// SearchMode is a bit-flag enum mirroring
// `src/NzbDrone.Core/Indexers/SearchMode.cs`. SearchID and SearchTitle
// are independent flags; Default selects both behaviours via
// fallthrough in the request generators.
//
// Slice 4.BW (C-cleanup [C14]) relocated the type from indexersearch
// to decisionengine so [SearchCriteria] could grow the SearchMode
// field without an indexersearch import cycle. The indexersearch
// package re-exports the type + constants via a type alias for
// callers that still spell `indexersearch.SearchMode*`.
type SearchMode int

// SearchMode flag values. Order + values mirror C#.
const (
	SearchModeDefault     SearchMode = 0
	SearchModeRSS         SearchMode = 1 << 0 // 1
	SearchModeSearchID    SearchMode = 1 << 1 // 2
	SearchModeSearchTitle SearchMode = 1 << 2 // 4
)

// HasFlag reports whether m contains f. Mirrors C#
// `SearchMode.HasFlag(other)` bit-test semantics.
func (m SearchMode) HasFlag(f SearchMode) bool { return m&f == f }

// AllSceneTitles returns the union of SceneTitles + CleanSceneTitles,
// deduped, in iteration order. Mirrors C#
// `SearchCriteriaBase.cs:25`.
//
// Slice 4.BW (C-cleanup [C14]) lifted from indexersearch.CriteriaBase.
func (c SearchCriteria) AllSceneTitles() []string {
	seen := make(map[string]struct{}, len(c.SceneTitles)*2)
	out := make([]string, 0, len(c.SceneTitles)*2)
	for _, t := range c.SceneTitles {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	for _, t := range c.CleanSceneTitles() {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// CleanSceneTitles returns the SceneTitles each cleaned via
// [GetCleanSceneTitle], deduped. Mirrors C#
// `SearchCriteriaBase.cs:26`.
func (c SearchCriteria) CleanSceneTitles() []string {
	seen := make(map[string]struct{}, len(c.SceneTitles))
	out := make([]string, 0, len(c.SceneTitles))
	for _, t := range c.SceneTitles {
		clean := GetCleanSceneTitle(t)
		if _, ok := seen[clean]; !ok {
			seen[clean] = struct{}{}
			out = append(out, clean)
		}
	}
	return out
}

var (
	specialCharRegex = regexp.MustCompile(`['` + "`" + `´‘’.]`)
	// Use unicode-aware classes — Go's `\w` is ASCII-only by default
	// while .NET's is unicode-aware. Without this, `é` in "Café" gets
	// replaced with `+` before removeDiacritics has a chance to convert
	// it to `e`. Behaviour now matches the C# regex semantics.
	nonWordRegex      = regexp.MustCompile(`[^\p{L}\p{N}_]`)
	beginningTheRegex = regexp.MustCompile(`(?i)^the\s`)
	repeatedPlusRegex = regexp.MustCompile(`\+{2,}`)
)

// GetCleanSceneTitle ports `SearchCriteriaBase.cs:30-43`.
//
// Algorithm:
//  1. Strip leading "the " (case-insensitive).
//  2. Replace "&" with "and".
//  3. Strip apostrophes / backticks / unicode quotes / dots.
//  4. Replace any remaining non-word character with "+".
//  5. Collapse runs of "+" into a single "+".
//  6. Strip diacritics.
//  7. Trim leading/trailing "+" or whitespace.
//
// Slice 4.BW (C-cleanup [C14]) lifted from indexersearch — same
// reason as SearchMode: the helper lives next to the type that
// carries it on [SearchCriteria].
func GetCleanSceneTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return ""
	}
	cleanTitle := beginningTheRegex.ReplaceAllString(title, "")
	cleanTitle = strings.ReplaceAll(cleanTitle, "&", "and")
	cleanTitle = specialCharRegex.ReplaceAllString(cleanTitle, "")
	cleanTitle = nonWordRegex.ReplaceAllString(cleanTitle, "+")
	cleanTitle = repeatedPlusRegex.ReplaceAllString(cleanTitle, "+")
	cleanTitle = removeDiacritics(cleanTitle)
	return strings.Trim(cleanTitle, "+ ")
}

// removeDiacritics ports the C# `RemoveDiacritics` extension at
// `NzbDrone.Common.Extensions.StringExtensions`. NFD-decomposes first
// so pre-composed Latin characters (e.g. é = U+00E9) split into base
// letter + combining mark, then drops the combining marks. Without
// the NFD step, `é` would survive as a single non-Mn rune and never
// get stripped.
func removeDiacritics(s string) string {
	decomposed := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
