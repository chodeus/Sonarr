package xbmc_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadata/xbmc"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

func newDetectorFor(t *testing.T, files map[string]string) *xbmc.NfoDetector {
	t.Helper()
	fs := &fakeNfoFS{files: files}
	d, err := xbmc.NewNfoDetector(fs)
	if err != nil {
		t.Fatalf("NewNfoDetector: %v", err)
	}
	return d
}

func newXbmcFor(t *testing.T, files map[string]string) *xbmc.XbmcMetadata {
	t.Helper()
	def := &metadata.Metadata{ID: 1, Implementation: xbmc.ImplementationName, ConfigContract: xbmc.ConfigContractName}
	m, err := xbmc.NewXbmcMetadata(def, xbmc.DefaultSettings(), xbmc.Deps{Detector: newDetectorFor(t, files)})
	if err != nil {
		t.Fatalf("NewXbmcMetadata: %v", err)
	}
	return m
}

func TestNewXbmcMetadata_RejectsNilDetector(t *testing.T) {
	t.Parallel()
	if _, err := xbmc.NewXbmcMetadata(nil, xbmc.DefaultSettings(), xbmc.Deps{}); err == nil {
		t.Errorf("expected error on nil deps.Detector")
	}
}

func TestXbmcMetadata_Name(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	if got := m.Name(); got != "Kodi (XBMC) / Emby" {
		t.Errorf("Name() = %q, want %q", got, "Kodi (XBMC) / Emby")
	}
}

func TestXbmcMetadata_Definition_RoundTrips(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{ID: 42, Name: "Kodi", Implementation: xbmc.ImplementationName, ConfigContract: xbmc.ConfigContractName}
	m, _ := xbmc.NewXbmcMetadata(def, xbmc.DefaultSettings(), xbmc.Deps{Detector: newDetectorFor(t, nil)})
	if got := m.Definition(); got == nil || got.ID != 42 {
		t.Errorf("Definition = %+v", got)
	}
}

func TestXbmcMetadata_GetFilenameAfterMove(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	s := &series.Series{ID: 1, Path: "/tv/Show"}
	ef := &episodefiles.File{RelativePath: "Season 1/S01E02.mkv"}

	// EpisodeMetadata → swap ext to .nfo.
	mf := &metadatafiles.MetadataFile{Type: int(metadata.MetadataTypeEpisodeMetadata), RelativePath: "Season 1/S01E02.nfo"}
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/Season 1/S01E02.nfo" {
		t.Errorf("EpisodeMetadata: got %q", got)
	}

	// EpisodeImage → strip ext + "-thumb.jpg".
	mf.Type = int(metadata.MetadataTypeEpisodeImage)
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/Season 1/S01E02-thumb.jpg" {
		t.Errorf("EpisodeImage: got %q", got)
	}

	// Other types → fall back to Path.Combine(series.Path, RelativePath).
	mf.Type = int(metadata.MetadataTypeSeriesMetadata)
	mf.RelativePath = "tvshow.nfo"
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/tvshow.nfo" {
		t.Errorf("SeriesMetadata fallback: got %q", got)
	}

	// Nil receiver / nil args don't panic.
	if got := m.GetFilenameAfterMove(nil, ef, mf); got != "" {
		t.Errorf("nil series: got %q", got)
	}
}

func TestXbmcMetadata_FindMetadataFile_SeriesImage(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	got, err := m.FindMetadataFile(s, "/tv/Show/poster.jpg")
	if err != nil {
		t.Fatalf("FindMetadataFile: %v", err)
	}
	if got == nil {
		t.Fatal("got nil; want SeriesImage classification")
	}
	if metadata.MetadataType(got.Type) != metadata.MetadataTypeSeriesImage {
		t.Errorf("Type = %s", metadata.MetadataType(got.Type))
	}
	if got.SeriesID != 7 || got.Consumer != "XbmcMetadata" || got.RelativePath != "poster.jpg" {
		t.Errorf("base fields: %+v", got)
	}
}

func TestXbmcMetadata_FindMetadataFile_SeasonImage(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	s := &series.Series{ID: 7, Path: "/tv/Show"}

	cases := []struct {
		path     string
		wantSeas int
	}{
		{"/tv/Show/season01-poster.jpg", 1},
		{"/tv/Show/season12-banner.jpg", 12},
		{"/tv/Show/season-specials-poster.jpg", 0},
	}
	for _, tc := range cases {
		got, err := m.FindMetadataFile(s, tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeSeasonImage {
			t.Errorf("%s: type = %v", tc.path, got)
			continue
		}
		if got.SeasonNumber == nil || *got.SeasonNumber != tc.wantSeas {
			t.Errorf("%s: SeasonNumber = %v, want %d", tc.path, got.SeasonNumber, tc.wantSeas)
		}
	}

	// "season-all-..." is not a valid Kodi name (C# `:103-110` parses
	// "-all" as a non-int and returns nil).
	got, err := m.FindMetadataFile(s, "/tv/Show/season-all-poster.jpg")
	if err != nil {
		t.Fatalf("season-all: %v", err)
	}
	if got != nil {
		t.Errorf("season-all should be nil-row: %+v", got)
	}
}

func TestXbmcMetadata_FindMetadataFile_EpisodeImage(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	got, err := m.FindMetadataFile(s, "/tv/Show/Season 1/S01E02-thumb.jpg")
	if err != nil {
		t.Fatalf("FindMetadataFile: %v", err)
	}
	if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeEpisodeImage {
		t.Errorf("got = %v", got)
	}
}

func TestXbmcMetadata_FindMetadataFile_TvshowNfo(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	for _, name := range []string{"tvshow.nfo", "TVSHOW.NFO", "Tvshow.Nfo"} {
		got, err := m.FindMetadataFile(s, "/tv/Show/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeSeriesMetadata {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestXbmcMetadata_FindMetadataFile_EpisodeNfo(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"/tv/Show/Season 1/Show.S01E02.nfo": "<episodedetails><title>Pilot</title></episodedetails>",
	}
	m := newXbmcFor(t, files)
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	got, err := m.FindMetadataFile(s, "/tv/Show/Season 1/Show.S01E02.nfo")
	if err != nil {
		t.Fatalf("FindMetadataFile: %v", err)
	}
	if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeEpisodeMetadata {
		t.Errorf("got = %v", got)
	}
}

func TestXbmcMetadata_FindMetadataFile_NotMine(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"/tv/Show/Season 1/random.txt":      "hello",
		"/tv/Show/Season 1/Show.S01E02.nfo": "<other><x/></other>",
	}
	m := newXbmcFor(t, files)
	s := &series.Series{ID: 7, Path: "/tv/Show"}

	// Non-.nfo non-matching extension → nil.
	got, err := m.FindMetadataFile(s, "/tv/Show/Season 1/random.txt")
	if err != nil {
		t.Fatalf("random.txt: %v", err)
	}
	if got != nil {
		t.Errorf("random.txt should be nil-row: %+v", got)
	}

	// .nfo with foreign body → nil (detector rejects).
	got, err = m.FindMetadataFile(s, "/tv/Show/Season 1/Show.S01E02.nfo")
	if err != nil {
		t.Fatalf("foreign nfo: %v", err)
	}
	if got != nil {
		t.Errorf("foreign nfo should be nil-row: %+v", got)
	}
}

func TestXbmcMetadata_FindMetadataFile_NotChildOfSeriesPath(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	_, err := m.FindMetadataFile(s, "/elsewhere/file.jpg")
	if err == nil {
		t.Errorf("expected error for non-child path")
	}
}

func TestXbmcMetadata_RemainingStubsReturnZeroValues(t *testing.T) {
	t.Parallel()
	// After slice 4.BD (sub-arc W2c-i), SeriesMetadata has a real
	// body. EpisodeMetadata remains a stub pending slice 4.BE
	// (sub-arc W2c-ii). The image methods are real after 4.BC but
	// degrade to empty when the optional CoverPath / Episodes deps
	// are nil — pin that nil-tolerance here.
	m := newXbmcFor(t, nil)
	ctx := t.Context()
	s := &series.Series{ID: 1, Path: "/tv/Show"}

	res, err := m.EpisodeMetadata(ctx, s, &episodefiles.File{})
	if err != nil || res.RelativePath != "" || res.Contents != "" {
		t.Errorf("EpisodeMetadata stub: res=%+v err=%v", res, err)
	}

	imgs, err := m.SeriesImages(ctx, s)
	if err != nil || len(imgs) != 0 {
		t.Errorf("SeriesImages nil-CoverPath empty: imgs=%+v err=%v", imgs, err)
	}

	imgs, err = m.SeasonImages(ctx, s, metadata.Season{SeasonNumber: 1})
	if err != nil || len(imgs) != 0 {
		t.Errorf("SeasonImages empty-Images empty: imgs=%+v err=%v", imgs, err)
	}

	imgs, err = m.EpisodeImages(ctx, s, &episodefiles.File{})
	if err != nil || len(imgs) != 0 {
		t.Errorf("EpisodeImages nil-Episodes empty: imgs=%+v err=%v", imgs, err)
	}
}

func TestXbmcMetadata_Test_AlwaysSucceeds(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	failures, err := m.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if failures != nil {
		t.Errorf("failures = %+v, want nil", failures)
	}
}

func TestXbmcMetadata_RequestAction_NoOp(t *testing.T) {
	t.Parallel()
	m := newXbmcFor(t, nil)
	res, err := m.RequestAction(t.Context(), "anything", map[string]string{"k": "v"})
	if err != nil || res != nil {
		t.Errorf("RequestAction: res=%v err=%v", res, err)
	}
}

func TestXbmcMetadata_SatisfiesProviderInterface(t *testing.T) {
	t.Parallel()
	// Compile-time `var _ metadata.Provider = (*XbmcMetadata)(nil)`
	// is in xbmc.go; this test pins the runtime side too.
	var p metadata.Provider = newXbmcFor(t, nil)
	if p.Name() != "Kodi (XBMC) / Emby" {
		t.Errorf("Name() through interface = %q", p.Name())
	}
}
