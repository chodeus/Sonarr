package indexersearch

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/scenemappings"
	"sonarr.tv/sonarr/internal/series"
)

func intPtr(v int) *int { return &v }

func TestNonNegative(t *testing.T) {
	if got := nonNegative(nil); got != nil {
		t.Errorf("nil → nil expected")
	}
	if got := nonNegative(intPtr(-1)); got != nil {
		t.Errorf("negative → nil expected")
	}
	if got := nonNegative(intPtr(0)); got == nil || *got != 0 {
		t.Errorf("zero → 0 expected")
	}
	if got := nonNegative(intPtr(7)); got == nil || *got != 7 {
		t.Errorf("positive → value expected")
	}
}

func TestCleanSeriesTitle(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"The Office":      "theoffice",
		"30 Rock":         "30rock",
		"Brooklyn Nine-9": "brooklynnine9",
		"It's Always":     "itsalways",
	}
	for in, want := range cases {
		if got := cleanSeriesTitle(in); got != want {
			t.Errorf("cleanSeriesTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDistinctTitles(t *testing.T) {
	in := []string{"Foo", "Bar", "FOO", "baz", "BaZ"}
	out := distinctTitles(in)
	if len(out) != 3 {
		t.Fatalf("len=%d, want 3", len(out))
	}
	if out[0] != "Foo" || out[1] != "Bar" || out[2] != "baz" {
		t.Errorf("preserve-first-casing failed: %v", out)
	}
}

func TestDistinctEpisodes(t *testing.T) {
	in := []episodes.Episode{{ID: 1}, {ID: 2}, {ID: 1}, {ID: 3}}
	out := distinctEpisodes(in)
	if len(out) != 3 {
		t.Fatalf("len=%d, want 3", len(out))
	}
	got := []int{out[0].ID, out[1].ID, out[2].ID}
	want := []int{1, 2, 3}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

// --- getSceneEpisodeMappings (inner, no IO) ---

func TestGetSceneEpisodeMappings_NoRowsAddsImplicitGlobal(t *testing.T) {
	s := &series.Series{Title: "MyShow", TvdbID: 100}
	ep := &episodes.Episode{SeasonNumber: 2, EpisodeNumber: 5}
	got := getSceneEpisodeMappings(s, ep, nil)
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (implicit global)", len(got))
	}
	if got[0].SceneTitles[0] != "MyShow" {
		t.Errorf("scene title %v, want MyShow", got[0].SceneTitles)
	}
	if got[0].SeasonNumber != 2 || got[0].EpisodeNumber != 5 {
		t.Errorf("season/episode mismatch: %+v", got[0])
	}
}

func TestGetSceneEpisodeMappings_ExplicitNameSuppressesGlobal(t *testing.T) {
	s := &series.Series{Title: "MyShow", TvdbID: 100}
	ep := &episodes.Episode{SeasonNumber: 1, EpisodeNumber: 1}
	rows := []scenemappings.Mapping{
		{SearchTerm: "MyShow"}, // matches series.Title and FilterRegex empty
	}
	got := getSceneEpisodeMappings(s, ep, rows)
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (explicit only)", len(got))
	}
	if got[0].SceneTitles[0] != "MyShow" {
		t.Errorf("expected explicit mapping, got %v", got[0])
	}
}

func TestGetSceneEpisodeMappings_FiltersByMappingSeasonNumber(t *testing.T) {
	s := &series.Series{Title: "MyShow"}
	ep := &episodes.Episode{SeasonNumber: 2, EpisodeNumber: 1}
	rows := []scenemappings.Mapping{
		{SearchTerm: "Wrong", SeasonNumber: intPtr(99)}, // skipped
		{SearchTerm: "Right", SeasonNumber: intPtr(2)},  // kept
	}
	got := getSceneEpisodeMappings(s, ep, rows)
	// Right + implicit global = 2.
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].SceneTitles[0] != "Right" {
		t.Errorf("first should be 'Right', got %v", got[0])
	}
}

func TestGetSceneEpisodeMappings_IgnoreSceneNumberingUsesEpisodeSeason(t *testing.T) {
	s := &series.Series{Title: "MyShow"}
	ep := &episodes.Episode{
		SeasonNumber:               4,
		EpisodeNumber:              7,
		SceneSeasonNumber:          intPtr(2),
		SceneEpisodeNumber:         intPtr(3),
		AbsoluteEpisodeNumber:      intPtr(50),
		SceneAbsoluteEpisodeNumber: intPtr(60),
	}
	rows := []scenemappings.Mapping{
		{SearchTerm: "Tvdb", SceneOrigin: "tvdb"},
	}
	got := getSceneEpisodeMappings(s, ep, rows)
	if len(got) < 1 {
		t.Fatalf("expected at least 1 mapping")
	}
	m := got[0]
	if m.SeasonNumber != 4 || m.EpisodeNumber != 7 {
		t.Errorf("ignoreSceneNumbering should use episode.SeasonNumber/EpisodeNumber, got s=%d e=%d", m.SeasonNumber, m.EpisodeNumber)
	}
	if m.AbsoluteEpisodeNumber == nil || *m.AbsoluteEpisodeNumber != 50 {
		t.Errorf("ignoreSceneNumbering should use AbsoluteEpisodeNumber=50, got %v", m.AbsoluteEpisodeNumber)
	}
}

func TestGetSceneEpisodeMappings_SceneNumberingUsesSceneFields(t *testing.T) {
	s := &series.Series{Title: "MyShow"}
	ep := &episodes.Episode{
		SeasonNumber:       4,
		EpisodeNumber:      7,
		SceneSeasonNumber:  intPtr(2),
		SceneEpisodeNumber: intPtr(3),
	}
	rows := []scenemappings.Mapping{
		{SearchTerm: "ScenePath", SceneOrigin: "scene"},
	}
	got := getSceneEpisodeMappings(s, ep, rows)
	if len(got) < 1 {
		t.Fatalf("expected at least 1 mapping")
	}
	m := got[0]
	// non-tvdb scene-origin → use scene fields when present.
	if m.SeasonNumber != 2 {
		t.Errorf("expected SeasonNumber=2 (from SceneSeasonNumber), got %d", m.SeasonNumber)
	}
	if m.EpisodeNumber != 3 {
		t.Errorf("expected EpisodeNumber=3 (from SceneEpisodeNumber), got %d", m.EpisodeNumber)
	}
}

// --- getSceneEpisodeMappingsForEpisode (with stub mapper) ---

type stubMapper struct {
	rows         []scenemappings.Mapping
	rowsErr      error
	names        []string
	namesErr     error
	calledTvdbID int
}

func (m *stubMapper) FindByTvdbID(_ context.Context, tvdbID int) ([]scenemappings.Mapping, error) {
	m.calledTvdbID = tvdbID
	return m.rows, m.rowsErr
}

func (m *stubMapper) GetSceneNames(_ context.Context, _ int, _, _ []int) ([]string, error) {
	return m.names, m.namesErr
}

func TestGetSceneEpisodeMappingsForEpisode_DedupsByKey(t *testing.T) {
	s := &series.Series{Title: "MyShow", TvdbID: 100}
	ep := &episodes.Episode{SeasonNumber: 1, EpisodeNumber: 2}
	mapper := &stubMapper{
		rows: []scenemappings.Mapping{
			{SearchTerm: "AltA"},
			{SearchTerm: "AltB"},
			{SearchTerm: "Alta"}, // case-insensitive duplicate of AltA
		},
	}
	got, err := getSceneEpisodeMappingsForEpisode(context.Background(), mapper, s, ep)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// All three rows have identical (SeasonNumber=1, EpisodeNumber=2,
	// SearchMode=Default) → collapse into one entry whose SceneTitles
	// contains the de-duped union (AltA, AltB), preserving order.
	// Plus the implicit global ("MyShow") merges into the same key.
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	titles := got[0].SceneTitles
	wantTitles := []string{"AltA", "AltB", "MyShow"}
	if len(titles) != len(wantTitles) {
		t.Fatalf("titles=%v, want %v", titles, wantTitles)
	}
	for i := range titles {
		if titles[i] != wantTitles[i] {
			t.Errorf("idx %d: got %q want %q", i, titles[i], wantTitles[i])
		}
	}
}

func TestGetSceneEpisodeMappingsForEpisode_PropagatesError(t *testing.T) {
	s := &series.Series{Title: "MyShow", TvdbID: 100}
	ep := &episodes.Episode{SeasonNumber: 1, EpisodeNumber: 2}
	mapper := &stubMapper{rowsErr: errors.New("db gone")}
	if _, err := getSceneEpisodeMappingsForEpisode(context.Background(), mapper, s, ep); err == nil {
		t.Fatalf("expected error to propagate")
	}
}

// --- getSceneSeasonMappings ---

func TestGetSceneSeasonMappings_GroupsAndMerges(t *testing.T) {
	s := &series.Series{Title: "MyShow", TvdbID: 100}
	eps := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 2},
	}
	mapper := &stubMapper{}
	got, err := getSceneSeasonMappings(context.Background(), mapper, s, eps)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// No mappings → only the implicit global → one SceneSeasonMapping
	// covering both episodes.
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].SeasonNumber != 1 {
		t.Errorf("season=%d, want 1", got[0].SeasonNumber)
	}
	if len(got[0].Episodes) != 2 {
		t.Errorf("episodes=%d, want 2", len(got[0].Episodes))
	}
}

func TestGetSceneSeasonMappings_EmptyEpisodesReturnsNil(t *testing.T) {
	s := &series.Series{Title: "MyShow"}
	got, err := getSceneSeasonMappings(context.Background(), &stubMapper{}, s, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
