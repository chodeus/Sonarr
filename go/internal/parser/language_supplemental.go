package parser

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/language"
)

// Second slice of the LanguageParser port. Mirrors three small support
// regexes from `LanguageParser.cs:28-32` plus the case-sensitive arm
// of the C# private `RegexLanguage` consumer at `:354-384`:
//
//   - CaseSensitiveLanguageRegex — bare 2-letter codes (LT/CZ/PL/BG/SK/DE)
//     that must NOT match when adjacent to `SUB` (so `LT-SUB` and
//     `SUB-LT` don't register as Lithuanian, etc.).
//   - GermanDualLanguageRegex — `\bDL\b` not preceded by `WEB[-_. ]?`,
//     drives the "add Original" arm for German releases at `:213-217`.
//   - GermanMultiLanguageRegex — `\bML\b`, drives the "add Original
//     and English" arm at `:218-223`.
//
// Together with slice (a)'s `regexLanguagesCI`, these three primitives
// are the complete set of regex-based language detection. The remaining
// surface (the 30+ `lower.Contains(...)` arm + the German DL/ML
// post-processing wiring + DistinctBy de-dup + the four subtitle-file
// entry points) lands in slice (c).

// caseSensitiveLanguageRe is the C# `CaseSensitiveLanguageRegex` from
// `LanguageParser.cs:28` rewritten for RE2.
//
// The C# original:
//
//	(?:(?i)(?<!SUB[\W|_|^]))
//	(?:(?<lithuanian>\bLT\b)|(?<czech>\bCZ\b)|(?<polish>\bPL\b)
//	  |(?<bulgarian>\bBG\b)|(?<slovak>\bSK\b)|(?<german>\bDE\b))
//	(?:(?i)(?![\W|_|^]SUB))
//	flags: c (no `i` at the top-level — the lookarounds carry their own
//	       inline `(?i)` so the case-insensitive scope is the SUB token,
//	       not the LT/CZ/etc. alternation, which stays case-sensitive)
//
// Two RE2 reformulations:
//
//  1. The negative lookbehind `(?<!SUB[\W|_|^])` and lookahead
//     `(?![\W|_|^]SUB)` are unsupported by RE2. Both are post-match
//     filtered in regexLanguagesCS by inspecting up to 4 bytes
//     around the match position.
//
//  2. `[\W|_|^]` in the C# tree is the literal char class `[\W|_^]`
//     (the `|` chars inside `[...]` are literal pipes, NOT alternation
//     metachars). `\W` already covers `|` and `^` so the effective set
//     is `\W | _` — i.e. any non-alphanumeric byte. Preserved verbatim
//     as `subAdjacentChar`. This was almost certainly an authoring
//     bug in the C# tree (the intent was likely `[\W_]`), but the
//     behaviour is identical to that intent and the Go port follows
//     suit without correcting.
var caseSensitiveLanguageRe = regexp.MustCompile(
	`(?P<lithuanian>\bLT\b)` +
		`|(?P<czech>\bCZ\b)` +
		`|(?P<polish>\bPL\b)` +
		`|(?P<bulgarian>\bBG\b)` +
		`|(?P<slovak>\bSK\b)` +
		`|(?P<german>\bDE\b)`,
)

// caseSensitiveGroupOrder maps each named capture in
// caseSensitiveLanguageRe to the Language it implies, in declaration
// order (matches the C# alternation at `LanguageParser.cs:28` and the
// consumer at `:356-384`).
var caseSensitiveGroupOrder = []struct {
	name string
	lang language.Language
}{
	{"lithuanian", language.Lithuanian},
	{"czech", language.Czech},
	{"polish", language.Polish},
	{"bulgarian", language.Bulgarian},
	{"slovak", language.Slovak},
	{"german", language.German},
}

var caseSensitiveGroupIndex = func() map[string]int {
	m := make(map[string]int, len(caseSensitiveGroupOrder))
	for _, g := range caseSensitiveGroupOrder {
		m[g.name] = caseSensitiveLanguageRe.SubexpIndex(g.name)
	}
	return m
}()

// subAdjacentChar reports whether b is in the C# `[\W|_|^]` class —
// effectively any non-alphanumeric byte (`\W` covers `|` / `^` and the
// extra `_` brings underscore into the set, since `_` is a word char in
// `\w`). See the package note above for the C# typo discussion.
func subAdjacentChar(b byte) bool {
	if b == '_' {
		return true
	}
	return (b < 'A' || b > 'Z') && (b < 'a' || b > 'z') && (b < '0' || b > '9')
}

// regexLanguagesCS walks every CaseSensitiveLanguageRegex match in
// title and returns the languages implied by the surviving matches, in
// match order. Mirrors the case-sensitive arm of `RegexLanguage` at
// `LanguageParser.cs:354-384`.
//
// Two post-match filters substitute the unsupported lookarounds:
//
//  1. Pre-filter: `(?<!SUB[\W|_|^])` — the 4 bytes immediately preceding
//     the match must NOT case-insensitively spell `SUB` followed by one
//     `[\W_]` byte. So `Foo.SUB-LT.Bar` is rejected (the `LT` match is
//     preceded by `SUB-`); `Foo.AC3.LT.Bar` is accepted.
//  2. Post-filter: `(?![\W|_|^]SUB)` — the 4 bytes immediately after
//     the match must NOT spell one `[\W_]` byte followed by SUB. So
//     `Foo.LT-SUB.Bar` is rejected; `Foo.LT.Bar` is accepted.
//
// The C# regex deliberately operates with the top-level alternation
// case-SENSITIVE (no `(?i)` flag at the outer scope) — only the
// lookarounds carry inline `(?i)` so that SUB matches sub/Sub/etc. The
// Go port preserves this: the compiled regex is case-sensitive, and
// the post-filter byte comparisons use strings.EqualFold for the SUB
// token.
//
// De-duplication is the caller's responsibility (ParseLanguages does
// it via DistinctBy in slice c).
func regexLanguagesCS(title string) []language.Language {
	matches := caseSensitiveLanguageRe.FindAllStringSubmatchIndex(title, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]language.Language, 0, len(matches))
	for _, m := range matches {
		matchStart, matchEnd := m[0], m[1]

		// Pre-filter: SUB[\W_] immediately before.
		if matchStart >= 4 &&
			subAdjacentChar(title[matchStart-1]) &&
			strings.EqualFold(title[matchStart-4:matchStart-1], "SUB") {
			continue
		}

		// Post-filter: [\W_]SUB immediately after.
		if matchEnd+4 <= len(title) &&
			subAdjacentChar(title[matchEnd]) &&
			strings.EqualFold(title[matchEnd+1:matchEnd+4], "SUB") {
			continue
		}

		// Determine which named group fired.
		for _, g := range caseSensitiveGroupOrder {
			idx := caseSensitiveGroupIndex[g.name]
			if idx <= 0 || 2*idx+1 >= len(m) {
				continue
			}
			if m[2*idx] != -1 {
				out = append(out, g.lang)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dlRe is the bare `\bDL\b` (case-insensitive) match used by
// IsGermanDualLanguage. The lookbehind `(?<!WEB[-_. ]?)` from the C#
// `GermanDualLanguageRegex` at `LanguageParser.cs:31` is enforced
// post-match.
var dlRe = regexp.MustCompile(`(?i)\bDL\b`)

// IsGermanDualLanguage reports whether title contains a `\bDL\b` token
// NOT preceded by `WEB[-_. ]?` — i.e. the German "DL" (Dual Language)
// release tag, distinct from the source-tag `WEB-DL`. Drives the
// "add Original" arm for German releases at `LanguageParser.cs:213-217`.
//
// RE2 reformulation: the C# `(?<!WEB[-_. ]?)` lookbehind is unsupported.
// For each `\bDL\b` match, the up-to-4 bytes before the match are
// inspected:
//
//   - If matchStart-4 begins with `WEB` (case-insensitive) followed by
//     one `[-_. ]` separator → reject (this is `WEB-DL` or `WEB DL`,
//     which is a source tag, not a language tag).
//   - If matchStart-3 begins with `WEB` (case-insensitive) → reject
//     (this is `WEBDL` — though `\bDL\b` would not normally match
//     inside `WEBDL` because `B` and `D` are both word chars and `\b`
//     does not fire; preserved here for parity with the C# pattern's
//     `?`-optional separator).
//
// The first surviving match makes this function return true.
func IsGermanDualLanguage(name string) bool {
	matches := dlRe.FindAllStringIndex(name, -1)
	for _, m := range matches {
		start := m[0]
		// 4-byte form: `WEB` + separator.
		if start >= 4 &&
			strings.EqualFold(name[start-4:start-1], "WEB") &&
			isWebDLSeparator(name[start-1]) {
			continue
		}
		// 3-byte form: bare `WEB`.
		if start >= 3 && strings.EqualFold(name[start-3:start], "WEB") {
			continue
		}
		return true
	}
	return false
}

// mlRe is the C# `GermanMultiLanguageRegex` at `LanguageParser.cs:32`.
// Case-insensitive, RE2-clean.
var mlRe = regexp.MustCompile(`(?i)\bML\b`)

// IsGermanMultiLanguage reports whether title contains a `\bML\b`
// token. Drives the "add Original AND English" arm for German releases
// at `LanguageParser.cs:218-223`.
func IsGermanMultiLanguage(name string) bool {
	return mlRe.MatchString(name)
}

func isWebDLSeparator(b byte) bool {
	return b == '-' || b == '_' || b == '.' || b == ' '
}
