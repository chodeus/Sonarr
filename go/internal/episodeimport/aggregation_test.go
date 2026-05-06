package episodeimport_test

import (
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// --- AugmentQualityFromFileName / Folder / DownloadClientItem -----

func TestAugmentQualityFromParsedInfo(t *testing.T) {
	t.Parallel()

	hd, _ := quality.FindByID(4) // HDTV-720p
	q := quality.Model{
		Quality:                   hd,
		Revision:                  quality.Revision{Version: 2},
		SourceDetectionSource:     quality.DetectionName,
		ResolutionDetectionSource: quality.DetectionName,
		RevisionDetectionSource:   quality.DetectionName,
	}

	cases := []struct {
		name      string
		setField  func(*episodeimport.LocalEpisode, *parser.ParsedEpisodeInfo)
		augmenter episodeimport.QualityAugmenter
	}{
		{
			"FileName",
			func(le *episodeimport.LocalEpisode, p *parser.ParsedEpisodeInfo) { le.FileEpisodeInfo = p },
			episodeimport.AugmentQualityFromFileName{},
		},
		{
			"Folder",
			func(le *episodeimport.LocalEpisode, p *parser.ParsedEpisodeInfo) { le.FolderEpisodeInfo = p },
			episodeimport.AugmentQualityFromFolder{},
		},
		{
			"DownloadClientItem",
			func(le *episodeimport.LocalEpisode, p *parser.ParsedEpisodeInfo) { le.DownloadClientEpisodeInfo = p },
			episodeimport.AugmentQualityFromDownloadClientItem{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/nil source returns nil", func(t *testing.T) {
			le := episodeimport.NewLocalEpisode()
			if got := tc.augmenter.AugmentQuality(le, nil); got != nil {
				t.Errorf("nil source: got %+v, want nil", got)
			}
		})
		t.Run(tc.name+"/non-zero quality returns Tag confidences", func(t *testing.T) {
			le := episodeimport.NewLocalEpisode()
			tc.setField(le, &parser.ParsedEpisodeInfo{Quality: q})
			got := tc.augmenter.AugmentQuality(le, nil)
			if got == nil {
				t.Fatalf("got nil")
			}
			if got.SourceConfidence != episodeimport.QualityConfidenceTag {
				t.Errorf("SourceConfidence = %v, want Tag", got.SourceConfidence)
			}
			if got.Resolution != hd.Resolution {
				t.Errorf("Resolution = %d, want %d", got.Resolution, hd.Resolution)
			}
			if got.Revision == nil || got.Revision.Version != 2 {
				t.Errorf("Revision = %+v", got.Revision)
			}
		})
	}

	// Order ordinals match C#.
	if (episodeimport.AugmentQualityFromFileName{}).Order() != 1 {
		t.Errorf("FileName.Order != 1")
	}
	if (episodeimport.AugmentQualityFromFolder{}).Order() != 2 {
		t.Errorf("Folder.Order != 2")
	}
	if (episodeimport.AugmentQualityFromDownloadClientItem{}).Order() != 3 {
		t.Errorf("DCI.Order != 3")
	}
}

// --- AugmentQualityFromMediaInfo -----------------------------------

func TestAugmentQualityFromMediaInfo(t *testing.T) {
	t.Parallel()
	a := episodeimport.NewAugmentQualityFromMediaInfo(slog.New(slog.DiscardHandler), nil)

	t.Run("nil MediaInfo returns nil", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		if got := a.AugmentQuality(le, nil); got != nil {
			t.Errorf("nil MediaInfo: got %+v", got)
		}
	})

	t.Run("zero dimensions returns nil", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		le.MediaInfo = &mediainfo.Model{}
		if got := a.AugmentQuality(le, nil); got != nil {
			t.Errorf("zero dimensions: got %+v", got)
		}
	})

	bucketCases := []struct {
		w, h int
		want int
	}{
		{3840, 2160, 2160},
		{3200, 1800, 2160}, // width threshold only
		{1920, 1080, 1080},
		{1800, 800, 1080}, // width threshold
		{1280, 720, 720},
		{1024, 576, 576},
		{640, 480, 480},
		{1, 1, 480}, // any positive collapses to 480p
	}
	for _, c := range bucketCases {
		le := episodeimport.NewLocalEpisode()
		le.MediaInfo = &mediainfo.Model{Width: c.w, Height: c.h}
		got := a.AugmentQuality(le, nil)
		if got == nil {
			t.Errorf("(%d,%d): got nil", c.w, c.h)
			continue
		}
		if got.Resolution != c.want {
			t.Errorf("(%d,%d) resolution = %d, want %d", c.w, c.h, got.Resolution, c.want)
		}
		if got.ResolutionConfidence != episodeimport.QualityConfidenceMediaInfo {
			t.Errorf("(%d,%d) confidence = %v", c.w, c.h, got.ResolutionConfidence)
		}
	}

	t.Run("MediaInfo title parsed for source tag", func(t *testing.T) {
		// stub parser returns Source=Web at DetectionName
		stub := func(_ string) quality.Model {
			return quality.Model{
				Quality:                   quality.Quality{Source: quality.SourceWeb, Resolution: 1080},
				SourceDetectionSource:     quality.DetectionName,
				ResolutionDetectionSource: quality.DetectionName,
			}
		}
		am := episodeimport.NewAugmentQualityFromMediaInfo(slog.New(slog.DiscardHandler), stub)
		le := episodeimport.NewLocalEpisode()
		le.MediaInfo = &mediainfo.Model{Width: 1920, Height: 1080, Title: "MyShow.S01E01.WEB-DL"}
		got := am.AugmentQuality(le, nil)
		if got == nil {
			t.Fatalf("got nil")
		}
		if got.Source != quality.SourceWeb {
			t.Errorf("Source = %v, want Web", got.Source)
		}
		if got.SourceConfidence != episodeimport.QualityConfidenceMediaInfo {
			t.Errorf("SourceConfidence = %v", got.SourceConfidence)
		}
	})

	if a.Order() != 4 {
		t.Errorf("MediaInfo.Order != 4")
	}
}

// --- AugmentLanguageFromParsedInfo --------------------------------

func TestAugmentLanguageFromParsedInfo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		setField   func(*episodeimport.LocalEpisode, *parser.ParsedEpisodeInfo)
		augmenter  episodeimport.LanguageAugmenter
		confidence episodeimport.LanguageConfidence
	}{
		{
			"FileName",
			func(le *episodeimport.LocalEpisode, p *parser.ParsedEpisodeInfo) { le.FileEpisodeInfo = p },
			episodeimport.AugmentLanguageFromFileName{},
			episodeimport.LanguageConfidenceFilename,
		},
		{
			"Folder",
			func(le *episodeimport.LocalEpisode, p *parser.ParsedEpisodeInfo) { le.FolderEpisodeInfo = p },
			episodeimport.AugmentLanguageFromFolder{},
			episodeimport.LanguageConfidenceFoldername,
		},
		{
			"DownloadClientItem",
			func(le *episodeimport.LocalEpisode, p *parser.ParsedEpisodeInfo) { le.DownloadClientEpisodeInfo = p },
			episodeimport.AugmentLanguageFromDownloadClientItem{},
			episodeimport.LanguageConfidenceDownloadClientItem,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/nil source returns nil", func(t *testing.T) {
			le := episodeimport.NewLocalEpisode()
			if got := tc.augmenter.AugmentLanguage(le, nil); got != nil {
				t.Errorf("nil: got %+v", got)
			}
		})
		t.Run(tc.name+"/languages set returns confidence", func(t *testing.T) {
			le := episodeimport.NewLocalEpisode()
			tc.setField(le, &parser.ParsedEpisodeInfo{Languages: []language.Language{language.English}})
			got := tc.augmenter.AugmentLanguage(le, nil)
			if got == nil {
				t.Fatalf("got nil")
			}
			if got.Confidence != tc.confidence {
				t.Errorf("confidence = %v, want %v", got.Confidence, tc.confidence)
			}
		})
	}
}

func TestAugmentLanguage_EpisodeTitleSubtraction(t *testing.T) {
	t.Parallel()
	// Simulate an episode whose title parses to French — that
	// language should be Excepted from the source list.
	le := episodeimport.NewLocalEpisode()
	le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{Languages: []language.Language{language.English, language.French}}
	le.Episodes = []episodes.Episode{{ID: 1, Title: "The French Connection"}}
	got := episodeimport.AugmentLanguageFromFileName{}.AugmentLanguage(le, nil)
	if got == nil {
		t.Fatalf("got nil")
	}
	for _, l := range got.Languages {
		if l == language.French {
			t.Errorf("French should have been subtracted by episode title parse")
		}
	}
}

func TestAugmentLanguageFromMediaInfo(t *testing.T) {
	t.Parallel()
	a := episodeimport.NewAugmentLanguageFromMediaInfo(nil)

	t.Run("nil MediaInfo returns nil", func(t *testing.T) {
		if got := a.AugmentLanguage(episodeimport.NewLocalEpisode(), nil); got != nil {
			t.Errorf("nil MI: got %+v", got)
		}
	})
	t.Run("empty AudioStreams returns nil", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		le.MediaInfo = &mediainfo.Model{}
		if got := a.AugmentLanguage(le, nil); got != nil {
			t.Errorf("empty streams: got %+v", got)
		}
	})
	t.Run("known iso codes resolve, distinct'd", func(t *testing.T) {
		le := episodeimport.NewLocalEpisode()
		le.MediaInfo = &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{
			{Language: "eng"}, {Language: "fra"}, {Language: "eng"}, // dup
		}}
		got := a.AugmentLanguage(le, nil)
		if got == nil {
			t.Fatalf("got nil")
		}
		if len(got.Languages) != 2 {
			t.Errorf("len = %d, want 2 distinct", len(got.Languages))
		}
		if got.Confidence != episodeimport.LanguageConfidenceMediaInfo {
			t.Errorf("confidence = %v", got.Confidence)
		}
	})
	t.Run("unknown iso codes return nil", func(t *testing.T) {
		stub := func(string) (language.Language, bool) { return language.Unknown, false }
		am := episodeimport.NewAugmentLanguageFromMediaInfo(stub)
		le := episodeimport.NewLocalEpisode()
		le.MediaInfo = &mediainfo.Model{AudioStreams: []mediainfo.AudioStream{{Language: "zzz"}}}
		if got := am.AugmentLanguage(le, nil); got != nil {
			t.Errorf("unknown codes: got %+v, want nil", got)
		}
	})
}

// --- AggregateReleaseGroup / AggregateReleaseHash -----------------

func TestAggregateReleaseGroup_Priority(t *testing.T) {
	t.Parallel()
	ag := episodeimport.AggregateReleaseGroup{}

	// Priority 1: DownloadClient (non-FullSeason).
	le := episodeimport.NewLocalEpisode()
	le.DownloadClientEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "DCI"}
	le.FolderEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "FOLDER"}
	le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "FILE"}
	ag.Aggregate(le, nil)
	if le.ReleaseGroup != "DCI" {
		t.Errorf("priority 1: got %q", le.ReleaseGroup)
	}

	// FullSeason DCI is skipped → falls to Folder.
	le = episodeimport.NewLocalEpisode()
	le.DownloadClientEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "DCI", FullSeason: true}
	le.FolderEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "FOLDER"}
	ag.Aggregate(le, nil)
	if le.ReleaseGroup != "FOLDER" {
		t.Errorf("FullSeason DCI skipped: got %q", le.ReleaseGroup)
	}

	// Both FullSeason → falls through to File (no skip).
	le = episodeimport.NewLocalEpisode()
	le.DownloadClientEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "DCI", FullSeason: true}
	le.FolderEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "FOLDER", FullSeason: true}
	le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "FILE"}
	ag.Aggregate(le, nil)
	if le.ReleaseGroup != "FILE" {
		t.Errorf("falls to File: got %q", le.ReleaseGroup)
	}

	// All empty → empty.
	le = episodeimport.NewLocalEpisode()
	ag.Aggregate(le, nil)
	if le.ReleaseGroup != "" {
		t.Errorf("all empty: got %q", le.ReleaseGroup)
	}
}

func TestAggregateReleaseHash_Priority(t *testing.T) {
	t.Parallel()
	ag := episodeimport.AggregateReleaseHash{}

	le := episodeimport.NewLocalEpisode()
	le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseHash: "AAA"}
	le.DownloadClientEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseHash: "BBB"}
	ag.Aggregate(le, nil)
	if le.ReleaseHash != "AAA" {
		t.Errorf("File priority: got %q", le.ReleaseHash)
	}

	// File FullSeason → skipped, falls to DCI.
	le = episodeimport.NewLocalEpisode()
	le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseHash: "AAA", FullSeason: true}
	le.DownloadClientEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseHash: "BBB"}
	ag.Aggregate(le, nil)
	if le.ReleaseHash != "BBB" {
		t.Errorf("FullSeason File skipped: got %q", le.ReleaseHash)
	}
}

// --- AggregateLanguage --------------------------------------------

type stubLanguageAug struct {
	order  int
	out    *episodeimport.AugmentLanguageResult
	called int
}

func (s *stubLanguageAug) Order() int            { s.called++; return s.order }
func (s *stubLanguageAug) AugmenterName() string { return "stub" }
func (s *stubLanguageAug) AugmentLanguage(_ *episodeimport.LocalEpisode, _ *downloadclients.Item) *episodeimport.AugmentLanguageResult {
	return s.out
}

func TestAggregateLanguage(t *testing.T) {
	t.Parallel()

	mkAg := func(augs ...episodeimport.LanguageAugmenter) *episodeimport.AggregateLanguage {
		// originalLanguage stub: 0 → Unknown, 1 → English.
		return episodeimport.NewAggregateLanguage(augs, func(id int) language.Language {
			if id == 1 {
				return language.English
			}
			return language.Unknown
		}, slog.New(slog.DiscardHandler))
	}

	t.Run("no augmenters keeps OriginalLanguage", func(t *testing.T) {
		ag := mkAg()
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{OriginalLanguage: 1}
		ag.Aggregate(le, nil)
		if len(le.Languages) != 1 || le.Languages[0] != language.English {
			t.Errorf("got %v", le.Languages)
		}
	})

	t.Run("augmenter returning nil is skipped", func(t *testing.T) {
		ag := mkAg(&stubLanguageAug{order: 1, out: nil})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{OriginalLanguage: 1}
		ag.Aggregate(le, nil)
		if len(le.Languages) != 1 || le.Languages[0] != language.English {
			t.Errorf("got %v", le.Languages)
		}
	})

	t.Run("augmenter with empty Languages skipped", func(t *testing.T) {
		ag := mkAg(&stubLanguageAug{order: 1, out: &episodeimport.AugmentLanguageResult{Languages: []language.Language{}, Confidence: episodeimport.LanguageConfidenceFilename}})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{OriginalLanguage: 1}
		ag.Aggregate(le, nil)
		if le.Languages[0] != language.English {
			t.Errorf("got %v", le.Languages)
		}
	})

	t.Run("[Unknown] singleton is treated as no-opinion", func(t *testing.T) {
		ag := mkAg(&stubLanguageAug{order: 1, out: &episodeimport.AugmentLanguageResult{Languages: []language.Language{language.Unknown}, Confidence: episodeimport.LanguageConfidenceFilename}})
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{OriginalLanguage: 1}
		ag.Aggregate(le, nil)
		if le.Languages[0] != language.English {
			t.Errorf("got %v", le.Languages)
		}
	})

	t.Run("higher-order augmenter replaces", func(t *testing.T) {
		ag := mkAg(
			&stubLanguageAug{order: 1, out: &episodeimport.AugmentLanguageResult{Languages: []language.Language{language.German}, Confidence: episodeimport.LanguageConfidenceFilename}},
			&stubLanguageAug{order: 2, out: &episodeimport.AugmentLanguageResult{Languages: []language.Language{language.French}, Confidence: episodeimport.LanguageConfidenceFoldername}},
		)
		le := episodeimport.NewLocalEpisode()
		le.Series = &series.Series{OriginalLanguage: 1}
		ag.Aggregate(le, nil)
		if len(le.Languages) != 1 || le.Languages[0] != language.French {
			t.Errorf("got %v", le.Languages)
		}
	})
}

// --- AggregateQuality ----------------------------------------------

type stubQualityAug struct {
	order int
	out   *episodeimport.AugmentQualityResult
}

func (s stubQualityAug) Order() int            { return s.order }
func (s stubQualityAug) AugmenterName() string { return "stub" }
func (s stubQualityAug) AugmentQuality(_ *episodeimport.LocalEpisode, _ *downloadclients.Item) *episodeimport.AugmentQualityResult {
	return s.out
}

func TestAggregateQuality(t *testing.T) {
	t.Parallel()

	t.Run("no augmenters → SourceUnknown + Resolution=0 → Unknown quality", func(t *testing.T) {
		ag := episodeimport.NewAggregateQuality(nil, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		ag.Aggregate(le, nil)
		// FindBySourceAndResolution(Unknown, 0) returns Unknown.
		if le.Quality.Quality.ID != 0 {
			t.Errorf("quality ID = %d, want 0 (Unknown)", le.Quality.Quality.ID)
		}
	})

	t.Run("higher confidence overrides", func(t *testing.T) {
		// Augmenter A: source=HDTV at Tag, resolution=720 at Tag.
		// Augmenter B: source=Web at MediaInfo, resolution=1080 at MediaInfo.
		// Final: Web/1080p (MediaInfo wins both components).
		rev := quality.Revision{Version: 1}
		augA := stubQualityAug{order: 1, out: &episodeimport.AugmentQualityResult{
			Source: quality.SourceTelevision, SourceConfidence: episodeimport.QualityConfidenceTag,
			Resolution: 720, ResolutionConfidence: episodeimport.QualityConfidenceTag,
			Revision: &rev, RevisionConfidence: episodeimport.QualityConfidenceTag,
		}}
		augB := stubQualityAug{order: 2, out: &episodeimport.AugmentQualityResult{
			Source: quality.SourceWeb, SourceConfidence: episodeimport.QualityConfidenceMediaInfo,
			Resolution: 1080, ResolutionConfidence: episodeimport.QualityConfidenceMediaInfo,
		}}
		ag := episodeimport.NewAggregateQuality([]episodeimport.QualityAugmenter{augA, augB}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		ag.Aggregate(le, nil)
		if le.Quality.Quality.Source != quality.SourceWeb {
			t.Errorf("Source = %v, want Web", le.Quality.Quality.Source)
		}
		if le.Quality.Quality.Resolution != 1080 {
			t.Errorf("Resolution = %d, want 1080", le.Quality.Quality.Resolution)
		}
		if le.Quality.ResolutionDetectionSource != quality.DetectionMediaInfo {
			t.Errorf("ResolutionDetectionSource = %v", le.Quality.ResolutionDetectionSource)
		}
	})

	t.Run("equal confidence revision tiebreak picks higher", func(t *testing.T) {
		revLow := quality.Revision{Version: 1}
		revHigh := quality.Revision{Version: 3}
		augA := stubQualityAug{order: 1, out: &episodeimport.AugmentQualityResult{
			Revision: &revLow, RevisionConfidence: episodeimport.QualityConfidenceTag,
		}}
		augB := stubQualityAug{order: 2, out: &episodeimport.AugmentQualityResult{
			Revision: &revHigh, RevisionConfidence: episodeimport.QualityConfidenceTag,
		}}
		ag := episodeimport.NewAggregateQuality([]episodeimport.QualityAugmenter{augA, augB}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		ag.Aggregate(le, nil)
		if le.Quality.Revision.Version != 3 {
			t.Errorf("revision.Version = %d, want 3", le.Quality.Revision.Version)
		}
	})
}

func TestIsSubtitleExtension(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".srt", ".SRT", ".ass", ".vtt"} {
		if !episodeimport.IsSubtitleExtension(ext) {
			t.Errorf("IsSubtitleExtension(%q) = false", ext)
		}
	}
	for _, ext := range []string{"", ".mkv", ".mp4", ".unknown"} {
		if episodeimport.IsSubtitleExtension(ext) {
			t.Errorf("IsSubtitleExtension(%q) = true (should be false)", ext)
		}
	}
}

// --- AggregationService --------------------------------------------

type stubFileSize struct {
	size int64
	err  error
}

func (s stubFileSize) GetFileSize(string) (int64, error) { return s.size, s.err }

type stubVideoMI struct {
	out *mediainfo.Model
	err error
}

func (s stubVideoMI) GetMediaInfo(string) (*mediainfo.Model, error) { return s.out, s.err }

type stubAggCfg struct{ enable bool }

func (s stubAggCfg) EnableMediaInfo() bool { return s.enable }

type stubAggregator struct {
	order int
	calls int
}

func (s *stubAggregator) Order() int { return s.order }
func (s *stubAggregator) Aggregate(le *episodeimport.LocalEpisode, _ *downloadclients.Item) *episodeimport.LocalEpisode {
	s.calls++
	return le
}

type panicAggregator struct{}

func (panicAggregator) Order() int { return 99 }
func (panicAggregator) Aggregate(_ *episodeimport.LocalEpisode, _ *downloadclients.Item) *episodeimport.LocalEpisode {
	panic("boom")
}

func TestAggregationService_Augment(t *testing.T) {
	t.Parallel()

	mk := func(t *testing.T, augs []episodeimport.LocalEpisodeAggregator, disk episodeimport.FileSizeReader, video episodeimport.VideoMediaInfoReader, cfg episodeimport.AggregationConfig) *episodeimport.AggregationService {
		t.Helper()
		s, err := episodeimport.NewAggregationService(augs, disk, video, cfg, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatalf("NewAggregationService: %v", err)
		}
		return s
	}

	t.Run("nil disk rejected", func(t *testing.T) {
		if _, err := episodeimport.NewAggregationService(nil, nil, nil, nil, nil); err == nil {
			t.Errorf("expected nil-disk error")
		}
	})

	t.Run("media file with no parsed info errors", func(t *testing.T) {
		s := mk(t, nil, stubFileSize{size: 1000}, nil, nil)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/x.mkv"
		_, err := s.Augment(le, nil)
		if !errors.Is(err, episodeimport.ErrAugmentingFailed) {
			t.Errorf("err = %v, want ErrAugmentingFailed", err)
		}
	})

	t.Run("non-media file w/o parsed info OK", func(t *testing.T) {
		s := mk(t, nil, stubFileSize{size: 5}, nil, nil)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/x.txt"
		_, err := s.Augment(le, nil)
		if err != nil {
			t.Errorf("err = %v, want nil for non-media path", err)
		}
	})

	t.Run("size + sceneName + mediainfo populated", func(t *testing.T) {
		mi := &mediainfo.Model{Width: 1920, Height: 1080, RunTime: datastore.STDuration(0)}
		s := mk(t, nil, stubFileSize{size: 1024}, stubVideoMI{out: mi}, stubAggCfg{enable: true})
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/x.mkv"
		le.SceneSource = true
		le.SceneName = ""
		le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseTitle: "abc.mkv"}
		if _, err := s.Augment(le, nil); err != nil {
			t.Fatalf("Augment: %v", err)
		}
		if le.Size != 1024 {
			t.Errorf("Size = %d", le.Size)
		}
		if le.MediaInfo != mi {
			t.Errorf("MediaInfo = %+v", le.MediaInfo)
		}
	})

	t.Run("ExistingFile + EnableMediaInfo=false skips MediaInfo", func(t *testing.T) {
		v := &spyVideo{out: &mediainfo.Model{Width: 1920}}
		s := mk(t, nil, stubFileSize{size: 1024}, v, stubAggCfg{enable: false})
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/x.mkv"
		le.ExistingFile = true
		le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{}
		if _, err := s.Augment(le, nil); err != nil {
			t.Fatalf("Augment: %v", err)
		}
		if v.calls != 0 {
			t.Errorf("video.GetMediaInfo called %d times; want 0 (ExistingFile + EnableMediaInfo=false)", v.calls)
		}
	})

	t.Run("aggregators called in Order ascending", func(t *testing.T) {
		// register out of order; expect sorted execution order
		// (called counter increments on each Order() call, so we
		// just verify each ran).
		a, b, c := &stubAggregator{order: 5}, &stubAggregator{order: 1}, &stubAggregator{order: 3}
		s := mk(t, []episodeimport.LocalEpisodeAggregator{a, b, c}, stubFileSize{}, nil, nil)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/x.mkv"
		le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{}
		if _, err := s.Augment(le, nil); err != nil {
			t.Fatalf("Augment: %v", err)
		}
		if a.calls != 1 || b.calls != 1 || c.calls != 1 {
			t.Errorf("calls a=%d b=%d c=%d", a.calls, b.calls, c.calls)
		}
	})

	t.Run("panicking aggregator does not poison batch", func(t *testing.T) {
		good := &stubAggregator{order: 100}
		s := mk(t, []episodeimport.LocalEpisodeAggregator{panicAggregator{}, good}, stubFileSize{}, nil, nil)
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/x.mkv"
		le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{}
		if _, err := s.Augment(le, nil); err != nil {
			t.Errorf("err = %v, want nil (panic recovered)", err)
		}
		if good.calls != 1 {
			t.Errorf("good aggregator should have run despite panic upstream")
		}
	})
}

type spyVideo struct {
	out   *mediainfo.Model
	err   error
	calls int
}

func (s *spyVideo) GetMediaInfo(string) (*mediainfo.Model, error) {
	s.calls++
	return s.out, s.err
}
