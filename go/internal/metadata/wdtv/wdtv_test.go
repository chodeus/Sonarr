package wdtv_test

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
	"sonarr.tv/sonarr/internal/metadata/wdtv"
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

func newWdtvFor(t *testing.T, settings wdtv.Settings, deps wdtv.Deps) *wdtv.WdtvMetadata {
	t.Helper()
	m, err := wdtv.NewWdtvMetadata(&metadata.Metadata{ID: 1}, settings, deps)
	if err != nil {
		t.Fatalf("NewWdtvMetadata: %v", err)
	}
	return m
}

func TestDefaultSettings(t *testing.T) {
	t.Parallel()
	s := wdtv.DefaultSettings()
	if !s.EpisodeMetadata || !s.SeriesImages || !s.SeasonImages || !s.EpisodeImages {
		t.Errorf("defaults should all be true: %+v", s)
	}
}

func TestSettings_JSONOptionA(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(&wdtv.Settings{})
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

func TestWdtvMetadata_Name(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	if got := m.Name(); got != "WDTV" {
		t.Errorf("Name = %q", got)
	}
}

func TestWdtvMetadata_SatisfiesProviderInterface(t *testing.T) {
	t.Parallel()
	var p metadata.Provider = newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	if p.Name() != "WDTV" {
		t.Errorf("interface Name = %q", p.Name())
	}
}

func TestWdtvMetadata_GetFilenameAfterMove(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	s := &series.Series{Path: "/tv/Show"}
	ef := &episodefiles.File{RelativePath: "Season 1/S01E02.mkv"}

	mf := &metadatafiles.MetadataFile{Type: int(metadata.MetadataTypeEpisodeMetadata), RelativePath: "Season 1/S01E02.xml"}
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/Season 1/S01E02.xml" {
		t.Errorf("EpisodeMetadata: %q", got)
	}

	// .metathumb extension distinctive to WDTV.
	mf.Type = int(metadata.MetadataTypeEpisodeImage)
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/Season 1/S01E02.metathumb" {
		t.Errorf("EpisodeImage (should be .metathumb): %q", got)
	}

	mf.Type = int(metadata.MetadataTypeSeriesImage)
	mf.RelativePath = "folder.jpg"
	if got := m.GetFilenameAfterMove(s, ef, mf); got != "/tv/Show/folder.jpg" {
		t.Errorf("SeriesImage fallback: %q", got)
	}
}

func TestWdtvMetadata_FindMetadataFile_SeriesImage(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	got, err := m.FindMetadataFile(s, "/tv/Show/folder.jpg")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeSeriesImage {
		t.Errorf("got = %+v, want SeriesImage", got)
	}
	if got.Consumer != "WdtvMetadata" {
		t.Errorf("Consumer = %q", got.Consumer)
	}
}

func TestWdtvMetadata_FindMetadataFile_SeasonImage(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	cases := []struct {
		path     string
		wantSeas int
	}{
		{"/tv/Show/Season 1/folder.jpg", 1},
		{"/tv/Show/Season 12/folder.jpg", 12},
		{"/tv/Show/Specials/folder.jpg", 0},
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

func TestWdtvMetadata_FindMetadataFile_EpisodeMetadata(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	got, err := m.FindMetadataFile(s, "/tv/Show/Season 1/Show.S01E02.xml")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeEpisodeMetadata {
		t.Errorf("got = %+v", got)
	}
}

func TestWdtvMetadata_FindMetadataFile_EpisodeImage_Metathumb(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	// WDTV uses .metathumb — NOT .jpg.
	got, err := m.FindMetadataFile(s, "/tv/Show/Season 1/Show.S01E02.metathumb")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got == nil || metadata.MetadataType(got.Type) != metadata.MetadataTypeEpisodeImage {
		t.Errorf("got = %+v", got)
	}
}

func TestWdtvMetadata_FindMetadataFile_NotMine(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	cases := []string{
		"/tv/Show/Season 1/Show.S01E02.nfo",       // wrong ext (xbmc territory)
		"/tv/Show/Season 1/Show.S01E02-thumb.jpg", // wrong ext + xbmc-suffix
		"/tv/Show/Season 1/random.txt",
		"/tv/Show/Season 1/Show.S01E02.jpg", // bare .jpg is Roksbox territory, not WDTV
	}
	for _, path := range cases {
		got, err := m.FindMetadataFile(s, path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got != nil {
			t.Errorf("%s should be nil-row, got %+v", path, got)
		}
	}
}

func TestWdtvMetadata_FindMetadataFile_NotChildOfSeriesPath(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	_, err := m.FindMetadataFile(s, "/elsewhere/folder.jpg")
	if err == nil {
		t.Errorf("expected error for non-child path")
	}
}

func TestWdtvMetadata_SeriesMetadata_AlwaysNil(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
	res, err := m.SeriesMetadata(t.Context(), &series.Series{Title: "X"}, metadata.SeriesMetadataReasonScan)
	if err != nil || res.RelativePath != "" || res.Contents != "" {
		t.Errorf("SeriesMetadata should always be zero: %+v / %v", res, err)
	}
}

func TestWdtvMetadata_EpisodeMetadata_HappyPath(t *testing.T) {
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
		ID:     42,
		Title:  "Breaking Bad",
		Genres: mustMarshal(t, []string{"Drama", "Crime"}),
		// No actor cap — emit all 4.
		Actors: mustMarshal(t, []map[string]any{
			{"name": "Bryan Cranston", "character": "Walter White"},
			{"name": "Aaron Paul", "character": "Jesse Pinkman"},
			{"name": "Anna Gunn", "character": "Skyler White"},
			{"name": "Dean Norris", "character": "Hank Schrader"},
		}),
	}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{42: {ep}}}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Episodes: lookup})

	ef := &episodefiles.File{ID: 42, RelativePath: "Season 1/S01E02.mkv"}
	res, err := m.EpisodeMetadata(t.Context(), s, ef)
	if err != nil {
		t.Fatalf("EpisodeMetadata: %v", err)
	}
	if res.RelativePath != "Season 1/S01E02.xml" {
		t.Errorf("RelativePath = %q", res.RelativePath)
	}

	var d struct {
		XMLName       xml.Name `xml:"details"`
		ID            int      `xml:"id"`
		Title         string   `xml:"title"`
		SeriesName    string   `xml:"series_name"`
		EpisodeName   string   `xml:"episode_name"`
		SeasonNumber  string   `xml:"season_number"`
		EpisodeNumber string   `xml:"episode_number"`
		FirstAired    string   `xml:"firstaired"`
		Genre         string   `xml:"genre"`
		Actor         string   `xml:"actor"`
		Overview      string   `xml:"overview"`
	}
	if err := xml.Unmarshal([]byte(res.Contents), &d); err != nil {
		t.Fatalf("parse XML: %v\nbody: %s", err, res.Contents)
	}
	if d.ID != 42 {
		t.Errorf("ID = %d, want SeriesID=42", d.ID)
	}
	// Title: "<Series> - <Season>x<Episode:00> - <EpisodeTitle>".
	if d.Title != "Breaking Bad - 1x02 - Cat's in the Bag..." {
		t.Errorf("Title = %q", d.Title)
	}
	if d.SeriesName != "Breaking Bad" {
		t.Errorf("SeriesName = %q", d.SeriesName)
	}
	if d.EpisodeName != "Cat's in the Bag..." {
		t.Errorf("EpisodeName = %q", d.EpisodeName)
	}
	// Season + episode both 2-digit zero-padded.
	if d.SeasonNumber != "01" {
		t.Errorf("SeasonNumber = %q, want %q", d.SeasonNumber, "01")
	}
	if d.EpisodeNumber != "02" {
		t.Errorf("EpisodeNumber = %q, want %q", d.EpisodeNumber, "02")
	}
	if d.FirstAired != "2008-01-27" {
		t.Errorf("FirstAired = %q", d.FirstAired)
	}
	if d.Genre != "Drama / Crime" {
		t.Errorf("Genre = %q", d.Genre)
	}
	// Actor: " / " separator, NO cap — all 4 emitted.
	wantActor := "Bryan Cranston - Walter White / Aaron Paul - Jesse Pinkman / Anna Gunn - Skyler White / Dean Norris - Hank Schrader"
	if d.Actor != wantActor {
		t.Errorf("Actor:\n got: %q\nwant: %q", d.Actor, wantActor)
	}
}

func TestWdtvMetadata_EpisodeMetadata_TwoDigitSeason(t *testing.T) {
	t.Parallel()
	// SeasonNumber=12, EpisodeNumber=3 → "12x03" in title; "12"/"03" in tags.
	ep := episodes.Episode{ID: 1, SeasonNumber: 12, EpisodeNumber: 3, Title: "X"}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Episodes: lookup})
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{Title: "Show"}, &episodefiles.File{ID: 1, RelativePath: "ep.mkv"})
	var d struct {
		Title         string `xml:"title"`
		SeasonNumber  string `xml:"season_number"`
		EpisodeNumber string `xml:"episode_number"`
	}
	_ = xml.Unmarshal([]byte(res.Contents), &d)
	if d.Title != "Show - 12x03 - X" {
		t.Errorf("Title = %q", d.Title)
	}
	if d.SeasonNumber != "12" || d.EpisodeNumber != "03" {
		t.Errorf("padded fields: season=%q episode=%q", d.SeasonNumber, d.EpisodeNumber)
	}
}

func TestWdtvMetadata_EpisodeMetadata_MultiEpisode(t *testing.T) {
	t.Parallel()
	ep1 := episodes.Episode{ID: 1, SeasonNumber: 1, EpisodeNumber: 5}
	ep2 := episodes.Episode{ID: 2, SeasonNumber: 1, EpisodeNumber: 6}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep1, ep2}}}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Episodes: lookup})
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{Title: "X"}, &episodefiles.File{ID: 1, RelativePath: "ep.mkv"})
	if strings.Count(res.Contents, "<details>") != 2 {
		t.Errorf("expected 2 <details> blocks, body:\n%s", res.Contents)
	}
}

func TestWdtvMetadata_EpisodeMetadata_NoActorCap(t *testing.T) {
	t.Parallel()
	// 5 actors — verifies WDTV emits ALL of them (no Roksbox-style cap).
	actorList := make([]map[string]any, 5)
	for i := range actorList {
		actorList[i] = map[string]any{"name": "Actor" + itoa(i+1), "character": "Char" + itoa(i+1)}
	}
	s := &series.Series{Title: "X", Actors: mustMarshal(t, actorList)}
	ep := episodes.Episode{ID: 1, EpisodeNumber: 1}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Episodes: lookup})
	res, _ := m.EpisodeMetadata(t.Context(), s, &episodefiles.File{ID: 1, RelativePath: "ep.mkv"})
	var d struct {
		Actor string `xml:"actor"`
	}
	_ = xml.Unmarshal([]byte(res.Contents), &d)
	// All 5 actors present, separated by " / ".
	for i := 1; i <= 5; i++ {
		if !strings.Contains(d.Actor, "Actor"+itoa(i)+" - Char"+itoa(i)) {
			t.Errorf("missing actor %d in %q", i, d.Actor)
		}
	}
}

func TestWdtvMetadata_EpisodeMetadata_DisabledSetting(t *testing.T) {
	t.Parallel()
	settings := wdtv.DefaultSettings()
	settings.EpisodeMetadata = false
	m := newWdtvFor(t, settings, wdtv.Deps{Episodes: &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {{ID: 1}}}}})
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if res.RelativePath != "" {
		t.Errorf("disabled: %+v", res)
	}
}

func TestWdtvMetadata_EpisodeMetadata_NoLinkedEpisodes(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Episodes: &fakeEpisodes{}})
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if res.RelativePath != "" {
		t.Errorf("no eps: %+v", res)
	}
}

func TestWdtvMetadata_EpisodeMetadata_LookupError(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Episodes: &fakeEpisodes{err: errors.New("db down")}})
	_, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestWdtvMetadata_SeriesImages_HappyPath(t *testing.T) {
	t.Parallel()
	s := &series.Series{
		ID: 7, Path: "/tv/Show",
		Images: mustMarshal(t, []mediacover.Cover{
			{CoverType: mediacover.CoverTypeBanner, RemoteUrl: "https://cdn/banner.jpg"},
			{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/poster.jpg"},
		}),
	}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{CoverPath: fakeCoverPath{}})
	imgs, err := m.SeriesImages(t.Context(), s)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d", len(imgs))
	}
	// destination = "folder" + ext from cover path. WDTV is "folder.jpg" (not series-folder name).
	if imgs[0].RelativePath != "folder.jpg" {
		t.Errorf("RelativePath = %q, want %q", imgs[0].RelativePath, "folder.jpg")
	}
	if imgs[0].URL != "/var/sonarr/MediaCover/7/poster.jpg" {
		t.Errorf("URL = %q", imgs[0].URL)
	}
}

func TestWdtvMetadata_SeriesImages_Clearlogo_ExtensionFollowsSource(t *testing.T) {
	t.Parallel()
	// Clearlogo is .png — destination should follow the source extension.
	s := &series.Series{
		ID: 7, Path: "/tv/Show",
		Images: mustMarshal(t, []mediacover.Cover{
			{CoverType: mediacover.CoverTypeClearlogo, RemoteUrl: "https://cdn/clearlogo.png"},
		}),
	}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{CoverPath: fakeCoverPath{}})
	imgs, _ := m.SeriesImages(t.Context(), s)
	if len(imgs) != 1 || imgs[0].RelativePath != "folder.png" {
		t.Errorf("Clearlogo destination should be folder.png, got %+v", imgs)
	}
}

func TestWdtvMetadata_SeasonImages_HappyPath(t *testing.T) {
	t.Parallel()
	s := &series.Series{ID: 7, Path: "/tv/Show"}
	folders := &fakeFolders{folders: []string{"/tv/Show/Season 1", "/tv/Show/Season 2"}}
	season := metadata.Season{
		SeasonNumber: 1,
		Images:       []mediacover.Cover{{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/s1.jpg"}},
	}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Folders: folders})
	imgs, err := m.SeasonImages(t.Context(), s, season)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d", len(imgs))
	}
	// destination = "<seasonFolder>/folder.jpg".
	if imgs[0].RelativePath != "Season 1/folder.jpg" {
		t.Errorf("RelativePath = %q", imgs[0].RelativePath)
	}
	if imgs[0].URL != "https://cdn/s1.jpg" {
		t.Errorf("URL = %q", imgs[0].URL)
	}
}

func TestWdtvMetadata_SeasonImages_Specials(t *testing.T) {
	t.Parallel()
	folders := &fakeFolders{folders: []string{"/tv/Show/Specials"}}
	season := metadata.Season{
		SeasonNumber: 0,
		Images:       []mediacover.Cover{{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/s.jpg"}},
	}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Folders: folders})
	imgs, _ := m.SeasonImages(t.Context(), &series.Series{Path: "/tv/Show"}, season)
	if len(imgs) != 1 || imgs[0].RelativePath != "Specials/folder.jpg" {
		t.Errorf("Specials: %+v", imgs)
	}
}

func TestWdtvMetadata_EpisodeImages_HappyPath(t *testing.T) {
	t.Parallel()
	epImgs := mustMarshal(t, []mediacover.Cover{
		{CoverType: mediacover.CoverTypeScreenshot, RemoteUrl: "https://cdn/shot.jpg"},
	})
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {{ID: 1, Images: epImgs}}}}
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{Episodes: lookup})
	imgs, err := m.EpisodeImages(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "Season 1/S01E02.mkv"})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d", len(imgs))
	}
	// .metathumb extension is the WDTV-distinctive marker.
	if imgs[0].RelativePath != "Season 1/S01E02.metathumb" {
		t.Errorf("RelativePath = %q, want %q (WDTV-distinctive)", imgs[0].RelativePath, "Season 1/S01E02.metathumb")
	}
	if imgs[0].URL != "https://cdn/shot.jpg" {
		t.Errorf("URL = %q", imgs[0].URL)
	}
}

func TestWdtvMetadata_AllNilDepsNoCrash(t *testing.T) {
	t.Parallel()
	m := newWdtvFor(t, wdtv.DefaultSettings(), wdtv.Deps{})
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

func TestWdtvMetadata_NilReceiver(t *testing.T) {
	t.Parallel()
	var m *wdtv.WdtvMetadata
	if m.Name() != "WDTV" {
		t.Errorf("nil Name = %q", m.Name())
	}
	if d := m.Definition(); d != nil {
		t.Errorf("nil Definition = %+v", d)
	}
	if m.SettingsConfig() != (wdtv.Settings{}) {
		t.Errorf("nil SettingsConfig = %+v", m.SettingsConfig())
	}
}

func TestRegistration_DefaultRegistry(t *testing.T) {
	t.Parallel()
	if !metadata.Default().Has(wdtv.ImplementationName, wdtv.ConfigContractName) {
		t.Errorf("metadata.Default() missing entry for (%q, %q)", wdtv.ImplementationName, wdtv.ConfigContractName)
	}
}

func TestRegistration_Construct(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             7,
		Implementation: wdtv.ImplementationName,
		ConfigContract: wdtv.ConfigContractName,
		Settings:       `{"episodeMetadata":false,"seriesImages":true,"seasonImages":true,"episodeImages":true}`,
	}
	p, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if p.Name() != "WDTV" {
		t.Errorf("Name = %q", p.Name())
	}
	pm := p.(*wdtv.WdtvMetadata)
	if pm.SettingsConfig().EpisodeMetadata {
		t.Errorf("Settings did not deserialise — EpisodeMetadata should be false")
	}
}

func TestRegistration_Construct_EmptySettings(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{Implementation: wdtv.ImplementationName, ConfigContract: wdtv.ConfigContractName, Settings: ""}
	p, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !p.(*wdtv.WdtvMetadata).SettingsConfig().EpisodeMetadata {
		t.Errorf("default EpisodeMetadata not applied")
	}
}

func TestRegistration_Construct_MalformedSettings(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{Implementation: wdtv.ImplementationName, ConfigContract: wdtv.ConfigContractName, Settings: "{bad"}
	_, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err == nil {
		t.Errorf("expected error on malformed Settings")
	}
}
