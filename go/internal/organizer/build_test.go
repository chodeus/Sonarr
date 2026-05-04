package organizer_test

import (
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/series"
)

func defaultBuildOpts() organizer.BuildOptions {
	return organizer.BuildOptions{
		RenameEpisodes:           true,
		StandardEpisodeFormat:    "{Series Title} - S{season:00}E{episode:00} - {Episode Title}",
		DailyEpisodeFormat:       "{Series Title} - {Air Date}",
		AnimeEpisodeFormat:       "{Series Title} - {absolute:000}",
		SeriesFolderFormat:       "{Series Title}",
		SeasonFolderFormat:       "Season {season:00}",
		SpecialsFolderFormat:     "Specials",
		MultiEpisodeStyle:        organizer.MultiEpisodeExtend,
		ReplaceIllegalCharacters: true,
		ColonReplacementFormat:   organizer.ColonSmart,
	}
}

func TestBuildFileName_RenameEpisodesFalse(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	opts.RenameEpisodes = false
	file := &episodefiles.File{SceneName: "Show.S01E01.1080p"}
	got, err := organizer.BuildFileName(
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}},
		&series.Series{Title: "Show"},
		file,
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFileName: %v", err)
	}
	if got != "Show.S01E01.1080p.mkv" {
		t.Errorf("RenameEpisodes=false: got %q want %q", got, "Show.S01E01.1080p.mkv")
	}
}

func TestBuildFileName_StandardEpisode(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	got, err := organizer.BuildFileName(
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}},
		&series.Series{Title: "Show", SeriesType: int(organizer.SeriesTypeStandard)},
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFileName: %v", err)
	}
	want := "Show - S01E01 - Pilot.mkv"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestBuildFileName_MultiEpisodeExtend(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	got, err := organizer.BuildFileName(
		[]episodes.Episode{
			{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"},
			{SeasonNumber: 1, EpisodeNumber: 2, Title: "Pilot"}, // dup → dedup → "Pilot"
		},
		&series.Series{Title: "Show"},
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFileName: %v", err)
	}
	if got != "Show - S01E01-02 - Pilot.mkv" {
		t.Errorf("got %q want %q", got, "Show - S01E01-02 - Pilot.mkv")
	}
}

func TestBuildFileName_DailyFormat(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	got, err := organizer.BuildFileName(
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2020-05-04"}},
		&series.Series{Title: "Daily Show", SeriesType: int(organizer.SeriesTypeDaily)},
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFileName: %v", err)
	}
	if got != "Daily Show - 2020 05 04.mkv" {
		t.Errorf("got %q want %q", got, "Daily Show - 2020 05 04.mkv")
	}
}

func TestBuildFileName_AnimeFormat(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	abs := 100
	got, err := organizer.BuildFileName(
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 5, AbsoluteEpisodeNumber: &abs, Title: "Naruto Begins"}},
		&series.Series{Title: "Anime Show", SeriesType: int(organizer.SeriesTypeAnime)},
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFileName: %v", err)
	}
	if got != "Anime Show - 100.mkv" {
		t.Errorf("got %q want %q", got, "Anime Show - 100.mkv")
	}
}

func TestBuildFileName_PathSeparatorSplitsComponents(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	opts.StandardEpisodeFormat = "{Series Title}/Season {season}/{Series Title} - S{season:00}E{episode:00}"
	got, err := organizer.BuildFileName(
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 5, Title: "Title"}},
		&series.Series{Title: "Show"},
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFileName: %v", err)
	}
	// Path separator splits the format; output uses platform separator.
	if !strings.Contains(got, "Show") || !strings.Contains(got, "Season 1") || !strings.Contains(got, "S01E05") {
		t.Errorf("path-split components missing: got %q", got)
	}
}

func TestBuildFileName_RejectsEmptyFormatForType(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	opts.StandardEpisodeFormat = ""
	_, err := organizer.BuildFileName(
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1}},
		&series.Series{Title: "Show", SeriesType: int(organizer.SeriesTypeStandard)},
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if !errors.Is(err, organizer.ErrNamingFormatRequired) {
		t.Errorf("got %v want ErrNamingFormatRequired", err)
	}
}

func TestBuildFilePath_JoinsSeasonPath(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	got, err := organizer.BuildFilePath(
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 5, Title: "Pilot"}},
		&series.Series{Title: "Show", Path: "/tv/Show", SeasonFolder: true},
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFilePath: %v", err)
	}
	want := "/tv/Show/Season 01/Show - S01E05 - Pilot.mkv"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestBuildFilePath_RejectsEmptyExtension(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	_, err := organizer.BuildFilePath(
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1}},
		&series.Series{Title: "Show"},
		&episodefiles.File{},
		"", // empty extension
		opts,
	)
	if err == nil {
		t.Errorf("expected error for empty extension")
	}
}

func TestRequiresEpisodeTitle_True(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts() // StandardEpisodeFormat contains {Episode Title}
	got := organizer.RequiresEpisodeTitle(
		&series.Series{},
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1}},
		opts,
	)
	if !got {
		t.Errorf("expected RequiresEpisodeTitle=true")
	}
}

func TestRequiresEpisodeTitle_False(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	opts.StandardEpisodeFormat = "{Series Title} - S{season:00}E{episode:00}"
	got := organizer.RequiresEpisodeTitle(
		&series.Series{},
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1}},
		opts,
	)
	if got {
		t.Errorf("RequiresEpisodeTitle: got true want false")
	}
}

func TestRequiresEpisodeTitle_RenameOff(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	opts.RenameEpisodes = false
	got := organizer.RequiresEpisodeTitle(
		&series.Series{},
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1}},
		opts,
	)
	if got {
		t.Errorf("RequiresEpisodeTitle (rename off) should be false")
	}
}

func TestRequiresAbsoluteEpisodeNumber(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts() // anime format has {absolute:000}
	if !organizer.RequiresAbsoluteEpisodeNumber(opts) {
		t.Errorf("expected RequiresAbsoluteEpisodeNumber=true")
	}

	opts.AnimeEpisodeFormat = "{Series Title} - S{season:00}E{episode:00}"
	if organizer.RequiresAbsoluteEpisodeNumber(opts) {
		t.Errorf("expected false (no {absolute} in pattern)")
	}
}

func TestBuildFileName_IllegalCharactersInTitleSurvive(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	got, err := organizer.BuildFileName(
		// Note: episode-title trim chars (" .?") strip a trailing `?`
		// before CleanFileName runs (matches C# :80). Use a `?` mid-
		// title to exercise the BadCharacter→GoodCharacter mapping.
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: "Is This OK? Pilot"}},
		&series.Series{Title: "Show: The Beginning"}, // colon → smart replace
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFileName: %v", err)
	}
	// Smart colon: `: ` → ` - `; `?` → `!` via BadCharacters/GoodCharacters.
	if !strings.Contains(got, "Show - The Beginning") {
		t.Errorf("colon smart-replace missing: %q", got)
	}
	if !strings.Contains(got, "OK!") {
		t.Errorf("question-mark replace missing (mid-title): %q", got)
	}
}

func TestBuildFileName_AnimeWithoutAbsoluteFallsBackToStandard(t *testing.T) {
	t.Parallel()
	opts := defaultBuildOpts()
	got, err := organizer.BuildFileName(
		// Anime series but episode missing AbsoluteEpisodeNumber AND
		// AnimeEpisodeFormat requires absolute → falls back to Standard.
		[]episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 5, Title: "Title"}},
		&series.Series{Title: "Anime Show", SeriesType: int(organizer.SeriesTypeAnime)},
		&episodefiles.File{},
		".mkv",
		opts,
	)
	if err != nil {
		t.Fatalf("BuildFileName: %v", err)
	}
	// Should use StandardEpisodeFormat → S01E05.
	if !strings.Contains(got, "S01E05") {
		t.Errorf("expected fallback to Standard: got %q", got)
	}
}
