// Package parser ports `NzbDrone.Core.Parser` — Sonarr's release-title
// parser. The C# parser is the single most regex-heavy subsystem in
// the codebase (185 active regex literals per
// analysis/parser/regex-corpus.md, 77% of which use RE2-incompatible
// features). The Go port rewrites each non-RE2 pattern into RE2-
// compatible form rather than vendor a Perl-compatible regex engine —
// the corpus document worked out that all 143 non-RE2 patterns are
// mechanically rewritable.
//
// Phase 2 lands the parser in slices, smallest-first, each gated on
// porting the corresponding *Fixture.cs as a table-driven Go test.
// This file ships the diacritics-removal helper that the title
// normalisers below build on.
//
// # Diacritic removal vs NFKD
//
// The C# tree calls `Diacritical.StringExtensions.RemoveDiacritics`
// (NuGet `Diacritical`), which does NFKD normalisation + combining-
// mark strip PLUS a hand-curated lookup table for Latin extension
// characters that don't decompose under NFKD: `ð → d`, `þ → th`,
// `æ → ae`, `ø → o`, `ß → ss`, `ł → l`, `đ → d`, `œ → oe`. The
// `NormalizeSeriesTitleFixture` test `Ríkið` → `rikid` exercises
// the `ð → d` mapping; without the lookup table, the bare NFKD
// pass would leave `ð` intact.
//
// Why this lives in `internal/parser` and not in `internal/auth`
// (which has its own narrower stripDiacritics for cookie names): the
// auth path's `[a-zA-Z0-9]` filter already drops any non-ASCII the
// NFKD pass leaves behind, so a cookie name derived from "Ríkið"
// converges to "Rikid" via different code paths. The parser needs
// the resulting string to MATCH what an existing user database
// stored, byte-for-byte — so the Diacritical lookup table is
// load-bearing here.
package parser

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// diacriticReplacer maps Latin extension characters that don't
// decompose under NFKD onto their ASCII equivalents. Sourced from the
// Diacritical NuGet package's transliteration table; extended only
// when a new test fixture surfaces a missing mapping (avoid creep
// per CONVERSION.md). Order matters for two-character expansions
// (e.g. `ß → ss`) since strings.NewReplacer matches in order.
var diacriticReplacer = strings.NewReplacer(
	"ß", "ss",
	"ẞ", "SS",
	"æ", "ae",
	"Æ", "AE",
	"œ", "oe",
	"Œ", "OE",
	"ð", "d",
	"Ð", "D",
	"đ", "d",
	"Đ", "D",
	"ø", "o",
	"Ø", "O",
	"þ", "th",
	"Þ", "Th",
	"ł", "l",
	"Ł", "L",
)

// RemoveDiacritics returns s with diacritics stripped, matching the
// C# `Diacritical.StringExtensions.RemoveDiacritics` shape used
// across `Parser.cs`. Two-stage process:
//
//  1. NFKD-normalise and drop combining marks. Handles every
//     Unicode character that decomposes under NFKD (é, à, í, ç, ä,
//     and so on).
//  2. Apply the diacriticReplacer for Latin extension characters
//     that don't decompose (ð, þ, æ, ø, ß, ł, đ, œ).
//
// The output is callable-safe: empty in → empty out; ASCII in →
// identical out.
func RemoveDiacritics(s string) string {
	if s == "" {
		return s
	}
	// Stage 1: NFKD + mark strip.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(s) {
		if unicode.IsMark(r) {
			continue
		}
		b.WriteRune(r)
	}
	// Stage 2: lookup-table replacement for non-decomposing chars.
	return diacriticReplacer.Replace(b.String())
}
