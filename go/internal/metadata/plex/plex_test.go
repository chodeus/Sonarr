package plex_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadata/plex"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

// fakeMappings implements plex.MappingsLookup.
type fakeMappings struct {
	eps      []episodes.Episode
	files    []episodefiles.File
	epsErr   error
	filesErr error
}

func (f *fakeMappings) EpisodesBySeries(_ context.Context, _ int) ([]episodes.Episode, error) {
	return f.eps, f.epsErr
}

func (f *fakeMappings) FilesBySeries(_ context.Context, _ int) ([]episodefiles.File, error) {
	return f.files, f.filesErr
}

func newPlexFor(t *testing.T, settings plex.Settings, deps plex.Deps) *plex.PlexMetadata {
	t.Helper()
	m, err := plex.NewPlexMetadata(&metadata.Metadata{ID: 1}, settings, deps)
	if err != nil {
		t.Fatalf("NewPlexMetadata: %v", err)
	}
	return m
}

func TestDefaultSettings(t *testing.T) {
	t.Parallel()
	s := plex.DefaultSettings()
	if !s.SeriesPlexMatchFile {
		t.Errorf("SeriesPlexMatchFile default should be true")
	}
	if s.EpisodeMappings {
		t.Errorf("EpisodeMappings default should be false")
	}
}

func TestSettings_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	want := plex.DefaultSettings()
	body, err := json.Marshal(&want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got plex.Settings
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("roundtrip drift: got %+v want %+v", got, want)
	}
}

func TestSettings_JSONOptionA(t *testing.T) {
	t.Parallel()
	// Both keys present at zero value (Option A).
	body, _ := json.Marshal(&plex.Settings{})
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	for _, k := range []string{"seriesPlexMatchFile", "episodeMappings"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing JSON key %q", k)
		}
	}
	if len(raw) != 2 {
		t.Errorf("got %d keys, want 2: %+v", len(raw), raw)
	}
}

func TestPlexMetadata_Name(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	if got := m.Name(); got != "Plex" {
		t.Errorf("Name = %q", got)
	}
}

func TestPlexMetadata_SatisfiesProviderInterface(t *testing.T) {
	t.Parallel()
	var p metadata.Provider = newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	if p.Name() != "Plex" {
		t.Errorf("Name through interface = %q", p.Name())
	}
}

func TestPlexMetadata_FindMetadataFile_PlexMatch(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	got, err := m.FindMetadataFile(s, "/tv/Show/.plexmatch")
	if err != nil {
		t.Fatalf("FindMetadataFile: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want .plexmatch classification")
	}
	if metadata.MetadataType(got.Type) != metadata.MetadataTypeSeriesMetadata {
		t.Errorf("Type = %s, want SeriesMetadata", metadata.MetadataType(got.Type))
	}
	if got.SeriesID != 7 || got.Consumer != "PlexMetadata" || got.RelativePath != ".plexmatch" {
		t.Errorf("base fields: %+v", got)
	}
}

func TestPlexMetadata_FindMetadataFile_NonPlexMatchIsNil(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	for _, name := range []string{"tvshow.nfo", "poster.jpg", "S01E02.nfo", "Season 1/S01E02-thumb.jpg"} {
		got, err := m.FindMetadataFile(s, "/tv/Show/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != nil {
			t.Errorf("%s should be nil-row, got %+v", name, got)
		}
	}
}

func TestPlexMetadata_FindMetadataFile_NotChildOfSeriesPath(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	_, err := m.FindMetadataFile(s, "/elsewhere/.plexmatch")
	if err == nil {
		t.Errorf("expected error for non-child path")
	}
}

func TestPlexMetadata_GetFilenameAfterMove_DefaultJoin(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	s := &series.Series{Path: "/tv/Show"}
	mf := &metadatafiles.MetadataFile{RelativePath: ".plexmatch"}
	got := m.GetFilenameAfterMove(s, &episodefiles.File{}, mf)
	if got != "/tv/Show/.plexmatch" {
		t.Errorf("GetFilenameAfterMove = %q", got)
	}
}

func TestPlexMetadata_GetFilenameAfterMove_NilArgs(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	if got := m.GetFilenameAfterMove(nil, nil, nil); got != "" {
		t.Errorf("nil args should return empty, got %q", got)
	}
}

func TestPlexMetadata_SeriesMetadata_HappyPath_HeaderOnly(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	s := &series.Series{ID: 7, Title: "Breaking Bad", Year: 2008, TvdbID: 81189, ImdbID: "tt0903747"}
	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("SeriesMetadata: %v", err)
	}
	if res.RelativePath != ".plexmatch" {
		t.Errorf("RelativePath = %q", res.RelativePath)
	}
	want := "Title: Breaking Bad\nYear: 2008\nTvdbId: 81189\nImdbId: tt0903747\n"
	if res.Contents != want {
		t.Errorf("Contents:\n got:  %q\nwant: %q", res.Contents, want)
	}
}

func TestPlexMetadata_SeriesMetadata_DisabledSetting(t *testing.T) {
	t.Parallel()
	settings := plex.DefaultSettings()
	settings.SeriesPlexMatchFile = false
	m := newPlexFor(t, settings, plex.Deps{})
	res, err := m.SeriesMetadata(t.Context(), &series.Series{Title: "X"}, metadata.SeriesMetadataReasonScan)
	if err != nil || res.RelativePath != "" || res.Contents != "" {
		t.Errorf("disabled: res=%+v err=%v, want zero-value", res, err)
	}
}

func TestPlexMetadata_SeriesMetadata_EpisodeMappings(t *testing.T) {
	t.Parallel()
	settings := plex.DefaultSettings()
	settings.EpisodeMappings = true

	fileID1 := 100
	fileID2 := 101
	fileID3 := 102 // specials
	mappings := &fakeMappings{
		files: []episodefiles.File{
			{ID: 100, SeasonNumber: 1, RelativePath: "Season 1/S01E01.mkv"},
			{ID: 101, SeasonNumber: 1, RelativePath: "Season 1/S01E02-E03.mkv"}, // multi-episode
			{ID: 102, SeasonNumber: 0, RelativePath: "Specials/SP01.mkv"},
		},
		eps: []episodes.Episode{
			{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, EpisodeFileID: &fileID1},
			// Multi-episode file gets two rows pointing at the same fileID.
			{ID: 3, SeasonNumber: 1, EpisodeNumber: 3, EpisodeFileID: &fileID2}, // out of order on purpose
			{ID: 2, SeasonNumber: 1, EpisodeNumber: 2, EpisodeFileID: &fileID2},
			{ID: 99, SeasonNumber: 0, EpisodeNumber: 1, EpisodeFileID: &fileID3},
		},
	}
	m := newPlexFor(t, settings, plex.Deps{Mappings: mappings})
	s := &series.Series{ID: 7, Title: "X", Year: 2020, TvdbID: 99}
	res, err := m.SeriesMetadata(t.Context(), s, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("SeriesMetadata: %v", err)
	}
	lines := strings.Split(strings.TrimRight(res.Contents, "\n"), "\n")
	// 4 header lines + 3 episode lines = 7.
	if len(lines) != 7 {
		t.Fatalf("got %d lines, want 7:\n%s", len(lines), res.Contents)
	}
	// Verify the header lines.
	if lines[0] != "Title: X" || lines[1] != "Year: 2020" || lines[2] != "TvdbId: 99" || lines[3] != "ImdbId: " {
		t.Errorf("header lines: %+v", lines[:4])
	}
	// Episode lines (file order: 100, 101, 102).
	if lines[4] != "Episode: S01E01: Season 1/S01E01.mkv" {
		t.Errorf("line 4 = %q", lines[4])
	}
	// Multi-episode file should sort E02, E03 (ascending) regardless of input order.
	if lines[5] != "Episode: S01E02-E03: Season 1/S01E02-E03.mkv" {
		t.Errorf("line 5 (multi-ep ordering): %q", lines[5])
	}
	// Specials use SP<n> with only the first episode's number.
	if lines[6] != "Episode: SP01: Specials/SP01.mkv" {
		t.Errorf("line 6 (specials format): %q", lines[6])
	}
}

func TestPlexMetadata_SeriesMetadata_EpisodeMappings_NilMappingsNoOp(t *testing.T) {
	t.Parallel()
	settings := plex.DefaultSettings()
	settings.EpisodeMappings = true
	// Mappings dep is nil — feature is silently no-op.
	m := newPlexFor(t, settings, plex.Deps{})
	res, _ := m.SeriesMetadata(t.Context(), &series.Series{Title: "X", Year: 2020}, metadata.SeriesMetadataReasonScan)
	want := "Title: X\nYear: 2020\nTvdbId: 0\nImdbId: \n"
	if res.Contents != want {
		t.Errorf("nil-mappings body should be header-only:\n got:  %q\nwant: %q", res.Contents, want)
	}
}

func TestPlexMetadata_SeriesMetadata_EpisodeMappings_ErrorPropagates(t *testing.T) {
	t.Parallel()
	settings := plex.DefaultSettings()
	settings.EpisodeMappings = true
	mappings := &fakeMappings{filesErr: errors.New("db down")}
	m := newPlexFor(t, settings, plex.Deps{Mappings: mappings})
	_, err := m.SeriesMetadata(t.Context(), &series.Series{Title: "X"}, metadata.SeriesMetadataReasonScan)
	if err == nil {
		t.Errorf("expected error from FilesBySeries lookup")
	}
}

func TestPlexMetadata_SeriesMetadata_EpisodeMappings_FilesWithoutEpisodes(t *testing.T) {
	t.Parallel()
	settings := plex.DefaultSettings()
	settings.EpisodeMappings = true
	// 1 file but no linked episodes — file should be SKIPPED.
	mappings := &fakeMappings{
		files: []episodefiles.File{{ID: 1, SeasonNumber: 1, RelativePath: "S01E01.mkv"}},
		eps:   nil,
	}
	m := newPlexFor(t, settings, plex.Deps{Mappings: mappings})
	res, _ := m.SeriesMetadata(t.Context(), &series.Series{Title: "X"}, metadata.SeriesMetadataReasonScan)
	if strings.Contains(res.Contents, "Episode:") {
		t.Errorf("file without episodes should be skipped, got: %q", res.Contents)
	}
}

func TestPlexMetadata_EpisodeMetadata_NoOp(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	res, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1})
	if err != nil || res.RelativePath != "" || res.Contents != "" {
		t.Errorf("EpisodeMetadata should always be no-op: %+v / %v", res, err)
	}
}

func TestPlexMetadata_AllImagesAreNoOp(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	ctx := t.Context()
	imgs, err := m.SeriesImages(ctx, &series.Series{})
	if err != nil || len(imgs) != 0 {
		t.Errorf("SeriesImages: %+v / %v", imgs, err)
	}
	imgs, err = m.SeasonImages(ctx, &series.Series{}, metadata.Season{SeasonNumber: 1})
	if err != nil || len(imgs) != 0 {
		t.Errorf("SeasonImages: %+v / %v", imgs, err)
	}
	imgs, err = m.EpisodeImages(ctx, &series.Series{}, &episodefiles.File{ID: 1})
	if err != nil || len(imgs) != 0 {
		t.Errorf("EpisodeImages: %+v / %v", imgs, err)
	}
}

func TestPlexMetadata_TestAlwaysSucceeds(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	f, err := m.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if f != nil {
		t.Errorf("Test failures = %+v, want nil", f)
	}
}

func TestPlexMetadata_RequestAction_NoOp(t *testing.T) {
	t.Parallel()
	m := newPlexFor(t, plex.DefaultSettings(), plex.Deps{})
	res, err := m.RequestAction(t.Context(), "anything", map[string]string{"k": "v"})
	if err != nil || res != nil {
		t.Errorf("RequestAction: res=%v err=%v", res, err)
	}
}

func TestPlexMetadata_NilReceiver(t *testing.T) {
	t.Parallel()
	var m *plex.PlexMetadata
	if m.Name() != "Plex" {
		t.Errorf("nil Name = %q", m.Name())
	}
	if d := m.Definition(); d != nil {
		t.Errorf("nil Definition = %+v", d)
	}
	if m.SettingsConfig() != (plex.Settings{}) {
		t.Errorf("nil SettingsConfig = %+v", m.SettingsConfig())
	}
}

// TestRegistration_DefaultRegistry verifies the init() side effect.
func TestRegistration_DefaultRegistry(t *testing.T) {
	t.Parallel()
	if !metadata.Default().Has(plex.ImplementationName, plex.ConfigContractName) {
		t.Errorf("metadata.Default() missing entry for (%q, %q)", plex.ImplementationName, plex.ConfigContractName)
	}
}

func TestRegistration_Construct(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             7,
		Implementation: plex.ImplementationName,
		ConfigContract: plex.ConfigContractName,
		Settings:       `{"seriesPlexMatchFile":true,"episodeMappings":true}`,
	}
	p, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if p.Name() != "Plex" {
		t.Errorf("Name = %q", p.Name())
	}
	pm, ok := p.(*plex.PlexMetadata)
	if !ok {
		t.Fatalf("Construct returned %T, want *plex.PlexMetadata", p)
	}
	if !pm.SettingsConfig().EpisodeMappings {
		t.Errorf("Settings did not deserialise — EpisodeMappings should be true")
	}
}

func TestRegistration_Construct_EmptySettingsTakesDefaults(t *testing.T) {
	t.Parallel()
	for _, blob := range []string{"", "{}", "   "} {
		def := &metadata.Metadata{
			ID:             1,
			Implementation: plex.ImplementationName,
			ConfigContract: plex.ConfigContractName,
			Settings:       blob,
		}
		p, err := metadata.Default().Construct(def, metadata.Dependencies{})
		if err != nil {
			t.Fatalf("blob=%q: %v", blob, err)
		}
		pm := p.(*plex.PlexMetadata)
		if !pm.SettingsConfig().SeriesPlexMatchFile {
			t.Errorf("blob=%q: default SeriesPlexMatchFile not applied", blob)
		}
	}
}

func TestRegistration_Construct_MalformedSettings(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             1,
		Implementation: plex.ImplementationName,
		ConfigContract: plex.ConfigContractName,
		Settings:       `{not json`,
	}
	_, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err == nil {
		t.Errorf("expected error on malformed Settings")
	}
}
