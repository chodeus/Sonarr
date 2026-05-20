package historyhandlers_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/historyhandlers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubEpisodeFileReader stubs the EpisodeFileReader interface; tests
// can pin a per-fileID episodes slice or simulate an error.
type stubEpisodeFileReader struct {
	byFile map[int][]episodes.Episode
	err    error
}

func (s *stubEpisodeFileReader) GetEpisodesByFileID(_ context.Context, fileID int) ([]episodes.Episode, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.byFile[fileID], nil
}

func renamedFile() *episodefiles.File {
	return &episodefiles.File{
		ID:           7373,
		SeriesID:     7,
		Quality:      `{"quality":3,"revision":{"version":1,"real":0,"isRepack":false}}`,
		Size:         700_000_000,
		ReleaseGroup: "GROUP",
		RelativePath: "Season 03/Show.S03E07.mkv",
		Languages:    []int{1},
		IndexerFlags: int(parser.IndexerFlagInternal),
		ReleaseType:  int(parser.ReleaseTypeSingleEpisode),
	}
}

func renamedEvent() *episodefiles.EpisodeFileRenamedEvent {
	return &episodefiles.EpisodeFileRenamedEvent{
		Series:       &series.Series{ID: 7, Path: "/tv/Show"},
		EpisodeFile:  renamedFile(),
		OriginalPath: "/tv/Show/Season 03/Show.S03E07.OldName.mkv",
	}
}

func TestHandleEpisodeFileRenamed_NilWriter(t *testing.T) {
	t.Parallel()
	r := &stubEpisodeFileReader{}
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), nil, r, renamedEvent()); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestHandleEpisodeFileRenamed_NilReader(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, nil, renamedEvent()); err == nil {
		t.Errorf("expected error on nil reader")
	}
}

func TestHandleEpisodeFileRenamed_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{}
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil event: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeFileRenamed_NoOpOnNilEpisodeFile(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{}
	ev := &episodefiles.EpisodeFileRenamedEvent{Series: &series.Series{Path: "/tv/Show"}}
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, ev); err != nil {
		t.Errorf("nil EpisodeFile: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil EpisodeFile: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeFileRenamed_NoOpOnEmptyLinked(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{byFile: map[int][]episodes.Episode{}}
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, renamedEvent()); err != nil {
		t.Errorf("empty linked: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("empty linked: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeFileRenamed_ReaderErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{err: errors.New("repo down")}
	err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, renamedEvent())
	if err == nil {
		t.Fatal("expected error from reader")
	}
	if !errors.Is(err, r.err) {
		t.Errorf("error chain missing reader error: %v", err)
	}
}

func TestHandleEpisodeFileRenamed_WritesPerEpisode(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{
		byFile: map[int][]episodes.Episode{
			7373: {{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}},
		},
	}
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, renamedEvent()); err != nil {
		t.Fatalf("HandleEpisodeFileRenamed: %v", err)
	}
	if len(w.added) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(w.added))
	}
	for i, want := range []int{11, 12} {
		got := w.added[i]
		if got.EpisodeID != want {
			t.Errorf("entry %d EpisodeID = %d, want %d", i, got.EpisodeID, want)
		}
		if got.SeriesID != 7 {
			t.Errorf("entry %d SeriesID = %d, want 7", i, got.SeriesID)
		}
		if got.SourceTitle != "/tv/Show/Season 03/Show.S03E07.OldName.mkv" {
			t.Errorf("entry %d SourceTitle = %q (want OriginalPath)", i, got.SourceTitle)
		}
		if got.DownloadID != "" {
			t.Errorf("entry %d DownloadID = %q, want empty", i, got.DownloadID)
		}
		if got.EventType == nil || history.EventType(*got.EventType) != history.EventTypeEpisodeFileRenamed {
			t.Errorf("entry %d EventType = %v", i, got.EventType)
		}
	}
}

func TestHandleEpisodeFileRenamed_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{
		byFile: map[int][]episodes.Episode{7373: {{ID: 11, SeriesID: 7}}},
	}
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, renamedEvent()); err != nil {
		t.Fatalf("HandleEpisodeFileRenamed: %v", err)
	}
	d := w.added[0].Data
	cases := []struct{ key, want string }{
		{"SourcePath", "/tv/Show/Season 03/Show.S03E07.OldName.mkv"},
		{"SourceRelativePath", "Season 03/Show.S03E07.OldName.mkv"},
		{"Path", "/tv/Show/Season 03/Show.S03E07.mkv"},
		{"RelativePath", "Season 03/Show.S03E07.mkv"},
		{"ReleaseGroup", "GROUP"},
		{"Size", "700000000"},
		{"IndexerFlags", "Internal"},
		{"ReleaseType", "SingleEpisode"},
	}
	for _, c := range cases {
		if got := d[c.key]; got != c.want {
			t.Errorf("Data[%q] = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestHandleEpisodeFileRenamed_NilSeriesFallsBack(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{
		byFile: map[int][]episodes.Episode{7373: {{ID: 11, SeriesID: 7}}},
	}
	ev := renamedEvent()
	ev.Series = nil
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, ev); err != nil {
		t.Fatalf("HandleEpisodeFileRenamed: %v", err)
	}
	d := w.added[0].Data
	if got := d["Path"]; got != "Season 03/Show.S03E07.mkv" {
		t.Errorf("Path (nil series) = %q, want fallback to RelativePath", got)
	}
	if got := d["SourceRelativePath"]; got != ev.OriginalPath {
		t.Errorf("SourceRelativePath (nil series) = %q, want OriginalPath fallback", got)
	}
}

func TestHandleEpisodeFileRenamed_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	r := &stubEpisodeFileReader{
		byFile: map[int][]episodes.Episode{7373: {{ID: 11, SeriesID: 7}}},
	}
	err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, renamedEvent())
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing repo error: %v", err)
	}
}

func TestHandleEpisodeFileRenamed_LanguagesPerEpisodeIndependent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{
		byFile: map[int][]episodes.Episode{7373: {{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}}},
	}
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, renamedEvent()); err != nil {
		t.Fatalf("HandleEpisodeFileRenamed: %v", err)
	}
	if len(w.added) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(w.added))
	}
	w.added[0].Languages[0] = -999
	if w.added[1].Languages[0] == -999 {
		t.Errorf("Languages slice aliased between per-episode entries")
	}
}

func TestHandleEpisodeFileRenamed_EmptyQualityFallsBack(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := &stubEpisodeFileReader{
		byFile: map[int][]episodes.Episode{7373: {{ID: 11, SeriesID: 7}}},
	}
	ev := renamedEvent()
	ev.EpisodeFile.Quality = ""
	if err := historyhandlers.HandleEpisodeFileRenamed(t.Context(), w, r, ev); err != nil {
		t.Fatalf("HandleEpisodeFileRenamed: %v", err)
	}
	if got := w.added[0].Quality; got != "{}" {
		t.Errorf("empty Quality fallback = %q, want '{}'", got)
	}
}
