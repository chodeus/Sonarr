package parser

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/language"
)

// First slice of the LanguageParser port. Mirrors the two regex
// primitives at `LanguageParser.cs:23-26`:
//
//   - LanguagesOnlyRegex — the 25-named-group alternation that
//     classifies a single language token (eng, ita, GERMAN, jp, etc.).
//   - LanguageRegex — wraps LanguagesOnlyRegex with a negative
//     lookahead that rejects matches embedded in a `[-_. ]subs?`
//     suffix (so `Eng.Sub` is NOT classified as English).
//
// LanguageRegex is dynamically interpolated from LanguagesOnlyRegex at
// field-init time in C# (`@$"(?:{LanguagesOnlyRegex})..."` —
// REGEX-Q8). The Go port mirrors via package-init string concatenation;
// the same source pattern feeds both the case-insensitive matcher AND
// the embedded suffix-filter regex (with its named groups rewritten as
// non-capturing — RE2 rejects duplicate named groups).
//
// Two RE2 reformulations vs the C# literal:
//
//  1. The spanish arm `\b(?:español|castellano|esp|spa(?!\(Latino\)))\b`
//     uses a negative lookahead which RE2 does not support. The bare
//     `spa` token is pulled into its own named group `spaspecial` so
//     the post-match filter in regexLanguagesCI can reject matches
//     where the captured `spa` is immediately followed by `(Latino)`.
//     Both `spanish` and `spaspecial` map to Language.Spanish.
//
//  2. The outer LanguageRegex's negative lookahead
//     `(?!(?:[-_. ](?:LanguagesOnlyRegex))*[-_. ]subs?)` is unsupported
//     by RE2. It is re-implemented as `subsFilterRe` (an anchored
//     positive form of the same pattern) checked against `title[matchEnd:]`
//     for each candidate match — if it matches, the candidate is dropped.

// languagesOnlyExpr is the C# `LanguagesOnlyRegex` from
// `LanguageParser.cs:23` rewritten for RE2: `(?<name>...)` →
// `(?P<name>...)`, plus the `spaspecial` split described above. Flags
// (case-insensitive) are applied at compile time on `languageRe`, not
// embedded in the literal — embedding `(?i)` here would propagate into
// the suffix-filter regex when the non-capturing variant is interpolated.
const languagesOnlyExpr = `(?P<english>\b(?:ing|eng)\b)` +
	`|(?P<italian>\b(?:ita|italian)\b)` +
	`|(?P<german>(?:swiss)?german\b|videomann|ger[. ]dub|\bger\b)` +
	`|(?P<flemish>flemish)` +
	`|(?P<greek>greek)` +
	`|(?P<french>(?:_|\b)(?:FR|VF|VF2|VFF|VFI|VFQ|TRUEFRENCH|FRENCH|FRE|FRA)(?:_|\b))` +
	`|(?P<russian>\b(?:rus|ru)\b)` +
	`|(?P<hungarian>\b(?:HUNDUB|HUN)\b)` +
	`|(?P<hebrew>\bHebDub\b)` +
	`|(?P<polish>\b(?:PL\W?DUB|DUB\W?PL|LEK\W?PL|PL\W?LEK)\b)` +
	`|(?P<chinese>\[(?:CH[ST]|BIG5|GB)\]|简|繁|字幕|国语音轨[.+])` +
	`|(?P<bulgarian>\bbgaudio\b)` +
	`|(?P<spanish>\b(?:español|castellano|esp)\b)` +
	`|(?P<spaspecial>\bspa\b)` +
	`|(?P<ukrainian>\b(?:\dx?)?(?:ukr))` +
	`|(?P<thai>\b(?:THAI)\b)` +
	`|(?P<romanian>\b(?:RoDubbed|ROMANIAN)\b)` +
	`|(?P<catalan>[-,. ]cat[. ](?:DD|subs)|\b(?:catalan|catalán)\b)` +
	`|(?P<latvian>\b(?:lat|lav|lv)\b)` +
	`|(?P<turkish>\b(?:tur)\b)` +
	`|(?P<urdu>\burdu\b)` +
	`|(?P<romansh>\b(?:romansh|rumantsch|romansch)\b)` +
	`|(?P<georgian>\b(?:geo|ka|kat|georgian)\b)` +
	`|(?P<japanese>\(JA\)|JAP|JPN)` +
	`|(?P<portuguese>[_. ]por[_. ])` +
	`|(?P<original>\b(?:orig|original)\b)`

// namedGroupHeader matches the `(?P<name>` prefix of any RE2 named
// capture so we can rewrite the alternation into a non-capturing form
// (`(?:`) for embedding inside the suffix-filter regex.
var namedGroupHeader = regexp.MustCompile(`\(\?P<[^>]+>`)

// languagesOnlyNonCapturingExpr is languagesOnlyExpr with every named
// capture replaced by a non-capturing group. The outer LanguageRegex
// embeds LanguagesOnlyRegex twice (once at the head, once inside the
// negative lookahead) — RE2 rejects duplicate named groups, so the
// inside-lookahead occurrence must be non-capturing.
var languagesOnlyNonCapturingExpr = namedGroupHeader.ReplaceAllString(languagesOnlyExpr, `(?:`)

// languageRe is the case-insensitive matcher for one language token.
// Mirrors `LanguagesOnlyRegex` at `LanguageParser.cs:23` plus the
// outer wrapper at `:25` whose negative lookahead is enforced
// post-match via `subsFilterRe` and `latinoSuffix` in
// regexLanguagesCI.
var languageRe = regexp.MustCompile(`(?i)` + languagesOnlyExpr)

// subsFilterRe is the positive form of the C# negative-lookahead
// filter at `LanguageParser.cs:25`:
// `(?!(?:[-_. ](?:LanguagesOnlyRegex))*[-_. ]subs?)`. Anchored at the
// start of the suffix slice so `MatchString(title[matchEnd:])`
// resolves the C# semantic exactly: if it matches, the candidate
// language token is followed by `(?:[-_. ](?:lang))*[-_. ]subs?` and
// must be dropped.
var subsFilterRe = regexp.MustCompile(`(?i)^(?:[-_. ](?:` + languagesOnlyNonCapturingExpr + `))*[-_. ]subs?`)

// latinoSuffix is the literal byte sequence that disqualifies a `spa`
// match per the C# `spa(?!\(Latino\))` arm of LanguagesOnlyRegex.
const latinoSuffix = "(Latino)"

// languageGroupOrder maps each named capture in languagesOnlyExpr to
// the Language it implies, in declaration order (which mirrors the C#
// alternation at `LanguageParser.cs:23` and the consumer's case-
// insensitive arm at `LanguageParser.cs:386-520`). Both `spanish` and
// `spaspecial` map to Language.Spanish — `spaspecial` is the RE2-rewrite
// split of the `spa(?!\(Latino\))` lookahead arm.
var languageGroupOrder = []struct {
	name string
	lang language.Language
}{
	{"english", language.English},
	{"italian", language.Italian},
	{"german", language.German},
	{"flemish", language.Flemish},
	{"greek", language.Greek},
	{"french", language.French},
	{"russian", language.Russian},
	{"hungarian", language.Hungarian},
	{"hebrew", language.Hebrew},
	{"polish", language.Polish},
	{"chinese", language.Chinese},
	{"bulgarian", language.Bulgarian},
	{"spanish", language.Spanish},
	{"spaspecial", language.Spanish},
	{"ukrainian", language.Ukrainian},
	{"thai", language.Thai},
	{"romanian", language.Romanian},
	{"catalan", language.Catalan},
	{"latvian", language.Latvian},
	{"turkish", language.Turkish},
	{"urdu", language.Urdu},
	{"romansh", language.Romansh},
	{"georgian", language.Georgian},
	{"japanese", language.Japanese},
	{"portuguese", language.Portuguese},
	{"original", language.Original},
}

// languageGroupIndex pre-computes each named group's SubexpIndex on
// languageRe so the hot path in regexLanguagesCI doesn't pay the
// lookup cost per match.
var languageGroupIndex = func() map[string]int {
	m := make(map[string]int, len(languageGroupOrder))
	for _, g := range languageGroupOrder {
		m[g.name] = languageRe.SubexpIndex(g.name)
	}
	return m
}()

// regexLanguagesCI runs the case-insensitive arm of LanguageParser's
// `RegexLanguage` helper against title and returns the languages
// implied by every surviving match, in match order.
//
// Mirrors the consumer at `LanguageParser.cs:386-520`:
//
//	var matches = LanguageRegex.Matches(title);
//	foreach (Match match in matches) {
//	    if (match.Groups["english"].Success) languages.Add(Language.English);
//	    ... (one branch per named group)
//	}
//
// Two post-match filters preserve the C# negative-lookahead semantics:
//
//  1. subsFilterRe — rejects matches followed by `(?:[-_. ](?:lang))*[-_. ]subs?`
//     (the LanguageRegex outer wrapper at `LanguageParser.cs:25`).
//  2. latinoSuffix — rejects `spaspecial` matches followed by
//     `(Latino)` (the spanish-arm `spa(?!\(Latino\))` at `:23`).
//
// Returned slice may contain duplicates if a language matches multiple
// times in the same title (e.g. multi-language packs); de-duplication
// is the caller's responsibility (ParseLanguages does it via DistinctBy
// in slice c).
func regexLanguagesCI(title string) []language.Language {
	matches := languageRe.FindAllStringSubmatchIndex(title, -1)
	if len(matches) == 0 {
		return nil
	}
	spaIdx := languageGroupIndex["spaspecial"]
	out := make([]language.Language, 0, len(matches))
	for _, m := range matches {
		matchEnd := m[1]
		if subsFilterRe.MatchString(title[matchEnd:]) {
			continue
		}
		if spaIdx > 0 && 2*spaIdx+1 < len(m) && m[2*spaIdx] != -1 {
			if matchEnd+len(latinoSuffix) <= len(title) &&
				strings.EqualFold(title[matchEnd:matchEnd+len(latinoSuffix)], latinoSuffix) {
				continue
			}
		}
		for _, g := range languageGroupOrder {
			idx := languageGroupIndex[g.name]
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
