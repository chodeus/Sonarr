package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

type stubQualityResolver struct{ titles map[int]string }

func (s *stubQualityResolver) GetQualityTitle(q quality.Quality) (string, bool) {
	if t, ok := s.titles[q.ID]; ok {
		return t, true
	}
	return "", false
}

func TestRegisterQualityTokens_BasicFull(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	model := quality.NewModel(quality.HDTV720p)
	resolver := &stubQualityResolver{titles: map[int]string{quality.HDTV720p.ID: "HDTV-720p"}}

	organizer.RegisterQualityTokens(handlers, &series.Series{}, model, resolver)

	if got := handlers[organizer.NormalizeTokenKey("Quality Title")](organizer.TokenMatch{}); got != "HDTV-720p" {
		t.Errorf("Quality Title: got %q", got)
	}
	if got := handlers[organizer.NormalizeTokenKey("Quality Proper")](organizer.TokenMatch{}); got != "" {
		t.Errorf("Quality Proper: got %q want empty (Version=1)", got)
	}
	if got := handlers[organizer.NormalizeTokenKey("Quality Real")](organizer.TokenMatch{}); got != "" {
		t.Errorf("Quality Real: got %q want empty (Real=0)", got)
	}
	if got := handlers[organizer.NormalizeTokenKey("Quality Full")](organizer.TokenMatch{}); got != "HDTV-720p" {
		t.Errorf("Quality Full: got %q (only title, no proper or real)", got)
	}
}

func TestRegisterQualityTokens_ProperVersion(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	model := quality.NewModelWithRevision(quality.HDTV720p, quality.Revision{Version: 2})
	organizer.RegisterQualityTokens(handlers, &series.Series{}, model, nil)
	if got := handlers[organizer.NormalizeTokenKey("Quality Proper")](organizer.TokenMatch{}); got != "Proper" {
		t.Errorf("non-anime Version=2: got %q want Proper", got)
	}
}

func TestRegisterQualityTokens_AnimeVN(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	model := quality.NewModelWithRevision(quality.HDTV720p, quality.Revision{Version: 3})
	organizer.RegisterQualityTokens(handlers,
		&series.Series{SeriesType: int(organizer.SeriesTypeAnime)},
		model, nil)
	if got := handlers[organizer.NormalizeTokenKey("Quality Proper")](organizer.TokenMatch{}); got != "v3" {
		t.Errorf("anime Version=3: got %q want v3", got)
	}
}

func TestRegisterQualityTokens_RealRevision(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	model := quality.NewModelWithRevision(quality.HDTV720p, quality.Revision{Version: 1, Real: 1})
	organizer.RegisterQualityTokens(handlers, &series.Series{}, model, nil)
	if got := handlers[organizer.NormalizeTokenKey("Quality Real")](organizer.TokenMatch{}); got != "REAL" {
		t.Errorf("Real=1: got %q want REAL", got)
	}
}

func TestRegisterQualityTokens_FullJoinsAllParts(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	model := quality.NewModelWithRevision(quality.HDTV720p, quality.Revision{Version: 2, Real: 1})
	organizer.RegisterQualityTokens(handlers, &series.Series{}, model, nil)
	got := handlers[organizer.NormalizeTokenKey("Quality Full")](organizer.TokenMatch{})
	want := "HDTV-720p Proper REAL"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRegisterCustomFormatTokens_AllFormats(t *testing.T) {
	t.Parallel()
	formats := []*customformats.Format{
		{ID: 1, Name: "DV", IncludeCustomFormatWhenRenaming: true},
		{ID: 2, Name: "HDR10", IncludeCustomFormatWhenRenaming: true},
		{ID: 3, Name: "Disabled", IncludeCustomFormatWhenRenaming: false},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterCustomFormatTokens(handlers, formats)
	got := handlers[organizer.NormalizeTokenKey("Custom Formats")](organizer.TokenMatch{})
	if got != "DV HDR10" {
		t.Errorf("default Custom Formats: got %q want DV HDR10", got)
	}
}

func TestRegisterCustomFormatTokens_FilterIncludes(t *testing.T) {
	t.Parallel()
	formats := []*customformats.Format{
		{ID: 1, Name: "DV", IncludeCustomFormatWhenRenaming: true},
		{ID: 2, Name: "HDR10", IncludeCustomFormatWhenRenaming: true},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterCustomFormatTokens(handlers, formats)
	// `:DV` filter restricts to DV.
	got := handlers[organizer.NormalizeTokenKey("Custom Formats")](organizer.TokenMatch{CustomFormat: "DV"})
	if got != "DV" {
		t.Errorf("got %q want DV", got)
	}
}

func TestRegisterCustomFormatTokens_FilterExcludes(t *testing.T) {
	t.Parallel()
	formats := []*customformats.Format{
		{ID: 1, Name: "DV", IncludeCustomFormatWhenRenaming: true},
		{ID: 2, Name: "HDR10", IncludeCustomFormatWhenRenaming: true},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterCustomFormatTokens(handlers, formats)
	// `:-DV` excludes DV → HDR10 remains.
	got := handlers[organizer.NormalizeTokenKey("Custom Formats")](organizer.TokenMatch{CustomFormat: "-DV"})
	if got != "HDR10" {
		t.Errorf("got %q want HDR10", got)
	}
}

func TestRegisterCustomFormatTokens_SingleByName(t *testing.T) {
	t.Parallel()
	formats := []*customformats.Format{
		{ID: 1, Name: "DV", IncludeCustomFormatWhenRenaming: true},
	}
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterCustomFormatTokens(handlers, formats)
	got := handlers[organizer.NormalizeTokenKey("Custom Format")](organizer.TokenMatch{CustomFormat: "DV"})
	if got != "DV" {
		t.Errorf("got %q want DV", got)
	}
	// Unknown name → empty.
	got2 := handlers[organizer.NormalizeTokenKey("Custom Format")](organizer.TokenMatch{CustomFormat: "UnknownName"})
	if got2 != "" {
		t.Errorf("got %q want empty", got2)
	}
	// Empty CustomFormat → empty (matches `:707-710` short-circuit).
	got3 := handlers[organizer.NormalizeTokenKey("Custom Format")](organizer.TokenMatch{})
	if got3 != "" {
		t.Errorf("empty filter: got %q want empty", got3)
	}
}

func TestRegisterMediaInfoStubs_AllRegisterAndReturnEmpty(t *testing.T) {
	t.Parallel()
	handlers := map[string]organizer.TokenHandler{}
	organizer.RegisterMediaInfoStubs(handlers)
	for _, name := range []string{
		"MediaInfo Video", "MediaInfo VideoCodec", "MediaInfo VideoBitDepth",
		"MediaInfo VideoDynamicRange", "MediaInfo VideoDynamicRangeType",
		"MediaInfo Audio", "MediaInfo AudioCodec", "MediaInfo AudioChannels",
		"MediaInfo AudioLanguages", "MediaInfo AudioLanguagesAll",
		"MediaInfo SubtitleLanguages", "MediaInfo SubtitleLanguagesAll",
		"MediaInfo Simple", "MediaInfo Full",
	} {
		h, ok := handlers[organizer.NormalizeTokenKey(name)]
		if !ok {
			t.Errorf("%s: not registered", name)
			continue
		}
		if got := h(organizer.TokenMatch{}); got != "" {
			t.Errorf("%s: got %q want empty (stub)", name, got)
		}
	}
}
