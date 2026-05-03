package parser

import (
	"testing"
)

// White-box tests for the ported PreSubstitutionRegex rules.
//
// The C# fixture `ReleaseGroupParserFixture.cs` does not directly
// exercise PreSubstitutionRegex — the rules are normalisation steps
// applied before the main release-group regexes run, and the existing
// fixture rows don't trigger any of them. The tests below pin each
// shipped rule (1 / 2 / 14) against synthetic inputs that exercise
// the rule's intent.

func TestApplyPreSubstitution_Rule1_Korean(t *testing.T) {
	// Rule 1 input shape: `<title>.E<episode>.<airdate6>.<rest>-<F1RST|NEXT>`
	// The C# replacement reshapes to `<title>.S01E<episode>.<rest>-<F1RST|NEXT>`.
	cases := []struct {
		in   string
		want string
	}{
		{
			"Korean.Show.E14.230415.1080p.HDTV.H264-F1RST",
			"Korean.Show.S01E14.1080p.HDTV.H264-F1RST",
		},
		{
			"Korean.Show.E1234.221201.720p.WEB-DL.AAC-NEXT",
			"Korean.Show.S01E1234.720p.WEB-DL.AAC-NEXT",
		},
		// Negative: missing F1RST/NEXT suffix → no transform.
		{
			"Korean.Show.E14.230415.1080p.HDTV.H264-OTHER",
			"Korean.Show.E14.230415.1080p.HDTV.H264-OTHER",
		},
		// Negative: airdate not 6 digits → no transform.
		{
			"Korean.Show.E14.2304.1080p.HDTV.H264-F1RST",
			"Korean.Show.E14.2304.1080p.HDTV.H264-F1RST",
		},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := applyPreSubstitution(tc.in)
			if got != tc.want {
				t.Errorf("applyPreSubstitution(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestApplyPreSubstitution_Rule2_ChineseDoubleTitle(t *testing.T) {
	// Rule 2 input shape: `[group][title chinesetitle][episode]`
	// (where chinesetitle is at least 1 CJK char). Replacement:
	// `[group] title - episode - `. Rule 1 must NOT have fired
	// already (no `.E<n>.<airdate>.<rest>-<F1RST|NEXT>` at end).
	cases := []struct {
		in   string
		want string
	}{
		{
			"[GroupName][English Title 中文标题][05]",
			"[GroupName] English Title - 05 - ",
		},
		{
			"[GroupName][Show 漫画][1234]",
			"[GroupName] Show - 1234 - ",
		},
		// Negative: no chinesetitle (no CJK chars in title bracket) → no transform.
		{
			"[GroupName][English Only][05]",
			"[GroupName][English Only][05]",
		},
		// Negative: not 1-4 digit episode → no transform (regex requires \d{1,4}).
		{
			"[GroupName][Title 中文][12345]",
			"[GroupName][Title 中文][12345]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := applyPreSubstitution(tc.in)
			if got != tc.want {
				t.Errorf("applyPreSubstitution(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestApplyPreSubstitution_Rule14_SpanishInfo(t *testing.T) {
	// Rule 14 input shape: `<title>[ ._-](<year>/<info>...)`.
	// Replacement: `<title> (<year>) - <info> `.
	cases := []struct {
		in   string
		want string
	}{
		// Note the C# `<info>` group is `S[^\/]+` — captures every
		// non-`/` char to end of input including the trailing `)`,
		// since `)` is not `/`. The replacement emits everything in
		// the info group verbatim.
		{
			"El Marginal (2024/S01E05 1080p WEB-DL x264)",
			"El Marginal (2024) - S01E05 1080p WEB-DL x264) ",
		},
		// `<title>` ends BEFORE the `[ ._-]` boundary char, so the
		// trailing `.` of `Show.Title.` is NOT in title.
		{
			"Show.Title.(2023/S02 BDRip)",
			"Show.Title (2023) - S02 BDRip) ",
		},
		// Negative: info doesn't start with `S` → no transform.
		{
			"Show (2024/E01 stuff)",
			"Show (2024/E01 stuff)",
		},
		// Negative: missing `(` boundary char → no transform.
		{
			"Show 2024 S01E01",
			"Show 2024 S01E01",
		},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := applyPreSubstitution(tc.in)
			if got != tc.want {
				t.Errorf("applyPreSubstitution(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestApplyPreSubstitution_FirstMatchWins pins the C# `foreach +
// TryReplace + break` semantic: only the FIRST matching rule fires.
// Inputs that satisfy multiple rules trigger only the first one in
// declaration order.
func TestApplyPreSubstitution_FirstMatchWins(t *testing.T) {
	// Synthetic: an input that satisfies rule 1 (Korean) gets that
	// transformation applied, even if it would also match other rules
	// post-transform. The break ensures rule 2 / 14 never run.
	in := "Korean.Show.E14.230415.1080p.HDTV.H264-F1RST"
	got := applyPreSubstitution(in)
	if got != "Korean.Show.S01E14.1080p.HDTV.H264-F1RST" {
		t.Errorf("first-match-wins failed: got %q", got)
	}
}

// TestApplyPreSubstitution_NoMatch confirms titles that don't match
// any rule pass through unchanged.
func TestApplyPreSubstitution_NoMatch(t *testing.T) {
	cases := []string{
		"Series.S01E01.720p.HDTV.x264-LOL",
		"",
		"Plain text with no special structure",
		"[Erai-raws] Anime - 01 [1080p]",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := applyPreSubstitution(in)
			if got != in {
				t.Errorf("applyPreSubstitution(%q) changed to %q", in, got)
			}
		})
	}
}
