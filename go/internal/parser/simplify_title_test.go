package parser

import "testing"

func TestApplyFancyBrackets(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"【喵萌奶茶屋】[Anime Title][12]", "[喵萌奶茶屋][Anime Title][12]"},
		{"plain ASCII [foo]", "plain ASCII [foo]"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := applyFancyBrackets(tc.in); got != tc.want {
			t.Errorf("applyFancyBrackets(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestApplyReversedTitle(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "p027 token triggers reversal",
			in:   "yrucreM-462.H.0.2CAA.LD-BEW.p027.10E10S.esaeleR.dehsaH.emoS.mkv",
			want: "Some.Hashed.Release.S01E01.720p.WEB-DL.AAC2.0.H.264-Mercury.mkv",
		},
		{
			// applyReversedTitle only character-reverses; the
			// downstream SimpleTitle pass strips 1080P / DD5.1.
			name: "p0801 token triggers reversal (space-separated)",
			in:   "yrucreM-LN 1.5DD LD-BEW P0801 10E10S esaeleR dehsaH emoS.mkv",
			want: "Some Hashed Release S01E01 1080P WEB-DL DD5.1 NL-Mercury.mkv",
		},
		{
			name: "no token leaves title alone",
			in:   "Series.Title.S01E01.720p.HDTV.x264-LOL.mkv",
			want: "Series.Title.S01E01.720p.HDTV.x264-LOL.mkv",
		},
		{
			name: "Backup_72023S02-12.mkv must NOT trigger (no separator before 72023S02)",
			in:   "Backup_72023S02-12.mkv",
			want: "Backup_72023S02-12.mkv",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyReversedTitle(tc.in); got != tc.want {
				t.Errorf("applyReversedTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestApplySimpleTitle(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// Resolution arms — `\s*?` is lazy so adjacent `.` separators
		// are NOT consumed; matches C# behaviour byte-for-byte.
		{"720p strip", "Series.S01E01.720p.HDTV", "Series.S01E01..HDTV"},
		{"1080p strip", "Series.1080p.HDTV", "Series..HDTV"},
		// Codec arms
		{"x264 strip", "Series.x264-LOL", "Series.-LOL"},
		{"H 264 strip", "Series H 264-LOL", "Series -LOL"},
		{"H.264 strip", "Series.H.264-LOL", "Series.-LOL"},
		// DD5.1 / DD 5 1 — the trailing `\s*?` IS lazy but the
		// regex engine consumes following whitespace when a subsequent
		// alternation match abuts (e.g., `DD 5 1 x264` strips both).
		{"DD5.1 strip", "Series.DD5.1.x264", "Series.."},
		{"DD 5 1 strip", "Series DD 5 1 x264", "Series  "},
		// Dimensions
		{"1920x1080 strip", "Series.1920x1080.HDTV", "Series..HDTV"},
		// Bit-depth — accept
		{"10bit strip", "Series.10bit.HDTV", "Series..HDTV"},
		{"8bit strip", "Series.8bit.HDTV", "Series..HDTV"},
		{"10b strip at end", "Series.10b", "Series."},
		{"10 bit strip with space", "Series.10 bit.HDTV", "Series..HDTV"},
		// Bit-depth — reject (lookbehind: hex digit before the digit)
		{"e8bit preserved (hex prefix)", "DEADBEEFe8bit.mkv", "DEADBEEFe8bit.mkv"},
		{"a10bit preserved (hex prefix)", "abcdefa10bit", "abcdefa10bit"},
		// Bit-depth — reject (lookahead: bare 'b' followed by alphanum)
		{"10bxyz preserved (b followed by alnum)", "Series.10bxyz", "Series.10bxyz"},
		{"10b1c preserved (b followed by digit)", "10b1c", "10b1c"},
		// Bit-depth — accept (b at end of string)
		{"trailing 8b accepted", "Series.8b", "Series."},
		// HEVC-10Bit-2160P combo (real fixture row pattern)
		{"HEVC-10Bit-2160P", "[HEVC-10Bit-2160P AAC]", "[HEVC-- AAC]"},
		// 8 with non-alnum suffix is fine for the bare 'b' arm? No, this
		// arm is 8|10 -> b|bit. '8' alone doesn't match, only '8b'/'8bit'.
		{"plain 8 not stripped", "Series.8.x264", "Series.8."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applySimpleTitle(tc.in); got != tc.want {
				t.Errorf("applySimpleTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestApplyCleanQualityBrackets(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"recognised quality strips", "Series Title [HDTV-720p]", "Series Title "},
		{"recognised quality strips with mixed case", "Series Title [BluRay 1080p]", "Series Title "},
		{
			name: "non-quality bracket preserved",
			in:   "Series Title [DarkData]",
			want: "Series Title [DarkData]",
		},
		{
			name: "release-group hash bracket preserved",
			in:   "Series Title [34073169]",
			want: "Series Title [34073169]",
		},
		{
			name: "anchored at end only — leading bracket untouched",
			in:   "[Tag] Series Title",
			want: "[Tag] Series Title",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyCleanQualityBrackets(tc.in); got != tc.want {
				t.Errorf("applyCleanQualityBrackets(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestApplySimplifyTitle_EndToEnd is a smoke test that the full
// pipeline composes without surprises. Per-stage behaviour is
// covered by the per-helper tests above; the bulk-spec dispatcher
// at parser_spec_test.go exercises the integrated pipeline against
// the 200-row fixture.
func TestApplySimplifyTitle_EndToEnd(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			// `[720p]` strips to `[]`; the empty bracket survives because
			// CleanQualityBracketsRegex requires `[a-z0-9 ._-]+` (1+ chars).
			// Mirrors C# behaviour byte-for-byte.
			name: "fancy brackets + simple-title strip",
			in:   "【喵萌奶茶屋】[Anime Title][12][720p].mkv",
			want: "[喵萌奶茶屋][Anime Title][12][]",
		},
		{
			// After char-reversal, SimpleTitleRegex strips `720p` and
			// `H.264` from the unreversed body, leaving the `.`
			// separators around the stripped tokens (matches C# behaviour).
			name: "reversed-title flips back, ext stripped",
			in:   "yrucreM-462.H.0.2CAA.LD-BEW.p027.10E10S.esaeleR.dehsaH.emoS.mkv",
			want: "Some.Hashed.Release.S01E01..WEB-DL.AAC2.0.-Mercury",
		},
		{
			name: "trailing quality-bracket stripped",
			in:   "Series.Title.S01E01[HDTV-720p].mkv",
			want: "Series.Title.S01E01",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applySimplifyTitle(tc.in); got != tc.want {
				t.Errorf("applySimplifyTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
