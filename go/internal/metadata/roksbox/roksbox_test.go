package roksbox_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadata/roksbox"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

type fakeCoverPath struct{}

func (fakeCoverPath) GetCoverPath(seriesID int, coverType mediacover.CoverType, _ int) string {
	return "/var/sonarr/MediaCover/" + itoa(seriesID) + "/" + coverType.String() + mediacover.PathExtensionFor(coverType)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

type fakeEpisodes struct {
	byFile map[int][]episodes.Episode
	err    error
}

func (f *fakeEpisodes) AllByFileID(_ context.Context, fileID int) ([]episodes.Episode, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byFile[fileID], nil
}

type fakeFolders struct {
	folders []string
	err     error
}

func (f *fakeFolders) GetDirectories(_ string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.folders, nil
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

func newRoksboxFor(t *testing.T, settings roksbox.Settings, deps roksbox.Deps) *roksbox.RoksboxMetadata {
	t.Helper()
	m, err := roksbox.NewRoksboxMetadata(&metadata.Metadata{ID: 1}, settings, deps)
	if err != nil {
		t.Fatalf("NewRoksboxMetadata: %v", err)
	}
	return m
}

func TestDefaultSettings(t *testing.T) {
	t.Parallel()
	s := roksbox.DefaultSettings()
	if !s.EpisodeMetadata || !s.SeriesImages || !s.SeasonImages || !s.EpisodeImages {
		t.Errorf("all defaults should be true: %+v", s)
	}
}

func TestSettings_JSONOptionA(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(&roksbox.Settings{})
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	for _, k := range []string{"episodeMetadata", "seriesImages", "seasonImages", "episodeImages"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing JSON key %q", k)
		}
	}
	if len(raw) != 4 {
		t.Errorf("got %d keys, want 4: %+v", len(raw), raw)
	}
}

func TestRoksboxMetadata_Name(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	if got := m.Name(); got != "Roksbox" {
		t.Errorf("Name = %q", got)
	}
}

func TestRoksboxMetadata_SatisfiesProviderInterface(t *testing.T) {
	t.Parallel()
	var p metadata.Provider = newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	if p.Name() != "Roksbox" {
		t.Errorf("interface Name = %q", p.Name())
	}
}

func TestRoksboxMetadata_GetFilenameAfterMove(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	s := &series.Series{Path: "/tv/Show"}
	ef := &episodefiles.File{RelativePath: "Season 1/S01E02.mkv"}

	mf := &metadatafiles.MetadataFile{Type: int(metadata.MetadataTypeEpisodeMetadata), RelativePath: "Season 1/S01E02.xml"}
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/Season 1/S01E02.xml" {
		t.Errorf("EpisodeMetadata: %q", got)
	}

	mf.Type = int(metadata.MetadataTypeEpisodeImage)
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/Season 1/S01E02.jpg" {
		t.Errorf("EpisodeImage: %q", got)
	}

	mf.Type = int(metadata.MetadataTypeSeriesImage)
	mf.RelativePath = "Show.jpg"
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/Show.jpg" {
		t.Errorf("SeriesImage fallback: %q", got)
	}
}

func TestRoksboxMetadata_FindMetadataFile_SeriesImage(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	// stem "Show" matches parent dir "Show" → SeriesImage.
	got, err := m.FindMetadataFile(s, "/tv/Show/Show.jpg")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeSeriesImage {
		t.Errorf("got = %+v", got)
	}
	if got.Consumer != "RoksboxMetadata" || got.SeriesID != 7 || got.RelativePath != "Show.jpg" {
		t.Errorf("base fields: %+v", got)
	}
}

func TestRoksboxMetadata_FindMetadataFile_SeasonImage(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	cases := []struct {
		path     string
		wantSeas int
	}{
		// "Season 1/Season 1.jpg" — stem matches parent dir.
		{"/tv/Show/Season 1/Season 1.jpg", 1},
		{"/tv/Show/Season 12/Season 12.jpg", 12},
		{"/tv/Show/Specials/Specials.jpg", 0},
	}
	for _, tc := range cases {
		got, err := m.FindMetadataFile(s, tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeSeasonImage {
			t.Errorf("%s: got %+v", tc.path, got)
			continue
		}
		if got.SeasonNumber == nil || *got.SeasonNumber != tc.wantSeas {
			t.Errorf("%s: SeasonNumber = %v, want %d", tc.path, got.SeasonNumber, tc.wantSeas)
		}
	}
}

func TestRoksboxMetadata_FindMetadataFile_EpisodeMetadata(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	got, err := m.FindMetadataFile(s, "/tv/Show/Season 1/Show.S01E02.xml")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeEpisodeMetadata {
		t.Errorf("got = %+v", got)
	}
}

func TestRoksboxMetadata_FindMetadataFile_EpisodeImage(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	got, err := m.FindMetadataFile(s, "/tv/Show/Season 1/Show.S01E02.jpg")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeEpisodeImage {
		t.Errorf("got = %+v", got)
	}
}

func TestRoksboxMetadata_FindMetadataFile_RejectThumbSuffix(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	// `-thumb` suffix → NOT a Roksbox episode image (xbmc territory).
	got, err := m.FindMetadataFile(s, "/tv/Show/Season 1/Show.S01E02-thumb.jpg")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != nil {
		t.Errorf("-thumb suffix should not classify: %+v", got)
	}
}

func TestRoksboxMetadata_FindMetadataFile_RejectMetadataDir(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	// Parent dir "metadata" → NOT a Roksbox episode image.
	got, err := m.FindMetadataFile(s, "/tv/Show/metadata/Show.S01E02.jpg")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != nil {
		t.Errorf("metadata-parent dir should not classify: %+v", got)
	}
}

func TestRoksboxMetadata_SeriesMetadata_AlwaysNil(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	res, err := m.SeriesMetadata(t.Context(), &series.Series{Title: "X"}, metadata.SeriesMetadataReasonScan)
	if err != nil || res.RelativePath != "" || res.Contents != "" {
		t.Errorf("SeriesMetadata should always be zero: %+v / %v", res, err)
	}
}

func TestRoksboxMetadata_EpisodeMetadata_HappyPath(t *testing.T) {
	t.Parallel()
	ep := episodes.Episode{
		ID:            1,
		SeasonNumber:  1,
		EpisodeNumber: 2,
		Title:         "Cat's in the Bag...",
		Overview:      "Walter and Jesse attempt to dispose.",
		AirDate:       "2008-01-27",
	}
	s := &series.Series{
		ID:            7,
		Title:         "Breaking Bad",
		Runtime:       48,
		Certification: "TV-MA",
		Genres:        mustMarshal(t, []string{"Drama", "Crime"}),
		Actors: mustMarshal(t, []map[string]any{
			{"name": "Bryan Cranston", "character": "Walter White"},
			{"name": "Aaron Paul", "character": "Jesse Pinkman"},
			{"name": "Anna Gunn", "character": "Skyler White"},
			{"name": "Dean Norris", "character": "Hank Schrader"}, // 4th — should be CAPPED.
		}),
	}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{42: {ep}}}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Episodes: lookup})

	ef := &episodefiles.File{ID: 42, RelativePath: "Season 1/S01E02.mkv"}
	res, err := m.EpisodeMetadata(t.Context(), s, ef)
	if err != nil {
		t.Fatalf("EpisodeMetadata: %v", err)
	}
	if res.RelativePath != "Season 1/S01E02.xml" {
		t.Errorf("RelativePath = %q", res.RelativePath)
	}

	// Parse the single <video> body and assert fields.
	var v struct {
		XMLName     xml.Name `xml:"video"`
		Title       string   `xml:"title"`
		Year        string   `xml:"year"`
		Genre       string   `xml:"genre"`
		Actors      string   `xml:"actors"`
		Description string   `xml:"description"`
		Length      int      `xml:"length"`
		Mpaa        string   `xml:"mpaa"`
	}
	if err := xml.Unmarshal([]byte(res.Contents), &v); err != nil {
		t.Fatalf("parse XML: %v\nbody: %s", err, res.Contents)
	}
	if v.Title != "Breaking Bad - 1x2 - Cat's in the Bag..." {
		t.Errorf("Title = %q", v.Title)
	}
	if v.Year != "2008-01-27" {
		t.Errorf("Year = %q", v.Year)
	}
	if v.Genre != "Drama / Crime" {
		t.Errorf("Genre = %q", v.Genre)
	}
	// Actors capped at 3, separated by " , ", each "Name - Character".
	wantActors := "Bryan Cranston - Walter White , Aaron Paul - Jesse Pinkman , Anna Gunn - Skyler White"
	if v.Actors != wantActors {
		t.Errorf("Actors = %q\n want = %q", v.Actors, wantActors)
	}
	if v.Description == "" {
		t.Errorf("Description empty")
	}
	if v.Length != 48 {
		t.Errorf("Length = %d", v.Length)
	}
	if v.Mpaa != "TV-MA" {
		t.Errorf("Mpaa = %q", v.Mpaa)
	}
}

func TestRoksboxMetadata_EpisodeMetadata_MultiEpisode(t *testing.T) {
	t.Parallel()
	ep1 := episodes.Episode{ID: 1, SeasonNumber: 1, EpisodeNumber: 5, Title: "Part 1", AirDate: "2010-01-01"}
	ep2 := episodes.Episode{ID: 2, SeasonNumber: 1, EpisodeNumber: 6, Title: "Part 2", AirDate: "2010-01-02"}
	s := &series.Series{Title: "X", Runtime: 30, Certification: "PG"}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep1, ep2}}}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Episodes: lookup})
	res, err := m.EpisodeMetadata(t.Context(), s, &episodefiles.File{ID: 1, RelativePath: "ep.mkv"})
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Two <video> blocks separated by "\n".
	if strings.Count(res.Contents, "<video>") != 2 {
		t.Errorf("expected 2 <video> blocks, body: %s", res.Contents)
	}
}

func TestRoksboxMetadata_EpisodeMetadata_CertificationVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		certification string
		want          string
	}{
		{"TV-MA", "TV-MA"},
		{"tv-ma", "TV-MA"},
		{"PG", "PG"},
		{"R", "R"},
		{"", "UNRATED"},
		{"   ", "UNRATED"},
		{"NotAReal", "UNRATED"},
		{"X", "UNRATED"}, // X is NOT in the allow-list.
	}
	for _, tc := range cases {
		s := &series.Series{Title: "X", Certification: tc.certification}
		lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {{ID: 1, EpisodeNumber: 1}}}}
		m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Episodes: lookup})
		res, _ := m.EpisodeMetadata(t.Context(), s, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
		var v struct {
			Mpaa string `xml:"mpaa"`
		}
		_ = xml.Unmarshal([]byte(res.Contents), &v)
		if v.Mpaa != tc.want {
			t.Errorf("cert=%q → Mpaa=%q, want %q", tc.certification, v.Mpaa, tc.want)
		}
	}
}

func TestRoksboxMetadata_EpisodeMetadata_DisabledSetting(t *testing.T) {
	t.Parallel()
	settings := roksbox.DefaultSettings()
	settings.EpisodeMetadata = false
	m := newRoksboxFor(t, settings, roksbox.Deps{Episodes: &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {{ID: 1}}}}})
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if res.RelativePath != "" || res.Contents != "" {
		t.Errorf("disabled: %+v", res)
	}
}

func TestRoksboxMetadata_EpisodeMetadata_NoLinkedEpisodes(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Episodes: &fakeEpisodes{}})
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if res.RelativePath != "" {
		t.Errorf("no episodes: %+v", res)
	}
}

func TestRoksboxMetadata_EpisodeMetadata_LookupError(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Episodes: &fakeEpisodes{err: errors.New("db down")}})
	_, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestRoksboxMetadata_SeriesImages_HappyPath(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		ID:   7,
		Path: "/tv/Show Name",
		Images: mustMarshal(t, []mediacover.Cover{
			{CoverType: mediacover.CoverTypeBanner, RemoteUrl: "https://cdn/banner.jpg"},
			{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/poster.jpg"},
		}),
	}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{CoverPath: fakeCoverPath{}})
	imgs, err := m.SeriesImages(t.Context(), s)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d, want 1", len(imgs))
	}
	// destination = "<seriesFolderName>.jpg"
	if imgs[0].RelativePath != "Show Name.jpg" {
		t.Errorf("RelativePath = %q", imgs[0].RelativePath)
	}
	// URL = local cover path (poster preferred).
	if imgs[0].URL != "/var/sonarr/MediaCover/7/poster.jpg" {
		t.Errorf("URL = %q", imgs[0].URL)
	}
}

func TestRoksboxMetadata_SeriesImages_FallbackToFirst(t *testing.T) {
	t.Parallel()
	// No poster — fall back to first image (banner here).
	s := &series.Series{
		ID:   7,
		Path: "/tv/Show",
		Images: mustMarshal(t, []mediacover.Cover{
			{CoverType: mediacover.CoverTypeBanner, RemoteUrl: "https://cdn/banner.jpg"},
		}),
	}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{CoverPath: fakeCoverPath{}})
	imgs, _ := m.SeriesImages(t.Context(), s)
	if len(imgs) != 1 {
		t.Fatalf("got %d", len(imgs))
	}
	if imgs[0].URL != "/var/sonarr/MediaCover/7/banner.jpg" {
		t.Errorf("fallback URL = %q", imgs[0].URL)
	}
}

func TestRoksboxMetadata_SeriesImages_NoImages(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{CoverPath: fakeCoverPath{}})
	imgs, _ := m.SeriesImages(t.Context(), &series.Series{ID: 7})
	if len(imgs) != 0 {
		t.Errorf("no images: got %d", len(imgs))
	}
}

func TestRoksboxMetadata_SeasonImages_HappyPath(t *testing.T) {
	t.Parallel()
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	folders := &fakeFolders{folders: []string{"/tv/Show/Season 1", "/tv/Show/Season 2", "/tv/Show/Specials"}}
	season := metadata.Season{
		SeasonNumber: 1,
		Images: []mediacover.Cover{
			{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/season1-poster.jpg"},
		},
	}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Folders: folders})
	imgs, err := m.SeasonImages(t.Context(), s, season)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d, want 1", len(imgs))
	}
	if imgs[0].RelativePath != "Season 1/Season 1.jpg" {
		t.Errorf("RelativePath = %q", imgs[0].RelativePath)
	}
	if imgs[0].URL != "https://cdn/season1-poster.jpg" {
		t.Errorf("URL = %q (should be RemoteUrl)", imgs[0].URL)
	}
}

func TestRoksboxMetadata_SeasonImages_Specials(t *testing.T) {
	t.Parallel()
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	folders := &fakeFolders{folders: []string{"/tv/Show/Specials"}}
	season := metadata.Season{
		SeasonNumber: 0,
		Images:       []mediacover.Cover{{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/specials.jpg"}},
	}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Folders: folders})
	imgs, _ := m.SeasonImages(t.Context(), s, season)
	if len(imgs) != 1 || imgs[0].RelativePath != "Specials/Specials.jpg" {
		t.Errorf("Specials: %+v", imgs)
	}
}

func TestRoksboxMetadata_SeasonImages_UnknownSeason(t *testing.T) {
	t.Parallel()
	folders := &fakeFolders{folders: []string{"/tv/Show/Season 1"}}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Folders: folders})
	imgs, _ := m.SeasonImages(t.Context(), &series.Series{Path: "/tv/Show"}, metadata.Season{SeasonNumber: 99, Images: []mediacover.Cover{{CoverType: mediacover.CoverTypePoster}}})
	if len(imgs) != 0 {
		t.Errorf("unknown season: %+v", imgs)
	}
}

func TestRoksboxMetadata_EpisodeImages_HappyPath(t *testing.T) {
	t.Parallel()
	epImgs := mustMarshal(t, []mediacover.Cover{
		{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/poster.jpg"},
		{CoverType: mediacover.CoverTypeScreenshot, RemoteUrl: "https://cdn/screenshot.jpg"},
	})
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {{ID: 1, Images: epImgs}}}}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Episodes: lookup})
	imgs, err := m.EpisodeImages(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "Season 1/S01E02.mkv"})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d", len(imgs))
	}
	if imgs[0].RelativePath != "Season 1/S01E02.jpg" {
		t.Errorf("RelativePath = %q (should NOT have -thumb suffix)", imgs[0].RelativePath)
	}
	if imgs[0].URL != "https://cdn/screenshot.jpg" {
		t.Errorf("URL = %q", imgs[0].URL)
	}
}

func TestRoksboxMetadata_EpisodeImages_NoScreenshot(t *testing.T) {
	t.Parallel()
	epImgs := mustMarshal(t, []mediacover.Cover{{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/p.jpg"}})
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {{ID: 1, Images: epImgs}}}}
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{Episodes: lookup})
	imgs, _ := m.EpisodeImages(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "ep.mkv"})
	if len(imgs) != 0 {
		t.Errorf("no screenshot: %+v", imgs)
	}
}

func TestRoksboxMetadata_AllNilDepsNoCrash(t *testing.T) {
	t.Parallel()
	m := newRoksboxFor(t, roksbox.DefaultSettings(), roksbox.Deps{})
	ctx := t.Context()
	if _, err := m.SeriesImages(ctx, &series.Series{}); err != nil {
		t.Errorf("SeriesImages nil-deps: %v", err)
	}
	if _, err := m.SeasonImages(ctx, &series.Series{}, metadata.Season{}); err != nil {
		t.Errorf("SeasonImages nil-deps: %v", err)
	}
	if _, err := m.EpisodeImages(ctx, &series.Series{}, &episodefiles.File{ID: 1}); err != nil {
		t.Errorf("EpisodeImages nil-deps: %v", err)
	}
}

func TestRoksboxMetadata_NilReceiver(t *testing.T) {
	t.Parallel()
	var m *roksbox.RoksboxMetadata
	if m.Name() != "Roksbox" {
		t.Errorf("nil Name = %q", m.Name())
	}
	if d := m.Definition(); d != nil {
		t.Errorf("nil Definition = %+v", d)
	}
	if m.SettingsConfig() != (roksbox.Settings{}) {
		t.Errorf("nil SettingsConfig = %+v", m.SettingsConfig())
	}
}

func TestRegistration_DefaultRegistry(t *testing.T) {
	t.Parallel()
	if !metadata.Default().Has(roksbox.ImplementationName, roksbox.ConfigContractName) {
		t.Errorf("metadata.Default() missing entry for (%q, %q)", roksbox.ImplementationName, roksbox.ConfigContractName)
	}
}

func TestRegistration_Construct(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             7,
		Implementation: roksbox.ImplementationName,
		ConfigContract: roksbox.ConfigContractName,
		Settings:       `{"episodeMetadata":false,"seriesImages":true,"seasonImages":true,"episodeImages":true}`,
	}
	p, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if p.Name() != "Roksbox" {
		t.Errorf("Name = %q", p.Name())
	}
	pm := p.(*roksbox.RoksboxMetadata)
	if pm.SettingsConfig().EpisodeMetadata {
		t.Errorf("Settings did not deserialise — EpisodeMetadata should be false")
	}
}

func TestRegistration_Construct_EmptySettings(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{Implementation: roksbox.ImplementationName, ConfigContract: roksbox.ConfigContractName, Settings: ""}
	p, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !p.(*roksbox.RoksboxMetadata).SettingsConfig().EpisodeMetadata {
		t.Errorf("default EpisodeMetadata not applied")
	}
}

func TestRegistration_Construct_MalformedSettings(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{Implementation: roksbox.ImplementationName, ConfigContract: roksbox.ConfigContractName, Settings: "{bad"}
	_, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err == nil {
		t.Errorf("expected error on malformed Settings")
	}
}
