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
