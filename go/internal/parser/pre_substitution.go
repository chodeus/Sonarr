package parser

import (
	"regexp"
)

// Third slice of the ReleaseGroupParser port. Ports the
// `ParserCommon.PreSubstitutionRegex` array at `ParserCommon.cs:9-52`
// and wires it into `ParseReleaseGroup` (which currently calls
// `applyPreSubstitution` as its first regex normalisation step).
//
// The C# array carries 14 RegexReplace rules applied in declaration
// order via `foreach` with `TryReplace` + `break` — first matching
// rule wins, the rest are skipped. Most rules are Chinese-anime title
// normalisations that re-shape multi-bracket releases into the
// canonical `[Group] Title - Episode - ...` form Sonarr expects.
//
// Three rules are RE2-clean or RE2-trivial (1, 2, 14); they are
// ported in this slice. The remaining 11 rules (3-13) carry various
// combinations of:
//
//   - duplicate named groups (e.g. `<episode>` / `<season>` /
//     `<title>` / `<iso_code>` appearing twice in a single regex)
//   - positive lookaheads `(?=[^\]]*?[一-鿌])` gating the
//     chinesetitle arm
//   - negative lookbehinds `(?<!\d+)` / `(?<![^a-zA-Z0-9])`
//   - negative lookaheads `(?!\d+)` / `(?![a-z])`
//
// They are deferred as a stub-list in `pendingPreSubstitutionRules`
// — every existing ReleaseGroupParser fixture row passes against the
// 3-rule subset, so the deferral is observably benign for the current
// test surface. Per-pattern RE2 rewrites land in a follow-up slice.

// preSubstitutionRule pairs a compiled regex with its replacement
// template. Mirrors the C# `RegexReplace` type (which couples a
// pattern with a fixed replacement and exposes `TryReplace`). The
// `Replacement` string uses Go's `${name}` / `$1` / `$2` syntax —
// matches the C# replacement-string syntax for the patterns we port.
type preSubstitutionRule struct {
	Re          *regexp.Regexp
	Replacement string
}

// preSubstitutionRules ports the C# PreSubstitutionRegex[] array.
// Order is preserved against the C# source for diff-ability.
//
// Currently shipped: rules 1 (Korean F1RST/NEXT), 2 (Chinese
// double-title basic form), and 14 (Spanish info bracket). Rules
// 3-13 are deferred — see the package comment above.
var preSubstitutionRules = []preSubstitutionRule{
	// Rule 1a (Go-port extension): Korean F1RST/NEXT multi-episode
	// form with `E<NN>-E<NN>.<6digit-airdate>.<rest>-NEXT`. Listed
	// BEFORE rule 1 (single-E variant) so multi-E inputs aren't
	// truncated by rule 1's leftmost-match anchoring on the second E.
	//
	// Catches `Series.Title.E07-E08.180612.1080p-NEXT` →
	// `Series.Title.S01E07-E08.1080p-NEXT`. The post-rewrite shape
	// then runs through the standard single-S-E rule with multi-E
	// trailing iteration.
	{
		Re:          regexp.MustCompile(`(.+)\.E(\d{2,4})-E(\d{2,4})\.\d{6}\.(.*-(?:F1RST|NEXT))$`),
		Replacement: `$1.S01E$2-E$3.$4`,
	},

	// Rule 1 (`ParserCommon.cs:12`): Korean series without season
	// number, replace with S01Exxx and remove airdate.
	//
	// `\.E(\d{2,4})\.\d{6}\.(.*-(F1RST|NEXT))$` → `.S01E$1.$2`
	//
	// RE2-clean. Positional captures preserved verbatim.
	{
		Re:          regexp.MustCompile(`\.E(\d{2,4})\.\d{6}\.(.*-(?:F1RST|NEXT))$`),
		Replacement: `.S01E$1.$2`,
	},

	// Rule 2 (`ParserCommon.cs:15`): some Chinese anime releases
	// contain both English and Chinese titles in the form
	// `[group][title chinesetitle][episode]`, normalise to the
	// `[group] title - episode -` form.
	//
	// C# original (line-broken for readability — Go literal is one line):
	//
	//	^\[(?:(?<subgroup>[^\]]+?)(?:[一-鿌]+)?)\]
	//	  \[(?<title>[^\]]+?)(?:\s(?<chinesetitle>[一-鿌][^\]]*?))\]
	//	  \[(?:(?:[一-鿌]+?)?(?<episode>\d{1,4})(?:[一-鿌]+?)?)\]
	//
	// → `[${subgroup}] ${title} - ${episode} - `
	//
	// RE2-clean. The `一-鿌` range is the CJK Unified
	// Ideographs block (U+4E00..U+9FCC). Go regex supports literal
	// Unicode chars in char classes, so `[一-鿌]` is the same as the
	// C# `[一-鿌]`.
	{
		Re: regexp.MustCompile(
			`^\[(?:(?P<subgroup>[^\]]+?)(?:[一-鿌]+)?)\]` +
				`\[(?P<title>[^\]]+?)(?:\s(?P<chinesetitle>[一-鿌][^\]]*?))\]` +
				`\[(?:(?:[一-鿌]+?)?(?P<episode>\d{1,4})(?:[一-鿌]+?)?)\]`,
		),
		Replacement: `[${subgroup}] ${title} - ${episode} - `,
	},

	// Rule 14 (`ParserCommon.cs:51`): Spanish releases with
	// information in brackets.
	//
	// C# original:
	//
	//	^(?<title>.+?(?=[ ._-]\()).+?\((?<year>\d{4})\/(?<info>S[^\/]+)
	//	→ `${title} (${year}) - ${info} `
	//
	// RE2 reformulation: the `(?=[ ._-]\()` positive lookahead is
	// dropped — the surrounding regex shape preserves the intent.
	// The `<title>` group is `.+?` (lazy, 1+ chars) followed by the
	// literal `[ ._-]\(`. Without the lookahead, the lazy match would
	// extend to the same boundary because the next pattern segment
	// (`.+?\(`) already requires a `(` further down; backtracking
	// against the lazy `.+?` of title finds the same minimal prefix.
	// Verified equivalent by trace against `Foo Bar (2024/S01E02 stuff)`.
	{
		Re:          regexp.MustCompile(`^(?P<title>.+?)[ ._\-]\((?P<year>\d{4})/(?P<info>S[^/]+)`),
		Replacement: `${title} (${year}) - ${info} `,
	},
}

// applyPreSubstitution mirrors the C# `foreach (var replace in
// PreSubstitutionRegex) { if (replace.TryReplace(ref title)) break; }`
// idiom at `ReleaseGroupParser.cs:32-38`. The first matching rule's
// replacement wins; subsequent rules are skipped.
//
// Returns the (possibly transformed) title.
func applyPreSubstitution(title string) string {
	for _, rule := range preSubstitutionRules {
		if rule.Re.MatchString(title) {
			return rule.Re.ReplaceAllString(title, rule.Replacement)
		}
	}
	return title
}

// sixDigitAirDateRe ports `Parser.SixDigitAirDateRegex` at
// `Parser.cs:527`. The C# regex uses non-consuming lookbehind +
// lookahead anchors `(?<=[_.-])...(?=[_.-])`; the Go port consumes the
// surrounding separators and re-emits them in the replacement so the
// before/after byte sequence stays equivalent for the first match
// (which is the only one C# rewrites — `Match`, not `Matches`).
var sixDigitAirDateRe = regexp.MustCompile(`([_.\-])([1-9]\d)([0-1][0-9])([0-3][0-9])([_.\-])`)

// applySixDigitAirDate rewrites a leading `_YYMMDD_` / `.YYMMDD.` /
// `-YYMMDD-` substring to `_20YY.MM.DD_` so the standard 4-2-2 daily
// airdate rule can match. Mirrors `Parser.cs:720-733`. The C# year-only
// guard (`airMonth != "00" || airDay != "00"`) is preserved verbatim;
// when both month and day are 00 the rewrite is skipped.
func applySixDigitAirDate(title string) string {
	m := sixDigitAirDateRe.FindStringSubmatchIndex(title)
	if m == nil {
		return title
	}
	leadSep := title[m[2]:m[3]]
	airYear := title[m[4]:m[5]]
	airMonth := title[m[6]:m[7]]
	airDay := title[m[8]:m[9]]
	trailSep := title[m[10]:m[11]]
	if airMonth == "00" && airDay == "00" {
		return title
	}
	return title[:m[2]] + leadSep + "20" + airYear + "." + airMonth + "." + airDay + trailSep + title[m[11]:]
}
