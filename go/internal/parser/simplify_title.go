package parser

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/quality"
)

// First slice of the SimplifyTitle pipeline integration. Ports the
// title-cleanup passes that `Parser.ParseTitle` runs on its input
// BEFORE the ReportTitleRegex rule walk (`Parser.cs:681-734`):
//
//  1. ReversedTitleRegex check + character-reverse of the
//     extension-less prefix.
//  2. RemoveFileExtension → releaseTitle.
//  3. 【 / 】 → [ / ] normalisation.
//  4. PreSubstitutionRegex.
//  5. SimpleTitleRegex (resolution / codec / DD5.1 / dimension /
//     bit-depth strip).
//  6. WebsitePrefix / WebsitePostfix / CleanTorrentSuffix.
//  7. CleanQualityBracketsRegex (only strips the trailing bracket
//     when its content parses as a recognised quality).
//  8. SixDigitAirDate rewrite.
//
// Helpers 4, 6, and 8 ship in earlier slices (PreSubstitution
// 2.11c, ParserCommon 2.11a, SixDigitAirDate 2.12.0); this slice
// wires them into the same C# call order plus adds 1, 3, 5, 7.
//
// Post-match enrichment (Languages, Quality, ReleaseGroup,
// ReleaseHash, GetSeriesTitleInfo, GetSubGroup) is deferred to
// subsequent slices.

// reversedTitleRe ports `Parser.ReversedTitleRegex` at
// `Parser.cs:513`. Detects releases whose extension-less portion
// has been character-reversed by checking for the distinctive
// `p027` / `p0801` / `<NN>E-?<NN>S` token (the reversed forms of
// `720p` / `1080p` / `S<NN>E<NN>`).
var reversedTitleRe = regexp.MustCompile(`(?:^|[-._ ])(p027|p0801|\d{2,3}E-?\d{2}S)[-._ ]`)

// simpleTitleRe ports `Parser.SimpleTitleRegex` at `Parser.cs:521-523`.
// Strips resolution / codec / DD5.1 / dimension / bit-depth tokens
// from a release title.
//
// Two RE2 reformulations vs the C# literal, both implemented as
// post-match filters in `applySimpleTitle`:
//
//  1. The C# `(?<![a-f0-9])` negative lookbehind on the bit-depth
//     arm rejects matches preceded by a hex digit (so the trailing
//     hex of a release-hash like `DEADBEEF8bit` doesn't fool the
//     bit-depth strip). RE2 has no lookbehind.
//  2. The C# `b(?![a-z0-9])` negative lookahead on the bit-depth
//     arm rejects matches where a standalone `b` is followed by an
//     alphanumeric byte. RE2 has no lookahead.
//
// The bit-depth alternative is wrapped in a named `bd` capture
// group so the post-match filter can detect when the bit-depth
// branch fired (vs the resolution / codec / dimension arms, which
// have no lookarounds).
//
// The C# bit-depth arm `(b(?![a-z0-9])|bit)` tries `b` with
// lookahead first, falling through to `bit` when the lookahead
// fails. RE2 has no lookahead, so the Go port re-orders the
// alternation as `bit|b` (longer arm first). This makes the
// regex engine prefer the 3-char `bit` match where possible —
// otherwise a `10 bit` input would match as `10 b` and the
// post-match lookahead-equivalent filter would reject the whole
// thing rather than matching the longer `bit` form. With `bit|b`,
// the engine picks `bit` directly when the input contains it,
// and falls back to `b` for the standalone-letter case.
//
// `\W` is preserved verbatim. C# `\W` matches any non-word char
// (Unicode-aware by default); Go's RE2 default `\W` is ASCII-only.
// All real release-name separators between codec letter and
// `26[45]` are ASCII (`.`, `-`, `_`, ` `), so the ASCII-only `\W`
// catches every fixture row.
var simpleTitleRe = regexp.MustCompile(
	`(?i)(?:(?:480|540|576|720|1080|1440|2160)[ip]` +
		`|[xh][\W_]?26[45]` +
		`|DD\W?5\W1` +
		`|[<>?*]` +
		`|848x480|1280x720|1920x1080|3840x2160|4096x2160` +
		`|(?P<bd>(?:8|10)[ -]?(?:bit|b)))\s*?`,
)

// cleanQualityBracketsRe ports `Parser.CleanQualityBracketsRegex` at
// `Parser.cs:530-531`. Matches a trailing `[<lowercase + digits +
// space + . _ -]+]` bracket. The caller (applyCleanQualityBrackets)
// strips the bracket only when its content parses as a recognised
// quality.
var cleanQualityBracketsRe = regexp.MustCompile(`(?i)\[[a-z0-9 ._-]+\]$`)

// fancyBracketReplacer maps the CJK-fullwidth bracket characters to
// their ASCII equivalents. Mirrors `Parser.cs:693`
// `releaseTitle.Replace("【", "[").Replace("】", "]")`.
var fancyBracketReplacer = strings.NewReplacer("【", "[", "】", "]")

// applyFancyBrackets normalises CJK-fullwidth `【` / `】` brackets to
// ASCII `[` / `]`. Mirrors `Parser.cs:693`.
func applyFancyBrackets(title string) string {
	return fancyBracketReplacer.Replace(title)
}

// applyReversedTitle detects a reversed-title release via the
// distinctive ReversedTitleRegex token, character-reverses the
// extension-less portion, and re-appends the extension. Mirrors
// `Parser.cs:681-689`.
//
// The C# code character-reverses by `char[]` (UTF-16 code units);
// the Go port reverses by rune so multi-byte UTF-8 chars stay
// intact. Real reversed-title releases are ASCII per the fixture,
// so the two implementations agree byte-for-byte.
func applyReversedTitle(title string) string {
	if !reversedTitleRe.MatchString(title) {
		return title
	}
	withoutExt := RemoveFileExtension(title)
	return reverseString(withoutExt) + title[len(withoutExt):]
}

func reverseString(s string) string {
	rs := []rune(s)
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	return string(rs)
}

// applySimpleTitle ports the C# `SimpleTitleRegex.Replace(s, "")` at
// `Parser.cs:704`. The two negative lookarounds on the bit-depth arm
// (see simpleTitleRe doc) are applied as post-match filters.
func applySimpleTitle(s string) string {
	matches := simpleTitleRe.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	bdIdx := simpleTitleRe.SubexpIndex("bd")
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, m := range matches {
		if bdIdx >= 0 {
			bdStart, bdEnd := m[2*bdIdx], m[2*bdIdx+1]
			if bdStart != -1 {
				// (?<![a-f0-9]) before the bit-depth match start
				if m[0] > 0 && isHexByte(s[m[0]-1]) {
					continue
				}
				// b(?![a-z0-9]): when the bd group ends in 'b' (vs
				// 'bit'/'BIT' which ends in 't'), the next byte must
				// not be alphanumeric.
				lastByte := s[bdEnd-1]
				if lastByte == 'b' || lastByte == 'B' {
					if bdEnd < len(s) && isAlphaNumByte(s[bdEnd]) {
						continue
					}
				}
			}
		}
		b.WriteString(s[last:m[0]])
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isAlphaNumByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// applyCleanQualityBrackets strips a trailing `[...]` bracket iff
// its content parses as a recognised quality. Mirrors the
// MatchEvaluator at `Parser.cs:711-719`.
func applyCleanQualityBrackets(title string) string {
	return cleanQualityBracketsRe.ReplaceAllStringFunc(title, func(match string) string {
		// `match` includes the surrounding brackets (`[...]`). C#
		// passes the whole bracket text to ParseQualityName; the
		// quality regexes are tolerant of bracket wrappers.
		if quality.ParseQualityName(match).Quality.ID != quality.Unknown.ID {
			return ""
		}
		return match
	})
}

// applySimplifyTitle runs the full SimplifyTitle-equivalent cleanup
// pipeline on a release title. Returns the simpleTitle the
// ReportTitleRegex rules match against. Mirrors
// `Parser.ParseTitle:681-734`.
func applySimplifyTitle(title string) string {
	title = applyReversedTitle(title)
	s := RemoveFileExtension(title)
	s = applyFancyBrackets(s)
	s = applyPreSubstitution(s)
	s = applySimpleTitle(s)
	s = ApplyWebsitePrefix(s)
	s = ApplyWebsitePostfix(s)
	s = ApplyCleanTorrentSuffix(s)
	s = applyCleanQualityBrackets(s)
	s = applySixDigitAirDate(s)
	return s
}
