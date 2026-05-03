package parser

import (
	"regexp"
	"strconv"
	"strings"
)

// Second slice of the ReleaseGroupParser port. Six regexes (the simple
// five plus the heavy `ReleaseGroupRegex`) and the public
// `ParseReleaseGroup` pipeline. The 14-pattern PreSubstitutionRegex
// array stays stubbed (no-op) — its own slice (2.11c).

// rgWordChar is the C# `[A-Za-zÀ-ÖØ-öø-ÿ0-9]` char class — ASCII
// letters + Latin-1 supplement letters (`À-Ö` U+00C0-U+00D6, `Ø-ö`
// U+00D8-U+00F6, `ø-ÿ` U+00D8-U+00FF) + digits. Used in both arms
// of `ReleaseGroupRegex` to constrain the release-group token to
// letters in those ranges so accented release-group names like
// `Báleygr` and `Hveðrungr` are matched.
const rgWordChar = `[A-Za-zÀ-ÖØ-öø-ÿ0-9]`

// releaseGroupArmAWithPart2Re ports ARM A of the C# `ReleaseGroupRegex`
// at `ReleaseGroupParser.cs:9` in its with-part2 form, anchored at the
// start of the input slice (`\A`) so callers can iterate every `-`
// position and probe each one independently.
//
// The two C# lookarounds are stripped here and applied as post-match
// filters in findReleaseGroupCandidates: the negative lookahead
// `(?!.+?(?:HDTV|SDTV|480p|576p|720p|1080p|2160p))` and the negative
// lookbehind whose `\k<part2>` named-backreference RE2 doesn't support.
//
// C# original (one regex, two arms; this is just ARM A):
//
//	-(?<releasegroup>[A-Za-zÀ-ÖØ-öø-ÿ0-9]+
//	    (?<part2>-[A-Za-zÀ-ÖØ-öø-ÿ0-9]+)?
//	    (?!.+?(?:HDTV|SDTV|480p|576p|720p|1080p|2160p))
//	)
//	(?<!(?:HDTV|SDTV|WEB-DL|Blu-Ray|480p|576p|720p|1080p|2160p|
//	      DTS-HD|DTS-X|DTS-MA|DTS-ES|-ES|-EN|-CAT|-GER|-FRA|-FRE|-ITA|
//	      \d{1,2}-bit|[ ._]\d{4}-\d{2}|-\d{2})(?:\k<part2>)?)
//	(?:\b|[-._ ]|$)
//	flags: i, c
//
// Why two regexes (with-part2 and no-part2): the C# engine's internal
// backtracking will try the optional part2 first, then fall back to
// the no-part2 form at the SAME position when the lookbehind rejects
// the with-part2 form. Go's FindAllStringSubmatchIndex doesn't expose
// per-position backtracking, so we re-probe each position with both
// regex variants ourselves. Without this, an outer-rejected match
// (e.g. `-Bit-ZR-` failing on `8-Bit` matching `\d{1,2}-bit`) would
// hide the inner valid match (`-ZR-`) because Go's match-iteration
// skips past the rejected match's full span.
var releaseGroupArmAWithPart2Re = regexp.MustCompile(
	`(?i)\A-(?P<releasegroup>` + rgWordChar + `+(?P<part2>-` + rgWordChar + `+))(?:\b|[-._ ]|$)`,
)

// releaseGroupArmANoPart2Re is the same as releaseGroupArmAWithPart2Re
// but with the part2 alternative absent — corresponds to the C# regex
// when its `(?<part2>...)?` matches zero times. Used as the fallback
// at each `-` position when the with-part2 form is rejected by the
// lookbehind/lookahead post-filters.
var releaseGroupArmANoPart2Re = regexp.MustCompile(
	`(?i)\A-(?P<releasegroup>` + rgWordChar + `+)(?:\b|[-._ ]|$)`,
)

// releaseGroupArmBRe ports ARM B of the C# `ReleaseGroupRegex` —
// `[-._ ]\[<rg>\]$`. RE2-clean. Note ARM A and ARM B share the
// `<releasegroup>` group name in the C# tree (which RE2 rejects); the
// Go port compiles them as two separate regexes and merges results in
// findReleaseGroupCandidates.
var releaseGroupArmBRe = regexp.MustCompile(
	`(?i)[-._ ]\[(?P<releasegroup>` + rgWordChar + `+)\]$`,
)

// staticRejectTokens is the C# lookbehind alternation's static-string
// arms. Each is a literal that, if it ends at the position
// immediately after the captured releasegroup, rejects the match.
// Order is preserved against the C# source for diff-ability.
var staticRejectTokens = []string{
	"HDTV", "SDTV", "WEB-DL", "Blu-Ray",
	"480p", "576p", "720p", "1080p", "2160p",
	"DTS-HD", "DTS-X", "DTS-MA", "DTS-ES",
	"-ES", "-EN", "-CAT", "-GER", "-FRA", "-FRE", "-ITA",
}

// dynamicRejectRe captures the three regex-shaped reject patterns from
// the C# lookbehind: `\d{1,2}-bit` / `[ ._]\d{4}-\d{2}` / `-\d{2}`.
// Each is end-anchored against `title[:pos]` so MatchString returns
// true iff the chars immediately before pos spell the pattern.
var dynamicRejectRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\d{1,2}-bit$`),
	regexp.MustCompile(`[ ._]\d{4}-\d{2}$`),
	regexp.MustCompile(`-\d{2}$`),
}

// rejectionLookaheadRe ports the C# negative-lookahead body
// `.+?(?:HDTV|SDTV|480p|576p|720p|1080p|2160p)` from inside the
// `releasegroup` capture. If `MatchString(title[matchEnd:])` is true,
// the match is rejected — the rest of the title contains a resolution
// token, indicating the captured token is not actually a release group
// (e.g. a midstream `-XYZ-720p` shouldn't classify `XYZ` as the group).
var rejectionLookaheadRe = regexp.MustCompile(`(?i).+?(?:HDTV|SDTV|480p|576p|720p|1080p|2160p)`)

// endsAtPosWithReject reports whether title[:pos] ends with any of
// the C# lookbehind reject tokens (static + dynamic). Mirrors the
// `(?<!(?:HDTV|SDTV|...|-\d{2}))` pattern: at position pos, look back
// far enough to spell any reject token and report a match.
func endsAtPosWithReject(title string, pos int) bool {
	for _, tok := range staticRejectTokens {
		k := len(tok)
		if pos >= k && strings.EqualFold(title[pos-k:pos], tok) {
			return true
		}
	}
	prefix := title[:pos]
	for _, re := range dynamicRejectRe {
		if re.MatchString(prefix) {
			return true
		}
	}
	return false
}

// invalidReleaseGroupRe ports `InvalidReleaseGroupRegex` at
// `ReleaseGroupParser.cs:12` — `^([se]\d+|[0-9a-f]{8})$`. Rejects
// season/episode tokens (S01, e02) and 8-char hex hashes that
// otherwise look like release groups but aren't.
var invalidReleaseGroupRe = regexp.MustCompile(`(?i)^(?:[se]\d+|[0-9a-f]{8})$`)

// animeReleaseGroupRe ports `AnimeReleaseGroupRegex` at
// `ReleaseGroupParser.cs:14`. Matches a `[<subgroup>]<sep>?` prefix at
// the start of the title; the subgroup must NOT start or end with
// whitespace.
//
// C# original:
//
//	^(?:\[(?<subgroup>(?!\s).+?(?<!\s))\](?:_|-|\s|\.)?)
//
// RE2 reformulation: the `(?!\s)` and `(?<!\s)` boundary lookarounds
// (no-space at start AND end of subgroup) are inlined as `\S` anchors
// at the first and last character. The lazy `.+?` between them is the
// subgroup body.
var animeReleaseGroupRe = regexp.MustCompile(
	`(?i)^\[(?P<subgroup>\S(?:.*?\S)?)\](?:[_\-\s.])?`,
)

// exceptionReleaseGroupExactRe ports `ExceptionReleaseGroupRegexExact`
// at `ReleaseGroupParser.cs:19`. Manual-list of release groups that
// don't follow the `-RlsGrp` shape. The trailing `\b` is preserved.
// RE2-clean.
var exceptionReleaseGroupExactRe = regexp.MustCompile(
	`(?i)(?P<releasegroup>Fight-BB|VARYG|E\.N\.D|KRaLiMaRKo|BluDragon|DarQ|KCRT|BEN[_. ]THE[_. ]MEN|TAoE|QxR|Vialle)\b`,
)

// exceptionReleaseGroupRe ports `ExceptionReleaseGroupRegex` at
// `ReleaseGroupParser.cs:22`. Manual-list of release groups whose
// releases end with `RlsGroup)` or `RlsGroup]` — bracketed or
// parenthesised at the END but with a `[._ \[]` separator before.
//
// C# original:
//
//	(?<=[._ \[])(?<releasegroup>(Joy|ImE|UTR|t3nzin|Anime Time|...)(?=\]|\)))
//
// RE2 reformulation: lookbehind `(?<=[._ \[])` and lookahead `(?=\]|\))`
// rewritten as captured leading/trailing chars. The match consumes the
// surrounding `[`/`]`/`(`/`)` chars; the named `releasegroup` group
// contains only the inner token.
var exceptionReleaseGroupRe = regexp.MustCompile(
	`(?i)[._ \[](?P<releasegroup>Joy|ImE|UTR|t3nzin|Anime Time|Project Angel|Hakata Ramen|HONE|Vyndros|SEV|Garshasp|Kappa|Natty|RCVR|SAMPA|YOGI|r00t|EDGE2020)[\]\)]`,
)

// cleanReleaseGroupRe ports `CleanReleaseGroupRegex` at
// `ReleaseGroupParser.cs:24`. Strips:
//   - the series/episode prefix `<title>[-._ ]<S01E02>[-._ ]` from
//     the start of the title
//   - the trailing `-<repost-marker>` chains (RP / Obfuscated /
//     Scrambled / Rakuv* / etc.) one or more times.
//
// RE2-clean.
var cleanReleaseGroupRe = regexp.MustCompile(
	`(?i)(?:^.*?[-._ ]S\d+E\d+[-._ ])|(?:-(?:RP|1|NZBGeek|Obfuscated|Scrambled|sample|Pre|postbot|xpost|Rakuv[a-z0-9]*|WhiteRev|BUYMORE|AsRequested|AlternativeToRequested|GEROV|Z0iDS3N|Chamele0n|4P|4Planet|AlteZachen|RePACKPOST))+$`,
)

// ParseReleaseGroup mirrors `ReleaseGroupParser.ParseReleaseGroup` at
// `ReleaseGroupParser.cs:28-86`. Returns the detected release group or
// "" when no group is detected (the C# return is `string` with
// potential `null`; the Go port substitutes empty string since release
// groups are never legitimately empty).
//
// Pipeline:
//
//  1. Trim title.
//  2. Strip media/usenet file extension.
//  3. Apply PreSubstitutionRegex foreach with TryReplace+break — STUBBED
//     in this slice; the 14-pattern Chinese-anime normalisation array
//     lands in slice 2.11c.
//  4. Strip website prefix (with Naruto-Kun lookbehind).
//  5. Strip trailing tracker suffix.
//  6. Try AnimeReleaseGroupRegex (the `[Group]<title>` form). If
//     matched, return the captured subgroup.
//  7. Apply CleanReleaseGroupRegex (strips episode-prefix + repost
//     suffix chains).
//  8. Try ExceptionReleaseGroupRegex (manual-list, bracketed/parens).
//     If matched, return the LAST match's releasegroup.
//  9. Try ExceptionReleaseGroupRegexExact (manual-list, plain).
//     If matched, return the LAST match's releasegroup.
//  10. Try ReleaseGroupRegex (the heavy regex, both arms). If any
//     candidate survives the post-match filters, return the LAST
//     surviving candidate UNLESS it parses as an int OR matches
//     InvalidReleaseGroupRegex.
//  11. Otherwise return "".
func ParseReleaseGroup(title string) string {
	title = strings.TrimSpace(title)
	title = RemoveFileExtension(title)

	// `:32-38` PreSubstitutionRegex (foreach + TryReplace + break).
	// Three rules currently shipped (1 / 2 / 14); the remaining 11 are
	// stubbed in `pre_substitution.go` pending per-pattern RE2 rewrites.
	title = applyPreSubstitution(title)

	title = ApplyWebsitePrefix(title)
	title = ApplyCleanTorrentSuffix(title)

	// `:43-48` AnimeReleaseGroupRegex.
	if m := animeReleaseGroupRe.FindStringSubmatch(title); m != nil {
		idx := animeReleaseGroupRe.SubexpIndex("subgroup")
		if idx > 0 && idx < len(m) && m[idx] != "" {
			return m[idx]
		}
	}

	// `:50` CleanReleaseGroupRegex.
	title = cleanReleaseGroupRe.ReplaceAllString(title, "")

	// `:52-57` ExceptionReleaseGroupRegex (LAST match).
	if g := lastNamedMatch(exceptionReleaseGroupRe, title, "releasegroup"); g != "" {
		return g
	}

	// `:59-64` ExceptionReleaseGroupRegexExact (LAST match).
	if g := lastNamedMatch(exceptionReleaseGroupExactRe, title, "releasegroup"); g != "" {
		return g
	}

	// `:66-83` ReleaseGroupRegex.
	candidates := findReleaseGroupCandidates(title)
	if len(candidates) > 0 {
		last := candidates[len(candidates)-1]
		if _, err := strconv.Atoi(last); err == nil {
			return ""
		}
		if invalidReleaseGroupRe.MatchString(last) {
			return ""
		}
		return last
	}

	return ""
}

// lastNamedMatch finds all matches of re against title and returns
// the named-group value of the LAST match, or "" when no match.
// Mirrors the C# `regex.Matches(title).OfType<Match>().Last().Groups[name].Value`
// idiom used in the ExceptionReleaseGroup{,Exact} branches.
func lastNamedMatch(re *regexp.Regexp, title, group string) string {
	matches := re.FindAllStringSubmatch(title, -1)
	if len(matches) == 0 {
		return ""
	}
	idx := re.SubexpIndex(group)
	if idx <= 0 {
		return ""
	}
	last := matches[len(matches)-1]
	if idx >= len(last) {
		return ""
	}
	return last[idx]
}

// findReleaseGroupCandidates walks both arms of ReleaseGroupRegex
// against title and returns the surviving release-group strings in
// match order.
//
// ARM A is probed at every `-` position via two anchored regex
// variants (with-part2 and no-part2). At each position the with-part2
// form is tried first — if its post-filter passes, the candidate is
// emitted; if rejected, the no-part2 form is tried at the SAME
// position. This mirrors the C# regex engine's per-position
// backtracking when the lookbehind rejects the optional-part2 form.
// Without this two-pass shape, Go's `FindAllStringSubmatchIndex`
// would skip past an outer-rejected match's full span and miss the
// inner valid match (e.g. `-Bit-ZR-` rejected by `8-Bit`-matches-
// `\d{1,2}-bit` hides the valid `-ZR-` candidate).
//
// ARM B has no lookarounds and is `$`-anchored so a single
// FindStringSubmatchIndex pass suffices.
//
// LastOrDefault semantic: callers pick the LAST element of the
// returned slice — mirrors `matches.OfType<Match>().Last()` at
// `ReleaseGroupParser.cs:70`.
func findReleaseGroupCandidates(title string) []string {
	var out []string

	withPart2RgIdx := releaseGroupArmAWithPart2Re.SubexpIndex("releasegroup")
	withPart2Part2Idx := releaseGroupArmAWithPart2Re.SubexpIndex("part2")
	noPart2RgIdx := releaseGroupArmANoPart2Re.SubexpIndex("releasegroup")

	// skipUntil tracks the end of the most recently accepted candidate's
	// match span (in title coords). Subsequent positions inside that
	// span are skipped — mirrors the C# regex engine's non-overlapping
	// match advancement after a successful match. Without this, an
	// accepted match like "-Erai-raws" would also yield "-raws" as a
	// second candidate at the inner `-` position, and LastOrDefault
	// would wrongly return "raws".
	skipUntil := 0
	n := len(title)
	for pos := 0; pos < n; pos++ {
		if pos < skipUntil {
			continue
		}
		if title[pos] != '-' {
			continue
		}
		sub := title[pos:]

		// Try with-part2 form first.
		if g, matchEnd := tryArmACandidateWithEnd(sub, pos, title, releaseGroupArmAWithPart2Re, withPart2RgIdx, withPart2Part2Idx); g != "" {
			out = append(out, g)
			skipUntil = matchEnd
			continue
		}

		// Fall back to no-part2 form at the same position.
		if g, matchEnd := tryArmACandidateWithEnd(sub, pos, title, releaseGroupArmANoPart2Re, noPart2RgIdx, -1); g != "" {
			out = append(out, g)
			skipUntil = matchEnd
		}
	}

	armBRgIdx := releaseGroupArmBRe.SubexpIndex("releasegroup")
	if m := releaseGroupArmBRe.FindStringSubmatchIndex(title); m != nil &&
		armBRgIdx > 0 && 2*armBRgIdx+1 < len(m) {
		out = append(out, title[m[2*armBRgIdx]:m[2*armBRgIdx+1]])
	}

	return out
}

// tryArmACandidateWithEnd runs an ARM A regex variant against an
// anchored substring (sub == title[pos:]) and returns the captured
// release group + the end of the full match span (in title coords) if
// the post-match filters accept it, else ("", 0).
//
// post-filters:
//   - rejectionLookaheadRe: title[rgEnd:] must NOT match
//     `.+?(?:HDTV|SDTV|480p|576p|720p|1080p|2160p)`.
//   - Form A lookbehind: title[:rgEnd] must NOT end with any reject
//     token.
//   - Form B lookbehind: if part2 captured, title[:part2Start] must
//     NOT end with any reject token. (The `\k<part2>` C# backref
//     collapses to "part1 ends with reject token" since the part2
//     chars are captured verbatim.)
func tryArmACandidateWithEnd(sub string, pos int, title string, re *regexp.Regexp, rgIdx, part2Idx int) (string, int) {
	m := re.FindStringSubmatchIndex(sub)
	if m == nil || rgIdx <= 0 || 2*rgIdx+1 >= len(m) {
		return "", 0
	}
	rgStart := m[2*rgIdx] + pos
	rgEnd := m[2*rgIdx+1] + pos
	matchEnd := m[1] + pos

	if rejectionLookaheadRe.MatchString(title[rgEnd:]) {
		return "", 0
	}
	if endsAtPosWithReject(title, rgEnd) {
		return "", 0
	}
	if part2Idx > 0 && 2*part2Idx+1 < len(m) && m[2*part2Idx] != -1 {
		part2Start := m[2*part2Idx] + pos
		if endsAtPosWithReject(title, part2Start) {
			return "", 0
		}
	}
	return title[rgStart:rgEnd], matchEnd
}
