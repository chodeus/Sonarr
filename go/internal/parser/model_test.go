package parser_test

import (
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

// Black-box tests for ParsedEpisodeInfo helpers and ReleaseType
// classification. The C# fixture coverage for these getters is
// scattered across the broader Parser*Fixture suites; the tests below
// pin each computed-property branch directly so future ParseTitle
// slices can rely on the model contract.

func TestParsedEpisodeInfo_IsDaily(t *testing.T) {
	cases := []struct {
		airDate string
		want    bool
	}{
		{"2024-01-15", true},
		{"20240115", true},
		{"  2024-01-15  ", true}, // trimmed
		{"", false},
		{"   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.airDate, func(t *testing.T) {
			p := &parser.ParsedEpisodeInfo{AirDate: tc.airDate}
			if got := p.IsDaily(); got != tc.want {
				t.Errorf("IsDaily() = %v, want %v for AirDate=%q", got, tc.want, tc.airDate)
			}
		})
	}
}

func TestParsedEpisodeInfo_IsAbsoluteNumbering(t *testing.T) {
	p := parser.NewParsedEpisodeInfo()
	if p.IsAbsoluteNumbering() {
		t.Errorf("empty info: IsAbsoluteNumbering() should be false")
	}
	p.AbsoluteEpisodeNumbers = []int{42}
	if !p.IsAbsoluteNumbering() {
		t.Errorf("populated info: IsAbsoluteNumbering() should be true")
	}
}

func TestParsedEpisodeInfo_IsPossibleSpecialEpisode(t *testing.T) {
	cases := []struct {
		name string
		set  func(p *parser.ParsedEpisodeInfo)
		want bool
	}{
		{
			"empty info",
			func(_ *parser.ParsedEpisodeInfo) {},
			true, // no airDate + no title + no episodes → special
		},
		{
			"title with Special flag",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Some Show"
				p.Special = true
			},
			true,
		},
		{
			"title without Special",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Some Show"
				p.SeasonNumber = 1
				p.EpisodeNumbers = []int{1}
			},
			false,
		},
		{
			"single E0 sentinel",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Some Show"
				p.SeasonNumber = 5
				p.EpisodeNumbers = []int{0}
			},
			true,
		},
		{
			"daily-shaped",
			func(p *parser.ParsedEpisodeInfo) {
				p.AirDate = "2024-01-15"
				p.SeriesTitle = "Daily Show"
			},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parser.NewParsedEpisodeInfo()
			tc.set(p)
			if got := p.IsPossibleSpecialEpisode(); got != tc.want {
				t.Errorf("IsPossibleSpecialEpisode() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParsedEpisodeInfo_IsPossibleSceneSeasonSpecial(t *testing.T) {
	cases := []struct {
		name string
		set  func(p *parser.ParsedEpisodeInfo)
		want bool
	}{
		{
			"S5E0",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeasonNumber = 5
				p.EpisodeNumbers = []int{0}
			},
			true,
		},
		{
			"S0E0 — fails the season != 0 guard",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeasonNumber = 0
				p.EpisodeNumbers = []int{0}
			},
			false,
		},
		{
			"S5E1 — fails the episode == 0 guard",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeasonNumber = 5
				p.EpisodeNumbers = []int{1}
			},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parser.NewParsedEpisodeInfo()
			tc.set(p)
			if got := p.IsPossibleSceneSeasonSpecial(); got != tc.want {
				t.Errorf("IsPossibleSceneSeasonSpecial() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParsedEpisodeInfo_ReleaseType(t *testing.T) {
	cases := []struct {
		name string
		set  func(p *parser.ParsedEpisodeInfo)
		want parser.ReleaseType
	}{
		{
			"empty",
			func(_ *parser.ParsedEpisodeInfo) {},
			parser.ReleaseTypeUnknown,
		},
		{
			"FullSeason",
			func(p *parser.ParsedEpisodeInfo) { p.FullSeason = true },
			parser.ReleaseTypeSeasonPack,
		},
		{
			"single episode",
			func(p *parser.ParsedEpisodeInfo) { p.EpisodeNumbers = []int{5} },
			parser.ReleaseTypeSingleEpisode,
		},
		{
			"single absolute",
			func(p *parser.ParsedEpisodeInfo) { p.AbsoluteEpisodeNumbers = []int{99} },
			parser.ReleaseTypeSingleEpisode,
		},
		{
			"multi episode",
			func(p *parser.ParsedEpisodeInfo) { p.EpisodeNumbers = []int{5, 6} },
			parser.ReleaseTypeMultiEpisode,
		},
		{
			"multi absolute",
			func(p *parser.ParsedEpisodeInfo) { p.AbsoluteEpisodeNumbers = []int{99, 100} },
			parser.ReleaseTypeMultiEpisode,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parser.NewParsedEpisodeInfo()
			tc.set(p)
			if got := p.ReleaseType(); got != tc.want {
				t.Errorf("ReleaseType() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParsedEpisodeInfo_String(t *testing.T) {
	q := quality.Model{Quality: quality.HDTV720p}

	cases := []struct {
		name      string
		set       func(p *parser.ParsedEpisodeInfo)
		wantParts []string // each must be a substring of the result
	}{
		{
			"unknown",
			func(p *parser.ParsedEpisodeInfo) { p.SeriesTitle = "Show"; p.Quality = q },
			[]string{"Show", "[Unknown Episode]"},
		},
		{
			"daily no episodes",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Daily Show"
				p.AirDate = "2024-01-15"
				p.Quality = q
			},
			[]string{"Daily Show", "2024-01-15"},
		},
		{
			"full season",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Show"
				p.SeasonNumber = 3
				p.FullSeason = true
				p.Quality = q
			},
			[]string{"Show", "Season 03"},
		},
		{
			"single episode",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Show"
				p.SeasonNumber = 1
				p.EpisodeNumbers = []int{5}
				p.Quality = q
			},
			[]string{"Show", "S01E05"},
		},
		{
			"multi episode",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Show"
				p.SeasonNumber = 2
				p.EpisodeNumbers = []int{5, 6, 7}
				p.Quality = q
			},
			[]string{"Show", "S02E05-06-07"},
		},
		{
			"absolute episodes",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Anime"
				p.AbsoluteEpisodeNumbers = []int{42}
				p.Quality = q
			},
			[]string{"Anime", "042"},
		},
		{
			"special with season",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Show"
				p.SeasonNumber = 3
				p.Special = true
				p.Quality = q
			},
			[]string{"Show", "[Unknown Season 03 Special]"},
		},
		{
			"special without season",
			func(p *parser.ParsedEpisodeInfo) {
				p.SeriesTitle = "Show"
				p.Special = true
				p.Quality = q
			},
			[]string{"Show", "[Unknown Special]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parser.NewParsedEpisodeInfo()
			tc.set(p)
			s := p.String()
			for _, want := range tc.wantParts {
				if !strings.Contains(s, want) {
					t.Errorf("String() = %q, missing %q", s, want)
				}
			}
		})
	}
}

func TestNewParsedEpisodeInfo_DefaultsToEmptySlices(t *testing.T) {
	p := parser.NewParsedEpisodeInfo()
	if p.EpisodeNumbers == nil {
		t.Errorf("EpisodeNumbers should be non-nil empty slice")
	}
	if p.AbsoluteEpisodeNumbers == nil {
		t.Errorf("AbsoluteEpisodeNumbers should be non-nil empty slice")
	}
	if p.SpecialAbsoluteEpisodeNumbers == nil {
		t.Errorf("SpecialAbsoluteEpisodeNumbers should be non-nil empty slice")
	}
	if p.Languages == nil {
		t.Errorf("Languages should be non-nil empty slice")
	}
}
