package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/series"
)

func sampleSeriesForOrganizer(title string, year int) *series.Series {
	return &series.Series{
		Title:        title,
		Year:         year,
		ImdbID:       "tt8111088",
		TvdbID:       361753,
		TvMazeID:     12345,
		TmdbID:       82856,
		Path:         "/tv/" + title,
		SeasonFolder: true,
	}
}

func TestRegisterSeriesTokens_AllTokensReturnSomething(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("The Mandalorian", 2019)
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterSeriesTokens(handlers, s)

	expected := map[string]string{
		"Series Title":                    "The Mandalorian",
		"Series CleanTitle":               "The Mandalorian",
		"Series TitleYear":                "The Mandalorian (2019)",
		"Series CleanTitleYear":           "The Mandalorian 2019", // CleanTitle strips brackets
		"Series TitleWithoutYear":         "The Mandalorian",
		"Series CleanTitleWithoutYear":    "The Mandalorian",
		"Series TitleThe":                 "Mandalorian, The",
		"Series CleanTitleThe":            "Mandalorian, The",
		"Series TitleTheYear":             "Mandalorian, The (2019)",
		"Series CleanTitleTheYear":        "Mandalorian, The 2019",
		"Series TitleTheWithoutYear":      "Mandalorian, The",
		"Series CleanTitleTheWithoutYear": "Mandalorian, The",
		"Series TitleFirstCharacter":      "M",
		"Series Year":                     "2019",
	}
	for tokenName, want := range expected {
		key := organizer.NormalizeTokenKey(tokenName)
		h, ok := handlers[key]
		if !ok {
			t.Errorf("token %q (key %q) missing from registered handlers", tokenName, key)
			continue
		}
		got := h(organizer.TokenMatch{})
		if got != want {
			t.Errorf("%s: got %q want %q", tokenName, got, want)
		}
	}
}

func TestRegisterSeriesTokens_NilYearProducesEmptyYear(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("Show", 0)
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterSeriesTokens(handlers, s)

	got := handlers[organizer.NormalizeTokenKey("Series Year")](organizer.TokenMatch{})
	if got != "" {
		t.Errorf("Series Year (no year): got %q want empty", got)
	}
	got = handlers[organizer.NormalizeTokenKey("Series TitleYear")](organizer.TokenMatch{})
	if got != "Show" {
		t.Errorf("Series TitleYear (no year): got %q want %q", got, "Show")
	}
}

func TestRegisterIDTokens(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("Show", 2019)
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterIDTokens(handlers, s)

	cases := map[string]string{
		"ImdbId":   "tt8111088",
		"TvdbId":   "361753",
		"TvMazeId": "12345",
		"TmdbId":   "82856",
	}
	for tokenName, want := range cases {
		got := handlers[organizer.NormalizeTokenKey(tokenName)](organizer.TokenMatch{})
		if got != want {
			t.Errorf("%s: got %q want %q", tokenName, got, want)
		}
	}
}

func TestRegisterIDTokens_ZeroValuesEmitEmpty(t *testing.T) {
	t.Parallel()
	s := &series.Series{TvdbID: 100} // TvMazeID, TmdbID = 0; ImdbID = ""
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterIDTokens(handlers, s)

	if got := handlers[organizer.NormalizeTokenKey("ImdbId")](organizer.TokenMatch{}); got != "" {
		t.Errorf("ImdbId empty: got %q", got)
	}
	if got := handlers[organizer.NormalizeTokenKey("TvMazeId")](organizer.TokenMatch{}); got != "" {
		t.Errorf("TvMazeId zero: got %q", got)
	}
	if got := handlers[organizer.NormalizeTokenKey("TmdbId")](organizer.TokenMatch{}); got != "" {
		t.Errorf("TmdbId zero: got %q", got)
	}
	// TvdbId emits "100" — non-conditional.
	if got := handlers[organizer.NormalizeTokenKey("TvdbId")](organizer.TokenMatch{}); got != "100" {
		t.Errorf("TvdbId: got %q want 100", got)
	}
}

func TestRegisterSeasonTokens_HonoursCustomFormat(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterSeasonTokens(handlers, 5)
	h := handlers[organizer.NormalizeTokenKey("Season")]
	if got := h(organizer.TokenMatch{CustomFormat: ""}); got != "5" {
		t.Errorf("default: got %q want 5", got)
	}
	if got := h(organizer.TokenMatch{CustomFormat: "00"}); got != "05" {
		t.Errorf("zero-pad: got %q want 05", got)
	}
	if got := h(organizer.TokenMatch{CustomFormat: "0000"}); got != "0005" {
		t.Errorf("4-digit pad: got %q want 0005", got)
	}
}

func TestReplaceReservedDeviceNames(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"AUX.txt", "AUX_txt"},
		{"COM1.dat", "COM1_dat"},
		{"con.log", "con_log"},
		{"NUL.foo", "NUL_foo"},
		{"PRN.txt", "PRN_txt"},
		{"safe.txt", "safe.txt"},
		{"AUXfoo.txt", "AUXfoo.txt"},         // not reserved (no dot after)
		{"folder/AUX.txt", "folder/AUX.txt"}, // anchored at start; mid-string ignored
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.ReplaceReservedDeviceNames(tc.in)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestGetSeriesFolder(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("The Mandalorian", 2019)
	opts := organizer.FolderOptions{
		SeriesFolderFormat: "{Series Title}",
		Clean:              organizer.DefaultCleanNameOptions(),
	}
	got := organizer.GetSeriesFolder(s, opts)
	if got != "The Mandalorian" {
		t.Errorf("got %q want %q", got, "The Mandalorian")
	}
}

func TestGetSeriesFolder_TitleYearFormat(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("The Mandalorian", 2019)
	opts := organizer.FolderOptions{
		SeriesFolderFormat: "{Series TitleYear}",
		Clean:              organizer.DefaultCleanNameOptions(),
	}
	got := organizer.GetSeriesFolder(s, opts)
	if got != "The Mandalorian (2019)" {
		t.Errorf("got %q", got)
	}
}

func TestGetSeriesFolder_SeparatorVariants(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("The Mandalorian", 2019)
	cases := []struct{ format, want string }{
		{"{Series.Title}", "The.Mandalorian"},
		{"{Series_Title}", "The_Mandalorian"},
		{"{Series-Title}", "The-Mandalorian"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.format, func(t *testing.T) {
			t.Parallel()
			opts := organizer.FolderOptions{
				SeriesFolderFormat: tc.format,
				Clean:              organizer.DefaultCleanNameOptions(),
			}
			got := organizer.GetSeriesFolder(s, opts)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestGetSeasonFolder_RegularSeason(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("Show", 2020)
	opts := organizer.FolderOptions{
		SeasonFolderFormat:   "Season {season:00}",
		SpecialsFolderFormat: "Specials",
		Clean:                organizer.DefaultCleanNameOptions(),
	}
	got := organizer.GetSeasonFolder(s, 3, opts)
	if got != "Season 03" {
		t.Errorf("got %q want %q", got, "Season 03")
	}
}

func TestGetSeasonFolder_Season0UsesSpecialsFormat(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("Show", 2020)
	opts := organizer.FolderOptions{
		SeasonFolderFormat:   "Season {season:00}",
		SpecialsFolderFormat: "Specials",
		Clean:                organizer.DefaultCleanNameOptions(),
	}
	got := organizer.GetSeasonFolder(s, 0, opts)
	if got != "Specials" {
		t.Errorf("got %q want %q", got, "Specials")
	}
}

func TestGetSeasonFolder_SeriesTokensInsideFormat(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("Show", 2020)
	opts := organizer.FolderOptions{
		SeasonFolderFormat:   "{Series Title} S{season:00}",
		SpecialsFolderFormat: "Specials",
		Clean:                organizer.DefaultCleanNameOptions(),
	}
	got := organizer.GetSeasonFolder(s, 1, opts)
	if got != "Show S01" {
		t.Errorf("got %q want %q", got, "Show S01")
	}
}

func TestBuildSeasonPath_WithSeasonFolder(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("Show", 2020)
	s.Path = "/tv/Show"
	opts := organizer.FolderOptions{
		SeasonFolderFormat:   "Season {season:00}",
		SpecialsFolderFormat: "Specials",
		Clean:                organizer.DefaultCleanNameOptions(),
	}
	got := organizer.BuildSeasonPath(s, 2, opts)
	want := "/tv/Show/Season 02"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestBuildSeasonPath_WithoutSeasonFolderUsesRoot(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("Show", 2020)
	s.Path = "/tv/Show"
	s.SeasonFolder = false
	opts := organizer.FolderOptions{
		SeasonFolderFormat: "Season {season:00}",
	}
	got := organizer.BuildSeasonPath(s, 2, opts)
	if got != "/tv/Show" {
		t.Errorf("got %q want %q", got, "/tv/Show")
	}
}

func TestGetSeriesFolder_ReservedDeviceNames(t *testing.T) {
	t.Parallel()
	s := sampleSeriesForOrganizer("AUX.tv", 2020)
	opts := organizer.FolderOptions{
		SeriesFolderFormat: "{Series Title}",
		Clean:              organizer.DefaultCleanNameOptions(),
	}
	got := organizer.GetSeriesFolder(s, opts)
	if got != "AUX_tv" {
		t.Errorf("got %q want AUX_tv (reserved name protected)", got)
	}
}
