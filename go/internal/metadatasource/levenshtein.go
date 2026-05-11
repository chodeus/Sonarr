package metadatasource

// levenshtein.go ports `NzbDrone.Common.Extensions.LevenstheinExtensions`
// at `src/NzbDrone.Common/Extensions/LevenstheinExtensions.cs:1-61`.
//
// (The C# filename spells it "Levensthein" — sic. The Go port uses
// the correct spelling "levenshtein" for identifiers since we're
// not exposing them through any wire format that has to round-trip
// against the C# typo. The doc-comments cite the C# source path
// verbatim so the cross-reference still works.)
//
// Helpers live in this package (rather than a shared
// internal/textutil) because slice 4.AS is the only current consumer
// — the SearchSeriesComparer at C# `MetadataSource/SearchSeriesComparer.cs`.
// The C# tree's other Levenshtein call-sites
// (SeriesFolderAsRootFolderValidator at Sonarr.Api.V{3,5}/Series/...)
// are Phase-5 surfaces and HARD-GATED for now; when Phase 5 lands,
// these helpers can be promoted to an internal/textutil package if
// more than one Phase-5 module needs them.

// levenshteinDistance ports `LevenshteinDistance(this string text,
// string other, int costInsert = 1, int costDelete = 1, int
// costSubstitute = 1)` at C# `:7-51`.
//
// Standard 1-row dynamic-programming Levenshtein with parameterised
// edit costs. Returns the minimum total edit cost to transform `text`
// into `other` using the supplied unit costs for insertion / deletion
// / substitution.
//
// Algorithm faithfulness — the Go port walks code points (runes), not
// raw bytes. C# iterates `text[i]` and `other[j]` as char (UTF-16 code
// unit), which matches rune-indexing exactly for the Basic Multilingual
// Plane (U+0000-U+FFFF). Supplementary characters (emoji, rare CJK)
// would be counted as 2 code units on C# vs 1 rune on Go — a documented
// BMP-only-parity divergence. Practical impact: zero, since series
// titles SkyHook returns are dominated by Latin/CJK BMP characters and
// the comparator's downstream caller lowercases + strips punctuation
// before reaching this point.
//
// Returns 0 when the strings are equal (fast-path matches C# `:9-12`).
//
//nolint:unparam // costInsert preserved for C# API parity at LevenstheinExtensions.cs:7. Current callers all pass 1, but the C# tree's SeriesFolderAsRootFolderValidator (Phase-5, HARD-GATED) may pass a different value — keeping the signature signed-off matches the documented family port.
func levenshteinDistance(text, other string, costInsert, costDelete, costSubstitute int) int {
	if text == other {
		return 0
	}

	t := []rune(text)
	o := []rune(other)

	if len(t) == 0 {
		return len(o) * costInsert
	}
	if len(o) == 0 {
		return len(t) * costDelete
	}

	// matrix is the single-row DP buffer the C# port uses (one row of
	// the conceptual `(text.Length+1) × (other.Length+1)` Levenshtein
	// matrix). Index 0 is the "no characters of text consumed yet"
	// column; index j holds the running cost for the j-character prefix
	// of `other`.
	matrix := make([]int, len(o)+1)
	for i := 1; i < len(matrix); i++ {
		matrix[i] = i * costInsert
	}

	for i := 0; i < len(t); i++ {
		topLeft := matrix[0]
		matrix[0] += costDelete

		for j := 0; j < len(o); j++ {
			top := matrix[j]
			left := matrix[j+1]

			sumIns := top + costInsert
			sumDel := left + costDelete
			subCost := costSubstitute
			if t[i] == o[j] {
				subCost = 0
			}
			sumSub := topLeft + subCost

			topLeft = matrix[j+1]
			matrix[j+1] = minInt3(sumIns, sumDel, sumSub)
		}
	}

	return matrix[len(o)]
}

// levenshteinDistanceClean ports `LevenshteinDistanceClean(this string
// expected, string other)` at C# `:53-59`.
//
// Pre-cleans both inputs by lowercasing + dropping `.` characters,
// then runs Levenshtein with the weighted edit costs (1, 3, 3) the
// comparator uses for the year-aware match-quality tie-breaker. The
// asymmetric costs penalise deletion / substitution far more than
// insertion, which encodes the heuristic "long-form titles like
// 'Marvel's Agents Of S.H.I.E.L.D.' should match short queries like
// 'Agents of shield' but not the other way around".
//
// C# `Replace(".", "")` is a literal string replace — the Go port
// uses byte-wise dot-stripping via [stripDots] which produces identical
// output for ASCII / BMP input.
func levenshteinDistanceClean(expected, other string) int {
	return levenshteinDistance(
		stripDots(toLowerASCII(expected)),
		stripDots(toLowerASCII(other)),
		1, 3, 3,
	)
}

// toLowerASCII lower-cases an ASCII string in-place. The C# port uses
// `string.ToLower()` which on .NET defaults to ToLower(CurrentCulture)
// for chained Replace() — for the BMP+ASCII titles SkyHook returns this
// is equivalent to a fast ASCII fold. The Go port uses an ASCII-only
// fold because the only callers feed it lowercase strings that have
// already passed through `strings.ToLowerInvariant`-equivalent code
// upstream (SearchQuery construction in [Comparator]).
//
// Documented divergence: characters outside ASCII pass through
// unchanged. The C# tree would have applied culture-dependent casing
// rules; in practice the cleanTitle / cleanPunctuation upstream filter
// strips most non-ASCII characters before this reaches the comparator.
func toLowerASCII(s string) string {
	var changed bool
	buf := []byte(s)
	for i, b := range buf {
		if b >= 'A' && b <= 'Z' {
			buf[i] = b + ('a' - 'A')
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(buf)
}

// stripDots removes every '.' byte. C# parity for `Replace(".", "")`.
func stripDots(s string) string {
	if !hasByte(s, '.') {
		return s
	}
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			continue
		}
		buf = append(buf, s[i])
	}
	return string(buf)
}

func hasByte(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

func minInt3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
