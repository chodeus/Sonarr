package parser

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/language"
)

// Final slice of the LanguageParser port. Wires the regex primitives
// from slices (a) and (b) into the public ParseLanguages entry point
// and ports the 28 `lower.Contains(...)` arms from
// `LanguageParser.cs:54-192`.
//
// Control flow follows the C# source line-for-line so a future sync is
// direct: each labelled section here corresponds to a numbered branch
// of the C# function. Comments cite the source line ranges.

// cleanSeriesTitleRe ports `LanguageParser.CleanSeriesTitleRegex[0]`
// at `LanguageParser.cs:20`. Strips everything before the season-number
// token (S\d{2}). If matched, the title is replaced with capture group
// 1 (the season-onward portion); if not matched, the title is unchanged
// because Go's ReplaceAllString is a no-op on non-matching input.
//
// RE2-clean (entry #135 in `analysis/parser/regex-corpus.md`).
var cleanSeriesTitleRe = regexp.MustCompile(`(?i).*?[_. ](S\d{2}(?:E\d{2,4})*[_. ].*)`)

// containsLanguageRules ports the 28 `lower.Contains("...")` arms in
// `LanguageParser.ParseLanguages` at `:54-192`. Order is preserved
// because DistinctBy keeps the first occurrence and the arm order
// determines that. Each entry is one arm: when ANY of `substrings`
// is found in the lowercase title, `lang` is added once.
//
// Compound arms (Chinese / PortugueseBrazil / Azerbaijani / Uzbek)
// each list multiple substrings — same OR semantics as the C# `||`.
var containsLanguageRules = []struct {
	substrings []string
	lang       language.Language
}{
	{[]string{"spanish"}, language.Spanish},
	{[]string{"danish"}, language.Danish},
	{[]string{"dutch"}, language.Dutch},
	{[]string{"japanese"}, language.Japanese},
	{[]string{"icelandic"}, language.Icelandic},
	{[]string{"mandarin", "cantonese", "chinese"}, language.Chinese},
	{[]string{"korean"}, language.Korean},
	{[]string{"russian"}, language.Russian},
	{[]string{"polish"}, language.Polish},
	{[]string{"vietnamese"}, language.Vietnamese},
	{[]string{"swedish"}, language.Swedish},
	{[]string{"norwegian"}, language.Norwegian},
	{[]string{"finnish"}, language.Finnish},
	{[]string{"turkish"}, language.Turkish},
	{[]string{"portuguese"}, language.Portuguese},
	{[]string{"hungarian"}, language.Hungarian},
	{[]string{"hebrew"}, language.Hebrew},
	{[]string{"arabic"}, language.Arabic},
	{[]string{"hindi"}, language.Hindi},
	{[]string{"malayalam"}, language.Malayalam},
	{[]string{"ukrainian"}, language.Ukrainian},
	{[]string{"bulgarian"}, language.Bulgarian},
	{[]string{"slovak"}, language.Slovak},
	{[]string{"brazilian", "dublado"}, language.PortugueseBrazil},
	{[]string{"latino"}, language.SpanishLatino},
	{[]string{"latvian"}, language.Latvian},
	{[]string{"azerbaijani", "azerbaijan"}, language.Azerbaijani},
	{[]string{"uzbek", "uzbekistan"}, language.Uzbek},
}

// ParseLanguages mirrors `LanguageParser.ParseLanguages` at
// `LanguageParser.cs:40-227`. Returns the set of languages detected
// from a release title, with duplicates collapsed by Language.ID.
//
// Algorithm (line-for-line):
//
//  1. `:42-48` — strip the series-title prefix via CleanSeriesTitleRegex.
//     The C# foreach + break is moot since the array has one element;
//     the Go port is a single ReplaceAllString.
//  2. `:50-52` — lowerTitle for the .Contains arm. Allocates one extra
//     string; the alternative (28 calls to strings.EqualFold-equivalent
//     substring search) is meaningfully slower.
//  3. `:54-192` — 28 .Contains arms in declaration order.
//  4. `:194-199` — RegexLanguage(title) wired through. The C# helper
//     dispatches both case-sensitive (slice b) and case-insensitive
//     (slice a) arms in that order; the Go port preserves the order
//     by calling regexLanguagesCS first then regexLanguagesCI.
//  5. `:201-204` — post-regex `english` .Contains check. AFTER the
//     regex arm so the de-dup keeps whichever fires first.
//  6. `:206-209` — Unknown fallback when no arm fired.
//  7. `:211-224` — German DL/ML post-processing. Only fires when
//     German is the SOLE detected language.
//  8. `:226` — DistinctBy(Language.ID) preserving first-encounter
//     order. The C# original returns ToList() over the deduped
//     enumeration; Go returns the deduplicated slice.
//
// Returns a non-empty slice — at minimum, [Language.Unknown].
func ParseLanguages(title string) []language.Language {
	// `:42-48` — clean prefix.
	title = cleanSeriesTitleRe.ReplaceAllString(title, "${1}")

	// `:50-52` — lowerTitle for .Contains.
	lowerTitle := strings.ToLower(title)

	languages := make([]language.Language, 0, 4)

	// `:54-192` — 28 .Contains arms.
	for _, r := range containsLanguageRules {
		for _, s := range r.substrings {
			if strings.Contains(lowerTitle, s) {
				languages = append(languages, r.lang)
				break
			}
		}
	}

	// `:194-199` — regex arm. Case-sensitive first, then case-insensitive
	// — mirrors the C# RegexLanguage helper's internal order at `:354-520`.
	languages = append(languages, regexLanguagesCS(title)...)
	languages = append(languages, regexLanguagesCI(title)...)

	// `:201-204` — post-regex `english` .Contains.
	if strings.Contains(lowerTitle, "english") {
		languages = append(languages, language.English)
	}

	// `:206-209` — Unknown fallback.
	if len(languages) == 0 {
		languages = append(languages, language.Unknown)
	}

	// `:211-224` — German DL/ML post-processing. Note this fires AFTER
	// the Unknown fallback but BEFORE the DistinctBy at `:226`. The
	// C# guard `languages.Count == 1 && languages.Single() == Language.German`
	// permits the post-processing only when German is the sole tag —
	// the Go port checks the (possibly duplicate-bearing) pre-distinct
	// slice, which can only equal `[German]` if exactly one arm fired
	// (the regex arm; .Contains has no `german` rule).
	if len(languages) == 1 && languages[0] == language.German {
		switch {
		case IsGermanDualLanguage(title):
			languages = append(languages, language.Original)
		case IsGermanMultiLanguage(title):
			languages = append(languages, language.Original, language.English)
		}
	}

	// `:226` — DistinctBy(Language.ID).
	return distinctByLanguageID(languages)
}

// distinctByLanguageID mirrors the C# `DistinctBy(l => (int)l)` call
// at `:226`. Preserves first-encounter order. Required because both
// the .Contains arm and the regex arm can produce overlapping hits
// (e.g. a German release with `Ger.Dub` triggers regex CI's german
// arm; if the title also literally contains "german" lowercased,
// the .Contains arm — which has no "german" rule — won't fire, but
// other languages can collide).
func distinctByLanguageID(in []language.Language) []language.Language {
	seen := make(map[int]bool, len(in))
	out := make([]language.Language, 0, len(in))
	for _, l := range in {
		if seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		out = append(out, l)
	}
	return out
}
