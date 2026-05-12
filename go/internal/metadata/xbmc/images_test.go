package xbmc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadata/xbmc"
	"sonarr.tv/sonarr/internal/series"
)

// fakeCoverPath implements xbmc.CoverPathLookup.
type fakeCoverPath struct{}

func (fakeCoverPath) GetCoverPath(seriesID int, coverType mediacover.CoverType, _ int) string {
	// Match the production format from mediacover.Service.GetCoverPath:
	//   <root>/<seriesId>/<type>.<ext>
	// where ext is .png for Clearlogo, .jpg otherwise (per
	// internal/mediacover.PathExtensionFor).
	ext := mediacover.PathExtensionFor(coverType)
	return "/var/sonarr/MediaCover/" + itoa(seriesID) + "/" + coverType.String() + ext
}

// itoa avoids importing strconv at the top of the test file when only
// one call site needs it. Kept private so the test code is self-contained.
func itoa(n int) string {
	const digits = "0123456789"
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
		buf[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// fakeEpisodeLookup implements xbmc.EpisodeLookup.
type fakeEpisodeLookup struct {
	byFile map[int][]episodes.Episode
	err    error
}

func (f *fakeEpisodeLookup) AllByFileID(_ context.Context, fileID int) ([]episodes.Episode, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byFile[fileID], nil
}

func newWithImageDeps(t *testing.T, deps xbmc.Deps) *xbmc.XbmcMetadata {
	t.Helper()
	if deps.Detector == nil {
		deps.Detector = newDetectorFor(t, nil)
	}
	m, err := xbmc.NewXbmcMetadata(&metadata.Metadata{ID: 1}, xbmc.DefaultSettings(), deps)
	if err != nil {
		t.Fatalf("NewXbmcMetadata: %v", err)
	}
	return m
}

func mustMarshalCovers(t *testing.T, covers []mediacover.Cover) string {
	t.Helper()
	body, err := json.Marshal(covers)
	if err != nil {
		t.Fatalf("marshal covers: %v", err)
	}
	return string(body)
}

func TestXbmcMetadata_SeriesImages_HappyPath(t *testing.T) {
	t.Parallel()
	m := newWithImageDeps(t, xbmc.Deps{CoverPath: fakeCoverPath{}})
	covers := []mediacover.Cover{
		{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/poster.jpg"},
		{CoverType: mediacover.CoverTypeBanner, RemoteUrl: "https://cdn/banner.jpg"},
		{CoverType: mediacover.CoverTypeFanart, RemoteUrl: "https://cdn/fanart.jpg"},
		{CoverType: mediacover.CoverTypeClearlogo, RemoteUrl: "https://cdn/clearlogo.png"},
	}
	s := &series.Series{ID: 42, Images: mustMarshalCovers(t, covers)}

	imgs, err := m.SeriesImages(t.Context(), s)
	if err != nil {
		t.Fatalf("SeriesImages: %v", err)
	}
	if len(imgs) != 4 {
		t.Fatalf("got %d, want 4: %+v", len(imgs), imgs)
	}
	cases := []struct {
		destination string
		source      string
	}{
		{"poster.jpg", "/var/sonarr/MediaCover/42/poster.jpg"},
		{"banner.jpg", "/var/sonarr/MediaCover/42/banner.jpg"},
		{"fanart.jpg", "/var/sonarr/MediaCover/42/fanart.jpg"},
		{"clearlogo.png", "/var/sonarr/MediaCover/42/clearlogo.png"},
	}
	for i, want := range cases {
		if imgs[i].RelativePath != want.destination {
			t.Errorf("imgs[%d].RelativePath = %q, want %q", i, imgs[i].RelativePath, want.destination)
		}
		if imgs[i].URL != want.source {
			t.Errorf("imgs[%d].URL = %q, want %q (LOCAL path per C# parity)", i, imgs[i].URL, want.source)
		}
	}
}

func TestXbmcMetadata_SeriesImages_DisabledSetting(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{ID: 1}
	settings := xbmc.DefaultSettings()
	settings.SeriesImages = false
	m, _ := xbmc.NewXbmcMetadata(def, settings, xbmc.Deps{Detector: newDetectorFor(t, nil), CoverPath: fakeCoverPath{}})
	s := &series.Series{ID: 42, Images: `[{"coverType":1,"url":"","remoteUrl":"https://cdn/poster.jpg"}]`}
	imgs, err := m.SeriesImages(t.Context(), s)
	if err != nil || len(imgs) != 0 {
		t.Errorf("disabled: imgs=%+v err=%v, want empty", imgs, err)
	}
}

func TestXbmcMetadata_SeriesImages_NilCoverPath(t *testing.T) {
	t.Parallel()
	m := newWithImageDeps(t, xbmc.Deps{})
	s := &series.Series{ID: 42, Images: `[{"coverType":1}]`}
	imgs, err := m.SeriesImages(t.Context(), s)
	if err != nil || imgs != nil {
		t.Errorf("nil CoverPath: imgs=%+v err=%v, want nil/nil", imgs, err)
	}
}

func TestXbmcMetadata_SeriesImages_EmptyBlob(t *testing.T) {
	t.Parallel()
	m := newWithImageDeps(t, xbmc.Deps{CoverPath: fakeCoverPath{}})
	imgs, err := m.SeriesImages(t.Context(), &series.Series{ID: 1, Images: ""})
	if err != nil || len(imgs) != 0 {
		t.Errorf("empty blob: imgs=%+v err=%v", imgs, err)
	}
}

func TestXbmcMetadata_SeriesImages_MalformedBlob(t *testing.T) {
	t.Parallel()
	m := newWithImageDeps(t, xbmc.Deps{CoverPath: fakeCoverPath{}})
	_, err := m.SeriesImages(t.Context(), &series.Series{ID: 1, Images: `{not json`})
	if err == nil {
		t.Errorf("expected error on malformed JSON blob")
	}
}

func TestXbmcMetadata_SeasonImages_HappyPath(t *testing.T) {
	t.Parallel()
	m := newWithImageDeps(t, xbmc.Deps{})
	season := metadata.Season{
		SeasonNumber: 3,
		Images: []mediacover.Cover{
			{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/season3-poster.jpg"},
			{CoverType: mediacover.CoverTypeBanner, RemoteUrl: "https://cdn/season3-banner.jpg"},
		},
	}
	imgs, err := m.SeasonImages(t.Context(), &series.Series{ID: 42}, season)
	if err != nil {
		t.Fatalf("SeasonImages: %v", err)
	}
	if len(imgs) != 2 {
		t.Fatalf("got %d, want 2", len(imgs))
	}
	// SeasonN-padded format for season > 0.
	if imgs[0].RelativePath != "season03-poster.jpg" {
		t.Errorf("imgs[0].RelativePath = %q", imgs[0].RelativePath)
	}
	if imgs[0].URL != "https://cdn/season3-poster.jpg" {
		t.Errorf("imgs[0].URL = %q (should be RemoteUrl, NOT local path)", imgs[0].URL)
	}
	if imgs[1].RelativePath != "season03-banner.jpg" {
		t.Errorf("imgs[1].RelativePath = %q", imgs[1].RelativePath)
	}
}

func TestXbmcMetadata_SeasonImages_Specials(t *testing.T) {
	t.Parallel()
	m := newWithImageDeps(t, xbmc.Deps{})
	season := metadata.Season{
		SeasonNumber: 0,
		Images: []mediacover.Cover{
			{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/specials-poster.jpg"},
		},
	}
	imgs, err := m.SeasonImages(t.Context(), &series.Series{ID: 42}, season)
	if err != nil {
		t.Fatalf("SeasonImages: %v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d, want 1", len(imgs))
	}
	// Season 0 → "season-specials-<type>.jpg" format.
	if imgs[0].RelativePath != "season-specials-poster.jpg" {
		t.Errorf("imgs[0].RelativePath = %q, want %q", imgs[0].RelativePath, "season-specials-poster.jpg")
	}
}

func TestXbmcMetadata_SeasonImages_DisabledSetting(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{ID: 1}
	settings := xbmc.DefaultSettings()
	settings.SeasonImages = false
	m, _ := xbmc.NewXbmcMetadata(def, settings, xbmc.Deps{Detector: newDetectorFor(t, nil)})
	season := metadata.Season{SeasonNumber: 1, Images: []mediacover.Cover{{CoverType: mediacover.CoverTypePoster}}}
	imgs, err := m.SeasonImages(t.Context(), &series.Series{}, season)
	if err != nil || len(imgs) != 0 {
		t.Errorf("disabled: imgs=%+v err=%v", imgs, err)
	}
}

func TestXbmcMetadata_EpisodeImages_HappyPath(t *testing.T) {
	t.Parallel()
	epImages := mustMarshalCovers(t, []mediacover.Cover{
		{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/poster.jpg"},
		{CoverType: mediacover.CoverTypeScreenshot, RemoteUrl: "https://cdn/screenshot.jpg"},
	})
	lookup := &fakeEpisodeLookup{byFile: map[int][]episodes.Episode{
		77: {{ID: 5, Images: epImages}},
	}}
	m := newWithImageDeps(t, xbmc.Deps{Episodes: lookup})
	ef := &episodefiles.File{ID: 77, RelativePath: "Season 1/S01E02.mkv"}

	imgs, err := m.EpisodeImages(t.Context(), &series.Series{ID: 1}, ef)
	if err != nil {
		t.Fatalf("EpisodeImages: %v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d, want 1", len(imgs))
	}
	if imgs[0].RelativePath != "Season 1/S01E02-thumb.jpg" {
		t.Errorf("RelativePath = %q", imgs[0].RelativePath)
	}
	if imgs[0].URL != "https://cdn/screenshot.jpg" {
		t.Errorf("URL = %q, want screenshot RemoteUrl", imgs[0].URL)
	}
}

func TestXbmcMetadata_EpisodeImages_NoScreenshot(t *testing.T) {
	t.Parallel()
	// Only a poster — no screenshot present; expect empty.
	epImages := mustMarshalCovers(t, []mediacover.Cover{
		{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/poster.jpg"},
	})
	lookup := &fakeEpisodeLookup{byFile: map[int][]episodes.Episode{77: {{ID: 5, Images: epImages}}}}
	m := newWithImageDeps(t, xbmc.Deps{Episodes: lookup})
	ef := &episodefiles.File{ID: 77, RelativePath: "Season 1/S01E02.mkv"}
	imgs, err := m.EpisodeImages(t.Context(), &series.Series{}, ef)
	if err != nil || len(imgs) != 0 {
		t.Errorf("no screenshot: imgs=%+v err=%v", imgs, err)
	}
}

func TestXbmcMetadata_EpisodeImages_NoLinkedEpisodes(t *testing.T) {
	t.Parallel()
	lookup := &fakeEpisodeLookup{byFile: map[int][]episodes.Episode{}}
	m := newWithImageDeps(t, xbmc.Deps{Episodes: lookup})
	ef := &episodefiles.File{ID: 77, RelativePath: "S01E02.mkv"}
	imgs, err := m.EpisodeImages(t.Context(), &series.Series{}, ef)
	if err != nil || len(imgs) != 0 {
		t.Errorf("no episodes: imgs=%+v err=%v", imgs, err)
	}
}

func TestXbmcMetadata_EpisodeImages_LookupError(t *testing.T) {
	t.Parallel()
	lookup := &fakeEpisodeLookup{err: errors.New("db down")}
	m := newWithImageDeps(t, xbmc.Deps{Episodes: lookup})
	ef := &episodefiles.File{ID: 77, RelativePath: "S01E02.mkv"}
	_, err := m.EpisodeImages(t.Context(), &series.Series{}, ef)
	if err == nil {
		t.Errorf("expected error from lookup")
	}
}

func TestXbmcMetadata_EpisodeImages_NilLookup(t *testing.T) {
	t.Parallel()
	m := newWithImageDeps(t, xbmc.Deps{})
	ef := &episodefiles.File{ID: 77, RelativePath: "S01E02.mkv"}
	imgs, err := m.EpisodeImages(t.Context(), &series.Series{}, ef)
	if err != nil || imgs != nil {
		t.Errorf("nil lookup: imgs=%+v err=%v", imgs, err)
	}
}

func TestXbmcMetadata_EpisodeImages_DisabledSetting(t *testing.T) {
	t.Parallel()
	lookup := &fakeEpisodeLookup{byFile: map[int][]episodes.Episode{77: {{Images: `[{"coverType":4,"remoteUrl":"x"}]`}}}}
	def := &metadata.Metadata{ID: 1}
	settings := xbmc.DefaultSettings()
	settings.EpisodeImages = false
	m, _ := xbmc.NewXbmcMetadata(def, settings, xbmc.Deps{Detector: newDetectorFor(t, nil), Episodes: lookup})
	imgs, err := m.EpisodeImages(t.Context(), &series.Series{}, &episodefiles.File{ID: 77, RelativePath: "x.mkv"})
	if err != nil || len(imgs) != 0 {
		t.Errorf("disabled: imgs=%+v err=%v", imgs, err)
	}
}
