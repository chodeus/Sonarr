package indexersearch

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/series"
)

// Mirrors a subset of the C# SearchCriteriaBase tests. The full GetCleanSceneTitle
// behaviour is shared with the request_generator_test.go suite in newznab — these
// tests cover the package-local invariants (case-insensitive THE, & → and,
// special-char regex, repeated-plus collapse, diacritic strip).

func TestGetCleanSceneTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"The Office", "Office"},
		{"the office", "office"},
		{"Office", "Office"},
		{"Hello & Goodbye", "Hello+and+Goodbye"},
		{"It's a Wonderful Life", "Its+a+Wonderful+Life"},
		{"Star Trek: The Original Series", "Star+Trek+The+Original+Series"},
		{"Café Society", "Cafe+Society"},
		{"Pokémon", "Pokemon"},
		// Repeated whitespace + non-word collapse to a single +.
		{"a   b", "a+b"},
		{"a---b", "a+b"},
		// Trim leading/trailing +.
		{"  hello  ", "hello"},
		// Empty / whitespace-only.
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		got := GetCleanSceneTitle(c.in)
		if got != c.want {
			t.Errorf("GetCleanSceneTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSearchMode_HasFlag(t *testing.T) {
	combined := SearchModeSearchID | SearchModeSearchTitle
	if !combined.HasFlag(SearchModeSearchID) {
		t.Error("HasFlag SearchID failed")
	}
	if !combined.HasFlag(SearchModeSearchTitle) {
		t.Error("HasFlag SearchTitle failed")
	}
	if combined.HasFlag(SearchModeRSS) {
		t.Error("HasFlag RSS should be false")
	}
	// Default is the zero value; any HasFlag(non-zero) returns false.
	if SearchModeDefault.HasFlag(SearchModeSearchID) {
		t.Error("Default should not contain SearchID")
	}
}

func TestCriteriaBase_AllSceneTitles(t *testing.T) {
	c := CriteriaBase{
		SceneTitles: []string{"The Office", "office"}, // distinct raw + clean
	}
	got := c.AllSceneTitles()
	// Should include both raw and the deduped clean form. Order is
	// raw-first then clean-additions.
	want := []string{"The Office", "office", "Office"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] %q want %q", i, got[i], want[i])
		}
	}
}

func TestCriteriaBase_CleanSceneTitlesDedupes(t *testing.T) {
	c := CriteriaBase{
		SceneTitles: []string{"The Office", "the office", "OFFICE"},
	}
	clean := c.CleanSceneTitles()
	// "The Office" → "Office"; "the office" → "office"; "OFFICE" → "OFFICE"
	// Three distinct case-sensitive cleans (the cleaning preserves case).
	if len(clean) != 3 {
		t.Fatalf("got %v", clean)
	}
}

func TestSingleEpisodeCriteria_String(t *testing.T) {
	c := SingleEpisodeCriteria{
		CriteriaBase:  CriteriaBase{Series: &series.Series{Title: "Show"}},
		SeasonNumber:  1,
		EpisodeNumber: 2,
	}
	if got := c.String(); got != "[Show : S01E02]" {
		t.Errorf("got %q", got)
	}
}

func TestSeasonCriteria_String(t *testing.T) {
	c := SeasonCriteria{
		CriteriaBase: CriteriaBase{Series: &series.Series{Title: "Show"}},
		SeasonNumber: 5,
	}
	if got := c.String(); got != "[Show : S05]" {
		t.Errorf("got %q", got)
	}
}

func TestDailyEpisodeCriteria_String(t *testing.T) {
	c := DailyEpisodeCriteria{
		CriteriaBase: CriteriaBase{Series: &series.Series{Title: "Show"}},
		AirDate:      time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
	}
	if got := c.String(); got != "[Show : 2026-05-10]" {
		t.Errorf("got %q", got)
	}
}

func TestDailySeasonCriteria_String(t *testing.T) {
	c := DailySeasonCriteria{
		CriteriaBase: CriteriaBase{Series: &series.Series{Title: "Show"}},
		Year:         2026,
	}
	if got := c.String(); got != "[Show : 2026]" {
		t.Errorf("got %q", got)
	}
}

func TestAnimeEpisodeCriteria_String(t *testing.T) {
	c := AnimeEpisodeCriteria{
		CriteriaBase:          CriteriaBase{Series: &series.Series{Title: "Anime"}},
		SeasonNumber:          5,
		EpisodeNumber:         4,
		AbsoluteEpisodeNumber: 100,
	}
	if got := c.String(); got != "[Anime : S05E04 (100)]" {
		t.Errorf("got %q", got)
	}
}

func TestSpecialEpisodeCriteria_StringWithTitles(t *testing.T) {
	c := SpecialEpisodeCriteria{
		CriteriaBase:       CriteriaBase{Series: &series.Series{Title: "Show", SeriesType: series.SeriesTypeStandard}},
		EpisodeQueryTitles: []string{"Christmas Special"},
	}
	got := c.String()
	if got != "[Show (Standard)] Specials" {
		t.Errorf("got %q", got)
	}
}

func TestSpecialEpisodeCriteria_StringNoTitles(t *testing.T) {
	c := SpecialEpisodeCriteria{
		CriteriaBase: CriteriaBase{Series: &series.Series{Title: "Show", SeriesType: series.SeriesTypeAnime}},
	}
	got := c.String()
	if got != "[Show (Anime): ]" {
		t.Errorf("got %q", got)
	}
}
