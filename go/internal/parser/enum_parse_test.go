package parser_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

func TestParseIndexerFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want parser.IndexerFlags
	}{
		{"empty → 0", "", 0},
		{"whitespace → 0", "   ", 0},
		{"numeric zero", "0", 0},
		{"numeric single bit", "1", parser.IndexerFlagFreeleech},
		{"numeric multi-bit", "9", parser.IndexerFlagFreeleech | parser.IndexerFlagInternal},
		{"single name lowercase", "freeleech", parser.IndexerFlagFreeleech},
		{"single name camelcase", "Freeleech", parser.IndexerFlagFreeleech},
		{"single name uppercase", "FREELEECH", parser.IndexerFlagFreeleech},
		{"comma-separated case-insensitive", "Freeleech, Internal",
			parser.IndexerFlagFreeleech | parser.IndexerFlagInternal},
		{"comma-separated no-space", "Freeleech,Internal",
			parser.IndexerFlagFreeleech | parser.IndexerFlagInternal},
		{"unknown name → 0", "Bogus", 0},
		{"unknown + known → only known", "Bogus, Internal", parser.IndexerFlagInternal},
		{"DoubleUpload (multi-syllable name)", "DoubleUpload", parser.IndexerFlagDoubleUpload},
		{"all 9 names", "Freeleech, Halfleech, DoubleUpload, Internal, Scene, Freeleech75, Freeleech25, Nuked, Subtitles",
			parser.IndexerFlagFreeleech | parser.IndexerFlagHalfleech | parser.IndexerFlagDoubleUpload |
				parser.IndexerFlagInternal | parser.IndexerFlagScene | parser.IndexerFlagFreeleech75 |
				parser.IndexerFlagFreeleech25 | parser.IndexerFlagNuked | parser.IndexerFlagSubtitles},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parser.ParseIndexerFlags(tc.in); got != tc.want {
				t.Errorf("ParseIndexerFlags(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseReleaseType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want parser.ReleaseType
	}{
		{"empty → Unknown", "", parser.ReleaseTypeUnknown},
		{"whitespace → Unknown", "  ", parser.ReleaseTypeUnknown},
		{"numeric Unknown", "0", parser.ReleaseTypeUnknown},
		{"numeric SingleEpisode", "1", parser.ReleaseTypeSingleEpisode},
		{"numeric MultiEpisode", "2", parser.ReleaseTypeMultiEpisode},
		{"numeric SeasonPack", "3", parser.ReleaseTypeSeasonPack},
		{"name SingleEpisode", "SingleEpisode", parser.ReleaseTypeSingleEpisode},
		{"name lowercase", "seasonpack", parser.ReleaseTypeSeasonPack},
		{"name uppercase", "MULTIEPISODE", parser.ReleaseTypeMultiEpisode},
		{"unknown name → Unknown", "Bogus", parser.ReleaseTypeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parser.ParseReleaseType(tc.in); got != tc.want {
				t.Errorf("ParseReleaseType(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestReleaseType_String covers the J159 PascalCase Stringer — every
// named value + an out-of-range cast that should fall through to the
// integer string (mirrors C# `Enum.ToString()` for unnamed values).
func TestReleaseType_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   parser.ReleaseType
		want string
	}{
		{parser.ReleaseTypeUnknown, "Unknown"},
		{parser.ReleaseTypeSingleEpisode, "SingleEpisode"},
		{parser.ReleaseTypeMultiEpisode, "MultiEpisode"},
		{parser.ReleaseTypeSeasonPack, "SeasonPack"},
		{parser.ReleaseType(99), "99"}, // out-of-range → int form
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Errorf("(%d).String() = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIndexerFlags_String covers the J160 `[Flags]` Stringer.
// Confirms: zero returns "0", single-bit returns the bit name, multi-
// bit returns comma-separated names in descending-value order (the
// .NET runtime walks declared values high-to-low), and unknown bits
// fall through to the integer string.
func TestIndexerFlags_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   parser.IndexerFlags
		want string
	}{
		{"zero", 0, "0"},
		{"Freeleech only", parser.IndexerFlagFreeleech, "Freeleech"},
		{"Internal only", parser.IndexerFlagInternal, "Internal"},
		{"Subtitles only (high bit)", parser.IndexerFlagSubtitles, "Subtitles"},
		{
			"Freeleech + Internal (value 9)",
			parser.IndexerFlagFreeleech | parser.IndexerFlagInternal,
			"Internal, Freeleech",
		},
		{
			"all 9 named bits",
			parser.IndexerFlagFreeleech | parser.IndexerFlagHalfleech | parser.IndexerFlagDoubleUpload |
				parser.IndexerFlagInternal | parser.IndexerFlagScene | parser.IndexerFlagFreeleech75 |
				parser.IndexerFlagFreeleech25 | parser.IndexerFlagNuked | parser.IndexerFlagSubtitles,
			"Subtitles, Nuked, Freeleech25, Freeleech75, Scene, Internal, DoubleUpload, Halfleech, Freeleech",
		},
		{
			"unknown bit (value 512 = 2^9) → int form",
			parser.IndexerFlags(512),
			"512",
		},
		{
			"named bit + unknown bit → int form (residue triggers fallback)",
			parser.IndexerFlagFreeleech | parser.IndexerFlags(1024),
			"1025",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Errorf("(%d).String() = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
