package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/organizer"
)

func ep(episode int) episodes.Episode {
	return episodes.Episode{SeasonNumber: 1, EpisodeNumber: episode}
}

func epWithSeason(season, episode int) episodes.Episode {
	return episodes.Episode{SeasonNumber: season, EpisodeNumber: episode}
}

func epAbs(season, episode, absolute int) episodes.Episode {
	abs := absolute
	return episodes.Episode{
		SeasonNumber:          season,
		EpisodeNumber:         episode,
		AbsoluteEpisodeNumber: &abs,
	}
}

var _ = epWithSeason // reserved for future tests with non-S1 episodes

func TestReplaceNumberToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		token string
		value int
		want  string
	}{
		{"{episode}", 5, "5"},
		{"{episode:00}", 5, "05"},
		{"{episode:000}", 5, "005"},
		{"{season}", 1, "1"},
		{"{season:00}", 1, "01"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.token, func(t *testing.T) {
			t.Parallel()
			got := organizer.ReplaceNumberToken(tc.token, tc.value)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestExtractEpisodeFormats_Standard(t *testing.T) {
	t.Parallel()
	formats := organizer.ExtractEpisodeFormats("S{season:00}E{episode:00}")
	if len(formats) != 1 {
		t.Fatalf("len=%d want 1", len(formats))
	}
	if formats[0].EpisodeSeparator != "E" {
		t.Errorf("EpisodeSeparator=%q want E", formats[0].EpisodeSeparator)
	}
	if formats[0].EpisodePattern != "{episode:00}" {
		t.Errorf("EpisodePattern=%q", formats[0].EpisodePattern)
	}
}

func TestExtractAbsoluteEpisodeFormats(t *testing.T) {
	t.Parallel()
	formats := organizer.ExtractAbsoluteEpisodeFormats("- {absolute:000}")
	if len(formats) != 1 {
		t.Fatalf("len=%d want 1", len(formats))
	}
	if formats[0].AbsoluteEpisodePattern != "{absolute:000}" {
		t.Errorf("AbsoluteEpisodePattern=%q", formats[0].AbsoluteEpisodePattern)
	}
}

func TestFormatNumberTokens_Single(t *testing.T) {
	t.Parallel()
	got := organizer.FormatNumberTokens("S{season:00}E{episode:00}", "-{episode:00}", []episodes.Episode{ep(5)})
	if got != "S01E05" {
		t.Errorf("got %q want %q", got, "S01E05")
	}
}

func TestFormatNumberTokens_MultiExtend(t *testing.T) {
	t.Parallel()
	// Extend pattern: S{season}E{ep1}-{ep2}
	got := organizer.FormatNumberTokens("S{season:00}E{episode:00}", "-{episode:00}", []episodes.Episode{ep(5), ep(6)})
	if got != "S01E05-06" {
		t.Errorf("got %q want %q", got, "S01E05-06")
	}
}

func TestFormatRangeNumberTokens_FoldsToFirstAndLast(t *testing.T) {
	t.Parallel()
	// 4 episodes — Range collapses to first+last.
	got := organizer.FormatRangeNumberTokens("S{season:00}E{episode:00}", "-{episode:00}",
		[]episodes.Episode{ep(5), ep(6), ep(7), ep(8)})
	if got != "S01E05-08" {
		t.Errorf("got %q want %q", got, "S01E05-08")
	}
}

func TestAddSeasonEpisodeNumberingTokens_ExtendsPattern(t *testing.T) {
	t.Parallel()
	pattern := "Show - S{season:00}E{episode:00} - Title"
	handlers := map[string]organizer.TokenHandler{}
	rewritten := organizer.AddSeasonEpisodeNumberingTokens(pattern, handlers,
		[]episodes.Episode{ep(5), ep(6)}, organizer.MultiEpisodeExtend)
	// Original pattern's S01E01 chunk should be replaced with the
	// placeholder {Season Episode1}.
	if rewritten != "Show - {Season Episode1} - Title" {
		t.Errorf("rewritten=%q", rewritten)
	}
	h, ok := handlers[organizer.NormalizeTokenKey("Season Episode1")]
	if !ok {
		t.Fatalf("placeholder handler missing")
	}
	got := h(organizer.TokenMatch{})
	if got != "S01E05-06" {
		t.Errorf("placeholder render=%q want %q", got, "S01E05-06")
	}
}

func TestAddSeasonEpisodeNumberingTokens_RangePicksFirstLast(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.AddSeasonEpisodeNumberingTokens("S{season:00}E{episode:00}", handlers,
		[]episodes.Episode{ep(1), ep(2), ep(3)},
		organizer.MultiEpisodeRange)
	got := handlers[organizer.NormalizeTokenKey("Season Episode1")](organizer.TokenMatch{})
	if got != "S01E01-03" {
		t.Errorf("got %q want %q", got, "S01E01-03")
	}
}

func TestAddSeasonEpisodeNumberingTokens_RegistersEpisodeRangeForMulti(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.AddSeasonEpisodeNumberingTokens("S{season:00}E{episode:00}", handlers,
		[]episodes.Episode{ep(5), ep(6), ep(7)},
		organizer.MultiEpisodeExtend)
	h := handlers[organizer.NormalizeTokenKey("Episode")]
	got := h(organizer.TokenMatch{CustomFormat: "00"})
	if got != "05-07" {
		t.Errorf("got %q want %q", got, "05-07")
	}
}

func TestAddAbsoluteNumberingTokens_NonAnimeStripsPattern(t *testing.T) {
	t.Parallel()
	pattern := "S01E05 - {absolute:000}"
	handlers := map[string]organizer.TokenHandler{}
	rewritten := organizer.AddAbsoluteNumberingTokens(pattern, handlers, nil,
		[]episodes.Episode{epAbs(1, 5, 100)}, organizer.MultiEpisodeExtend, organizer.SeriesTypeStandard)
	if rewritten != "S01E05 - " {
		t.Errorf("rewritten=%q", rewritten)
	}
}

func TestAddAbsoluteNumberingTokens_AnimeRendersAbsolute(t *testing.T) {
	t.Parallel()
	pattern := "{absolute:000}"
	handlers := map[string]organizer.TokenHandler{}
	rewritten := organizer.AddAbsoluteNumberingTokens(pattern, handlers, nil,
		[]episodes.Episode{epAbs(1, 5, 100)}, organizer.MultiEpisodeExtend, organizer.SeriesTypeAnime)
	if rewritten != "{Absolute Pattern1}" {
		t.Errorf("rewritten=%q", rewritten)
	}
	got := handlers[organizer.NormalizeTokenKey("Absolute Pattern1")](organizer.TokenMatch{})
	if got != "100" {
		t.Errorf("placeholder render=%q want %q", got, "100")
	}
}

func TestAddAbsoluteNumberingTokens_AnimeMissingAbsoluteStripsPattern(t *testing.T) {
	t.Parallel()
	// One episode missing AbsoluteEpisodeNumber → strip even though
	// SeriesType is anime.
	pattern := "{absolute:000}"
	handlers := map[string]organizer.TokenHandler{}
	rewritten := organizer.AddAbsoluteNumberingTokens(pattern, handlers, nil,
		[]episodes.Episode{epAbs(1, 5, 100), ep(6)}, organizer.MultiEpisodeExtend, organizer.SeriesTypeAnime)
	if rewritten != "" {
		t.Errorf("rewritten=%q want empty (missing absolute)", rewritten)
	}
}

func TestAddEpisodeTokens_AirDate(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.AddEpisodeTokens(handlers, []episodes.Episode{{AirDate: "2020-05-04"}})
	got := handlers[organizer.NormalizeTokenKey("Air Date")](organizer.TokenMatch{})
	if got != "2020 05 04" {
		t.Errorf("got %q want %q", got, "2020 05 04")
	}
}

func TestAddEpisodeTokens_AirDateMissing(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.AddEpisodeTokens(handlers, []episodes.Episode{{AirDate: ""}})
	got := handlers[organizer.NormalizeTokenKey("Air Date")](organizer.TokenMatch{})
	if got != "Unknown" {
		t.Errorf("got %q want %q", got, "Unknown")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input, formatter, want string
	}{
		{"Hello, World!", "10", "Hello,{ellipsis}"},  // 10-3=7 chars, then {ellipsis}
		{"short", "20", "short"},                     // under limit, unchanged
		{"", "10", ""},                               // empty input
		{"Hello", "", "Hello"},                       // no formatter → unchanged
		{"Hello", "0", "Hello"},                      // formatter=0 → unchanged
		{"Hello, World!", "-10", "{ellipsis}World!"}, // negative → tail-keep (10-3=7 chars)
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input+"|"+tc.formatter, func(t *testing.T) {
			t.Parallel()
			got := organizer.Truncate(tc.input, tc.formatter)
			if got != tc.want {
				t.Errorf("Truncate(%q,%q) = %q want %q", tc.input, tc.formatter, got, tc.want)
			}
		})
	}
}
