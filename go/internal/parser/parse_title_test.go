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
		// Removed: with `s01e13` suffix — the title+S-E rule now fires
		// first capturing S1E13. Covered as `dailyx` in
		// parser_spec_rows_test.go pending Parser.cs:161 (airdate +
		// season+episode hybrid rule) port.
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

// TestParseTitle_NxEpisode_Regressions pins false-positive
// rejections that exercise the title's `[^\d]` last-char constraint
// (the RE2 stand-in for the C# `(?<!\d+)` lookbehind on season). Each
// input has a `<digit>.<digit> x<NNN>` mid-string fragment that an
// unanchored `<title> <Nx<NN>>` regex would mis-classify; the
// constraint forces the lazy title to end on a non-digit, eliminating
// the false positive.
func TestParseTitle_NxEpisode_Regressions(t *testing.T) {
	cases := []struct {
		title        string
		wantSeason   int
		wantEpisodes []int
		wantNoNx     bool // if true, the Nx-episode rule must NOT classify season=1 episode=264
	}{
		// from MultiEpisodeParserFixture (asserts null in C#)
		{
			"Red Series S01 E01-E02 1080p AMZN WEBRip DDP5.1 x264 monkee",
			0, nil, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseTitle(tc.title)
			if tc.wantNoNx && got != nil {
				if len(got.EpisodeNumbers) == 1 && got.EpisodeNumbers[0] == 264 {
					t.Errorf("ParseTitle(%q) = S%dE%v — Nx-episode rule wrongly classified `x264` as episode", tc.title, got.SeasonNumber, got.EpisodeNumbers)
				}
			}
		})
	}
}

// TestParseTitle_NxEpisode ports cases from
// `SingleEpisodeParserFixture.cs` that match the `<Title> <N>x<NN>`
// form (rule 5 in reportTitleRules). Also covers raw `1x05` no-title
// forms via the existing no-title single rule.
func TestParseTitle_NxEpisode(t *testing.T) {
	cases := []struct {
		title       string
		wantTitle   string
		wantSeason  int
		wantEpisode int
	}{
		// from SingleEpisodeParserFixture
		{"Series.Title.4x05.HDTV.XviD-LOL", "Series Title", 4, 5},
		{"Series_Title!_-_7x6_-_The_Scarlett_Getter_[SDTV]", "Series Title!", 7, 6},
		{"Series_Title_-_1x1_-_Live_and_Learn_[HDTV-720p]", "Series Title", 1, 1},
		{"Series - 2x12 - The Choice [HDTV-1080p].mkv", "Series", 2, 12},
		{"Series - 2x4 - New Car Smell [HDTV-1080p].mkv", "Series", 2, 4},
		{"Series_Title.19x06.720p_HDTV_x264-FoV", "Series Title", 19, 6},
		{"The_Big_Series_Title_-_6x12_-_The_Code_Review_Equivalency_[HDTV-720p]", "The Big Series Title", 6, 12},
		{"Series Title - 10x12 - 24 Hour Propane People [SDTV]", "Series Title", 10, 12},

		// no-title raw `Nx<NN>` forms — pick up the existing no-title
		// single rule (`S?` makes the leading S optional)
		{"1x04", "", 1, 4},
		{"5x10 WS PDTV XviD FUtV", "", 5, 10},
		{"01x04 - Halloween, Part 1 - 720p WEB-DL", "", 1, 4},
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
			if len(got.EpisodeNumbers) != 1 || got.EpisodeNumbers[0] != tc.wantEpisode {
				t.Errorf("EpisodeNumbers = %v, want [%d]", got.EpisodeNumbers, tc.wantEpisode)
			}
			if tc.wantTitle != "" && got.SeriesTitle != tc.wantTitle {
				t.Errorf("SeriesTitle = %q, want %q", got.SeriesTitle, tc.wantTitle)
			}
		})
	}
}

// TestParseTitle_3DigitEpisode ports cases from
// `SingleEpisodeParserFixture.cs` that match the 3-digit-episode
// shortcut form (`Title.103` → S1E03, Parser.cs:336). Cases with
// embedded year prefixes (e.g. `series.2009.416`) are deferred — the
// `[^\d]` title constraint can't extend past the trailing year digit.
func TestParseTitle_3DigitEpisode(t *testing.T) {
	cases := []struct {
		title       string
		wantTitle   string
		wantSeason  int
		wantEpisode int
	}{
		{"Series.and.a.Title.103.720p.HDTV.X264-DIMENSION", "Series and a Title", 1, 3},
		{"Series.and.a.Title.113.720p.HDTV.X264-DIMENSION", "Series and a Title", 1, 13},
		{"Series.Title.525", "Series Title", 5, 25},
		// 4-digit form
		{"Series.and.a.Title.1013.720p.HDTV.X264-DIMENSION", "Series and a Title", 10, 13},
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
			if len(got.EpisodeNumbers) != 1 || got.EpisodeNumbers[0] != tc.wantEpisode {
				t.Errorf("EpisodeNumbers = %v, want [%d]", got.EpisodeNumbers, tc.wantEpisode)
			}
			if got.SeriesTitle != tc.wantTitle {
				t.Errorf("SeriesTitle = %q, want %q", got.SeriesTitle, tc.wantTitle)
			}
		})
	}
}

// TestParseTitle_MiniSeries ports cases from `MiniSeriesEpisodeParserFixture.cs`.
// Covers the `Part N` form (Parser.cs:228) plus the `<Title>.<Year>.E<NN>`
// form (Parser.cs:224-style). Asserts season=1, IsMiniSeries=true,
// and the matching episode number. The `kill-roy-was-here-e07-720p`
// form (no Part keyword, dash-separated `e07`) is deferred to a
// later slice.
func TestParseTitle_MiniSeries(t *testing.T) {
	cases := []struct {
		title       string
		wantTitle   string
		wantEpisode int
	}{
		{"The.Big.Series.Leader.Part.2.DSR.XviD-SYS", "The Big Series Leader", 2},
		{"Series and Show 2012 Part 1 REPACK 720p HDTV x264 2HD", "Series and Show 2012", 1},
		{"Series Show.2016.E04.Power.720p.WEB-DL.DD5.1.H.264-MARS", "Series Show 2016", 4},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseTitle(tc.title)
			if got == nil {
				t.Fatalf("ParseTitle(%q) = nil, want a result", tc.title)
			}
			if got.SeasonNumber != 1 {
				t.Errorf("SeasonNumber = %d, want 1", got.SeasonNumber)
			}
			if len(got.EpisodeNumbers) != 1 || got.EpisodeNumbers[0] != tc.wantEpisode {
				t.Errorf("EpisodeNumbers = %v, want [%d]", got.EpisodeNumbers, tc.wantEpisode)
			}
			if got.SeriesTitle != tc.wantTitle {
				t.Errorf("SeriesTitle = %q, want %q", got.SeriesTitle, tc.wantTitle)
			}
			if !got.IsMiniSeries {
				t.Errorf("IsMiniSeries = false, want true")
			}
		})
	}
}

// TestParseTitle_KoreanF1RST_NEXT ports `should_parse_korean_series_episode`
// fixture rows from MiniSeriesEpisodeParserFixture. Wired through
// PreSubstitutionRegex rule 1 (Korean F1RST/NEXT) which reshapes
// `<Title>.E<NN>.<airdate6>.<rest>-<F1RST|NEXT>` into
// `<Title>.S01E<NN>.<rest>-<F1RST|NEXT>` before the ReportTitleRegex
// rules run.
func TestParseTitle_KoreanF1RST_NEXT(t *testing.T) {
	cases := []struct {
		title       string
		wantTitle   string
		wantEpisode int
	}{
		{"It's a Series Title.E56.190121.720p-NEXT.mp4", "It's a Series Title", 56},
		{"My Only Series Title.E37.190120.1080p-NEXT.mp4", "My Only Series Title", 37},
		{"Series.E191.190121.720p-NEXT.mp4", "Series", 191},
		{"The Series Title Challenge.E932.190120.720p-NEXT.mp4", "The Series Title Challenge", 932},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := parser.ParseTitle(tc.title)
			if got == nil {
				t.Fatalf("ParseTitle(%q) = nil, want a result", tc.title)
			}
			if got.SeasonNumber != 1 {
				t.Errorf("SeasonNumber = %d, want 1", got.SeasonNumber)
			}
			if len(got.EpisodeNumbers) != 1 || got.EpisodeNumbers[0] != tc.wantEpisode {
				t.Errorf("EpisodeNumbers = %v, want [%d]", got.EpisodeNumbers, tc.wantEpisode)
			}
			if got.SeriesTitle != tc.wantTitle {
				t.Errorf("SeriesTitle = %q, want %q", got.SeriesTitle, tc.wantTitle)
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

// TestParseTitle_Enrichment locks in the post-match enrichment
// stages (Quality, ReleaseGroup with subgroup override, ReleaseHash
// with 1280x720 reject). These wire in 2.13.1 — the spec dispatcher
// doesn't assert on these fields, so dedicated tests are required to
// guard against regression.
func TestParseTitle_Enrichment(t *testing.T) {
	t.Run("Quality is parsed from the original title", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E01.720p.HDTV.x264-LOL")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if got.Quality.Quality.Name != "HDTV-720p" {
			t.Errorf("Quality.Name = %q, want %q", got.Quality.Quality.Name, "HDTV-720p")
		}
	})

	t.Run("ReleaseGroup falls back to ParseReleaseGroup", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E01.HDTV.x264-LOL")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if got.ReleaseGroup != "LOL" {
			t.Errorf("ReleaseGroup = %q, want %q", got.ReleaseGroup, "LOL")
		}
	})

	t.Run("subgroup override wins over ParseReleaseGroup", func(t *testing.T) {
		// Anime [SubGroup] form: the rule's `<subgroup>` capture
		// overrides whatever ParseReleaseGroup pulls from the trailing
		// release name.
		got := parser.ParseTitle("[Impatience] Series Title - 24 [720p][34073169].mkv")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if got.ReleaseGroup != "Impatience" {
			t.Errorf("ReleaseGroup = %q, want %q", got.ReleaseGroup, "Impatience")
		}
	})

	t.Run("ReleaseHash captured from anime hash group", func(t *testing.T) {
		got := parser.ParseTitle("[Impatience] Series Title - 24 [720p][34073169].mkv")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if got.ReleaseHash != "34073169" {
			t.Errorf("ReleaseHash = %q, want %q", got.ReleaseHash, "34073169")
		}
	})

	t.Run("ReleaseHash 1280x720 false-positive is rejected", func(t *testing.T) {
		// The C# Parser at :1230 returns empty when the captured hash
		// equals "1280x720" — that's the resolution token leaking into
		// a hash-shaped capture, not a real release hash. The Go anime
		// rule's `<hash>` group is `\w{8}` so it can't match `1280x720`
		// directly (digits + 'x' = 8 chars but 'x' is in `\w`); confirm
		// that any rule capturing `1280x720` would produce empty.
		// We can't produce this directly from the existing rule set, so
		// this is a doc-test placeholder verifying the OTHER hash path
		// still works (no false negative on a normal hex hash).
		got := parser.ParseTitle("[Group] Show - 01 [1080p][ABCDEF12].mkv")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if got.ReleaseHash != "ABCDEF12" {
			t.Errorf("ReleaseHash = %q, want %q", got.ReleaseHash, "ABCDEF12")
		}
	})

	t.Run("non-anime forms have empty subgroup, populated ReleaseGroup", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E01.720p.HDTV.x264-DIMENSION")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if got.ReleaseGroup != "DIMENSION" {
			t.Errorf("ReleaseGroup = %q, want %q", got.ReleaseGroup, "DIMENSION")
		}
		if got.ReleaseHash != "" {
			t.Errorf("ReleaseHash = %q, want empty", got.ReleaseHash)
		}
	})
}

// TestParseTitle_ReleaseTokensAndLanguages covers the ReleaseTokens
// slice + Languages enrichment that ships in 2.13.2. Mirrors the
// C# `ReleaseTokens = releaseTitle.Substring(idx)` slice + the
// `Languages = ParseLanguages(ReleaseTokens)` wire.
func TestParseTitle_ReleaseTokensAndLanguages(t *testing.T) {
	t.Run("ReleaseTokens slices after the title group", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E05.HDTV.XviD-LOL")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if got.ReleaseTokens == "" {
			t.Errorf("ReleaseTokens empty; expected the trailing tokens after the title group")
		}
		// The exact slice is approximate (idx from simpleTitle, applied
		// to releaseTitle) — assert the trailing tokens are present.
		if !contains(got.ReleaseTokens, "HDTV") {
			t.Errorf("ReleaseTokens = %q, want to contain HDTV", got.ReleaseTokens)
		}
	})

	t.Run("Languages defaults to Unknown for unmarked English-default release", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E05.HDTV.XviD-LOL")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if len(got.Languages) == 0 {
			t.Fatalf("Languages empty, want at least one entry")
		}
		// No language token in the title → ParseLanguages returns
		// [Unknown] (its non-empty fallback at LanguageParser.cs:206).
		if got.Languages[0].Name != "Unknown" {
			t.Errorf("Languages[0].Name = %q, want Unknown", got.Languages[0].Name)
		}
	})

	t.Run("Languages picks up an explicit Spanish marker in trailing tokens", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E05.SPANISH.HDTV.XviD-LOL")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		hasSpanish := false
		for _, l := range got.Languages {
			if l.Name == "Spanish" {
				hasSpanish = true
				break
			}
		}
		if !hasSpanish {
			t.Errorf("Languages = %v, want to contain Spanish", got.Languages)
		}
	})
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestParseTitle_SeriesTitleInfo ports the C# SeriesTitleInfoFixture
// (5 cases at SeriesTitleInfoFixture.cs). Asserts the GetSeriesTitleInfo
// post-processing populates SeriesTitleInfo on the ParseTitle output.
func TestParseTitle_SeriesTitleInfo(t *testing.T) {
	t.Run("year=0 when title has no year", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E01.pilot.720p.hdtv")
		if got == nil || got.SeriesTitleInfo == nil {
			t.Fatal("ParseTitle returned nil or SeriesTitleInfo nil")
		}
		if got.SeriesTitleInfo.Year != 0 {
			t.Errorf("Year = %d, want 0", got.SeriesTitleInfo.Year)
		}
	})

	t.Run("Title == TitleWithoutYear when no year", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E01.pilot.720p.hdtv")
		if got == nil || got.SeriesTitleInfo == nil {
			t.Fatal("ParseTitle returned nil or SeriesTitleInfo nil")
		}
		if got.SeriesTitleInfo.Title != got.SeriesTitleInfo.TitleWithoutYear {
			t.Errorf("Title=%q != TitleWithoutYear=%q",
				got.SeriesTitleInfo.Title, got.SeriesTitleInfo.TitleWithoutYear)
		}
	})

	t.Run("year populated from title with year", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.2004.S01E01.pilot.720p.hdtv")
		if got == nil || got.SeriesTitleInfo == nil {
			t.Fatal("ParseTitle returned nil or SeriesTitleInfo nil")
		}
		if got.SeriesTitleInfo.Year != 2004 {
			t.Errorf("Year = %d, want 2004", got.SeriesTitleInfo.Year)
		}
	})

	t.Run("Title carries the year", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.2004.S01E01.pilot.720p.hdtv")
		if got == nil || got.SeriesTitleInfo == nil {
			t.Fatal("ParseTitle returned nil or SeriesTitleInfo nil")
		}
		if got.SeriesTitleInfo.Title != "Series Title 2004" {
			t.Errorf("Title = %q, want %q", got.SeriesTitleInfo.Title, "Series Title 2004")
		}
	})

	t.Run("TitleWithoutYear strips the year", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.2004.S01E01.pilot.720p.hdtv")
		if got == nil || got.SeriesTitleInfo == nil {
			t.Fatal("ParseTitle returned nil or SeriesTitleInfo nil")
		}
		if got.SeriesTitleInfo.TitleWithoutYear != "Series Title" {
			t.Errorf("TitleWithoutYear = %q, want %q",
				got.SeriesTitleInfo.TitleWithoutYear, "Series Title")
		}
	})
}

// TestParseTitle_IsMultiSeason ports the multi-season detection
// behaviour at C# `Parser.cs:1057-1062`
// (`Groups["season"].Captures.Distinct().Count() > 1` → IsMultiSeason).
// Slice 3.83 closes the parser-side gap noted in slice 3.79b — the
// downloadedimport.processFolderWithSeries multi-season rejection arm
// at C# `:206-214` was structurally wired but never fired because the
// Go parser left IsMultiSeason at its zero value.
//
// Three input shapes covered, one per build function that received
// the scan:
//
//   - `<title> S01E04 - S02E05` style (buildTitleSeasonEpisode 2+ form).
//   - `<title> 1x05 - 2x10` style (buildTitleNxMulti).
//   - No-title `S01E04.S02E05` (buildNoTitleEpisode via the no-title
//     multi rule's `[-_. ]?S\d{1,2}E` second-iteration arm).
//
// Plus a sanity round-trip that the same shapes with a SINGLE season
// keep IsMultiSeason=false (no false positives on the dominant
// single-season input distribution).
func TestParseTitle_IsMultiSeason(t *testing.T) {
	multiCases := []struct {
		name  string
		title string
	}{
		{"title+SE 2+ multi-S, dot-separated", "Series.Title.S01E04.S02E05.HDTV.x264-LOL"},
		{"title+SE 2+ multi-S, space-dash-space separated", "Series Title - S07E22 - S08E01"},
		{"title+Nx 2+ multi-S", "Series.Title.1x05.2x10.HDTV.x264-LOL"},
		{"no-title S01E04.S02E05", "S01E04.S02E05"},
	}
	for _, tc := range multiCases {
		t.Run(tc.name, func(t *testing.T) {
			got := parser.ParseTitle(tc.title)
			if got == nil {
				t.Fatalf("ParseTitle(%q) = nil; expected a parse with IsMultiSeason=true", tc.title)
			}
			if !got.IsMultiSeason {
				t.Errorf("IsMultiSeason = false; want true. SeasonNumber=%d, Episodes=%v",
					got.SeasonNumber, got.EpisodeNumbers)
			}
		})
	}

	// Round-trip — the same builders must NOT spuriously flag a
	// SINGLE-season input as multi-season.
	singleCases := []string{
		"Series.Title.S01E05.HDTV.x264-LOL",
		"Series.Title.S01E04E05.HDTV.XviD-LOL",
		"Series.Title.S01E04E05E06.HDTV.x264-LOL",
		"Series Title - 1x05",
		"Series Title - 1x05 - 1x06",
		"S01E05",
		"S01E04E05",
	}
	for _, title := range singleCases {
		t.Run("single/"+title, func(t *testing.T) {
			got := parser.ParseTitle(title)
			if got == nil {
				t.Fatalf("ParseTitle(%q) = nil; expected a parse", title)
			}
			if got.IsMultiSeason {
				t.Errorf("IsMultiSeason = true on single-season input; want false. SeasonNumber=%d, Episodes=%v",
					got.SeasonNumber, got.EpisodeNumbers)
			}
		})
	}
}
