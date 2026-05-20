package historyhandlers_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/historyhandlers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

func ignoredTracked() *trackeddownloads.TrackedDownload {
	return &trackeddownloads.TrackedDownload{
		DownloadID: "tok-ignored-1",
		DownloadItem: &downloadclients.Item{
			TotalSize: 555_444_333,
		},
		RemoteEpisode: &decisionengine.Subject{
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
				ReleaseGroup:   "GROUP-Z",
				EpisodeNumbers: []int{1, 2}, // → ReleaseType MultiEpisode
			},
			Release: &parser.ReleaseInfo{Indexer: "Indexer-Z"},
		},
	}
}

func ignoredEvent(epIDs []int) *trackeddownloads.DownloadIgnoredEvent {
	return &trackeddownloads.DownloadIgnoredEvent{
		SeriesID:    7,
		EpisodeIDs:  epIDs,
		Languages:   []language.Language{language.English},
		Quality:     quality.Model{Quality: quality.WEBDL1080p, Revision: quality.Revision{Version: 1}},
		SourceTitle: "Show.S01E01.WEB-DL-GROUP",
		DownloadClientInfo: &downloadclients.ItemClientInfo{
			Type: "Sabnzbd",
			Name: "Local Sab",
		},
		DownloadID:      "tok-ignored-1",
		TrackedDownload: ignoredTracked(),
		Message:         "Forgotten by user",
	}
}

func TestHandleDownloadIgnored_NilWriter(t *testing.T) {
	t.Parallel()
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), nil, &trackeddownloads.DownloadIgnoredEvent{EpisodeIDs: []int{1}}); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestHandleDownloadIgnored_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil event: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleDownloadIgnored_NoOpOnEmptyEpisodeIDs(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &trackeddownloads.DownloadIgnoredEvent{SeriesID: 7}
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Errorf("empty EpisodeIDs: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("empty EpisodeIDs: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleDownloadIgnored_WritesPerEpisode(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21, 22, 23})
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	if len(w.added) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(w.added))
	}
	for i, want := range []int{21, 22, 23} {
		got := w.added[i]
		if got.EpisodeID != want {
			t.Errorf("entry %d EpisodeID = %d, want %d", i, got.EpisodeID, want)
		}
		if got.SeriesID != 7 {
			t.Errorf("entry %d SeriesID = %d, want 7", i, got.SeriesID)
		}
		if got.DownloadID != "tok-ignored-1" {
			t.Errorf("entry %d DownloadID = %q", i, got.DownloadID)
		}
		if got.SourceTitle != "Show.S01E01.WEB-DL-GROUP" {
			t.Errorf("entry %d SourceTitle = %q", i, got.SourceTitle)
		}
		if got.EventType == nil || history.EventType(*got.EventType) != history.EventTypeDownloadIgnored {
			t.Errorf("entry %d EventType = %v, want DownloadIgnored", i, got.EventType)
		}
		if got.Date.IsZero() {
			t.Errorf("entry %d Date is zero", i)
		}
	}
}

func TestHandleDownloadIgnored_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21})
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	d := w.added[0].Data
	cases := []struct{ key, want string }{
		{"DownloadClient", "Sabnzbd"},
		{"DownloadClientName", "Local Sab"},
		{"Message", "Forgotten by user"},
		{"ReleaseGroup", "GROUP-Z"},
		{"Size", "555444333"},
		{"Indexer", "Indexer-Z"},
		{"ReleaseType", "MultiEpisode"},
	}
	for _, c := range cases {
		if got := d[c.key]; got != c.want {
			t.Errorf("Data[%q] = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestHandleDownloadIgnored_NilDownloadClientInfo(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21})
	ev.DownloadClientInfo = nil
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	d := w.added[0].Data
	if d["DownloadClient"] != "" {
		t.Errorf("DownloadClient (nil info) = %q, want empty", d["DownloadClient"])
	}
	if d["DownloadClientName"] != "" {
		t.Errorf("DownloadClientName (nil info) = %q, want empty", d["DownloadClientName"])
	}
}

func TestHandleDownloadIgnored_NilTrackedDownload(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21})
	ev.TrackedDownload = nil
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	d := w.added[0].Data
	// All TrackedDownload-derived keys collapse to empty (no fallback).
	if d["ReleaseGroup"] != "" {
		t.Errorf("ReleaseGroup (no tracked) = %q, want empty", d["ReleaseGroup"])
	}
	if d["Size"] != "" {
		t.Errorf("Size (no tracked) = %q, want empty", d["Size"])
	}
	if d["Indexer"] != "" {
		t.Errorf("Indexer (no tracked) = %q, want empty", d["Indexer"])
	}
	if d["ReleaseType"] != "" {
		t.Errorf("ReleaseType (no tracked) = %q, want empty", d["ReleaseType"])
	}
}

func TestHandleDownloadIgnored_TorrentReleaseIndexer(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21})
	ev.TrackedDownload.RemoteEpisode.Release = &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{Indexer: "TorrentIdx"},
	}
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	if got := w.added[0].Data["Indexer"]; got != "TorrentIdx" {
		t.Errorf("Indexer (torrent) = %q", got)
	}
}

func TestHandleDownloadIgnored_SizeZeroFromTracked(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21})
	ev.TrackedDownload.DownloadItem.TotalSize = 0
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	if got := w.added[0].Data["Size"]; got != "0" {
		t.Errorf("Size (TotalSize=0 from tracked) = %q, want '0'", got)
	}
}

func TestHandleDownloadIgnored_NilDownloadItem(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21})
	ev.TrackedDownload.DownloadItem = nil
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	if got := w.added[0].Data["Size"]; got != "" {
		t.Errorf("Size (nil item) = %q, want empty", got)
	}
}

func TestHandleDownloadIgnored_LanguagesPerEpisodeIndependent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21, 22})
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	if len(w.added) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(w.added))
	}
	w.added[0].Languages[0] = -999
	if w.added[1].Languages[0] == -999 {
		t.Errorf("Languages slice aliased between per-episode entries")
	}
}

func TestHandleDownloadIgnored_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	ev := ignoredEvent([]int{21})
	err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev)
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing repo error: %v", err)
	}
}

func TestHandleDownloadIgnored_QualityRoundTrip(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := ignoredEvent([]int{21})
	if err := historyhandlers.HandleDownloadIgnored(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadIgnored: %v", err)
	}
	q := w.added[0].Quality
	var m quality.Model
	if err := m.UnmarshalJSON([]byte(q)); err != nil {
		t.Fatalf("Quality JSON did not round-trip: %v (raw=%q)", err, q)
	}
	if m.Quality.ID != quality.WEBDL1080p.ID {
		t.Errorf("Quality.ID after round-trip = %d, want %d", m.Quality.ID, quality.WEBDL1080p.ID)
	}
}
