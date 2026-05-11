package metadatasource

import (
	"strings"
	"testing"
)

// TestLevenshteinDistance ports the 12-case
// `LevenshteinDistanceFixture.LevenshteinDistance` at
// `src/NzbDrone.Common.Test/LevenshteinDistanceFixture.cs:11-26`.
//
// Each case verifies the default-cost Levenshtein (1/1/1) returns the
// expected edit distance. The "Agents Of SHIELD" cases stress mixed-
// case + punctuation handling — important because the comparator's
// final tie-breaker depends on these distances matching the C# tree
// down to the integer.
func TestLevenshteinDistance(t *testing.T) {
	cases := []struct {
		text  string
		other string
		want  int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abcd", 1},
		{"abcd", "abc", 1},
		{"abc", "abd", 1},
		{"abc", "adc", 1},
		{"abcdefgh", "abcghdef", 4},
		{"a.b.c.", "abc", 3},
		{"Agents Of SHIELD", "Marvel's Agents Of S.H.I.E.L.D.", 15},
		{"Agents of cracked", "Agents of shield", 6},
		{"ABCxxx", "ABC1xx", 1},
		{"ABC1xx", "ABCxxx", 1},
	}
	for _, tc := range cases {
		t.Run(tc.text+"_vs_"+tc.other, func(t *testing.T) {
			got := levenshteinDistance(tc.text, tc.other, 1, 1, 1)
			if got != tc.want {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tc.text, tc.other, got, tc.want)
			}
		})
	}
}

// TestLevenshteinDistanceClean ports the 12-case
// `LevenshteinDistanceFixture.LevenshteinDistanceClean` at
// `:28-44`. The C# fixture pre-lowercases both inputs in the test
// itself (`text.ToLower().LevenshteinDistanceClean(other.ToLower())`)
// — the Go port follows the same convention to keep the table aligned
// with the C# source.
//
// The (1, 3, 3) edit costs make this asymmetric — "the shield" vs
// "Agents of shield" costs 24 because deleting the "Agents of "
// prefix is expensive, whereas "Marvel's Agents Of S.H.I.E.L.D."
// vs "Agents of shield" costs only 9 because the long-form version
// pays the insertion cost (1 per inserted char) instead of the
// deletion cost (3 per deleted char).
func TestLevenshteinDistanceClean(t *testing.T) {
	cases := []struct {
		text  string
		other string
		want  int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abcd", 1},
		{"abcd", "abc", 3},
		{"abc", "abd", 3},
		{"abc", "adc", 3},
		{"abcdefgh", "abcghdef", 8},
		{"a.b.c.", "abc", 0},
		{"Agents of shield", "Marvel's Agents Of S.H.I.E.L.D.", 9},
		{"Agents of shield", "Agents of cracked", 14},
		{"Agents of shield", "the shield", 24},
		{"ABCxxx", "ABC1xx", 3},
		{"ABC1xx", "ABCxxx", 3},
	}
	for _, tc := range cases {
		t.Run(tc.text+"_vs_"+tc.other, func(t *testing.T) {
			got := levenshteinDistanceClean(strings.ToLower(tc.text), strings.ToLower(tc.other))
			if got != tc.want {
				t.Errorf("levenshteinDistanceClean(%q, %q) = %d, want %d", tc.text, tc.other, got, tc.want)
			}
		})
	}
}

// TestLevenshteinDistance_EdgeCases covers the early-return paths and
// the rune-aware path. None of these are in the C# fixture — they're
// Go-port safety nets so the rune iteration in [levenshteinDistance]
// stays sound for non-ASCII input.
func TestLevenshteinDistance_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{"empty_vs_empty", "", "", 0},
		{"empty_vs_nonempty_inserts", "", "abc", 3},
		{"nonempty_vs_empty_deletes", "abc", "", 3},
		{"identical", "hello", "hello", 0},
		{"single_char_diff", "a", "b", 1},
		{"unicode_smile", "café", "cafe", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := levenshteinDistance(tc.a, tc.b, 1, 1, 1)
			if got != tc.want {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestStripDots covers the literal '.' replace.
func TestStripDots(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"a.b.c.", "abc"},
		{"...", ""},
		{"S.H.I.E.L.D.", "SHIELD"},
		{"no dots here", "no dots here"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := stripDots(tc.in)
			if got != tc.want {
				t.Errorf("stripDots(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestToLowerASCII confirms ASCII upper-cases fold and non-ASCII
// passes through unchanged (the documented divergence at the helper).
func TestToLowerASCII(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ABC", "abc"},
		{"abc", "abc"},
		{"AbC123", "abc123"},
		{"", ""},
		{"already-lower", "already-lower"},
		// Non-ASCII Á / É / Ü pass through (BMP-only-parity note at
		// [toLowerASCII] documents this divergence from C#'s
		// ToLowerInvariant).
		{"Café", "café"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := toLowerASCII(tc.in)
			if got != tc.want {
				t.Errorf("toLowerASCII(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
