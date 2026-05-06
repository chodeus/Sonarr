package episodeimport_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/parser"
)

func TestGetSceneName_DownloadClientWins(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.OtherVideoFiles = false
	le.DownloadClientEpisodeInfo = &parser.ParsedEpisodeInfo{
		ReleaseTitle: "Series.S01E01.PROPER.GROUP.mkv",
		FullSeason:   false,
	}
	got := episodeimport.GetSceneName(le)
	if got != "Series.S01E01.PROPER.GROUP" {
		t.Errorf("GetSceneName = %q, want extension-stripped DC release title", got)
	}
}

func TestGetSceneName_FullSeasonDCSkipped(t *testing.T) {
	t.Parallel()
	// FullSeason DC info MUST be skipped — falls through to the
	// filename-then-folder rules.
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/Series.S01E01.HDTV.x264-GROUP.mkv"
	le.DownloadClientEpisodeInfo = &parser.ParsedEpisodeInfo{
		ReleaseTitle: "Series.S01.PACK.mkv",
		FullSeason:   true,
	}
	got := episodeimport.GetSceneName(le)
	// File name is scene-shaped → rule 2.
	if got != "Series.S01E01.HDTV.x264-GROUP" {
		t.Errorf("GetSceneName = %q, want filename basename via rule 2", got)
	}
}

func TestGetSceneName_OtherVideoFilesSkipsDCRule(t *testing.T) {
	t.Parallel()
	// OtherVideoFiles=true (multi-file batch) MUST skip the DC
	// rule even when DC info is non-FullSeason — falls through to
	// the filename rule.
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/Series.S01E01.HDTV.x264-GROUP.mkv"
	le.OtherVideoFiles = true
	le.DownloadClientEpisodeInfo = &parser.ParsedEpisodeInfo{
		ReleaseTitle: "Series.S01E01.PROPER.GROUP.mkv",
		FullSeason:   false,
	}
	got := episodeimport.GetSceneName(le)
	if got != "Series.S01E01.HDTV.x264-GROUP" {
		t.Errorf("GetSceneName = %q, want filename rule (DC suppressed by OtherVideoFiles)", got)
	}
}

func TestGetSceneName_FilenameSceneShaped(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/Series.S01E01.HDTV.x264-GROUP.mkv"
	got := episodeimport.GetSceneName(le)
	if got != "Series.S01E01.HDTV.x264-GROUP" {
		t.Errorf("GetSceneName = %q, want filename basename", got)
	}
}

func TestGetSceneName_FolderFallback(t *testing.T) {
	t.Parallel()
	// File name is NOT scene-shaped, but the folder name is — and
	// the OtherVideoFiles + FolderFullSeason gates pass.
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/Series.S01E01.HDTV.x264-GROUP/foo.mkv"
	le.FolderEpisodeInfo = &parser.ParsedEpisodeInfo{
		ReleaseTitle: "Series.S01E01.HDTV.x264-GROUP",
		FullSeason:   false,
	}
	got := episodeimport.GetSceneName(le)
	if got != "Series.S01E01.HDTV.x264-GROUP" {
		t.Errorf("GetSceneName = %q, want folder fallback", got)
	}
}

func TestGetSceneName_FolderFullSeasonSkipped(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.FolderEpisodeInfo = &parser.ParsedEpisodeInfo{
		ReleaseTitle: "Series.S01.PACK",
		FullSeason:   true,
	}
	if got := episodeimport.GetSceneName(le); got != "" {
		t.Errorf("GetSceneName = %q, want empty (folder FullSeason suppressed)", got)
	}
}

func TestGetSceneName_FolderOtherVideoFilesSkipped(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	le.OtherVideoFiles = true
	le.FolderEpisodeInfo = &parser.ParsedEpisodeInfo{
		ReleaseTitle: "Series.S01E01.HDTV.x264-GROUP",
		FullSeason:   false,
	}
	if got := episodeimport.GetSceneName(le); got != "" {
		t.Errorf("GetSceneName = %q, want empty (folder rule blocked by OtherVideoFiles)", got)
	}
}

func TestGetSceneName_NoneMatch(t *testing.T) {
	t.Parallel()
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/foo.mkv"
	if got := episodeimport.GetSceneName(le); got != "" {
		t.Errorf("GetSceneName = %q, want empty when no rule matches", got)
	}
}

func TestGetSceneName_NilSafe(t *testing.T) {
	t.Parallel()
	if got := episodeimport.GetSceneName(nil); got != "" {
		t.Errorf("nil GetSceneName = %q, want empty", got)
	}
}

func TestGetSceneName_PathWithTrailingSlash(t *testing.T) {
	t.Parallel()
	// Trailing slash on the path must be cleaned before basename
	// extraction — exercises the cleanFilePath helper.
	le := episodeimport.NewLocalEpisode()
	le.Path = "/tv/foo/Series.S01E01.HDTV.x264-GROUP.mkv  "
	got := episodeimport.GetSceneName(le)
	if got != "Series.S01E01.HDTV.x264-GROUP" {
		t.Errorf("GetSceneName = %q, want trimmed basename", got)
	}
}
