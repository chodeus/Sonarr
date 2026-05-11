package metadatasource

import (
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/series"
)

// comparator.go ports `SearchSeriesComparer : IComparer<Series>` at
// `src/NzbDrone.Core/MetadataSource/SearchSeriesComparer.cs:9-144`.
//
// Slice 4.AS (queue [M2] of Phase 4 focused-subset 5 — METADATA-SOURCES).
//
// The comparator orders a slice of search-result series so the
// "best match for the user's query" sorts first. The C# tree's
// IComparer<T>.Compare returns negative when x should sort before y;
// the Go port follows the same convention via [Comparator.Compare]
// which is wired into `slices.SortStableFunc` (or `sort.Slice` /
// `sort.SliceStable`) by the call-site.
//
// Six successive arms — each one returns the ordering as soon as it
// finds a difference between x and y; only if every arm ties does the
// final weighted-Levenshtein arm take over.
//
//  1. Exact match after punctuation-strip (cleanPunctuation: drop
//     `-._:` chars, lowercase). The first arm — exact matches sort
//     to the very top.
//
//  2. Article-stripped match: `^(a|an|the)\s` removed from the title
//     before lowercase + trim. Promotes "The Blacklist" / "Blacklist"
//     to the top when the user searched for either form.
//
//  3. Levenshtein-on-cleanPunctuation ≤ 1. Tolerates a single-character
//     typo on either side.
//
//  4. Year-aware Levenshtein-on-cleanTitle ≤ 1. The cleanTitle helper
//     additionally strips ` (US)` / ` (2010)` / ` (US) (2010)` suffixes
//     so "Battlestar Galactica (2003)" matches a search for
//     "Battlestar Galactica 2003" with zero edit distance after the
//     year is consumed by the SearchQuery's parsed-year mechanism.
//     Year-aware tie-breaker — when both x and y match the predicate,
//     prefer the one whose .Year equals the query's year (or the
//     numerically lower one if the query has no year hint).
//
//  5. Year-aware prefix-with-colon: `s.Title.StartsWith(query + ":")`.
//     Captures the "CSI: Miami" / "CSI: NY" pattern — both prefix-match
//     "CSI" and the year-aware step disambiguates.
//
//  6. Weighted Levenshtein-with-year-proximity: the fallback ordering
//     for everything that didn't match any of the early arms. Lower
//     LevenshteinDistanceClean(query, title) wins, with a year-proximity
//     bonus of 20 (same year), 10 (off-by-one), 0 (otherwise) subtracted
//     from the distance. Smaller-after-bonus sorts first.
//
// The C# code uses a single instance of the comparator per Sort call;
// the Go port mirrors that — construct once with [NewComparator],
// pass [Comparator.Compare] to `slices.SortStableFunc`.
//
// Goroutine-safety: a Comparator value is read-only after construction
// (the regexes are package-level vars, the parsed query/year are
// captured fields). Safe to share across goroutines, but a fresh
// Comparator per call-site is cheap and avoids accidental sharing of
// state if the C# tree later adds mutable scratch fields.

var (
	// reCleanPunctuation strips the four punctuation chars C# replaces
	// with empty in `RegexCleanPunctuation` at `:11`.
	reCleanPunctuation = regexp.MustCompile(`[-._:]`)

	// reCleanCountryYearPostfix strips trailing ` (XX)` / ` (YYYY)` /
	// ` (XX) (YYYY)` postfixes per `RegexCleanCountryYearPostfix` at
	// `:12`. The C# regex uses `(?<=.+)` to require at least one char
	// before the postfix — the Go regexp port does the same via a
	// non-empty prefix capture group. RE2 doesn't support lookbehind
	// so the prefix-must-exist requirement is enforced via the
	// `.+` capture and a length check in [cleanTitle].
	reCleanCountryYearPostfix = regexp.MustCompile(`^(.+?)( \([A-Z]{2}\)| \(\d{4}\)| \([A-Z]{2}\) \(\d{4}\))$`)

	// reArticle strips the leading `a /an / the ` article. C#
	// `ArticleRegex` at `:13` uses IgnoreCase; the Go port lowercases
	// the input first so a case-insensitive literal regex covers the
	// same input.
	reArticle = regexp.MustCompile(`(?i)^(a|an|the)\s`)

	// reQueryYear extracts an optional trailing year from the search
	// query. Two acceptable forms: ` 2003` (bare) or ` (2003)`
	// (parenthesised). C# `:24` uses
	// `^(?<query>.+)\s+(?:\((?<year>\d{4})\)|(?<year>\d{4}))$`. The
	// Go port uses a single named capture group — RE2 supports
	// duplicate group names but it's clearer to use one group via
	// the alternation.
	reQueryYear = regexp.MustCompile(`^(.+)\s+(?:\((\d{4})\)|(\d{4}))$`)
)

// Comparator wraps the parsed search-query state — the lowercase
// query stripped of its parsed year (if any), and the parsed year
// itself when present.
//
// Construct via [NewComparator], call [Comparator.Compare] from a
// `slices.SortStableFunc` or similar.
type Comparator struct {
	// SearchQuery is the original query string as the caller passed
	// it. Kept verbatim because arm 1 (exact-match) compares the
	// cleaned-punctuation versions of both the original and the
	// candidate.
	SearchQuery string

	// searchQueryWithoutYear is the query lowered + with any parsed
	// year suffix stripped. Used by arms 4 + 5 (the year-aware
	// matchers) and arm 6 (the fallback weighted Levenshtein).
	searchQueryWithoutYear string

	// year holds the parsed query year when [reQueryYear] matched the
	// query, or 0 when no year was supplied. The 0-vs-set distinction
	// is preserved via the [yearKnown] flag; we don't use *int because
	// the comparator path is hot enough that a pointer chase per
	// CompareWithYear call is needless overhead.
	year      int
	yearKnown bool
}

// NewComparator parses `searchQuery` and returns a comparator ready to
// drive `slices.SortStableFunc`. nil-safe: empty searchQuery is
// accepted and yields a Comparator that treats every candidate equally
// in arms 1-5 and falls through to arm 6's Levenshtein-only ordering
// (which becomes a "longest-distance-loses" stable sort).
//
// C# parity: the constructor mirrors `SearchSeriesComparer(string
// searchQuery)` at `:20-34`.
func NewComparator(searchQuery string) *Comparator {
	c := &Comparator{SearchQuery: searchQuery}
	if m := reQueryYear.FindStringSubmatch(searchQuery); m != nil {
		c.searchQueryWithoutYear = strings.ToLower(m[1])
		yearStr := m[2]
		if yearStr == "" {
			yearStr = m[3]
		}
		if y, err := strconv.Atoi(yearStr); err == nil {
			c.year = y
			c.yearKnown = true
		}
	} else {
		c.searchQueryWithoutYear = strings.ToLower(searchQuery)
	}
	return c
}

// Compare returns a negative number if x should sort before y, zero
// if they tie under every arm, and a positive number if x should
// sort after y. Designed to be passed to
// `slices.SortStableFunc(series, cmp.Compare)`.
//
// nil-safety: nil inputs are treated as "neither matches anything"
// — both arms 1-5 evaluate their predicates as false (the underlying
// helpers nil-tolerate via empty-string fallback), so a nil entry
// sorts after a non-nil entry through arm 6's Levenshtein(nil-title)
// = Levenshtein("") which produces a large distance.
//
// Faithfulness note — the C# Compare body negates the early-arm
// results (`return -result`) because IComparer<T>.Compare returns
// `<0` when `x` should sort before `y`. The Go port follows the same
// negation pattern so the underlying boolean comparisons read the
// same as C# (`true > false` → `key.CompareTo` returns positive,
// negation flips to negative = sort first).
func (c *Comparator) Compare(x, y *series.Series) int {
	xTitle := titleOf(x)
	yTitle := titleOf(y)

	// Arm 1 — exact match after punctuation-strip.
	cleanedQuery := cleanPunctuation(c.SearchQuery)
	if r := compareBool(
		cleanPunctuation(xTitle) == cleanedQuery,
		cleanPunctuation(yTitle) == cleanedQuery,
	); r != 0 {
		return -r
	}

	// Arm 2 — article-stripped match.
	cleanArticleQuery := cleanArticles(c.SearchQuery)
	if r := compareBool(
		cleanArticles(xTitle) == cleanArticleQuery,
		cleanArticles(yTitle) == cleanArticleQuery,
	); r != 0 {
		return -r
	}

	// Arm 3 — Levenshtein ≤ 1 on cleanPunctuation.
	if r := compareBool(
		levenshteinDistance(cleanPunctuation(xTitle), cleanedQuery, 1, 1, 1) <= 1,
		levenshteinDistance(cleanPunctuation(yTitle), cleanedQuery, 1, 1, 1) <= 1,
	); r != 0 {
		return -r
	}

	// Arm 4 — year-aware Levenshtein ≤ 1 on cleanTitle.
	if r := c.compareWithYear(x, y,
		levenshteinCleanTitleMatcher(c.searchQueryWithoutYear),
	); r != 0 {
		return -r
	}

	// Arm 5 — year-aware "Title:" prefix.
	if r := c.compareWithYear(x, y,
		titlePrefixWithColonMatcher(c.searchQueryWithoutYear),
	); r != 0 {
		return -r
	}

	// Arm 6 — weighted Levenshtein with year proximity bonus. The
	// raw Compare is NOT negated (a lower distance sorts first
	// naturally — `keyX.CompareTo(keyY)` already returns negative
	// when keyX < keyY).
	return compareInt(
		levenshteinDistanceClean(strings.ToLower(c.SearchQuery), strings.ToLower(xTitle))-c.yearFactor(x),
		levenshteinDistanceClean(strings.ToLower(c.SearchQuery), strings.ToLower(yTitle))-c.yearFactor(y),
	)
}

// compareWithYear ports `CompareWithYear` at `:87-107`. Evaluates
// `canMatch(x)` and `canMatch(y)`:
//
//   - if both match: prefer the one whose Year equals the parsed
//     query year (if any); otherwise prefer the lower Year value.
//   - if exactly one matches: that one sorts first.
//   - if neither matches: tie.
//
// Returns the result the OUTER Compare body negates with `-result`
// (the inner-when-both-match arm follows the same convention as the
// inner negation pattern documented at [Comparator.Compare]).
func (c *Comparator) compareWithYear(x, y *series.Series, canMatch func(*series.Series) bool) int {
	matchX := canMatch(x)
	matchY := canMatch(y)

	if matchX && matchY {
		if c.yearKnown {
			r := compareBool(yearOf(x) == c.year, yearOf(y) == c.year)
			if r != 0 {
				return r
			}
		}
		return compareInt(yearOf(x), yearOf(y))
	}

	return compareBool(matchX, matchY)
}

// yearFactor ports `GetYearFactor` at `:131-143`. Returns 20 when the
// series Year exactly matches the parsed query year; 10 when off by
// one; 0 otherwise. The arm-6 fallback subtracts this from the
// Levenshtein distance so candidates with closer year alignment sort
// ahead of equidistant edit-cost peers.
func (c *Comparator) yearFactor(s *series.Series) int {
	if !c.yearKnown {
		return 0
	}
	yr := yearOf(s)
	offset := yr - c.year
	if offset < 0 {
		offset = -offset
	}
	if offset <= 1 {
		return 20 - (10 * offset)
	}
	return 0
}

// titleOf returns the Series title with nil-safety. A nil Series is
// rendered as the empty string — the comparator's downstream arms
// treat that as "matches nothing" naturally.
func titleOf(s *series.Series) string {
	if s == nil {
		return ""
	}
	return s.Title
}

// yearOf returns the dereferenced Series.Year (or 0 when nil).
// C# parity — C# `Series.Year` is a non-nullable int defaulting to 0;
// the Go-port persists it as *int (DB column is nullable) so we
// flatten nil to 0 here for comparison purposes. Practical impact:
// when both candidates have nil Year, both compare as 0 and tie.
func yearOf(s *series.Series) int {
	if s == nil || s.Year == 0 {
		return 0
	}
	return s.Year
}

// cleanPunctuation ports the eponymous helper at `:109-114`. Strips
// `-._:` chars and lowercases.
func cleanPunctuation(title string) string {
	return strings.ToLower(reCleanPunctuation.ReplaceAllString(title, ""))
}

// cleanTitle ports `:116-122`. Strips punctuation + the
// country/year postfix and lowercases. RE2 doesn't support
// lookbehind so the C# `(?<=.+)` is enforced via the `^(.+?)` capture
// — at decoded-output time we just drop the suffix when the regex
// matched. The "must have at least one character before the suffix"
// requirement is naturally satisfied because `.+?` requires ≥ 1 char.
func cleanTitle(title string) string {
	title = reCleanPunctuation.ReplaceAllString(title, "")
	if m := reCleanCountryYearPostfix.FindStringSubmatch(title); m != nil {
		title = m[1]
	}
	return strings.ToLower(title)
}

// cleanArticles ports `:124-129`. Strips the leading
// `(a|an|the)\s` article and lowercases + trims. The C# code does
// `Trim().ToLowerInvariant()` — Go's strings.TrimSpace handles all
// whitespace classes Trim does.
func cleanArticles(title string) string {
	title = reArticle.ReplaceAllString(title, "")
	return strings.ToLower(strings.TrimSpace(title))
}

// levenshteinCleanTitleMatcher returns the arm-4 predicate
// `s => CleanTitle(s.Title).LevenshteinDistance(_searchQueryWithoutYear) <= 1`.
// Closure-captures the per-Comparator query state so the arm body
// stays compact.
func levenshteinCleanTitleMatcher(queryWithoutYear string) func(*series.Series) bool {
	return func(s *series.Series) bool {
		return levenshteinDistance(cleanTitle(titleOf(s)), queryWithoutYear, 1, 1, 1) <= 1
	}
}

// titlePrefixWithColonMatcher returns the arm-5 predicate
// `s => s.Title.ToLowerInvariant().StartsWith(_searchQueryWithoutYear + ":")`.
func titlePrefixWithColonMatcher(queryWithoutYear string) func(*series.Series) bool {
	prefix := queryWithoutYear + ":"
	return func(s *series.Series) bool {
		return strings.HasPrefix(strings.ToLower(titleOf(s)), prefix)
	}
}

// compareBool returns -1 / 0 / +1 for false<false / equal / true>false.
// Mirrors C# `bool.CompareTo(bool)` semantics which the comparator's
// boolean-predicate arms rely on. The OUTER Compare body wraps this
// in `-result` so the "matches predicate" candidate sorts first.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

// compareInt returns -1 / 0 / +1 — the standard 3-way int compare.
func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
