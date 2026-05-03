package parser

import (
	"regexp"
	"strconv"
	"strings"
)

// percentAfterDigitsRe captures the digits-followed-by-`%` pattern
// from the C# `(?<=\b\d+)%` regex. RE2 doesn't support lookbehind, so
// the digits are pulled into a capture group and re-emitted by the
// replace callback. Equivalent to the C# `PercentRegex.Replace(title,
// "percent")` invocation in `Parser.CleanSeriesTitle`.
var percentAfterDigitsRe = regexp.MustCompile(`\b(\d+)%`)

// commonWords are the short connectors that `CleanSeriesTitle` strips
// from the middle of a title. Mirrors the alternation
// `[aà]|an|the|and|or|of` in the C# `NormalizeRegex` literal at
// `Parser.cs:515`. Lowercased — matching is case-insensitive via an
// explicit ToLower of the candidate window before comparison.
var commonWords = []string{"a", "à", "an", "the", "and", "or", "of"}

// nonWordOrUnderscoreRe matches any non-word character including
// underscore, with Unicode semantics. The C# `NormalizeRegex`
// alternation `\W|_` is Unicode-aware (.NET's `\W` excludes
// `\p{L}\p{N}_`), so `à` and `í` are NOT matched by C# `\W` and
// stay in the title — only their diacritics are stripped later.
// Go's default `\W` is ASCII-only (excludes only `[A-Za-z0-9_]`),
// which would incorrectly strip non-ASCII letters like `à` here.
// The character class below restores Unicode parity:
// `[^\p{L}\p{N}]` matches any rune that is neither a letter nor a
// digit, which already includes `_` (so the explicit `_` from C# is
// implicit here).
var nonWordOrUnderscoreRe = regexp.MustCompile(`[^\p{L}\p{N}]`)

// partTrailingRe — `\(\d+\)$` — strips a trailing `(N)` group used by
// French / Spanish episode titles ("French Title (1)"). C# parity:
// `Parser.cs:539` `PartRegex`.
var partTrailingRe = regexp.MustCompile(`\(\d+\)$`)

// punctuationRe — `[^\w\s]` — strips every char that is neither a
// word character nor whitespace. Mirrors `Parser.cs:540`
// `PunctuationRegex`.
var punctuationRe = regexp.MustCompile(`[^\w\s]`)

// articlePrefixRe — `^(a|an|the)\s` — strips a leading article. Used
// by `NormalizeTitle` (NOT `NormalizeEpisodeTitle`). Mirrors
// `Parser.cs:541` `ArticleWordRegex`.
var articlePrefixRe = regexp.MustCompile(`(?i)^(a|an|the)\s`)

// duplicateSpacesRe — `\s{2,}` — collapses runs of whitespace.
// Mirrors `Parser.cs:543` `DuplicateSpacesRegex`.
var duplicateSpacesRe = regexp.MustCompile(`\s{2,}`)

// specialEpisodeTitleRes mirrors the two-element
// `SpecialEpisodeTitleRegex[]` array at `Parser.cs:454-461`. Both
// patterns extract a per-episode title from a full release name when
// the release is a special (`S<N>E00` or `S<N>.Special`). RE2-clean.
var specialEpisodeTitleRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\.S\d+E00\.(?P<episodetitle>.+?)(?:\.(?:720p|1080p|2160p|HDTV|WEB|WEBRip|WEB-DL)\.|$)`),
	regexp.MustCompile(`(?i)\.S\d+\.Special\.(?P<episodetitle>.+?)(?:\.(?:720p|1080p|2160p|HDTV|WEB|WEBRip|WEB-DL)\.|$)`),
}

// CleanSeriesTitle is the Go port of the C# extension
// `Parser.CleanSeriesTitle(this string)` at `Parser.cs:814-831`.
//
// Behaviour, exactly matching the C#:
//
//  1. Empty / whitespace-only input → return verbatim.
//  2. Pure-numeric input (`long.TryParse` succeeds) → return verbatim
//     (preserves "90210" → "90210", "24" → "24" for series titles
//     that are themselves a number).
//  3. Replace digits-followed-by-`%` with `<digits>percent`
//     (the "3%" case → "3percent").
//  4. Strip middle-of-string common words (`a/à/an/the/and/or/of`)
//     when bordered by a non-word boundary on both sides AND not at
//     string start AND not at string end. Case-insensitive.
//  5. Strip every remaining non-word character and underscore.
//  6. Lowercase.
//  7. Apply RemoveDiacritics (NFKD + Latin-extension lookup).
//
// The C# regex `((?:\b|_)(?<!^)([aà](?!$)|an|the|and|or|of)(?!$)(?:\b|_))|\W|_`
// uses lookbehind and lookahead (RE2-incompatible). The Go port
// reformulates step 4 as a hand-coded scan because that's clearer
// than fighting RE2 through a multi-pass alternation rewrite, and
// because the test corpus pins the exact expected behaviour
// (`NormalizeSeriesTitleFixture.cs` — ported in `normalize_test.go`).
func CleanSeriesTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return title
	}
	if _, err := strconv.ParseInt(title, 10, 64); err == nil {
		return title
	}

	// Step 3: percent → "percent" (when preceded by digits + word
	// boundary). RE2 rewrite emits the captured digits back.
	title = percentAfterDigitsRe.ReplaceAllString(title, "${1}percent")

	// Step 4: drop middle-of-string common words.
	title = stripMiddleCommonWords(title)

	// Step 5: drop every remaining non-word and underscore.
	title = nonWordOrUnderscoreRe.ReplaceAllString(title, "")

	// Steps 6 + 7: lowercase + strip diacritics.
	return RemoveDiacritics(strings.ToLower(title))
}

// stripMiddleCommonWords removes `a / à / an / the / and / or / of`
// when each appears between two non-word boundaries (or `_`),
// matching is case-insensitive, AND the match is not at the very
// start or very end of the string. The C# regex achieves this via
// `(?<!^)...(?!$)` lookarounds; the Go port walks runes and applies
// position checks directly.
func stripMiddleCommonWords(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		matched := false
		// Only attempt a common-word match when we're at a boundary
		// position: either the string start (which is a boundary) or
		// the previous emitted byte was a non-word/underscore. The C#
		// regex requires `(?:\b|_)` before, but ALSO requires NOT at
		// string start (`(?<!^)`), so the match must be preceded by
		// at least one byte AND that byte must be a non-word boundary.
		if i > 0 && isWordBoundaryByte(s[i-1]) {
			for _, w := range commonWords {
				wlen := len(w)
				if i+wlen > len(s) {
					continue
				}
				if lower[i:i+wlen] != w {
					continue
				}
				// After the match, require either end of string or a
				// non-word/underscore. C# `(?:\b|_)` after.
				afterEnd := i + wlen
				atEnd := afterEnd == len(s)
				if atEnd {
					// `(?!$)` blocks matches that end at string end.
					continue
				}
				if !isWordBoundaryByte(s[afterEnd]) {
					continue
				}
				// Special case: for `[aà]`, the C# inner alternation
				// adds an extra `(?!$)` directly inside the group.
				// That has the same effect as our atEnd check above —
				// already enforced.
				// Match accepted: drop the word from output. Continue
				// past the matched bytes; the boundary character that
				// follows is preserved in the next iteration's normal
				// path.
				i += wlen
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// isWordBoundaryByte reports whether b is a non-word character or
// underscore — the boundary class used by the C# `\b|_` alternation.
// Word characters in .NET regex are `[A-Za-z0-9_]`; the explicit
// `_` in `\W|_` makes underscore count as a boundary even though
// `\W` would normally exclude it. We use a byte check rather than
// rune because the boundary characters are all ASCII (`.`, `-`, ` `,
// `_`, etc. as observed in the test corpus); a non-ASCII rune at
// the boundary would mean we're inside a multi-byte sequence anyway,
// which the calling loop handles correctly by always advancing one
// byte when no match is found.
func isWordBoundaryByte(b byte) bool {
	if b == '_' {
		return true
	}
	if b >= '0' && b <= '9' {
		return false
	}
	if b >= 'a' && b <= 'z' {
		return false
	}
	if b >= 'A' && b <= 'Z' {
		return false
	}
	// Non-ASCII byte = part of a UTF-8 multi-byte sequence, which is
	// inside a word character (e.g. `à`). Treat as not-a-boundary so
	// the common-word match doesn't engage across multi-byte chars.
	if b >= 0x80 {
		return false
	}
	return true
}

// NormalizeEpisodeTitle is the Go port of the C# static
// `Parser.NormalizeEpisodeTitle(string)` at `Parser.cs:833-852`.
//
// Behaviour:
//
//  1. If the input matches one of the SpecialEpisodeTitleRegex
//     patterns, replace it with the captured `episodetitle` group.
//  2. Strip a trailing `(N)` group (PartRegex).
//  3. Replace every non-word non-whitespace character with a space
//     (PunctuationRegex).
//  4. Collapse multi-space runs (DuplicateSpacesRegex).
//  5. Trim + lowercase.
//
// The C# implementation has a commented-out `SpecialEpisodeWordRegex`
// strip in step 2.5 that's disabled "Until we run into specific
// testcases for the removal of these words." The Go port preserves
// the disabled state.
func NormalizeEpisodeTitle(title string) string {
	for _, re := range specialEpisodeTitleRes {
		if m := re.FindStringSubmatch(title); m != nil {
			idx := re.SubexpIndex("episodetitle")
			if idx > 0 && idx < len(m) {
				title = m[idx]
				break
			}
		}
	}
	title = partTrailingRe.ReplaceAllString(title, "")
	title = punctuationRe.ReplaceAllString(title, " ")
	title = duplicateSpacesRe.ReplaceAllString(title, " ")
	return strings.ToLower(strings.TrimSpace(title))
}

// NormalizeTitle is the Go port of `Parser.NormalizeTitle(string)` at
// `Parser.cs:854-861`. Strips punctuation, then a leading article
// (a / an / the), then collapses spaces. Trim + lowercase. Used by
// loose title-matching paths (e.g. SkyHook proxy series lookup) —
// distinct from CleanSeriesTitle which is the canonical search-key
// form.
func NormalizeTitle(title string) string {
	title = punctuationRe.ReplaceAllString(title, "")
	title = articlePrefixRe.ReplaceAllString(title, "")
	title = duplicateSpacesRe.ReplaceAllString(title, " ")
	return strings.ToLower(strings.TrimSpace(title))
}
