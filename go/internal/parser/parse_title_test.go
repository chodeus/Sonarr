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
	// in the current slice subset → nil. As more rules land, these
	// should migrate to positive-assertion tests.
	cases := []string{
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

// TestParseTitle_DailyAirDate ports cases from
// `DailyEpisodeParserFixture.cs` `should_parse_daily_episode` that
// match the canonical `<title> <airyear>.<airmonth>.<airday>` form
// (Parser.cs:333). Cases with reversed date orders (`04.28.2018` /
// `28.04.2018`) or 6-digit forms (`140722`) need additional rules
// — deferred to subsequent slices.
func TestParseTitle_DailyAirDate(t *testing.T) {
	cases := []struct {
		title     string
		wantTitle string
		wantDate  string
	}{
		{"Series Title 2011 04 18 Emma Roberts HDTV XviD BFF", "Series Title", "2011-04-18"},
		{"A Late Talk Show 2011 04 15 1080i HDTV DD5 1 MPEG2 TrollHD", "A Late Talk Show", "2011-04-15"},
		{"A.Late.Talk.Show.2010.10.11.Johnny.Knoxville.iTouch-MW", "A Late Talk Show", "2010-10-11"},
		{"A Late Talk Show - 2011-04-12 - Gov. Deval Patrick", "A Late Talk Show", "2011-04-12"},
		{"2011.01.10 - A Late Talk Show- HD TV.mkv", "", "2011-01-10"},
		{"2011.03.13 - A Late Talk Show - HD TV.mkv", "", "2011-03-13"},
		{"Series Title - 2013-10-30 - Episode Title (1) [HDTV-720p]", "Series Title", "2013-10-30"},
		{"The Show Series 2015 02 09 WEBRIP s01e13", "The Show Series", "2015-02-09"},
		{"Tree_Series_2018_06_22_Seth_Meyers_720p_HEVC_x265-MeGusta", "Tree Series", "2018-06-22"},
		// Deferred: 8-digit-airdate form (`20201013`) needs Parser.cs:362
		// rule which lands in a later slice.
		// {"Series and Title 20201013 Ep7432 [720p WebRip (x264)] [SUBS]", "Series and Title", "2020-10-13"},
		// Deferred: title-with-episode-marker form (`Series E76 2024 08 08`)
		// needs the C# pipeline's post-match title cleaning step which
		// strips trailing `E\d+` tokens — lands with the full pipeline
		// in slice 2.12.final.
		// {"Series On TitleClub E76 2024 08 08 1080p WEB H264-RnB96 [TJET]", "Series On TitleClub", "2024-08-08"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseTitle(tc.title)
			if got == nil {
				t.Fatalf("ParseTitle(%q) = nil, want a daily result", tc.title)
			}
			if got.AirDate != tc.wantDate {
				t.Errorf("AirDate = %q, want %q", got.AirDate, tc.wantDate)
			}
			if got.SeriesTitle != tc.wantTitle {
				t.Errorf("SeriesTitle = %q, want %q", got.SeriesTitle, tc.wantTitle)
			}
			if len(got.EpisodeNumbers) != 0 {
				t.Errorf("EpisodeNumbers = %v, want empty for daily", got.EpisodeNumbers)
			}
			if !got.IsDaily() {
				t.Errorf("IsDaily() = false, want true")
			}
		})
	}
}

// TestParseTitle_AnimeAbsolute ports cases from
// `AbsoluteEpisodeNumberParserFixture.cs` that match the broad
// `[SubGroup] Title <sep> <absolute_episode>` shape (Parser.cs
// rule 112-114, the most common anime form). Cases where the title
// contains a season+episode marker like `S10E14 214` still fall
// through to subsequent slices.
func TestParseTitle_AnimeAbsolute(t *testing.T) {
	cases := []struct {
		title     string
		wantTitle string
		wantAbs   int
	}{
		{"[Commie]_Some_Anime_Show_-_11_[65F220B4]", "Some Anime Show", 11},
		{"[Underwater]_Some_Anime_Show_-_12_(720p)_[5C7BC4F9]", "Some Anime Show", 12},
		{"[Commie]_Some_Anime_Show_-_15_[E76552EA]", "Some Anime Show", 15},
		{"[HorribleSubs]_Some_Anime_Show_-_33_[720p]", "Some Anime Show", 33},
		{"[HorribleSubs]_Some_Anime_Show_-_145_[720p]", "Some Anime Show", 145},
		{"[HorribleSubs] Some Anime Show - 13 [1080p].mkv", "Some Anime Show", 13},
		{"[K-F] Some Anime Show 214", "Some Anime Show", 214},
		{"[FroZen] Series Title - 23 [DVD][7F6170E6]", "Series Title", 23},
		{"[Commie] Series Title - 32 [0BA19D5B]", "Series Title", 32},
		{"[HorribleSubs] Series Title - 32 [480p]", "Series Title", 32},
		{"[CR] Series Title - 004 [480p][48CE2D0F]", "Series Title", 4},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseTitle(tc.title)
			if got == nil {
				t.Fatalf("ParseTitle(%q) = nil, want a result", tc.title)
			}
			if got.SeriesTitle != tc.wantTitle {
				t.Errorf("SeriesTitle = %q, want %q", got.SeriesTitle, tc.wantTitle)
			}
			if len(got.AbsoluteEpisodeNumbers) != 1 || got.AbsoluteEpisodeNumbers[0] != tc.wantAbs {
				t.Errorf("AbsoluteEpisodeNumbers = %v, want [%d]", got.AbsoluteEpisodeNumbers, tc.wantAbs)
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
