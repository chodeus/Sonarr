package extras

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/subtitlefiles"
)

type stubSubStore struct {
	rows []*subtitlefiles.SubtitleFile
}

func (s *stubSubStore) Upsert(_ context.Context, f *subtitlefiles.SubtitleFile) error {
	s.rows = append(s.rows, f)
	return nil
}

type stubFolders struct{ files []string }

func (s stubFolders) GetFiles(string, bool) ([]string, error) { return s.files, nil }

func subInput(sourcePath string) ImportInput {
	return ImportInput{
		Series:      &series.Series{ID: 7, Path: "/tv/Show", Title: "Show"},
		EpisodeFile: &episodefiles.File{ID: 55, SeasonNumber: 1, RelativePath: "Season 01/Show.S01E02.mkv"},
		SourcePath:  sourcePath,
		FileInfo:    &parser.ParsedEpisodeInfo{SeasonNumber: 1, EpisodeNumbers: []int{2}},
	}
}

func newSubMgr(store SubtitleStore, folders FolderVideoLister, samples SampleChecker) *SubtitleManager {
	return NewSubtitleManager(store, &passthroughTransferrer{}, stubHardlinkCfg{}, folders, samples, slog.New(slog.DiscardHandler))
}

func TestSubtitleManager_CanImportFile(t *testing.T) {
	t.Parallel()
	m := newSubMgr(&stubSubStore{}, nil, nil)
	if m.Order() != 1 {
		t.Errorf("Order = %d, want 1", m.Order())
	}
	if !m.CanImportFile("/x/y.srt", ".srt") || !m.CanImportFile("/x/y.ASS", ".ASS") {
		t.Error("must claim subtitle extensions")
	}
	if m.CanImportFile("/x/y.mkv", ".mkv") {
		t.Error("must not claim non-subtitle")
	}
}

func TestSubtitleManager_FilenameMatch_LanguageSuffix(t *testing.T) {
	t.Parallel()
	store := &stubSubStore{}
	m := newSubMgr(store, nil, nil)
	in := subInput("/dl/Show.S01E02.WEBDL.mkv")
	n, err := m.ImportFiles(context.Background(), in, []string{"/dl/Show.S01E02.WEBDL.eng.srt"})
	if err != nil || n != 1 {
		t.Fatalf("ImportFiles = (%d,%v), want (1,nil)", n, err)
	}
	row := store.rows[0]
	if row.RelativePath != "Season 01/Show.S01E02.en.srt" {
		t.Errorf("RelativePath = %q, want 'Season 01/Show.S01E02.en.srt'", row.RelativePath)
	}
	if row.Language != language.English.ID || row.SeriesID != 7 || row.EpisodeFileID != 55 || row.Extension != ".srt" {
		t.Errorf("row = %+v", row)
	}
}

func TestSubtitleManager_MultipleCopies_Numbered(t *testing.T) {
	t.Parallel()
	store := &stubSubStore{}
	m := newSubMgr(store, nil, nil)
	in := subInput("/dl/Show.S01E02.WEBDL.mkv")
	// Two English, no tags → same group → copies .1.en / .2.en.
	n, _ := m.ImportFiles(context.Background(), in, []string{
		"/dl/Show.S01E02.WEBDL.eng.srt",
		"/dl/Show.S01E02.WEBDL.en.srt",
	})
	if n != 2 {
		t.Fatalf("imported %d, want 2", n)
	}
	got := []string{store.rows[0].RelativePath, store.rows[1].RelativePath}
	want := map[string]bool{
		"Season 01/Show.S01E02.1.en.srt": true,
		"Season 01/Show.S01E02.2.en.srt": true,
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected copy path %q (got %v)", g, got)
		}
	}
}

func TestSubtitleManager_SingleVideoFallback(t *testing.T) {
	t.Parallel()
	store := &stubSubStore{}
	// One non-matching subtitle, but the folder has exactly one video.
	folders := stubFolders{files: []string{"/dl/Show.S01E02.mkv"}}
	m := newSubMgr(store, folders, nil)
	in := subInput("/dl/Show.S01E02.mkv")
	n, _ := m.ImportFiles(context.Background(), in, []string{"/dl/random-subtitle.srt"})
	if n != 1 || len(store.rows) != 1 {
		t.Fatalf("single-video fallback: imported %d rows=%d, want 1", n, len(store.rows))
	}
}

func TestSubtitleManager_SingleVideoFallback_TooManyVideos(t *testing.T) {
	t.Parallel()
	store := &stubSubStore{}
	folders := stubFolders{files: []string{"/dl/a.mkv", "/dl/b.mkv", "/dl/c.mkv"}}
	m := newSubMgr(store, folders, nil)
	in := subInput("/dl/Show.S01E02.mkv")
	n, _ := m.ImportFiles(context.Background(), in, []string{"/dl/random-subtitle.srt"})
	if n != 0 {
		t.Errorf(">2 videos must bail the fallback, imported %d", n)
	}
}

func TestSubtitleManager_NoSubtitleFiles_NoOp(t *testing.T) {
	t.Parallel()
	store := &stubSubStore{}
	m := newSubMgr(store, nil, nil)
	in := subInput("/dl/Show.S01E02.mkv")
	n, _ := m.ImportFiles(context.Background(), in, []string{"/dl/Show.S01E02.nfo"})
	if n != 0 {
		t.Errorf("non-subtitle files must be ignored, imported %d", n)
	}
}

func TestGetSuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lang  language.Language
		copy  int
		tags  []string
		multi bool
		want  string
	}{
		{language.English, 1, nil, false, ".en"},
		{language.English, 1, nil, true, ".1.en"},
		{language.English, 2, nil, true, ".2.en"},
		{language.English, 1, []string{"forced"}, false, ".en.forced"},
		{language.English, 1, []string{"forced", "sdh"}, false, ".en.forced.sdh"},
		{language.Unknown, 1, nil, false, ""},
		{language.Unknown, 1, []string{"forced"}, false, ".forced"},
	}
	for _, c := range cases {
		got := getSuffix(c.lang, c.copy, c.tags, c.multi)
		if got != c.want {
			t.Errorf("getSuffix(%v,%d,%v,%v) = %q, want %q", c.lang, c.copy, c.tags, c.multi, got, c.want)
		}
	}
}
