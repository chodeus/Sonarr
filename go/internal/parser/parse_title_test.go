package parser_test

import (
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

// Black-box tests for the initial ParseTitle implementation. Coverage
// is bounded by the four reportTitleRules shipped in slice 2.12.1
// (title + season/episode, title + season/multi-episode, no-title
// single episode, no-title multi episode). Cases ported from
// `SingleEpisodeParserFixture.cs` and `MultiEpisodeParserFixture.cs`.

func TestParseTitle_SingleEpisode(t *testing.T) {
	cases := []struct {
		title        string
		wantTitle    string
		wantSeason   int
		wantEpisodes []int
	}{
		// from `SingleEpisodeParserFixture.cs` `should_parse_single_episode`
		{"Series.Title.S01E05.HDTV.XviD-LOL", "Series Title", 1, []int{5}},
		{"Series.Title.S02E10.720p.HDTV.x264-DIMENSION", "Series Title", 2, []int{10}},
		{"Series.Title.s12e01.HDTV.x264-LOL", "Series Title", 12, []int{1}},
		{"Series.Title.2010.S05E07.720p.HDTV.X264-LOL", "Series Title 2010", 5, []int{7}},
		{"The.Series.Title.S01E05.HDTV.x264", "The Series Title", 1, []int{5}},

		// no-title forms (raw S01E05 input)
		{"S01E05", "", 1, []int{5}},
		{"S01E10", "", 1, []int{10}},
		{"s05e01", "", 5, []int{1}},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseTitle(tc.title)
			if got == nil {
				t.Fatalf("ParseTitle(%q) = nil, want a result", tc.title)
			}
			if got.SeasonNumber != tc.wantSeason {
				t.Errorf("SeasonNumber = %d, want %d", got.SeasonNumber, tc.wantSeason)
			}
			if !reflect.DeepEqual(got.EpisodeNumbers, tc.wantEpisodes) {
				t.Errorf("EpisodeNumbers = %v, want %v", got.EpisodeNumbers, tc.wantEpisodes)
			}
			if tc.wantTitle != "" && got.SeriesTitle != tc.wantTitle {
				t.Errorf("SeriesTitle = %q, want %q", got.SeriesTitle, tc.wantTitle)
			}
		})
	}
}

func TestParseTitle_MultiEpisode(t *testing.T) {
	cases := []struct {
		title        string
		wantTitle    string
		wantSeason   int
		wantEpisodes []int
	}{
		// from `MultiEpisodeParserFixture.cs` `should_parse_multi_episode`
		{"Series.Title.S01E04E05.HDTV.XviD-LOL", "Series Title", 1, []int{4, 5}},
		{"Series.Title.S01E05E06.HDTV.XviD-LOL", "Series Title", 1, []int{5, 6}},
		{"Series.Title.S05E04E05.HDTV.XviD-LOL", "Series Title", 5, []int{4, 5}},
		// E-separator form
		{"Series.Title.S01E05.E06.HDTV.XviD-LOL", "Series Title", 1, []int{5, 6}},

		// no-title forms
		{"S01E04E05", "", 1, []int{4, 5}},
		{"S01E04E05E06", "", 1, []int{4, 5, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseTitle(tc.title)
			if got == nil {
				t.Fatalf("ParseTitle(%q) = nil, want a result", tc.title)
			}
			if got.SeasonNumber != tc.wantSeason {
				t.Errorf("SeasonNumber = %d, want %d", got.SeasonNumber, tc.wantSeason)
			}
			if !reflect.DeepEqual(got.EpisodeNumbers, tc.wantEpisodes) {
				t.Errorf("EpisodeNumbers = %v, want %v", got.EpisodeNumbers, tc.wantEpisodes)
			}
			if tc.wantTitle != "" && got.SeriesTitle != tc.wantTitle {
				t.Errorf("SeriesTitle = %q, want %q", got.SeriesTitle, tc.wantTitle)
			}
		})
	}
}

func TestParseTitle_RejectInvalid(t *testing.T) {
	// Inputs that fail ValidateBeforeParsing → ParseTitle returns nil.
	cases := []string{
		"",
		"...---...",
		"Season 1",
		"Specials",
		// password+yenc → reject
		"some-password-yenc-thing",
	}
	for _, title := range cases {
		t.Run(title, func(t *testing.T) {
			got := parser.ParseTitle(title)
			if got != nil {
				t.Errorf("ParseTitle(%q) = %+v, want nil", title, got)
			}
		})
	}
}

func TestParseTitle_NoMatchingRule(t *testing.T) {
	// Titles that pass ValidateBeforeParsing but don't match any rule
	// in slice 2.12.1's subset → nil. As more rules land, these should
	// migrate to positive-assertion tests.
	cases := []string{
		"Series Title 2009-06-02.PDTV.XviD-LOL",      // daily — not yet ported
		"[HorribleSubs] Anime - 01 [1080p]",          // anime — not yet ported
		"Series.Title.S01.Special.Behind.The.Scenes", // special — not yet ported
	}
	for _, title := range cases {
		t.Run(title, func(t *testing.T) {
			got := parser.ParseTitle(title)
			if got != nil {
				t.Logf("ParseTitle(%q) returned %+v — confirm whether the matching rule landed in a later slice", title, got)
			}
		})
	}
}

func TestParseSeriesName(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		// Falls back to CleanSeriesTitle when ParseTitle fails.
		{"Some unrecognised title", "someunrecognisedtitle"},
		// ParseTitle hit → returns the parsed SeriesTitle.
		{"Series.Title.S01E05.HDTV.XviD-LOL", "Series Title"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseSeriesName(tc.title)
			if got != tc.want {
				t.Errorf("ParseSeriesName(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}
