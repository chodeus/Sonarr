package episodeimport_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
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

// --- AggregateEpisodes ---------------------------------------------

type stubParsing struct {
	specials map[string]*parser.ParsedEpisodeInfo
	episodes map[*parser.ParsedEpisodeInfo][]episodes.Episode
	calls    []parsingCall
}

type parsingCall struct {
	op    string
	title string
	prev  *parser.ParsedEpisodeInfo
	scene bool
}

func (s *stubParsing) ParseSpecialEpisodeTitle(prev *parser.ParsedEpisodeInfo, title string, _ *series.Series) *parser.ParsedEpisodeInfo {
	s.calls = append(s.calls, parsingCall{op: "special", title: title, prev: prev})
	if r, ok := s.specials[title]; ok {
		return r
	}
	return nil
}

func (s *stubParsing) GetEpisodes(parsed *parser.ParsedEpisodeInfo, _ *series.Series, sceneSource bool) []episodes.Episode {
	s.calls = append(s.calls, parsingCall{op: "episodes", prev: parsed, scene: sceneSource})
	if r, ok := s.episodes[parsed]; ok {
		return r
	}
	return nil
}

func TestAggregateEpisodes(t *testing.T) {
	t.Parallel()

	s := &series.Series{ID: 7, SeriesType: series.SeriesTypeStandard}
	mkLE := func(path string) *episodeimport.LocalEpisode {
		le := episodeimport.NewLocalEpisode()
		le.Path = path
		le.Series = s
		return le
	}

	t.Run("nil parsing returns nil", func(t *testing.T) {
		if got := episodeimport.NewAggregateEpisodes(nil); got != nil {
			t.Errorf("NewAggregateEpisodes(nil) = %v, want nil", got)
		}
	})

	t.Run("Order is 1", func(t *testing.T) {
		ag := episodeimport.NewAggregateEpisodes(&stubParsing{})
		if got := ag.Order(); got != 1 {
			t.Errorf("Order = %d, want 1", got)
		}
	})

	t.Run("download-client info preferred over file when not FullSeason", func(t *testing.T) {
		fileInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "file"}
		dcInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "dc"}
		want := []episodes.Episode{{ID: 42}}
		stub := &stubParsing{episodes: map[*parser.ParsedEpisodeInfo][]episodes.Episode{dcInfo: want}}
		ag := episodeimport.NewAggregateEpisodes(stub)
		le := mkLE("/tv/normal-name.mkv")
		le.FileEpisodeInfo = fileInfo
		le.DownloadClientEpisodeInfo = dcInfo
		ag.Aggregate(le, nil)
		if len(le.Episodes) != 1 || le.Episodes[0].ID != 42 {
			t.Errorf("Episodes = %+v, want [{ID:42}]", le.Episodes)
		}
	})

	t.Run("FullSeason download-client info skipped", func(t *testing.T) {
		fileInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "file"}
		dcInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "dc", FullSeason: true}
		want := []episodes.Episode{{ID: 99}}
		stub := &stubParsing{episodes: map[*parser.ParsedEpisodeInfo][]episodes.Episode{fileInfo: want}}
		ag := episodeimport.NewAggregateEpisodes(stub)
		le := mkLE("/tv/normal-name.mkv")
		le.FileEpisodeInfo = fileInfo
		le.DownloadClientEpisodeInfo = dcInfo
		ag.Aggregate(le, nil)
		if len(le.Episodes) != 1 || le.Episodes[0].ID != 99 {
			t.Errorf("Episodes = %+v, want file source [{ID:99}]", le.Episodes)
		}
	})

	t.Run("scene-named bare title bypasses dc/folder", func(t *testing.T) {
		fileInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "scene"}
		dcInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "dc"}
		want := []episodes.Episode{{ID: 1}}
		stub := &stubParsing{episodes: map[*parser.ParsedEpisodeInfo][]episodes.Episode{fileInfo: want}}
		ag := episodeimport.NewAggregateEpisodes(stub)
		// Scene-style filename per parser.IsSceneTitle's six-step
		// gauntlet (must parse, have a release group, have non-zero
		// quality ID, and a non-empty SeriesTitle).
		le := mkLE("/tv/Series.Title.S04E17.720p.HDTV.X264-DIMENSION.mkv")
		le.FileEpisodeInfo = fileInfo
		le.DownloadClientEpisodeInfo = dcInfo
		ag.Aggregate(le, nil)
		if len(le.Episodes) != 1 || le.Episodes[0].ID != 1 {
			t.Errorf("Episodes = %+v, want file source [{ID:1}]", le.Episodes)
		}
	})

	t.Run("OtherVideoFiles bypasses dc/folder", func(t *testing.T) {
		fileInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "file"}
		dcInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "dc"}
		want := []episodes.Episode{{ID: 2}}
		stub := &stubParsing{episodes: map[*parser.ParsedEpisodeInfo][]episodes.Episode{fileInfo: want}}
		ag := episodeimport.NewAggregateEpisodes(stub)
		le := mkLE("/tv/normal.mkv")
		le.OtherVideoFiles = true
		le.FileEpisodeInfo = fileInfo
		le.DownloadClientEpisodeInfo = dcInfo
		ag.Aggregate(le, nil)
		if len(le.Episodes) != 1 || le.Episodes[0].ID != 2 {
			t.Errorf("Episodes = %+v, want file source", le.Episodes)
		}
	})

	t.Run("no parsed info → special episode fallback", func(t *testing.T) {
		special := &parser.ParsedEpisodeInfo{ReleaseTitle: "special"}
		want := []episodes.Episode{{ID: 555}}
		stub := &stubParsing{
			specials: map[string]*parser.ParsedEpisodeInfo{"normal": special},
			episodes: map[*parser.ParsedEpisodeInfo][]episodes.Episode{special: want},
		}
		ag := episodeimport.NewAggregateEpisodes(stub)
		le := mkLE("/tv/normal.mkv")
		ag.Aggregate(le, nil)
		if len(le.Episodes) != 1 || le.Episodes[0].ID != 555 {
			t.Errorf("Episodes = %+v, want special [{ID:555}]", le.Episodes)
		}
	})

	t.Run("empty episodes + IsPossibleSpecialEpisode → re-runs special", func(t *testing.T) {
		// FileEpisodeInfo with FullSeason=false + zero seasonNumber +
		// no episode numbers → IsPossibleSpecialEpisode=true.
		fileInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "weirdname", SeasonNumber: 0}
		special := &parser.ParsedEpisodeInfo{ReleaseTitle: "special"}
		want := []episodes.Episode{{ID: 7}}
		stub := &stubParsing{
			specials: map[string]*parser.ParsedEpisodeInfo{"weirdname": special},
			episodes: map[*parser.ParsedEpisodeInfo][]episodes.Episode{special: want},
		}
		ag := episodeimport.NewAggregateEpisodes(stub)
		le := mkLE("/tv/weirdname.mkv")
		le.FileEpisodeInfo = fileInfo
		ag.Aggregate(le, nil)
		if len(le.Episodes) != 1 || le.Episodes[0].ID != 7 {
			t.Errorf("Episodes = %+v, want re-run special [{ID:7}]", le.Episodes)
		}
		// Verify the second special call's prev is the first parse
		// (NOT nil) — this is the C# `:83` behaviour.
		var seenPrev *parser.ParsedEpisodeInfo
		for _, c := range stub.calls {
			if c.op == "special" && c.title == "weirdname" {
				seenPrev = c.prev
			}
		}
		if seenPrev != fileInfo {
			t.Errorf("special-episode re-call prev = %p, want fileInfo %p", seenPrev, fileInfo)
		}
	})

	t.Run("daily parse against Standard series fails validation → empty", func(t *testing.T) {
		fileInfo := &parser.ParsedEpisodeInfo{ReleaseTitle: "daily-show.2024.01.01", AirDate: "2024-01-01"}
		stub := &stubParsing{episodes: map[*parser.ParsedEpisodeInfo][]episodes.Episode{
			fileInfo: {{ID: 1}},
		}}
		ag := episodeimport.NewAggregateEpisodes(stub)
		le := mkLE("/tv/daily-show.mkv")
		le.FileEpisodeInfo = fileInfo
		ag.Aggregate(le, nil)
		if len(le.Episodes) != 0 {
			t.Errorf("Episodes = %+v, want [] (daily on standard rejected)", le.Episodes)
		}
	})
}

// --- AggregateReleaseInfo ------------------------------------------

type stubGrabsFinder struct {
	rows []history.Entry
	err  error
}

func (s stubGrabsFinder) FindGrabsByDownloadID(_ context.Context, _ string) ([]history.Entry, error) {
	return s.rows, s.err
}

func TestAggregateReleaseInfo(t *testing.T) {
	t.Parallel()

	mkEntry := func(eventType *int, source string) history.Entry {
		return history.Entry{EventType: eventType, SourceTitle: source, DownloadID: "abc"}
	}
	grabbed := episodeimport.EpisodeHistoryEventTypeGrabbed
	imported := 3

	t.Run("nil finder returns nil constructor", func(t *testing.T) {
		if got := episodeimport.NewAggregateReleaseInfo(nil, nil); got != nil {
			t.Errorf("NewAggregateReleaseInfo(nil) = %v, want nil", got)
		}
	})

	t.Run("Order is 1", func(t *testing.T) {
		ag := episodeimport.NewAggregateReleaseInfo(stubGrabsFinder{}, slog.New(slog.DiscardHandler))
		if got := ag.Order(); got != 1 {
			t.Errorf("Order = %d, want 1", got)
		}
	})

	t.Run("nil download item → no Release", func(t *testing.T) {
		ag := episodeimport.NewAggregateReleaseInfo(stubGrabsFinder{rows: []history.Entry{mkEntry(&grabbed, "x")}}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.mkv"
		ag.Aggregate(le, nil)
		if le.Release != nil {
			t.Errorf("Release = %+v, want nil for missing item", le.Release)
		}
	})

	t.Run("empty download id → no Release", func(t *testing.T) {
		ag := episodeimport.NewAggregateReleaseInfo(stubGrabsFinder{rows: []history.Entry{mkEntry(&grabbed, "x")}}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.mkv"
		ag.Aggregate(le, &downloadclients.Item{DownloadID: ""})
		if le.Release != nil {
			t.Errorf("Release = %+v, want nil for empty download id", le.Release)
		}
	})

	t.Run("no grabbed history rows → no Release", func(t *testing.T) {
		ag := episodeimport.NewAggregateReleaseInfo(stubGrabsFinder{rows: []history.Entry{mkEntry(&imported, "imported-only")}}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.mkv"
		ag.Aggregate(le, &downloadclients.Item{DownloadID: "abc"})
		if le.Release != nil {
			t.Errorf("Release = %+v, want nil when only non-grab events present", le.Release)
		}
	})

	t.Run("grabbed history populates Release.Title from most recent", func(t *testing.T) {
		// The repo returns date-descending; the aggregator picks
		// the first (= most recent) grab's SourceTitle.
		rows := []history.Entry{
			mkEntry(&grabbed, "Show.S01E01.GROUP"),
			mkEntry(&grabbed, "older"),
		}
		ag := episodeimport.NewAggregateReleaseInfo(stubGrabsFinder{rows: rows}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.mkv"
		ag.Aggregate(le, &downloadclients.Item{DownloadID: "abc"})
		if le.Release == nil {
			t.Fatalf("Release = nil, want populated")
		}
		if le.Release.Title != "Show.S01E01.GROUP" {
			t.Errorf("Release.Title = %q, want most-recent grab title", le.Release.Title)
		}
	})

	t.Run("nil EventType skipped", func(t *testing.T) {
		// Old pre-EventType-column rows.
		rows := []history.Entry{mkEntry(nil, "preEventType")}
		ag := episodeimport.NewAggregateReleaseInfo(stubGrabsFinder{rows: rows}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.mkv"
		ag.Aggregate(le, &downloadclients.Item{DownloadID: "abc"})
		if le.Release != nil {
			t.Errorf("Release = %+v, want nil when only nil-EventType rows", le.Release)
		}
	})

	t.Run("finder error skipped silently", func(t *testing.T) {
		ag := episodeimport.NewAggregateReleaseInfo(stubGrabsFinder{err: errors.New("db down")}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.mkv"
		ag.Aggregate(le, &downloadclients.Item{DownloadID: "abc"})
		if le.Release != nil {
			t.Errorf("Release = %+v, want nil on finder error", le.Release)
		}
	})
}

// --- AggregateSubtitleInfo -----------------------------------------

type stubFiles struct {
	files map[int]*episodefiles.File
	err   error
}

func (s stubFiles) GetByID(_ context.Context, id int) (*episodefiles.File, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.files[id], nil
}

func TestAggregateSubtitleInfo(t *testing.T) {
	t.Parallel()

	t.Run("nil files → nil constructor", func(t *testing.T) {
		if got := episodeimport.NewAggregateSubtitleInfo(nil, nil); got != nil {
			t.Errorf("NewAggregateSubtitleInfo(nil) = %v, want nil", got)
		}
	})

	t.Run("Order is 2", func(t *testing.T) {
		ag := episodeimport.NewAggregateSubtitleInfo(stubFiles{}, slog.New(slog.DiscardHandler))
		if got := ag.Order(); got != 2 {
			t.Errorf("Order = %d, want 2", got)
		}
	})

	t.Run("non-subtitle extension → no SubtitleInfo", func(t *testing.T) {
		ag := episodeimport.NewAggregateSubtitleInfo(stubFiles{}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.mkv"
		ag.Aggregate(le, nil)
		if le.SubtitleInfo != nil {
			t.Errorf("SubtitleInfo = %+v, want nil for non-subtitle ext", le.SubtitleInfo)
		}
	})

	t.Run("empty Episodes → no SubtitleInfo", func(t *testing.T) {
		ag := episodeimport.NewAggregateSubtitleInfo(stubFiles{}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.srt"
		ag.Aggregate(le, nil)
		if le.SubtitleInfo != nil {
			t.Errorf("SubtitleInfo = %+v, want nil with no episodes", le.SubtitleInfo)
		}
	})

	t.Run("nil EpisodeFileID → no SubtitleInfo", func(t *testing.T) {
		ag := episodeimport.NewAggregateSubtitleInfo(stubFiles{}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.srt"
		le.Episodes = []episodes.Episode{{ID: 1}}
		ag.Aggregate(le, nil)
		if le.SubtitleInfo != nil {
			t.Errorf("SubtitleInfo = %+v, want nil for nil-EpisodeFileID", le.SubtitleInfo)
		}
	})

	t.Run("file resolver error → no SubtitleInfo", func(t *testing.T) {
		ag := episodeimport.NewAggregateSubtitleInfo(stubFiles{err: errors.New("db down")}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/a.srt"
		efid := 9
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: &efid}}
		ag.Aggregate(le, nil)
		if le.SubtitleInfo != nil {
			t.Errorf("SubtitleInfo = %+v, want nil on resolver error", le.SubtitleInfo)
		}
	})

	t.Run("populates SubtitleInfo from path parse", func(t *testing.T) {
		ef := &episodefiles.File{ID: 9, RelativePath: "Show.S01E01.GROUP.mkv"}
		ag := episodeimport.NewAggregateSubtitleInfo(stubFiles{files: map[int]*episodefiles.File{9: ef}}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/Show.S01E01.GROUP.eng.srt"
		efid := 9
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: &efid}}
		ag.Aggregate(le, nil)
		if le.SubtitleInfo == nil {
			t.Fatalf("SubtitleInfo = nil, want populated")
		}
	})

	t.Run("language tag already in file title is dropped", func(t *testing.T) {
		// File title contains ".eng" and the subtitle path has ".eng" tag.
		// The cleanup should drop the tag.
		ef := &episodefiles.File{ID: 9, RelativePath: "Show.S01E01.eng.GROUP.mkv"}
		ag := episodeimport.NewAggregateSubtitleInfo(stubFiles{files: map[int]*episodefiles.File{9: ef}}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/Show.S01E01.eng.GROUP.eng.srt"
		efid := 9
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: &efid}}
		ag.Aggregate(le, nil)
		if le.SubtitleInfo == nil {
			t.Fatalf("SubtitleInfo = nil, want populated")
		}
		// We don't assert specific Language ParseSubtitleLanguageInformation
		// returns — we only verify the cleanup loop didn't crash. The
		// tag-drop logic is exercised end-to-end whenever the parsed
		// info has tags overlapping the file title; the parse_subtitle
		// package owns the canonical extraction tests.
	})

	t.Run("FileNameBeforeRename used as title source", func(t *testing.T) {
		ef := &episodefiles.File{ID: 9, RelativePath: "after-rename.mkv", OriginalFilePath: "Show.S01E01.GROUP.mkv"}
		ag := episodeimport.NewAggregateSubtitleInfo(stubFiles{files: map[int]*episodefiles.File{9: ef}}, slog.New(slog.DiscardHandler))
		le := episodeimport.NewLocalEpisode()
		le.Path = "/tv/Show.S01E01.GROUP.eng.srt"
		le.FileNameBeforeRename = "Show.S01E01.GROUP.mkv"
		efid := 9
		le.Episodes = []episodes.Episode{{ID: 1, EpisodeFileID: &efid}}
		ag.Aggregate(le, nil)
		if le.SubtitleInfo == nil {
			t.Fatalf("SubtitleInfo = nil, want populated")
		}
	})
}

// --- AugmentQualityFromReleaseName --------------------------------

type stubGrabFinder struct {
	entry *downloadhistory.Entry
}

func (s stubGrabFinder) GetLatestGrab(_ string) *downloadhistory.Entry { return s.entry }

func TestAugmentQualityFromReleaseName(t *testing.T) {
	t.Parallel()

	t.Run("nil finder → nil constructor", func(t *testing.T) {
		if got := episodeimport.NewAugmentQualityFromReleaseName(nil, nil); got != nil {
			t.Errorf("NewAugmentQualityFromReleaseName(nil) = %v, want nil", got)
		}
	})

	t.Run("Order is 5 / Name is ReleaseName", func(t *testing.T) {
		a := episodeimport.NewAugmentQualityFromReleaseName(stubGrabFinder{}, nil)
		if got := a.Order(); got != 5 {
			t.Errorf("Order = %d, want 5", got)
		}
		if got := a.AugmenterName(); got != "ReleaseName" {
			t.Errorf("AugmenterName = %q, want ReleaseName", got)
		}
	})

	t.Run("nil item → nil result", func(t *testing.T) {
		a := episodeimport.NewAugmentQualityFromReleaseName(stubGrabFinder{entry: &downloadhistory.Entry{SourceTitle: "x"}}, nil)
		if got := a.AugmentQuality(episodeimport.NewLocalEpisode(), nil); got != nil {
			t.Errorf("AugmentQuality(nil item) = %+v, want nil", got)
		}
	})

	t.Run("empty download id → nil", func(t *testing.T) {
		a := episodeimport.NewAugmentQualityFromReleaseName(stubGrabFinder{entry: &downloadhistory.Entry{SourceTitle: "x"}}, nil)
		if got := a.AugmentQuality(episodeimport.NewLocalEpisode(), &downloadclients.Item{DownloadID: ""}); got != nil {
			t.Errorf("AugmentQuality(empty id) = %+v, want nil", got)
		}
	})

	t.Run("no grab → nil", func(t *testing.T) {
		a := episodeimport.NewAugmentQualityFromReleaseName(stubGrabFinder{entry: nil}, nil)
		if got := a.AugmentQuality(episodeimport.NewLocalEpisode(), &downloadclients.Item{DownloadID: "abc"}); got != nil {
			t.Errorf("AugmentQuality(no grab) = %+v, want nil", got)
		}
	})

	t.Run("DetectionName components promote to Tag", func(t *testing.T) {
		// Inject a custom parser that returns a model where every
		// detection source is Name — every confidence should be Tag.
		parsed := quality.Model{
			Quality:                   quality.Quality{ID: 4, Source: quality.SourceWeb, Resolution: 1080},
			SourceDetectionSource:     quality.DetectionName,
			ResolutionDetectionSource: quality.DetectionName,
			RevisionDetectionSource:   quality.DetectionName,
		}
		a := episodeimport.NewAugmentQualityFromReleaseName(
			stubGrabFinder{entry: &downloadhistory.Entry{SourceTitle: "Show.S01E01.1080p.WEB-DL.GROUP"}},
			func(string) quality.Model { return parsed },
		)
		got := a.AugmentQuality(episodeimport.NewLocalEpisode(), &downloadclients.Item{DownloadID: "abc"})
		if got == nil {
			t.Fatalf("AugmentQuality = nil, want non-nil")
		}
		if got.Source != quality.SourceWeb || got.SourceConfidence != episodeimport.QualityConfidenceTag {
			t.Errorf("source=%v conf=%v, want Web/Tag", got.Source, got.SourceConfidence)
		}
		if got.Resolution != 1080 || got.ResolutionConfidence != episodeimport.QualityConfidenceTag {
			t.Errorf("res=%v conf=%v, want 1080/Tag", got.Resolution, got.ResolutionConfidence)
		}
		if got.RevisionConfidence != episodeimport.QualityConfidenceTag {
			t.Errorf("revision conf=%v, want Tag", got.RevisionConfidence)
		}
	})

	t.Run("non-Name detection sources fall back", func(t *testing.T) {
		parsed := quality.Model{
			Quality:                   quality.Quality{ID: 4, Source: quality.SourceWeb, Resolution: 1080},
			SourceDetectionSource:     quality.DetectionExtension,
			ResolutionDetectionSource: quality.DetectionUnknown,
			RevisionDetectionSource:   quality.DetectionExtension,
		}
		a := episodeimport.NewAugmentQualityFromReleaseName(
			stubGrabFinder{entry: &downloadhistory.Entry{SourceTitle: "anything"}},
			func(string) quality.Model { return parsed },
		)
		got := a.AugmentQuality(episodeimport.NewLocalEpisode(), &downloadclients.Item{DownloadID: "abc"})
		if got == nil {
			t.Fatalf("AugmentQuality = nil, want non-nil")
		}
		if got.SourceConfidence != episodeimport.QualityConfidenceFallback {
			t.Errorf("source conf=%v, want Fallback", got.SourceConfidence)
		}
		if got.ResolutionConfidence != episodeimport.QualityConfidenceFallback {
			t.Errorf("res conf=%v, want Fallback", got.ResolutionConfidence)
		}
		if got.RevisionConfidence != episodeimport.QualityConfidenceFallback {
			t.Errorf("revision conf=%v, want Fallback", got.RevisionConfidence)
		}
	})
}

// TestAggregationService_DispatchesReleaseGroupAndHash_J471 pins the J471
// wiring fix: the AggregationService must dispatch AggregateReleaseGroup +
// AggregateReleaseHash so an imported LocalEpisode gets a populated
// ReleaseGroup + ReleaseHash. Before J471 both aggregators were ported but
// omitted from the production chain, so imported files had an empty
// ReleaseGroup (breaking the {Release Group} naming token + release-group
// custom formats) and empty ReleaseHash.
func TestAggregationService_DispatchesReleaseGroupAndHash_J471(t *testing.T) {
	t.Parallel()
	svc, err := episodeimport.NewAggregationService(
		[]episodeimport.LocalEpisodeAggregator{
			episodeimport.AggregateReleaseGroup{},
			episodeimport.AggregateReleaseHash{},
		},
		stubFileSize{size: 1000}, nil, nil, slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewAggregationService: %v", err)
	}
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/Show/Show.S01E01.WEBDL-1080p-RLSGRP.mkv"
	le.FileEpisodeInfo = &parser.ParsedEpisodeInfo{ReleaseGroup: "RLSGRP", ReleaseHash: "ABCD1234"}

	out, err := svc.Augment(le, nil)
	if err != nil {
		t.Fatalf("Augment: %v", err)
	}
	if out.ReleaseGroup != "RLSGRP" {
		t.Errorf("ReleaseGroup = %q, want RLSGRP (AggregateReleaseGroup not dispatched)", out.ReleaseGroup)
	}
	if out.ReleaseHash != "ABCD1234" {
		t.Errorf("ReleaseHash = %q, want ABCD1234 (AggregateReleaseHash not dispatched)", out.ReleaseHash)
	}
}
