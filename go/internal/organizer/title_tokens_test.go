package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/organizer"
)

func TestCleanupEpisodeTitle(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"Episode (1)", "Episode"},
		{"Episode (2)", "Episode"},
		{"Episode Part 1", "Episode"},
		{"Episode Pt. 2", "Episode"},
		{"Episode Pt 3", "Episode"},
		{"Plain Title", "Plain Title"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.CleanupEpisodeTitle(tc.in)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestGetEpisodeTitles_SingleEpisode(t *testing.T) {
	t.Parallel()
	got := organizer.GetEpisodeTitles([]episodes.Episode{{Title: "Title."}})
	if len(got) != 1 || got[0] != "Title" {
		t.Errorf("got %v want [Title]", got)
	}
}

func TestGetEpisodeTitles_MultiDeduplicates(t *testing.T) {
	t.Parallel()
	got := organizer.GetEpisodeTitles([]episodes.Episode{
		{Title: "Foo (1)"},
		{Title: "Foo (2)"},
		{Title: "Bar"},
	})
	if len(got) != 2 || got[0] != "Foo" || got[1] != "Bar" {
		t.Errorf("got %v want [Foo Bar]", got)
	}
}

func TestGetEpisodeTitle_FitsJoinedReturned(t *testing.T) {
	t.Parallel()
	got := organizer.GetEpisodeTitle([]string{"Foo", "Bar"}, "+", 100, "")
	if got != "Foo + Bar" {
		t.Errorf("got %q want %q", got, "Foo + Bar")
	}
}

func TestGetEpisodeTitle_TooLongFirstAndLastFit(t *testing.T) {
	t.Parallel()
	got := organizer.GetEpisodeTitle([]string{"FooLongTitle", "Baz"}, "+", 20, "")
	// Joined: "FooLongTitle + Baz" = 18 chars, ≤ 20, so returns joined.
	if got != "FooLongTitle + Baz" {
		t.Errorf("got %q", got)
	}
}

func TestGetEpisodeTitle_VeryLongTruncatesFirst(t *testing.T) {
	t.Parallel()
	got := organizer.GetEpisodeTitle([]string{"VeryLongFirstTitle"}, "+", 10, "")
	// 10-3=7 chars from "VeryLongFirstTitle", trim trailing space, append {ellipsis}
	if got != "VeryLon{ellipsis}" {
		t.Errorf("got %q", got)
	}
}

func TestGetEpisodeTitle_FormatterShortensLength(t *testing.T) {
	t.Parallel()
	got := organizer.GetEpisodeTitle([]string{"Hello World"}, "+", 100, "8")
	// formatter=8 < maxLength=100, so 8 wins. 8-3=5 chars + ellipsis.
	if got != "Hello{ellipsis}" {
		t.Errorf("got %q", got)
	}
}

func TestRegisterEpisodeFileTokens_BasicSurface(t *testing.T) {
	t.Parallel()
	file := &episodefiles.File{
		SceneName:    "Show.S01E01.1080p.WEB-DL-RELEASE",
		ReleaseGroup: "RELEASE",
		ReleaseHash:  "deadbeef",
		RelativePath: "Season 1/Show - S01E01.mkv",
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterEpisodeFileTokens(handlers, file, true)

	if got := handlers[organizer.NormalizeTokenKey("Original Title")](organizer.TokenMatch{}); got != "Show.S01E01.1080p.WEB-DL-RELEASE" {
		t.Errorf("Original Title: got %q", got)
	}
	if got := handlers[organizer.NormalizeTokenKey("Original Filename")](organizer.TokenMatch{}); got != "Show - S01E01" {
		t.Errorf("Original Filename: got %q", got)
	}
	if got := handlers[organizer.NormalizeTokenKey("Release Group")](organizer.TokenMatch{}); got != "RELEASE" {
		t.Errorf("Release Group: got %q", got)
	}
	if got := handlers[organizer.NormalizeTokenKey("Release Hash")](organizer.TokenMatch{}); got != "deadbeef" {
		t.Errorf("Release Hash: got %q", got)
	}
}

func TestRegisterEpisodeFileTokens_ReleaseGroupDefaultValue(t *testing.T) {
	t.Parallel()
	file := &episodefiles.File{} // empty ReleaseGroup
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterEpisodeFileTokens(handlers, file, false)
	// No prefix/suffix → Sonarr fallback.
	got := handlers[organizer.NormalizeTokenKey("Release Group")](organizer.TokenMatch{})
	if got != "Sonarr" {
		t.Errorf("got %q want Sonarr", got)
	}
	// With prefix → empty (matches DefaultValue contract).
	got2 := handlers[organizer.NormalizeTokenKey("Release Group")](organizer.TokenMatch{Prefix: " ("})
	if got2 != "" {
		t.Errorf("with prefix: got %q want empty", got2)
	}
}

func TestRegisterEpisodeFileTokens_NilFileEmits(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterEpisodeFileTokens(handlers, nil, true)
	for _, name := range []string{"Original Title", "Original Filename", "Release Group", "Release Hash"} {
		got := handlers[organizer.NormalizeTokenKey(name)](organizer.TokenMatch{})
		if got != "" {
			t.Errorf("%s with nil file: got %q want empty", name, got)
		}
	}
}

func TestRegisterEpisodeFileTokens_OriginalTitleFallsBackToFilename(t *testing.T) {
	t.Parallel()
	file := &episodefiles.File{
		// no SceneName
		RelativePath: "Show - S01E01.mkv",
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterEpisodeFileTokens(handlers, file, true)
	got := handlers[organizer.NormalizeTokenKey("Original Title")](organizer.TokenMatch{})
	if got != "Show - S01E01" {
		t.Errorf("got %q", got)
	}
}

func TestAddEpisodeTitleTokens_RegistersBothVariants(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.AddEpisodeTitleTokens(handlers, []episodes.Episode{{Title: "Pilot Episode"}}, 100)

	got := handlers[organizer.NormalizeTokenKey("Episode Title")](organizer.TokenMatch{})
	if got != "Pilot Episode" {
		t.Errorf("Episode Title: got %q", got)
	}
	gotClean := handlers[organizer.NormalizeTokenKey("Episode CleanTitle")](organizer.TokenMatch{})
	if gotClean != "Pilot Episode" {
		t.Errorf("Episode CleanTitle: got %q", gotClean)
	}
}

func TestAddEpisodeTitlePlaceholderTokens_PreserveSentinel(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.AddEpisodeTitlePlaceholderTokens(handlers)
	for _, name := range []string{"Episode Title", "Episode CleanTitle"} {
		got := handlers[organizer.NormalizeTokenKey(name)](organizer.TokenMatch{})
		if got != organizer.TokenPreserve {
			t.Errorf("%s: got %q want TokenPreserve", name, got)
		}
	}
}

func TestReplaceTokens_PreserveSentinelKeepsToken(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{
		organizer.NormalizeTokenKey("Episode Title"): func(organizer.TokenMatch) string {
			return organizer.TokenPreserve
		},
	}
	got := organizer.ReplaceTokens("S01E01 {Episode Title} END", handlers, organizer.DefaultCleanNameOptions(), false)
	if got != "S01E01 {Episode Title} END" {
		t.Errorf("got %q want token preserved", got)
	}
}
