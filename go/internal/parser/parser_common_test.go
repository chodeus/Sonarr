package parser_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

// Black-box tests for the ParserCommon helpers + RemoveFileExtension.
//
// The C# `WebsitePrefixRegex` / `WebsitePostfixRegex` /
// `CleanTorrentSuffixRegex` / `RemoveFileExtension` are exercised
// indirectly through ReleaseGroupParser fixture rows in the C# tree;
// the targeted unit tests below pin each helper's input/output
// contract independently.

func TestApplyWebsitePrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Plain torrent-site prefix is stripped.
		{"[ www.Torrenting.com ] - Series.S03E14.720p.HDTV.X264-DIMENSION", "Series.S03E14.720p.HDTV.X264-DIMENSION"},
		// Bracketed www. prefix.
		{"[www.example.com] - Foo.S01E02.720p.WEB-DL", "Foo.S01E02.720p.WEB-DL"},
		// Plain double-separator form.
		{"www.example.org -- Foo.S01E02", "Foo.S01E02"},
		// Naruto-Kun must NOT be stripped (the `(?<!Naruto-Kun\.)` lookbehind).
		{"Naruto-Kun.tv Foo.S01E02.720p", "Naruto-Kun.tv Foo.S01E02.720p"},
		// Mid-string website-shaped tokens are preserved (the regex is `^`-anchored).
		{"Series.S01E02.720p.www.tracker.com.mkv", "Series.S01E02.720p.www.tracker.com.mkv"},
		// No website prefix → unchanged.
		{"Series.S01E02.720p.HDTV.x264-LOL", "Series.S01E02.720p.HDTV.x264-LOL"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parser.ApplyWebsitePrefix(tc.in)
			if got != tc.want {
				t.Errorf("ApplyWebsitePrefix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestApplyCleanTorrentSuffix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// `[eztv]` is NOT in the C# alternation — preserved.
		{"Series S02E09 HDTV x264-2HD[eztv]", "Series S02E09 HDTV x264-2HD[eztv]"},
		{"Series.S01E01.720p.HDTV.x264-LOL[ettv]", "Series.S01E01.720p.HDTV.x264-LOL"},
		{"Series.S01E01.720p.HDTV.x264-LOL[rartv]", "Series.S01E01.720p.HDTV.x264-LOL"},
		{"Series.S01E01.720p.HDTV.x264-LOL[rarbg]", "Series.S01E01.720p.HDTV.x264-LOL"},
		{"Series.S01E01.720p.HDTV.x264-LOL[cttv]", "Series.S01E01.720p.HDTV.x264-LOL"},
		{"Series.S01E01.720p.HDTV.x264-LOL[publichd]", "Series.S01E01.720p.HDTV.x264-LOL"},
		// Mid-string `[rartv]` is preserved (the regex is `$`-anchored).
		{"Series[rartv]S01E01", "Series[rartv]S01E01"},
		// Other tracker tokens are preserved.
		{"Series.S01E01-LOL[other]", "Series.S01E01-LOL[other]"},
		// No suffix → unchanged.
		{"Series.S01E01.720p.HDTV.x264-LOL", "Series.S01E01.720p.HDTV.x264-LOL"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parser.ApplyCleanTorrentSuffix(tc.in)
			if got != tc.want {
				t.Errorf("ApplyCleanTorrentSuffix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestApplyWebsitePostfix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Series.S01E01[ www.tracker.com ]", "Series.S01E01"},
		{"Series.S01E01[www.tracker.com]", "Series.S01E01"},
		// punycode TLD
		{"Series.S01E01[www.example.xn--p1ai]", "Series.S01E01"},
		// No postfix → unchanged.
		{"Series.S01E01.720p.HDTV.x264-LOL", "Series.S01E01.720p.HDTV.x264-LOL"},
		// Mid-string website-shaped token preserved.
		{"Series.S01E01.www.tracker.com.HDTV", "Series.S01E01.www.tracker.com.HDTV"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parser.ApplyWebsitePostfix(tc.in)
			if got != tc.want {
				t.Errorf("ApplyWebsitePostfix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRemoveFileExtension(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Recognised media extensions.
		{"Series.S01E01.720p.HDTV.x264-LOL.mkv", "Series.S01E01.720p.HDTV.x264-LOL"},
		{"Series.S01E01.720p.HDTV.x264-LOL.mp4", "Series.S01E01.720p.HDTV.x264-LOL"},
		{"Series.S01E01.720p.HDTV.x264-LOL.avi", "Series.S01E01.720p.HDTV.x264-LOL"},
		// Recognised usenet extensions.
		{"Series.S01E01.720p.HDTV.x264-LOL.par2", "Series.S01E01.720p.HDTV.x264-LOL"},
		{"Series.S01E01.720p.HDTV.x264-LOL.nzb", "Series.S01E01.720p.HDTV.x264-LOL"},
		// Unrecognised extensions are preserved (the C# MatchEvaluator
		// pattern returns the original match for non-media tokens).
		{"Series.S01E01.www.tracker.com", "Series.S01E01.www.tracker.com"},
		{"Series.S01E01-LOL.org", "Series.S01E01-LOL.org"},
		// Case-insensitive match.
		{"Series.S01E01.720p.HDTV.x264-LOL.MKV", "Series.S01E01.720p.HDTV.x264-LOL"},
		// No extension → unchanged.
		{"Series.S01E01.720p.HDTV.x264-LOL", "Series.S01E01.720p.HDTV.x264-LOL"},
		// Empty input.
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parser.RemoveFileExtension(tc.in)
			if got != tc.want {
				t.Errorf("RemoveFileExtension(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
