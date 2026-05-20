package historyhandlers_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/historyhandlers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

func deletedFile() *episodefiles.File {
	return &episodefiles.File{
		ID:           5151,
		SeriesID:     7,
		Quality:      `{"quality":8,"revision":{"version":1,"real":0,"isRepack":false}}`,
		Size:         2_000_000_000,
		ReleaseGroup: "GROUP",
		RelativePath: "Season 02/Show.S02E03.mkv",
		Languages:    []int{1, 4},
		IndexerFlags: int(parser.IndexerFlagFreeleech),
		ReleaseType:  int(parser.ReleaseTypeMultiEpisode),
	}
}

func deletedEvent(reason episodefiles.DeleteMediaFileReason, linked []episodes.Episode) *episodefiles.EpisodeFileDeletedEvent {
	return &episodefiles.EpisodeFileDeletedEvent{
		EpisodeFile:    deletedFile(),
		LinkedEpisodes: linked,
		Series:         &series.Series{ID: 7, Path: "/tv/Show"},
		Reason:         reason,
	}
}

func TestHandleEpisodeFileDeleted_NilWriter(t *testing.T) {
	t.Parallel()
	ev := deletedEvent(episodefiles.ReasonManual, []episodes.Episode{{ID: 11}})
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), nil, ev); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestHandleEpisodeFileDeleted_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil event: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeFileDeleted_NoOpOnNilEpisodeFile(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &episodefiles.EpisodeFileDeletedEvent{Reason: episodefiles.ReasonManual}
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Errorf("nil EpisodeFile: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil EpisodeFile: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeFileDeleted_SkipReasons(t *testing.T) {
	t.Parallel()
	for _, reason := range []episodefiles.DeleteMediaFileReason{
		episodefiles.ReasonNoLinkedEpisodes,
		episodefiles.ReasonManualOverride,
	} {
		reason := reason
		t.Run(reason.String(), func(t *testing.T) {
			t.Parallel()
			w := &stubWriter{}
			ev := deletedEvent(reason, []episodes.Episode{{ID: 11}})
			if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
				t.Errorf("%s: %v", reason.String(), err)
			}
			if len(w.added) != 0 {
				t.Errorf("%s should be skipped; got %d entries", reason.String(), len(w.added))
			}
		})
	}
}

func TestHandleEpisodeFileDeleted_NoOpOnEmptyLinkedEpisodes(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := deletedEvent(episodefiles.ReasonManual, nil)
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Errorf("empty LinkedEpisodes: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("empty LinkedEpisodes: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeFileDeleted_WritesPerEpisode(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	linked := []episodes.Episode{{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}}
	ev := deletedEvent(episodefiles.ReasonManual, linked)
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeFileDeleted: %v", err)
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
		if got.SourceTitle != "/tv/Show/Season 02/Show.S02E03.mkv" {
			t.Errorf("entry %d SourceTitle = %q", i, got.SourceTitle)
		}
		if got.DownloadID != "" {
			t.Errorf("entry %d DownloadID = %q, want empty", i, got.DownloadID)
		}
		if got.EventType == nil || history.EventType(*got.EventType) != history.EventTypeEpisodeFileDeleted {
			t.Errorf("entry %d EventType = %v, want EpisodeFileDeleted", i, got.EventType)
		}
		if got.Date.IsZero() {
			t.Errorf("entry %d Date is zero", i)
		}
	}
}

func TestHandleEpisodeFileDeleted_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := deletedEvent(episodefiles.ReasonUpgrade, []episodes.Episode{{ID: 11, SeriesID: 7}})
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeFileDeleted: %v", err)
	}
	d := w.added[0].Data
	cases := []struct{ key, want string }{
		{"Reason", "Upgrade"},
		{"ReleaseGroup", "GROUP"},
		{"Size", "2000000000"},
		{"IndexerFlags", "Freeleech"},
		{"ReleaseType", "MultiEpisode"},
	}
	for _, c := range cases {
		if got := d[c.key]; got != c.want {
			t.Errorf("Data[%q] = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestHandleEpisodeFileDeleted_QualityPassThrough(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := deletedEvent(episodefiles.ReasonManual, []episodes.Episode{{ID: 11, SeriesID: 7}})
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeFileDeleted: %v", err)
	}
	if got := w.added[0].Quality; got != ev.EpisodeFile.Quality {
		t.Errorf("Quality not pass-through: got %q, want %q", got, ev.EpisodeFile.Quality)
	}
}

func TestHandleEpisodeFileDeleted_EmptyQualityFallsBackToObject(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := deletedEvent(episodefiles.ReasonManual, []episodes.Episode{{ID: 11, SeriesID: 7}})
	ev.EpisodeFile.Quality = ""
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeFileDeleted: %v", err)
	}
	if got := w.added[0].Quality; got != "{}" {
		t.Errorf("Empty Quality fallback = %q, want '{}'", got)
	}
}

func TestHandleEpisodeFileDeleted_LanguagesPerEpisodeIndependent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	linked := []episodes.Episode{{ID: 11, SeriesID: 7}, {ID: 12, SeriesID: 7}}
	ev := deletedEvent(episodefiles.ReasonManual, linked)
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeFileDeleted: %v", err)
	}
	if len(w.added) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(w.added))
	}
	w.added[0].Languages[0] = -999
	if w.added[1].Languages[0] == -999 {
		t.Errorf("Languages slice aliased between per-episode entries")
	}
}

func TestHandleEpisodeFileDeleted_NilSeriesFallsBackToRelative(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := deletedEvent(episodefiles.ReasonManual, []episodes.Episode{{ID: 11, SeriesID: 7}})
	ev.Series = nil
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeFileDeleted: %v", err)
	}
	if got := w.added[0].SourceTitle; got != "Season 02/Show.S02E03.mkv" {
		t.Errorf("SourceTitle (nil series) = %q, want fallback to RelativePath", got)
	}
}

func TestHandleEpisodeFileDeleted_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	ev := deletedEvent(episodefiles.ReasonManual, []episodes.Episode{{ID: 11, SeriesID: 7}})
	err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev)
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing repo error: %v", err)
	}
}

func TestHandleEpisodeFileDeleted_NilLanguages(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := deletedEvent(episodefiles.ReasonManual, []episodes.Episode{{ID: 11, SeriesID: 7}})
	ev.EpisodeFile.Languages = nil
	if err := historyhandlers.HandleEpisodeFileDeleted(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeFileDeleted: %v", err)
	}
	if got := w.added[0].Languages; got == nil || len(got) != 0 {
		t.Errorf("nil Languages: entry should have empty slice, got %v", got)
	}
}
