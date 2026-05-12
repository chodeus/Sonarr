package xbmc_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadata/xbmc"
	"sonarr.tv/sonarr/internal/series"
)

// episodeDetailsXML mirrors the writer's emitted episode shape for
// parse-back verification.
type episodeDetailsXML struct {
	XMLName            xml.Name         `xml:"episodedetails"`
	Title              string           `xml:"title"`
	Season             int              `xml:"season"`
	Episode            int              `xml:"episode"`
	Aired              string           `xml:"aired"`
	Plot               string           `xml:"plot"`
	DisplayAfterSeason *int             `xml:"displayafterseason,omitempty"`
	DisplaySeason      *int             `xml:"displayseason,omitempty"`
	DisplayEpisode     *int             `xml:"displayepisode,omitempty"`
	UniqueIDs          []uniqueIDXML    `xml:"uniqueid"`
	Thumb              *string          `xml:"thumb"`
	Watched            string           `xml:"watched"`
	Rating             *string          `xml:"rating,omitempty"`
	FileInfo           *fileInfoXMLTest `xml:"fileinfo,omitempty"`
}

type fileInfoXMLTest struct {
	StreamDetails struct {
		Video struct {
			Aspect            string  `xml:"aspect"`
			Bitrate           int64   `xml:"bitrate"`
			Codec             string  `xml:"codec"`
			Framerate         string  `xml:"framerate"`
			Height            int     `xml:"height"`
			ScanType          string  `xml:"scantype"`
			Width             int     `xml:"width"`
			Duration          string  `xml:"duration"`
			DurationInSeconds int64   `xml:"durationinseconds"`
			HdrType           *string `xml:"hdrtype"`
		} `xml:"video"`
		Audios []struct {
			Bitrate  int64  `xml:"bitrate"`
			Channels int    `xml:"channels"`
			Codec    string `xml:"codec"`
			Language string `xml:"language"`
		} `xml:"audio"`
		Subtitles []struct {
			Language string `xml:"language"`
		} `xml:"subtitle"`
	} `xml:"streamdetails"`
}

// parseEpisodeDetailsAll splits an EpisodeMetadata body into the
// declaration prefix + a slice of decoded <episodedetails> elements.
// The C# tree emits multiple roots inside one file (XML
// ConformanceLevel.Fragment); the Go port matches.
func parseEpisodeDetailsAll(t *testing.T, body string) []episodeDetailsXML {
	t.Helper()
	// Strip XML declaration prefix.
	const decl = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`
	if !strings.HasPrefix(body, decl) {
		t.Fatalf("missing XML declaration prefix: %q", body[:64])
	}
	rest := strings.TrimSpace(body[len(decl):])
	// Wrap into a single fake root so Go's xml decoder accepts it.
	wrapped := "<root>" + rest + "</root>"
	type wrapper struct {
		Episodes []episodeDetailsXML `xml:"episodedetails"`
	}
	var w wrapper
	if err := xml.Unmarshal([]byte(wrapped), &w); err != nil {
		t.Fatalf("parse multi-root XML: %v\nbody: %s", err, body)
	}
	return w.Episodes
}

// newEpisodeMetadataXbmc builds an XbmcMetadata wired with the given
// EpisodeLookup. Settings default to DefaultSettings unless overridden.
func newEpisodeMetadataXbmc(t *testing.T, settings xbmc.Settings, lookup xbmc.EpisodeLookup) *xbmc.XbmcMetadata {
	t.Helper()
	deps := xbmc.Deps{
		Detector: newDetectorFor(t, nil),
		Episodes: lookup,
	}
	m, err := xbmc.NewXbmcMetadata(&metadata.Metadata{ID: 1}, settings, deps)
	if err != nil {
		t.Fatalf("NewXbmcMetadata: %v", err)
	}
	return m
}

func mustMarshalForEp(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

// fakeEpisodes implements xbmc.EpisodeLookup for these tests
// (separate from fakeEpisodeLookup in images_test.go — same shape
// but kept independent for test-file isolation).
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

func TestEpisodeMetadata_HappyPath_SingleEpisode(t *testing.T) {
	t.Parallel()
	tvdbID := 12345
	ep := episodes.Episode{
		ID:            777,
		SeriesID:      1,
		SeasonNumber:  1,
		EpisodeNumber: 2,
		Title:         "Cat's in the Bag...",
		Overview:      "Walter and Jesse attempt to dispose.",
		AirDate:       "2008-01-27",
		TvdbID:        &tvdbID,
		Ratings:       mustMarshalForEp(t, map[string]any{"votes": 50, "value": 8.5}),
		Images: mustMarshalForEp(t, []mediacover.Cover{
			{CoverType: mediacover.CoverTypePoster, RemoteUrl: "https://cdn/poster.jpg"},
			{CoverType: mediacover.CoverTypeScreenshot, RemoteUrl: "https://cdn/s01e02-thumb.jpg"},
		}),
	}
	ef := &episodefiles.File{
		ID:           42,
		RelativePath: "Season 1/S01E02.mkv",
		SceneName:    "Breaking.Bad.S01E02.HDTV.x264-LOL",
		MediaInfo: &mediainfo.Model{
			VideoFormat:    "AVC",
			VideoBitrate:   2500000,
			VideoFps:       23.976,
			Width:          1920,
			Height:         1080,
			ScanType:       "Progressive",
			VideoHdrFormat: mediainfo.HdrNone,
			RunTime:        datastore.STDuration(48 * time.Minute),
			AudioStreams: []mediainfo.AudioStream{
				{Language: "eng", Format: "AC-3", Bitrate: 384000, Channels: 6},
			},
			SubtitleStreams: []mediainfo.SubtitleStream{
				{Language: "eng"},
			},
		},
	}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{42: {ep}}}

	// Default settings — EpisodeImageThumb defaults to false, so
	// thumb element is OMITTED when image is found.
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
	res, err := m.EpisodeMetadata(t.Context(), &series.Series{ID: 1}, ef)
	if err != nil {
		t.Fatalf("EpisodeMetadata: %v", err)
	}
	if res.RelativePath != "Season 1/S01E02.nfo" {
		t.Errorf("RelativePath = %q", res.RelativePath)
	}
	all := parseEpisodeDetailsAll(t, res.Contents)
	if len(all) != 1 {
		t.Fatalf("expected 1 episodedetails, got %d", len(all))
	}
	d := all[0]
	if d.Title != "Cat's in the Bag..." {
		t.Errorf("Title = %q", d.Title)
	}
	if d.Season != 1 || d.Episode != 2 {
		t.Errorf("Season/Episode = %d/%d", d.Season, d.Episode)
	}
	if d.Aired != "2008-01-27" {
		t.Errorf("Aired = %q", d.Aired)
	}
	if d.Plot == "" {
		t.Errorf("Plot empty")
	}
	if d.DisplayAfterSeason != nil || d.DisplaySeason != nil {
		t.Errorf("non-special episode: display fields should be nil")
	}
	if len(d.UniqueIDs) != 2 {
		t.Fatalf("UniqueIDs = %d, want 2: %+v", len(d.UniqueIDs), d.UniqueIDs)
	}
	if d.UniqueIDs[0].Type != "tvdb" || d.UniqueIDs[0].Default != "true" || d.UniqueIDs[0].Value != "12345" {
		t.Errorf("UniqueIDs[0] = %+v", d.UniqueIDs[0])
	}
	if d.UniqueIDs[1].Type != "sonarr" || d.UniqueIDs[1].Value != "777" {
		t.Errorf("UniqueIDs[1] = %+v", d.UniqueIDs[1])
	}

	// EpisodeImageThumb=false + image found → thumb omitted.
	if d.Thumb != nil {
		t.Errorf("thumb should be omitted when EpisodeImageThumb=false and screenshot found: %v", d.Thumb)
	}

	if d.Watched != "False" {
		t.Errorf("Watched = %q, want %q (TODO: real disk sniff)", d.Watched, "False")
	}
	if d.Rating == nil || *d.Rating != "8.5" {
		t.Errorf("Rating = %v, want 8.5", d.Rating)
	}

	if d.FileInfo == nil {
		t.Fatal("FileInfo nil")
	}
	v := d.FileInfo.StreamDetails.Video
	// aspect = 1920/1080 = 1.7777777...
	if !strings.HasPrefix(v.Aspect, "1.77") {
		t.Errorf("Aspect = %q (expected ~1.78)", v.Aspect)
	}
	if v.Bitrate != 2500000 {
		t.Errorf("Bitrate = %d", v.Bitrate)
	}
	if v.Codec != "AVC" {
		t.Errorf("Codec = %q (raw VideoFormat — TODO formatter)", v.Codec)
	}
	if v.Width != 1920 || v.Height != 1080 {
		t.Errorf("Width/Height = %d/%d", v.Width, v.Height)
	}
	if v.ScanType != "Progressive" {
		t.Errorf("ScanType = %q", v.ScanType)
	}
	// Duration = 48 minutes.
	if !strings.HasPrefix(v.Duration, "48") {
		t.Errorf("Duration = %q", v.Duration)
	}
	if v.DurationInSeconds != 48*60 {
		t.Errorf("DurationInSeconds = %d", v.DurationInSeconds)
	}
	// hdrtype empty element (HdrNone branch).
	if v.HdrType == nil || *v.HdrType != "" {
		t.Errorf("HdrType = %v, want pointer to empty (HdrNone branch)", v.HdrType)
	}

	if len(d.FileInfo.StreamDetails.Audios) != 1 {
		t.Fatalf("Audios = %d", len(d.FileInfo.StreamDetails.Audios))
	}
	a := d.FileInfo.StreamDetails.Audios[0]
	if a.Codec != "AC-3" || a.Channels != 6 || a.Bitrate != 384000 || a.Language != "eng" {
		t.Errorf("Audio[0] = %+v", a)
	}
	if len(d.FileInfo.StreamDetails.Subtitles) != 1 || d.FileInfo.StreamDetails.Subtitles[0].Language != "eng" {
		t.Errorf("Subtitles = %+v", d.FileInfo.StreamDetails.Subtitles)
	}
}

func TestEpisodeMetadata_MultiEpisode(t *testing.T) {
	t.Parallel()
	ep1 := episodes.Episode{ID: 1, SeasonNumber: 1, EpisodeNumber: 5, Title: "Part 1"}
	ep2 := episodes.Episode{ID: 2, SeasonNumber: 1, EpisodeNumber: 6, Title: "Part 2"}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep1, ep2}}}
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
	res, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "ep.mkv"})
	if err != nil {
		t.Fatalf("%v", err)
	}
	all := parseEpisodeDetailsAll(t, res.Contents)
	if len(all) != 2 {
		t.Fatalf("expected 2 episodedetails, got %d: %s", len(all), res.Contents)
	}
	if all[0].Episode != 5 || all[1].Episode != 6 {
		t.Errorf("episode order: %d, %d", all[0].Episode, all[1].Episode)
	}
}

func TestEpisodeMetadata_DisplayBranches(t *testing.T) {
	t.Parallel()
	five := 5

	// Specials episode with AiredAfterSeasonNumber → displayafterseason only.
	ep1 := episodes.Episode{ID: 1, SeasonNumber: 0, EpisodeNumber: 1, AiredAfterSeasonNumber: &five}
	lookup1 := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep1}}}
	m1 := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup1)
	res1, _ := m1.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all := parseEpisodeDetailsAll(t, res1.Contents)
	if all[0].DisplayAfterSeason == nil || *all[0].DisplayAfterSeason != 5 {
		t.Errorf("AiredAfter: DisplayAfterSeason = %v", all[0].DisplayAfterSeason)
	}
	if all[0].DisplaySeason != nil {
		t.Errorf("AiredAfter: DisplaySeason should be nil: %v", all[0].DisplaySeason)
	}

	// Specials with AiredBeforeSeasonNumber + AiredBeforeEpisodeNumber.
	three := 3
	seven := 7
	ep2 := episodes.Episode{
		ID:                       2,
		SeasonNumber:             0,
		AiredBeforeSeasonNumber:  &three,
		AiredBeforeEpisodeNumber: &seven,
	}
	lookup2 := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep2}}}
	m2 := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup2)
	res2, _ := m2.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all = parseEpisodeDetailsAll(t, res2.Contents)
	if all[0].DisplaySeason == nil || *all[0].DisplaySeason != 3 {
		t.Errorf("AiredBefore: DisplaySeason = %v", all[0].DisplaySeason)
	}
	if all[0].DisplayEpisode == nil || *all[0].DisplayEpisode != 7 {
		t.Errorf("AiredBefore: DisplayEpisode = %v", all[0].DisplayEpisode)
	}

	// Specials with AiredBeforeSeasonNumber but NO AiredBeforeEpisodeNumber
	// → displayepisode = -1 per C# `:319`.
	ep3 := episodes.Episode{ID: 3, SeasonNumber: 0, AiredBeforeSeasonNumber: &three}
	lookup3 := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep3}}}
	m3 := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup3)
	res3, _ := m3.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all = parseEpisodeDetailsAll(t, res3.Contents)
	if all[0].DisplayEpisode == nil || *all[0].DisplayEpisode != -1 {
		t.Errorf("nil AiredBeforeEpisode: should be -1, got %v", all[0].DisplayEpisode)
	}
}

func TestEpisodeMetadata_ThumbThreeStates(t *testing.T) {
	t.Parallel()
	// State 1: image == nil → emit empty <thumb></thumb>.
	ep1 := episodes.Episode{ID: 1, EpisodeNumber: 1, Images: ""}
	lookup1 := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep1}}}
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup1)
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all := parseEpisodeDetailsAll(t, res.Contents)
	if all[0].Thumb == nil || *all[0].Thumb != "" {
		t.Errorf("no image: Thumb should be ptr-to-empty, got %v", all[0].Thumb)
	}

	// State 2: image present + EpisodeImageThumb=true → emit URL.
	images := mustMarshalForEp(t, []mediacover.Cover{{CoverType: mediacover.CoverTypeScreenshot, RemoteUrl: "https://cdn/s.jpg"}})
	ep2 := episodes.Episode{ID: 2, EpisodeNumber: 1, Images: images}
	lookup2 := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep2}}}
	settings := xbmc.DefaultSettings()
	settings.EpisodeImageThumb = true
	m = newEpisodeMetadataXbmc(t, settings, lookup2)
	res, _ = m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all = parseEpisodeDetailsAll(t, res.Contents)
	if all[0].Thumb == nil || *all[0].Thumb != "https://cdn/s.jpg" {
		t.Errorf("image+EpisodeImageThumb: Thumb = %v", all[0].Thumb)
	}

	// State 3: image present + EpisodeImageThumb=false → OMITTED.
	settings.EpisodeImageThumb = false
	m = newEpisodeMetadataXbmc(t, settings, lookup2)
	res, _ = m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all = parseEpisodeDetailsAll(t, res.Contents)
	if all[0].Thumb != nil {
		t.Errorf("image+!EpisodeImageThumb: Thumb should be nil/omitted, got %v", all[0].Thumb)
	}
}

func TestEpisodeMetadata_RatingsGate(t *testing.T) {
	t.Parallel()
	ep := episodes.Episode{
		ID: 1, EpisodeNumber: 1,
		Ratings: mustMarshalForEp(t, map[string]any{"votes": 0, "value": 8.0}),
	}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all := parseEpisodeDetailsAll(t, res.Contents)
	if all[0].Rating != nil {
		t.Errorf("Votes==0: Rating should be omitted, got %v", all[0].Rating)
	}
}

func TestEpisodeMetadata_HdrTypeBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hdr     mediainfo.HdrFormat
		wantVal string
		wantOK  bool
	}{
		{mediainfo.HdrDolbyVision, "dolbyvision", true},
		{mediainfo.HdrDolbyVisionHdr10, "dolbyvision", true},
		{mediainfo.HdrDolbyVisionHdr10Plus, "dolbyvision", true},
		{mediainfo.HdrDolbyVisionHlg, "dolbyvision", true},
		{mediainfo.HdrDolbyVisionSdr, "dolbyvision", true},
		{mediainfo.Hdr10, "hdr10", true},
		{mediainfo.Hdr10Plus, "hdr10", true},
		{mediainfo.HdrPq10, "hdr10", true},
		{mediainfo.HdrHlg10, "hlg", true},
		{mediainfo.HdrNone, "", true},
		{mediainfo.HdrUnknown, "", false},
	}
	for _, tc := range cases {
		mi := &mediainfo.Model{Width: 1920, Height: 1080, VideoHdrFormat: tc.hdr}
		ef := &episodefiles.File{ID: 1, RelativePath: "x.mkv", MediaInfo: mi}
		ep := episodes.Episode{ID: 1, EpisodeNumber: 1}
		lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
		m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
		res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, ef)
		all := parseEpisodeDetailsAll(t, res.Contents)
		got := all[0].FileInfo.StreamDetails.Video.HdrType
		if tc.wantOK {
			if got == nil {
				t.Errorf("hdr=%v: expected element (%q), got nil", tc.hdr, tc.wantVal)
				continue
			}
			if *got != tc.wantVal {
				t.Errorf("hdr=%v: HdrType = %q, want %q", tc.hdr, *got, tc.wantVal)
			}
		} else if got != nil {
			t.Errorf("hdr=%v: expected no element, got %q", tc.hdr, *got)
		}
	}
}

func TestEpisodeMetadata_NoMediaInfo(t *testing.T) {
	t.Parallel()
	ep := episodes.Episode{ID: 1, EpisodeNumber: 1}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all := parseEpisodeDetailsAll(t, res.Contents)
	if all[0].FileInfo != nil {
		t.Errorf("nil MediaInfo: FileInfo should be nil/omitted")
	}
}

func TestEpisodeMetadata_DisabledSetting(t *testing.T) {
	t.Parallel()
	settings := xbmc.DefaultSettings()
	settings.EpisodeMetadata = false
	m := newEpisodeMetadataXbmc(t, settings, &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {{ID: 1}}}})
	res, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if err != nil || res.RelativePath != "" || res.Contents != "" {
		t.Errorf("disabled: %+v / %v", res, err)
	}
}

func TestEpisodeMetadata_NoLinkedEpisodes(t *testing.T) {
	t.Parallel()
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), &fakeEpisodes{byFile: map[int][]episodes.Episode{}})
	res, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if err != nil || res.RelativePath != "" || res.Contents != "" {
		t.Errorf("no episodes: %+v / %v", res, err)
	}
}

func TestEpisodeMetadata_NilEpisodeLookup(t *testing.T) {
	t.Parallel()
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), nil)
	res, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if err != nil || res.RelativePath != "" || res.Contents != "" {
		t.Errorf("nil lookup: %+v / %v", res, err)
	}
}

func TestEpisodeMetadata_LookupErrorPropagates(t *testing.T) {
	t.Parallel()
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), &fakeEpisodes{err: errors.New("db down")})
	_, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if err == nil {
		t.Errorf("expected error from lookup")
	}
}

func TestEpisodeMetadata_MalformedImages(t *testing.T) {
	t.Parallel()
	ep := episodes.Episode{ID: 1, EpisodeNumber: 1, Images: "{not json"}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
	_, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	if err == nil {
		t.Errorf("expected error on malformed Images blob")
	}
}

func TestEpisodeMetadata_AspectZeroHeightSafe(t *testing.T) {
	t.Parallel()
	// Height==0 must not panic — port returns "0" rather than +Inf.
	ep := episodes.Episode{ID: 1, EpisodeNumber: 1}
	ef := &episodefiles.File{ID: 1, RelativePath: "x.mkv", MediaInfo: &mediainfo.Model{Width: 1920, Height: 0}}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
	res, err := m.EpisodeMetadata(t.Context(), &series.Series{}, ef)
	if err != nil {
		t.Fatalf("%v", err)
	}
	all := parseEpisodeDetailsAll(t, res.Contents)
	if all[0].FileInfo.StreamDetails.Video.Aspect != "0" {
		t.Errorf("Aspect with Height=0 = %q, want %q", all[0].FileInfo.StreamDetails.Video.Aspect, "0")
	}
}

func TestEpisodeMetadata_XMLDeclaration(t *testing.T) {
	t.Parallel()
	ep := episodes.Episode{ID: 1, EpisodeNumber: 1}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	wantDecl := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`
	if !strings.HasPrefix(res.Contents, wantDecl) {
		t.Errorf("XML decl missing: %q", res.Contents[:64])
	}
}

func TestEpisodeMetadata_NilReceiver(t *testing.T) {
	t.Parallel()
	var m *xbmc.XbmcMetadata
	res, err := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1})
	if err != nil || res.RelativePath != "" {
		t.Errorf("nil receiver: %+v / %v", res, err)
	}
}

func TestEpisodeMetadata_WatchedAlwaysFalseForNow(t *testing.T) {
	t.Parallel()
	// Documents the W2c-ii deferral: real watched-status disk sniff
	// lands in queue [W6]. Pin the current behaviour so a future
	// regression is obvious.
	ep := episodes.Episode{ID: 1, EpisodeNumber: 1}
	lookup := &fakeEpisodes{byFile: map[int][]episodes.Episode{1: {ep}}}
	m := newEpisodeMetadataXbmc(t, xbmc.DefaultSettings(), lookup)
	res, _ := m.EpisodeMetadata(t.Context(), &series.Series{}, &episodefiles.File{ID: 1, RelativePath: "x.mkv"})
	all := parseEpisodeDetailsAll(t, res.Contents)
	if all[0].Watched != "False" {
		t.Errorf("Watched = %q, want %q (TODO: disk sniff)", all[0].Watched, "False")
	}
}
