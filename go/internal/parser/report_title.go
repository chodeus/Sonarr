package parser

import (
	"regexp"
	"strings"
)

// First slice of the C# `ReportTitleRegex[]` array port at
// `Parser.cs:21-452`. The full array is 122 patterns categorised by
// the C# inline comments — this slice ships the most common shapes
// (title-bearing single + multi episode, no-title single + multi
// episode) so the broader ParseTitle pipeline has working coverage of
// scene-standard release names. Subsequent slices add anime, daily,
// special, and miniseries patterns.
//
// Each rule pairs a compiled regex with a `Build` function that
// extracts capture groups into a *ParsedEpisodeInfo. Rules are tried
// in declaration order; first match wins (mirrors the C# "first
// regex with a match" foreach in `Parser.ParseTitle`).

// rgRule is one entry in the ReportTitleRegex table. The C# array is
// homogeneous (all entries are `Regex`), but the Go port pairs each
// regex with its capture-extraction logic so the table stays
// declarative and self-contained. Mirrors the C# pattern of "one
// regex per parse-title shape, dispatched by leftmost match".
type rgRule struct {
	Re    *regexp.Regexp
	Build func(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo
}

// reportTitleRules is the Go-side equivalent of
// `Parser.ReportTitleRegex[]`. Order matches the C# source.
//
// What's shipped in this slice (4 of 122 patterns):
//
//   - Title + season/episode (canonical scene release).
//   - Title + season/multi-episode.
//   - No-title single episode (raw `S01E05` filename).
//   - No-title multi-episode (raw `S01E05E06` filename).
//
// What's deferred to subsequent slices: anime patterns (~25), daily
// patterns (~5), special patterns (~5), miniseries patterns (~3),
// hashed/foreign-format/edge-case patterns (~80).
//
// The C# inline `(?<!\d+)` / `(?!\d+)` digit-boundary lookarounds
// are dropped here — the surrounding regex shape (literal `S` /
// `E` / `x` separators around the digit runs) already enforces the
// "exactly N digits at this position" constraint.
var reportTitleRules = []rgRule{
	// `Parser.cs:112-114` — Anime - [SubGroup] Title Absolute Episode
	// Number. Matches the most common anime release shape:
	// `[SubGroup] Title <separator> <absolute_episode> ... [hash]`.
	// Listed before the no-title single/multi rules so anime forms
	// like `[FFF] Show - 12` don't accidentally match the no-title
	// rules' prefix.
	//
	// C# original:
	//
	//	^\[(?<subgroup>.+?)\][-_. ]?
	//	(?<title>.+?)[-_. ]+
	//	(?<absoluteepisode>(?<!\d+)\d{2,3}(\.\d{1,2})?(?!\d+))
	//	.*?(?<hash>[(\[]\w{8}[)\]])?(?:$|\.mkv)
	//	flags: i, c
	//
	// RE2 reformulations: `(?<!\d+)` and `(?!\d+)` lookarounds dropped
	// — the surrounding shape (separator-then-2-3-digits with `\d{2,3}`
	// greedy) preserves the intent. Hash is captured into a separate
	// group so the post-processor can read the bare 8-char hash without
	// the surrounding bracket.
	{
		Re: regexp.MustCompile(
			`(?i)^\[(?P<subgroup>.+?)\][-_. ]?(?P<title>.+?)[-_. ]+(?P<absoluteepisode>\d{2,3}(?:\.\d{1,2})?).*?(?:[(\[](?P<hash>\w{8})[)\]])?(?:$|\.mkv)`,
		),
		Build: buildAnimeAbsolute,
	},

	// `Parser.cs:33` — No-title airdate at start of string. Mirrors
	// the C# `^<year>(<sep>)<month>\k<sep><day>` pattern that catches
	// release names like `2018-11-14.1080.all.mp4`,
	// `2019_08_20_1080_all.mp4`, and `20161024- Exotic Payback…`. The
	// year range `19[6-9]\d|20\d\d` (1960-2099) preserves the C#
	// pattern verbatim. Month uses the C# `0\d|1[0-2]` range (00-12)
	// rather than the standard rule's `[0-1][0-9]` (00-19) — the
	// no-title rule is stricter about validity since there's no title
	// context to disambiguate.
	//
	// Listed BEFORE the no-title multi/single + title+Nx rules so an
	// input that starts with a year-shaped run isn't first grabbed as
	// season=2018 episodes=[11,14] (no-title multi) or as a 21x41 Nx
	// shape inside the trailing tokens (title+Nx).
	//
	// RE2 reformulations:
	//
	//   - The C# `\k<sep>` named backreference (forces both separators
	//     to match — `2018-11-14` ✓ but `2018-11_14` ✗) is dropped.
	//     Both separators are independent `[-_]?`. None of the current
	//     fixture rows exercise the mixed-separator case.
	//   - The C# `(?!\d)` non-consuming lookahead becomes the
	//     consuming `(?:[^\d]|$)` end-anchor. The buildDailyAirdate
	//     consumer reads only the year/month/day groups, so consuming
	//     a trailing non-digit byte is harmless.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<airyear>19[6-9]\d|20\d\d)[-_]?(?P<airmonth>0\d|1[0-2])[-_]?(?P<airday>[0-2]\d|3[01])(?:[^\d]|$)`,
		),
		Build: buildDailyAirdate,
	},

	// `Parser.cs:45` — Episodes without a title, Multi (`S01E04E05`,
	// `1x04x05`). Listed BEFORE the no-title single rule so the
	// multi-episode form wins over the single-episode form's prefix.
	// Mirrors the C# array ordering at `Parser.cs:44-46` (multi at
	// index 5) preceding the single at index 7.
	//
	// `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_]|[ex]){1,2}(?<episode>\d{2,3}(?!\d+))){2,})`
	{
		Re: regexp.MustCompile(
			`(?i)^(?:S?(?P<season>\d{4}|\d{1,2})(?:(?:(?:[-_]|[ex]){1,2}|[-_. ]?S\d{1,2}E)\d{2,3}){2,})`,
		),
		Build: buildNoTitleMultiEpisode,
	},

	// `Parser.cs:53` — Episodes without a title, Single (`S01E05`, `1x05`).
	// `^(?:S?(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))(?:(?:[-_ ]?[ex])(?<episode>\d{2,3}(?!\d+))))`
	//
	// Episode `\d{1,3}` (vs C#'s `\d{2,3}`) so the bare `1x1` form
	// matches — `Parser.cs:282` has a separate single-digit-episode
	// rule the Go port collapses into this one.
	{
		Re: regexp.MustCompile(
			`(?i)^(?:S?(?P<season>\d{4}|\d{1,2})(?:[-_ ]?[ex]\d{1,3}))`,
		),
		Build: buildNoTitleEpisode,
	},

	// `Parser.cs:120` — Multi-episode Repeated Nx form, `<title>
	// <n>x<m> - <n>x<m>`. Catches `Series.Title.2x04.2x05`,
	// `Series Kings - 2x9-2x10`, `World Series of Sonarr - 2010x15 -
	// 2010x16`. Listed BEFORE the single Nx rule so multi wins on the
	// 2+-Nx-block prefix.
	//
	// C# original:
	//
	//	^(?<title>.+?)(?:(?:[-_\W](?<![()\[!]))+
	//	(?<season>(?<!\d+)(?:\d{1,2}|\d{4})(?!\d+))
	//	(?:x{1,2}(?<episode>\d{1,3}(?!\d+)))+){2,}
	//
	// `(?<!\d+)` lookbehind on season replaced by constraining the
	// title's last char to non-digit (`(?P<title>.+?[^\d])`). Episode
	// digits via the post-match findAllEpisodes scan that handles range
	// expansion.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?[^\d])(?:[-_. \W]+(?P<season>\d{4}|\d{1,2})x{1,2}\d{1,3}){2,}`,
		),
		Build: buildTitleNxMulti,
	},

	// `Parser.cs:188`-style — Episodes with a title, `<Title> <N>x<NN>`
	// form. Listed BEFORE the `S<n>E<nn>` rule so the dash/no-S form
	// is not shadowed by the no-title rule's `[ex]` matcher when a
	// title is present. Handles 1-3 digit episodes including the
	// `1x1` single-digit-episode form.
	//
	// `(?P<title>.+?[^\d])` — title's last char is constrained to
	// non-digit. RE2 stand-in for the C# `(?<!\d+)` lookbehind on the
	// season group: without it, lazy `.+?` would extend past digit
	// runs in the input (e.g. `...DDP5.1 x264 monkee` would
	// mis-classify `DDP5` as title and `1 x264` as `Nx<NN>` with
	// season=1 and episode=264). Forcing the title to end on a
	// non-digit char prevents this — the only valid match position
	// is now where a non-digit chars terminates the title.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?[^\d])[-_. \[]+(?P<season>\d{1,2})[-_ ]?[ex](?P<episode>\d{1,3})`,
		),
		Build: buildTitleNxEpisode,
	},

	// `Parser.cs:188` + `:192` (4-digit season) — Episodes with a title,
	// single + multi episode forms.
	// `^(?<title>.+?)(?:[-_. ]+S(?<season>(?<!\d+)\d{1,2}(?!\d+))
	//   (?:(?:(?:[-_. ]+E(?<episode>\d{2,3}(?!\d+)))+)
	//   |(?:E(?<episode>\d{2,3}(?!\d+))(?:[-_. ]?(?:E?(?<episode>\d{2,3}(?!\d+))))*)))`
	//
	// Listed BEFORE the 3-digit/4-digit shortcut rules below so an
	// explicit `<title>.S<n>E<nn>` form wins over the lazy
	// `<title>.<NNNN>` interpretation (e.g.
	// `Series.Title.2010.S05E07.720p.HDTV.X264-LOL` parses as title=
	// `Series.Title.2010` season=5 episode=7 instead of being grabbed
	// by the 4-digit rule as season=20 episode=10).
	//
	// `\d{1,2}|\d{4}` season alternation merges `Parser.cs:188` (1-2
	// digit season) and `Parser.cs:192` (4-digit season `S2016E05`).
	// 4-digit form is tried first (regex alternation order) so 4-digit
	// titles like `S1936E18` win over the truncated-2-digit reading.
	//
	// Go RE2 has no lookbehind; the `\d{1,2}` digit-count constraint is
	// preserved by the surrounding `S<digits>E<digits>` shape (both
	// flanking literals reject extra digits). The C# regex uses the
	// same `<episode>` group name for two duplicate alternation arms;
	// RE2 rejects duplicates so the duplicate-name aliases are dropped
	// and episode tokens are merged via a separate `findAllEpisodes`
	// scan over the matched span.
	// `Parser.cs:117` — Multi-episode Repeated (`S01E05 - S01E06`).
	// Listed BEFORE the title+S-E rule so multi-SxxExx forms with 2+
	// repetitions (e.g. `Series Title - S07E22 - S07E23` or
	// `S03E01.S03E02`) win over the single-SxxExx rule's prefix.
	//
	// The C# `(?:e|[-_. ]e){1,2}` separator-aware E matcher is
	// simplified to `E\d{2,3}` here — the `(S\d{1,2})?E\d{2,3}` shape
	// is what matters for the multi-S form.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)?(?:[-_. \W]+S(?P<season>\d{4}|\d{1,3})(?:E\d{1,4})+){2,}`,
		),
		Build: buildTitleSeasonEpisode,
	},

	// `Parser.cs:188` — Single SxxExx (or repeating `EXX` after one S).
	//
	// The pre-S separator class `[-_. \[]+` admits `[` so bracketed
	// shapes like `Title [S01E05]` and `Title (2010) - [S01E01-02-03]`
	// match — the C# regex's `\W+` separator covers them implicitly,
	// and the Go port keeps the narrower `[-_. ]` class plus an
	// explicit `[` to avoid eating other punctuation that would let
	// titles ending in `(YYYY)` collide with subsequent groups.
	//
	// Season `\d{4}|\d{1,3}` (was `\d{1,2}` before 2.12.11) admits
	// 3-digit seasons (`S010E16`, `S006E18`, `S101E01-E02`).
	//
	// Arm B's trailing iteration is split into:
	//
	//   - `(?:[-_. ]?E\d{1,3})*` — E-required iterations, allow
	//     1-digit episodes. Catches `S6E1.E2`, `S6E1-E2`, `S6E1E2`,
	//     `S6E1-E2-E3`, `S01E91-E100`.
	//   - `(?:[-_. ]?\d{2,3})*` — digits-only iterations, require 2+
	//     digits. Catches `S01E05-06`, `S01E01-02-03`.
	//
	// The 2-digit minimum on the digits-only arm is what stops
	// `Series.S01E04.2-45.PM.[HDTV-720p]` from slurping the trailing
	// `.2-45.PM` as multi-episode [4,5,6,…,45] (the C# regex achieves
	// the same constraint via `\d{2,3}(?!\d+)`; the Go port skips the
	// lookahead and keeps the minimum-length floor).
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)(?:[-_. \[]+S(?P<season>\d{4}|\d{1,3}))(?:(?:[-_. ]+E\d{1,4})+|(?:E\d{1,4}(?:[-_. ]?E\d{1,4})*(?:[-_. ]?\d{2,3})*))`,
		),
		Build: buildTitleSeasonEpisode,
	},

	// `Parser.cs:329` — Spanish tracker releases (Temporada / Cap.NN).
	// Two title shapes both anchored on the literal `[Cap` token:
	//
	//   1. `<Title> - Temporada N [<meta>][Cap.NN]...` (with the
	//      Temporada keyword).
	//   2. `<Title> [<meta>][Cap.NN]...` (no Temporada — just any
	//      bracketed metadata group right before `[Cap`).
	//
	// The 3-digit code after `Cap.` decomposes into 1-2 digit season +
	// 2 digit episode. Greedy `\d{1,2}` falls back when the fixed
	// 2-digit episode arm can't match — RE2's leftmost-first semantics
	// reach the same season/episode split as the C# regex.
	//
	// C# original:
	//
	//	^(?<title>.+?)(?:(?:[-_. ]+?Temporada.+?|\[.+?\])\[Cap)
	//	(?:[-_. ]+(?<season>(?<!\d+)\d{1,2})
	//	(?<episode>(?<!e|x)(?:[1-9][0-9]|[0][1-9])))+(?:\])
	//
	// The two RE2-incompatible lookbehinds (`(?<!\d+)` on season,
	// `(?<!e|x)` on episode) are dropped — the surrounding shape
	// (`[Cap[-_. ]+\d{1,2}<2-digit-episode>]`) constrains both
	// positions adequately on real fixture inputs.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)(?:(?:[-_. ]+?Temporada.+?|\[.+?\])\[Cap)(?:[-_. ]+(?P<season>\d{1,2})(?P<episode>(?:[1-9][0-9]|[0][1-9])))+(?:\])`,
		),
		Build: buildTitleNxEpisode,
	},

	// `Parser.cs:333` — Episodes with airdate (`2018.04.28` /
	// `2014-06-02` / `2014 04 28`). Listed AFTER the title+S-E + Nx
	// rules so titles like `Top Series - 06x11 - 2005.08.07` match
	// the Nx-episode rule. Listed BEFORE the 3/4-digit shortcuts so
	// titles like `A.Late.Talk.Show.2010.10.11` match the airdate
	// rather than being misclassified as season=20 episode=10.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)?\W*(?P<airyear>\d{4})[-_. ]+(?P<airmonth>[0-1][0-9])[-_. ]+(?P<airday>[0-3][0-9])`,
		),
		Build: buildDailyAirdate,
	},

	// `Parser.cs:362` — 8-digit airdate (`<title> 20201013` /
	// `20161024-...`). Listed AFTER the standard 4-2-2 airdate rule so
	// titles already carrying `YYYY.MM.DD` separators win first; listed
	// BEFORE the 3/4-digit shortcuts so an 8-digit run isn't mis-read
	// as a season+episode-shortcut prefix.
	//
	// C# original:
	//
	//	^(?<title>.+?)?\W*(?<!\d+)(?<airyear>\d{4})
	//	(?<airmonth>[0-1][0-9])(?<airday>[0-3][0-9])(?!\d+)
	//
	// RE2 reformulations: `(?<!\d+)` lookbehind on airyear is replaced
	// by constraining the title's last char to non-digit
	// (`(?P<title>.+?[^\d])?` — same idiom as the title+Nx and 3/4-digit
	// shortcut rules). `(?!\d+)` after airday becomes the explicit
	// `(?:\W|$)` end-anchor — with `[0-3][0-9]` already exact-length, the
	// only thing the lookahead guards against is a 9th digit.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?[^\d])?\W*(?P<airyear>\d{4})(?P<airmonth>[0-1][0-9])(?P<airday>[0-3][0-9])(?:\W|$)`,
		),
		Build: buildDailyAirdate,
	},

	// `Parser.cs:354` — Reversed date (month-day-year),
	// `Series.Title - 04.28.2014`. Listed AFTER the standard daily +
	// 8-digit rules so canonical year-first forms win first.
	//
	// C# original:
	//
	//	^(?<title>.+?)?\W*(?<ambiguousairmonth>[0-1][0-9])[-_. ]+
	//	(?<ambiguousairday>[0-3][0-9])[-_. ]+(?<airyear>\d{4})(?!\d+)
	//
	// `(?!\d+)` lookahead becomes the consuming `(?:[^\d]|$)` end-anchor.
	// The buildReversedAirdate consumer applies the C# ambiguous-date
	// logic at `Parser.cs:1082-1095`: if both groups are ≤ 12 the date
	// is truly ambiguous and Build returns nil so the next rule can
	// try; if month > 12 (rule-A interpretation invalid) Build also
	// returns nil so rule B's day-month-year interpretation gets a
	// chance.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)?\W*(?P<ambiguousairmonth>[0-1][0-9])[-_. ]+(?P<ambiguousairday>[0-3][0-9])[-_. ]+(?P<airyear>\d{4})(?:[^\d]|$)`,
		),
		Build: buildReversedAirdate,
	},

	// `Parser.cs:358` — Reversed date (day-month-year),
	// `Series Title - 30-04-2024`. Same shape as the rule above with
	// the first 2-digit group named `<ambiguousairday>` instead of
	// `<ambiguousairmonth>`. buildReversedAirdate is symmetric and
	// reads both groups by name.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)?\W*(?P<ambiguousairday>[0-3][0-9])[-_. ]+(?P<ambiguousairmonth>[0-1][0-9])[-_. ]+(?P<airyear>\d{4})(?:[^\d]|$)`,
		),
		Build: buildReversedAirdate,
	},

	// `Parser.cs:224`-style — Mini-Series with year in title +
	// E<NN> (e.g. `Series Show.2016.E04.Power.720p...`). Listed
	// BEFORE the 3/4-digit shortcuts so the year isn't misread as
	// season+episode (without this rule, `2016.E04` would let the
	// 4-digit shortcut interpret `2016` as season=20 episode=16).
	// Title CAPTURES the year, mirroring the C# fixture's
	// `title="Series Show 2016"` expectation at MiniSeriesEpisodeParserFixture.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?[-_. ]\d{4})[-_. ]+E(?P<episode>\d{1,3})(?:[-_. ]|$)`,
		),
		Build: buildMiniSeriesYearE,
	},

	// `Parser.cs:228` — Mini-Series, treated as season 1, episodes
	// labelled as `Part 01` / `Part.1` / `Part-01`. Listed BEFORE
	// the 3/4-digit shortcuts so a `Part 01` form doesn't get
	// misclassified as season+episode. Mini-series fixture cases:
	//   `The.Big.Series.Leader.Part.2.DSR.XviD-SYS` → S1E2
	//   `Series and Show 2012 Part 1 REPACK 720p HDTV x264 2HD` → S1E1
	//
	// C# original:
	//
	//	^(?<title>.+?)(?:\W+(?:[-_. ]Part[-_. ](?<episode>\d{1,2}(?!\d+))))
	//
	// Plus `Parser.cs:224`'s mini-series-with-year variant:
	//
	//	^(?<title>.+?\W(?<year>\d{4}))\W+(?:(?:Part|Pt)[-_. ]?(?<episode>\d{1,2}(?!\d+)))
	//
	// The Go port merges both via the broader `Part|Pt` separator
	// and the unified title pattern. The `(?!\d+)` lookahead is
	// dropped — `\d{1,2}` is greedy 1-2 digits and the trailing
	// non-greedy match (or the `\W+` separator that typically
	// follows) constrains the episode count.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)[-_. \W]+(?:Part|Pt)[-_. ]?(?P<episode>\d{1,2})(?:\W|$)`,
		),
		Build: buildMiniSeriesPart,
	},

	// `Parser.cs:336` — Supports 103/113 naming. Title + 3-digit
	// episode shortcut where 1 digit is season and 2 digits are
	// episode. Listed AFTER the more-specific S-E and Nx rules so
	// the explicit `S<n>E<nn>` and `<n>x<nn>` forms win when present.
	//
	// C# original:
	//
	//	^(?<title>.+?)?(?:(?:[_.-](?<![()\[!]))+
	//	(?<season>(?<!\d+)[1-9])(?<episode>[1-9][0-9]|[0][1-9])(?![a-z]|\d+))+
	//	(?:[_.]|$)
	//
	// RE2 reformulation: the `(?<!\d+)` lookbehind on season is
	// replaced with `(?P<title>.+?[^\d])` (title's last char must be
	// non-digit, same idiom as the Nx-episode rule). The
	// `(?![a-z]|\d+)` after episode is enforced via `(?:[_.]|$)`
	// end-anchor. The C# `(?<![()\[!])` lookbehind on the separator
	// group is dropped — it's a defensive guard against a few rare
	// scene formats and isn't exercised by current fixtures.
	//
	// Known limitation: titles with embedded years like
	// `series.2009.416` won't match this rule (the lazy `.+?` can't
	// extend past the trailing `9` digit to keep the title together
	// because of the `[^\d]` constraint). Current behaviour: returns
	// nil. C# behaviour: parses as S4E16. Documented; lands in a
	// later slice with a more sophisticated title-extraction path.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?[^\d])(?:[_.\-]+(?P<season>[1-9])(?P<episode>[1-9][0-9]|[0][1-9]))+(?:[_.]|$)`,
		),
		Build: buildThreeDigitShortcut,
	},

	// `Parser.cs:273` — Supports 1103/1113 naming. Title + 4-digit
	// episode shortcut where 2 digits are season and 2 digits are
	// episode. Listed AFTER the 3-digit rule (above) — for inputs
	// where the 3-digit form fails (e.g. 1013 has 4 digits and the
	// 3-digit rule's `(?:[_.]|$)` end-anchor rejects when a 4th digit
	// follows), the 4-digit rule picks them up.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?[^\d])[_.\-]+(?P<season>\d{2})(?P<episode>\d{2})(?:[_.]|$)`,
		),
		Build: buildTitleNxEpisode,
	},

	// `Parser.cs:438` — "Extant, terrible multi-episode naming".
	// Catches `Series.10708.hdtv-lol.mp4` (5-digit `<season=1><ep=07>
	// <ep=08>`). The C# rule allows `<season>[0]?\d?` (0, 1, or 2
	// digits with leading 0); the Go port narrows to `\d{1,2}` since
	// no fixture exercises the leading-0 case differently. Listed
	// AFTER the 4-digit shortcut so single 4-digit inputs aren't
	// shadowed.
	//
	// C# original:
	//
	//	^(?<title>.+?)[-_. ](?<season>[0]?\d?)
	//	(?:(?<episode>\d{2}){2}(?!\d+))[-_. ]
	//
	// `(?<episode>\d{2}){2}` captures 4 digits as 2x2; RE2 retains
	// only the last named-group capture, so the build extracts both
	// 2-digit chunks directly from the matched span.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?[^\d])[-_. ](?P<season>\d{1,2})(?P<eppair>\d{2}\d{2})(?:[-_. ]|$)`,
		),
		Build: buildExtantMultiEpisode,
	},

	// `Parser.cs:247` — Supports `Season 01 Episode 03` keyword form,
	// including multi-episode `Episode 05-06`. Listed last because the
	// `Season<n>...Episode<n>` keyword anchor is unique to this shape
	// and won't compete with any earlier rule's prefix.
	//
	// C# original:
	//
	//	(?:.*(?:\""|^))(?<title>.*?)(?:[-_\W](?<![()\[]))+
	//	(?:\W?Season\W?)(?<season>(?<!\d+)\d{1,2}(?!\d+))(?:\W|_)+
	//	(?:Episode\W)(?:[-_. ]?(?<episode>(?<!\d+)\d{1,2}(?!\d+)))+
	//
	// RE2 reformulations:
	//
	//   - The C# leading `(?:.*(?:\""|^))` is degenerate `^` for inputs
	//     without an embedded `"`; replaced by `^`.
	//   - The C# title-to-Season separator `(?:[-_\W](?<![()\[]))+`
	//     drops parens/brackets via lookbehind; the Go port narrows the
	//     class to `[-_. ]+` (no parens/brackets to begin with). This
	//     diverges from C# when the title carries a `(2010)` year
	//     suffix bordered by parens — none of the current fixture rows
	//     exercise that combination.
	//   - The C# `(?<!\d+)` / `(?!\d+)` digit-boundary lookarounds on
	//     season and episode are replaced by `\b` word boundaries —
	//     stops `\d{1,2}` from bleeding into longer digit runs like
	//     `720p` after the keyword block.
	//   - The C# `(?:[-_. ]?(?<episode>...))+` repeated-named-group
	//     capture is split into one explicit `<episode>` first-capture
	//     plus an `<extra>` group `(?:[-_. ]\d{1,2}\b)*` for additional
	//     episode digits. The Build scans `<extra>` for digit runs and
	//     range-expands first..last to mirror C# `Enumerable.Range`.
	{
		Re: regexp.MustCompile(
			`(?i)^(?P<title>.+?)[-_. ]+\W?Season\W?(?P<season>\d{1,2})\b(?:\W|_)+Episode(?:\W|_)+(?P<episode>\d{1,2})\b(?P<extra>(?:[-_. ]\d{1,2}\b)*)`,
		),
		Build: buildSeasonEpisodeKeyword,
	},
}

// episodeNumberRe is the post-match scanner for `E\d{2,3}` /
// `\d{2,3}` runs inside an already-anchored season+episode region.
// Used by Build functions to recover ALL episode numbers (Go RE2
// `(...)*` retains only the LAST capture per named group, so the C#
// `Captures` collection's per-iteration values don't transfer
// directly). See QualityParser slice 2.10c for the equivalent
// per-pattern fixup.
var episodeNumberRe = regexp.MustCompile(`(?i)[ex](\d{1,4})|(?:[-_]\s?)(\d{1,4})`)

// findAllEpisodes scans a season+episode region for every episode
// digit run, returning the integer values in match order. Mirrors the
// C# `match.Groups["episode"].Captures` iteration in
// `Parser.ParseMatchCollection` at `:944`.
//
// Range expansion: mirrors the C# `Enumerable.Range(first, count)`
// path at `:953-959`. If two or more episodes were captured, the
// returned slice is `first..last` (inclusive), filling in any gaps
// between the first and last captured numbers. So `S01E01-E03` (which
// captures [1, 3]) returns [1, 2, 3]; `S01E91-E100` returns
// [91, 92, ..., 100]; `S01E04E05` (already contiguous, captures
// [4, 5]) returns [4, 5] unchanged. Mirrors the C# behaviour where
// the regex's per-match captures provide the endpoints and the post-
// processor fills the interior.
func findAllEpisodes(region string) []int {
	matches := episodeNumberRe.FindAllStringSubmatch(region, -1)
	if len(matches) == 0 {
		return nil
	}
	captured := make([]int, 0, len(matches))
	for _, m := range matches {
		var raw string
		switch {
		case len(m) >= 2 && m[1] != "":
			raw = m[1]
		case len(m) >= 3 && m[2] != "":
			raw = m[2]
		default:
			continue
		}
		if n, ok := ParseNumber(raw); ok {
			captured = append(captured, n)
		}
	}
	if len(captured) == 0 {
		return nil
	}
	if len(captured) == 1 {
		return captured
	}
	first, last := captured[0], captured[len(captured)-1]
	if first > last {
		return captured
	}
	expanded := make([]int, 0, last-first+1)
	for n := first; n <= last; n++ {
		expanded = append(expanded, n)
	}
	return expanded
}

// extractGroupSpan returns (text, end) of the named group, or
// ("", -1) if absent. Used by Build functions to pull out captured
// substrings + their end position for downstream "scan from here"
// passes.
func extractGroupSpan(re *regexp.Regexp, m []int, name, title string) (string, int) {
	idx := re.SubexpIndex(name)
	if idx <= 0 || 2*idx+1 >= len(m) {
		return "", -1
	}
	start, end := m[2*idx], m[2*idx+1]
	if start == -1 {
		return "", -1
	}
	return title[start:end], end
}

func buildTitleSeasonEpisode(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	seasonText, _ := extractGroupSpan(re, m, "season", title)
	season, ok := ParseNumber(seasonText)
	if !ok {
		return nil
	}

	titleText, titleEnd := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)
	if titleEnd < 0 {
		titleEnd = m[0]
	}

	// Scan the post-title portion of the match for every `E<digits>`
	// / `<sep><digits>` token. This covers both the simple
	// `S01E05` form and the `S03E01.S03E02` repeated form (where the
	// regex's `{2,}` quantifier extends m[1] past multiple SxxExx
	// segments). Scanning from titleEnd (rather than seasonEnd)
	// avoids missing episode tokens in the FIRST `SxxExx` when the
	// regex captured `season` from a LATER iteration (Go RE2 retains
	// only the last capture per named group).
	episodeRegion := title[titleEnd:m[1]]
	episodes := findAllEpisodes(episodeRegion)

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = season
	info.EpisodeNumbers = episodes
	return info
}

// buildTitleNxEpisode handles the `<Title> <N>x<NN>` form where the
// episode digits are captured directly in a named `episode` group
// (rather than recovered post-match via findAllEpisodes). Only fires
// for single episodes; multi-episode `Nx05x06` forms fall through
// to subsequent slices.
func buildTitleNxEpisode(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	seasonText, _ := extractGroupSpan(re, m, "season", title)
	season, ok := ParseNumber(seasonText)
	if !ok {
		return nil
	}
	episodeText, _ := extractGroupSpan(re, m, "episode", title)
	episode, ok := ParseNumber(episodeText)
	if !ok {
		return nil
	}

	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = season
	info.EpisodeNumbers = []int{episode}
	return info
}

func buildNoTitleEpisode(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	seasonText, seasonEnd := extractGroupSpan(re, m, "season", title)
	season, ok := ParseNumber(seasonText)
	if !ok {
		return nil
	}
	episodes := findAllEpisodes(title[seasonEnd:m[1]])

	info := NewParsedEpisodeInfo()
	info.SeasonNumber = season
	info.EpisodeNumbers = episodes
	return info
}

func buildNoTitleMultiEpisode(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	return buildNoTitleEpisode(re, m, title)
}

// buildDailyAirdate extracts an `<airyear>-<airmonth>-<airday>` date
// from a release-name match and assembles it into the C# `AirDate`
// format (`yyyy-MM-dd`, mirrors `Episode.AIR_DATE_FORMAT`). Mirrors
// the airdate path of `ParseMatchCollection` at `Parser.cs:1075-1116`.
//
// Validation: rejects matches where the title group is empty AND the
// title text after stripping leading separators is also empty (the C#
// pipeline requires a coherent title; a title-less daily form like
// `2011.01.10 - Show.mkv` still parses with an empty SeriesTitle).
// Year < 1900 also rejects (matches the C# `airYear < 1900` guard at
// `:933`).
func buildDailyAirdate(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	yearText, _ := extractGroupSpan(re, m, "airyear", title)
	year, ok := ParseNumber(yearText)
	if !ok || year < 1900 {
		return nil
	}
	monthText, _ := extractGroupSpan(re, m, "airmonth", title)
	dayText, _ := extractGroupSpan(re, m, "airday", title)

	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.AirDate = yearText + "-" + monthText + "-" + dayText
	return info
}

// buildMiniSeriesYearE extracts (title-with-year, episode) from a
// `<Title>.<Year>.E<NN>` form. season=1, IsMiniSeries=true.
func buildMiniSeriesYearE(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	episodeText, _ := extractGroupSpan(re, m, "episode", title)
	episode, ok := ParseNumber(episodeText)
	if !ok {
		return nil
	}

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = 1
	info.EpisodeNumbers = []int{episode}
	info.IsMiniSeries = true
	return info
}

// buildMiniSeriesPart extracts (title, episode) from a `<title> Part <N>`
// form and assembles a mini-series ParsedEpisodeInfo with season=1 and
// IsMiniSeries=true. Mirrors `Parser.cs:1068-1073`'s mini-series
// classification path in ParseMatchCollection.
func buildMiniSeriesPart(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	episodeText, _ := extractGroupSpan(re, m, "episode", title)
	episode, ok := ParseNumber(episodeText)
	if !ok {
		return nil
	}

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = 1
	info.EpisodeNumbers = []int{episode}
	info.IsMiniSeries = true
	return info
}

// buildAnimeAbsolute extracts the subgroup / title / absoluteepisode
// (and optional hash) from an anime [SubGroup] match. Mirrors the
// C# read of the same named groups from a `MatchCollection` element
// in `Parser.ParseMatchCollection` plus `GetSubGroup` /
// `GetReleaseHash` at `Parser.cs:1210-1239`.
func buildAnimeAbsolute(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	subgroup, _ := extractGroupSpan(re, m, "subgroup", title)
	subgroup = strings.TrimSpace(subgroup)

	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	absText, _ := extractGroupSpan(re, m, "absoluteepisode", title)
	abs, ok := ParseNumber(absText)
	if !ok {
		return nil
	}

	hashText, _ := extractGroupSpan(re, m, "hash", title)

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.AbsoluteEpisodeNumbers = []int{abs}
	info.ReleaseGroup = subgroup
	info.ReleaseHash = hashText
	return info
}

// buildExtantMultiEpisode handles the `<title>.<season><ep1><ep2>`
// 5-digit form (Parser.cs:438). The `<eppair>` group is exactly 4
// digits split into two 2-digit episodes; range-expand first..last.
func buildExtantMultiEpisode(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	seasonText, _ := extractGroupSpan(re, m, "season", title)
	season, ok := ParseNumber(seasonText)
	if !ok {
		return nil
	}
	pair, _ := extractGroupSpan(re, m, "eppair", title)
	if len(pair) != 4 {
		return nil
	}
	ep1, ok1 := ParseNumber(pair[:2])
	ep2, ok2 := ParseNumber(pair[2:])
	if !ok1 || !ok2 {
		return nil
	}

	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	var episodes []int
	if ep1 <= ep2 {
		episodes = make([]int, 0, ep2-ep1+1)
		for n := ep1; n <= ep2; n++ {
			episodes = append(episodes, n)
		}
	} else {
		episodes = []int{ep1, ep2}
	}

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = season
	info.EpisodeNumbers = episodes
	return info
}

// threeDigitShortcutRe matches one iteration of the
// `<sep><1-digit-season><2-digit-episode>` shortcut form. Used by
// buildThreeDigitShortcut to recover all season+episode pairs when
// the rule's `+` quantifier captures multiple iterations (Go RE2
// retains only the last named-group capture, so a per-iteration scan
// is required).
var threeDigitShortcutRe = regexp.MustCompile(`[_.\-]+([1-9])([1-9][0-9]|[0][1-9])`)

// buildThreeDigitShortcut handles single + multi 3-digit shortcut
// matches. The first iteration's season is the canonical season; all
// iterations contribute episodes, range-expanded first..last.
func buildThreeDigitShortcut(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	titleText, titleEnd := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)
	if titleEnd < 0 {
		titleEnd = m[0]
	}

	region := title[titleEnd:m[1]]
	matches := threeDigitShortcutRe.FindAllStringSubmatch(region, -1)
	if len(matches) == 0 {
		return nil
	}
	season, ok := ParseNumber(matches[0][1])
	if !ok {
		return nil
	}
	captured := make([]int, 0, len(matches))
	for _, mm := range matches {
		if n, ok := ParseNumber(mm[2]); ok {
			captured = append(captured, n)
		}
	}
	if len(captured) == 0 {
		return nil
	}
	var episodes []int
	if len(captured) == 1 {
		episodes = captured
	} else {
		first, last := captured[0], captured[len(captured)-1]
		if first <= last {
			episodes = make([]int, 0, last-first+1)
			for n := first; n <= last; n++ {
				episodes = append(episodes, n)
			}
		} else {
			episodes = captured
		}
	}

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = season
	info.EpisodeNumbers = episodes
	return info
}

// nxEpisodeRe matches `x<digits>` (case-insensitive) — the episode
// marker for `<n>x<m>` Nx forms. Used by `buildTitleNxMulti` so the
// episode scan doesn't pick up `-<season>` digit runs from the
// second-iteration prefix that findAllEpisodes' `[-_]\d` arm would
// otherwise capture.
var nxEpisodeRe = regexp.MustCompile(`(?i)x(\d{1,3})`)

// buildTitleNxMulti extracts (title, season, episodes) from a
// `<title> <n>x<m> - <n>x<m>` multi-Nx match. Mirrors the C# multi-Nx
// rule's behaviour where the season is captured per-iteration (only
// the last is retained by RE2) and episodes are recovered via a
// post-match scan over the matched span. Range expansion mirrors
// `findAllEpisodes`.
func buildTitleNxMulti(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	seasonText, _ := extractGroupSpan(re, m, "season", title)
	season, ok := ParseNumber(seasonText)
	if !ok {
		return nil
	}
	titleText, titleEnd := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)
	if titleEnd < 0 {
		titleEnd = m[0]
	}

	region := title[titleEnd:m[1]]
	matches := nxEpisodeRe.FindAllStringSubmatch(region, -1)
	captured := make([]int, 0, len(matches))
	for _, mm := range matches {
		if n, ok := ParseNumber(mm[1]); ok {
			captured = append(captured, n)
		}
	}
	if len(captured) == 0 {
		return nil
	}

	var episodes []int
	if len(captured) == 1 {
		episodes = captured
	} else {
		first, last := captured[0], captured[len(captured)-1]
		if first <= last {
			episodes = make([]int, 0, last-first+1)
			for n := first; n <= last; n++ {
				episodes = append(episodes, n)
			}
		} else {
			episodes = captured
		}
	}

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = season
	info.EpisodeNumbers = episodes
	return info
}

// buildReversedAirdate handles both Parser.cs:354 (month-day-year)
// and Parser.cs:358 (day-month-year) reversed-date forms. The
// `<ambiguousairmonth>` and `<ambiguousairday>` named groups are read
// regardless of regex position; the C# ambiguous-date logic at
// `Parser.cs:1082-1095` is applied verbatim:
//
//   - Both ≤ 12 → truly ambiguous → return nil so the alternate
//     interpretation rule can try.
//   - Month > 12 (or < 1) → rule-A interpretation invalid → return nil.
//   - Day > 31 (or < 1) → invalid → return nil.
//   - Otherwise → use captured values verbatim.
//
// The C# `DateTime` constructor would throw on a bogus month/day; the
// thrown exception is caught upstream and the parse falls through to
// the next rule. The Go port returns nil to achieve the same effect.
func buildReversedAirdate(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	monthText, _ := extractGroupSpan(re, m, "ambiguousairmonth", title)
	dayText, _ := extractGroupSpan(re, m, "ambiguousairday", title)
	yearText, _ := extractGroupSpan(re, m, "airyear", title)

	month, ok := ParseNumber(monthText)
	if !ok {
		return nil
	}
	day, ok := ParseNumber(dayText)
	if !ok {
		return nil
	}
	year, ok := ParseNumber(yearText)
	if !ok || year < 1900 {
		return nil
	}
	if month <= 12 && day <= 12 {
		return nil
	}
	if month < 1 || month > 12 {
		return nil
	}
	if day < 1 || day > 31 {
		return nil
	}

	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.AirDate = yearText + "-" + monthText + "-" + dayText
	return info
}

// keywordExtraDigitsRe extracts trailing `\d{1,2}` runs from the
// `<extra>` group emitted by the `Season N Episode M(-K-…)` rule.
// `\b`-bordered to avoid bleeding into longer digit runs.
var keywordExtraDigitsRe = regexp.MustCompile(`\d{1,2}\b`)

// buildSeasonEpisodeKeyword extracts (title, season, episodes) from a
// `Season <n> Episode <m>(-<k>-…)` keyword match. The first episode
// comes from the explicit `<episode>` group; additional episodes come
// from scanning the `<extra>` group for `\d{1,2}\b` runs. Range
// expansion mirrors `findAllEpisodes`.
func buildSeasonEpisodeKeyword(re *regexp.Regexp, m []int, title string) *ParsedEpisodeInfo {
	seasonText, _ := extractGroupSpan(re, m, "season", title)
	season, ok := ParseNumber(seasonText)
	if !ok {
		return nil
	}
	episodeText, _ := extractGroupSpan(re, m, "episode", title)
	firstEp, ok := ParseNumber(episodeText)
	if !ok {
		return nil
	}

	titleText, _ := extractGroupSpan(re, m, "title", title)
	titleText = strings.ReplaceAll(strings.ReplaceAll(titleText, ".", " "), "_", " ")
	titleText = strings.TrimSpace(titleText)

	captured := []int{firstEp}
	extraText, _ := extractGroupSpan(re, m, "extra", title)
	for _, raw := range keywordExtraDigitsRe.FindAllString(extraText, -1) {
		if n, ok := ParseNumber(raw); ok {
			captured = append(captured, n)
		}
	}

	var episodes []int
	if len(captured) == 1 {
		episodes = captured
	} else {
		first, last := captured[0], captured[len(captured)-1]
		if first <= last {
			episodes = make([]int, 0, last-first+1)
			for n := first; n <= last; n++ {
				episodes = append(episodes, n)
			}
		} else {
			episodes = captured
		}
	}

	info := NewParsedEpisodeInfo()
	info.SeriesTitle = titleText
	info.SeasonNumber = season
	info.EpisodeNumbers = episodes
	return info
}
