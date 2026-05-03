package parser

import (
	"testing"
)

// White-box tests for the ParseTitle support helpers.
//
// These pin the behaviour of the small standalone functions ported from
// `NzbDrone.Core.Parser.Parser` — ValidateBeforeParsing,
// HasMultipleLanguages, NormalizeImdbID, ParseNumber, ParseDecimal,
// convertToNumerals — independently of the (still-unported)
// 122-element ReportTitleRegex array and ParseTitle pipeline.

func TestValidateBeforeParsing(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		// Plain title — accepts.
		{"Series.S01E01.720p.HDTV.x264-LOL", true},
		// `password` + `yenc` — rejects.
		{"some-password-yenc-thing", false},
		{"PASSWORD-YENC-FOO", false},
		// `password` alone — accepts (only the both-present combo rejects).
		{"password thing", true},
		// `yenc` alone — accepts.
		{"yenc thing", true},
		// No letter or digit — rejects.
		{"...---...", false},
		{"", false},
		// Hashed releases — reject (each entry exercises one
		// rejectHashedReleasesRegexes pattern).
		{"abc1234567890123456789012345678901", false}, // 32-char mixed
		{"123", false}, // bare 123
		{"abc", false}, // bare abc (case-insens)
		{"ABC", false},
		{"abc-xyz", false},
		{"b00bs", false},
		{"170424_26", false},
		{"Backup_12345S01-02", false},
		// Season-folder — rejects.
		{"Season 1", false},
		{"Season_2", false},
		{"Specials", false},
		// Season-folder with extra content — accepts.
		{"Season 1 Stuff", true},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := ValidateBeforeParsing(tc.title)
			if got != tc.want {
				t.Errorf("ValidateBeforeParsing(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

func TestHasMultipleLanguages(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"Series.S01.MULTI.1080p.WEB-DL", true},
		{"Series.S01.[multi].1080p.WEB-DL", true},
		{"Series.S01.multilang.1080p.WEB-DL", true},
		{"Series.S01.multilanguage.1080p.WEB-DL", true},
		{"Series.S01-multi-x264", true},
		{"Series_S01_multi_x264", true},
		// Negatives.
		{"Series.S01.1080p.WEB-DL", false},
		{"Multimedia.S01.x264", false}, // mid-word, no surrounding sep
		{"Series.S01.SinglE.x264", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := HasMultipleLanguages(tc.title)
			if got != tc.want {
				t.Errorf("HasMultipleLanguages(%q) = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

func TestNormalizeImdbID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 7-digit bare → tt-prefixed.
		{"1234567", "tt1234567"},
		// 6-digit bare → padded to 7.
		{"123456", "tt0123456"},
		// 1-digit bare → must satisfy len > 2 OR Length > 2 — so "1"
		// returns "" because length ≤ 2.
		{"1", ""},
		// 3-digit bare → padded to 7 (length > 2 passes the guard).
		{"123", "tt0000123"},
		// tt-prefixed already → trimmed and re-padded.
		{"tt0123456", "tt0123456"},
		{"tt1234567", "tt1234567"},
		{"tt12", "tt0000012"},
		// Invalid — too long.
		{"12345678901", ""},
		{"tt12345678901", ""},
		// Invalid — non-digits.
		{"abc1234", ""},
		{"tt12abc", ""},
		// Empty.
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := NormalizeImdbID(tc.in)
			if got != tc.want {
				t.Errorf("NormalizeImdbID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		// ASCII digits.
		{"5", 5, true},
		{"42", 42, true},
		{"100", 100, true},
		{"0", 0, true},
		// English number words (case-insensitive).
		{"zero", 0, true},
		{"one", 1, true},
		{"FIVE", 5, true},
		{"Nine", 9, true},
		// Full-width digit (U+FF10..U+FF19).
		{"５", 5, true},   // ５
		{"２３", 23, true}, // ２３
		// Invalid.
		{"abc", 0, false},
		{"ten", 0, false}, // beyond zero-nine range
		{"", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := ParseNumber(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("ParseNumber(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestParseDecimal(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"5", 5.0, true},
		{"5.5", 5.5, true},
		{"0.25", 0.25, true},
		// Full-width digits.
		{"５.５", 5.5, true},
		// Invalid.
		{"abc", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := ParseDecimal(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("ParseDecimal(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestConvertToNumerals(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// ASCII passthrough.
		{"abc", "abc"},
		{"123", "123"},
		// Full-width digits → ASCII digits.
		{"２３", "23"},
		{"S０１E０５", "S01E05"},
		// Mixed content.
		{"a５b", "a5b"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := convertToNumerals(tc.in)
			if got != tc.want {
				t.Errorf("convertToNumerals(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
