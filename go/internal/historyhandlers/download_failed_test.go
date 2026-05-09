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

func failedTracked() *trackeddownloads.TrackedDownload {
	return &trackeddownloads.TrackedDownload{
		DownloadID: "tok-failed-1",
		DownloadItem: &downloadclients.Item{
			TotalSize: 999_888_777,
			DownloadClientInfo: &downloadclients.ItemClientInfo{
				Type: "QBittorrent",
				Name: "Local QB",
			},
		},
		RemoteEpisode: &decisionengine.Subject{
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseGroup: "GROUP-X"},
			Release: &parser.ReleaseInfo{
				Indexer: "Indexer-X",
			},
		},
	}
}

func failedEvent(epIDs []int) *trackeddownloads.DownloadFailedEvent {
	return &trackeddownloads.DownloadFailedEvent{
		SeriesID:        7,
		EpisodeIDs:      epIDs,
		Quality:         quality.Model{Quality: quality.Bluray1080p, Revision: quality.Revision{Version: 1}},
		SourceTitle:     "Show.S01E01.1080p.WEB-DL-GROUP",
		DownloadClient:  "QBittorrent",
		DownloadID:      "tok-failed-1",
		Message:         "Removed by user",
		Source:          "ManualOverride",
		Data:            map[string]string{},
		TrackedDownload: failedTracked(),
		Languages:       []language.Language{language.English},
		ReleaseSource:   decisionengine.ReleaseSourceRSS,
	}
}

func TestHandleDownloadFailed_NilWriter(t *testing.T) {
	t.Parallel()
	if err := historyhandlers.HandleDownloadFailed(t.Context(), nil, &trackeddownloads.DownloadFailedEvent{EpisodeIDs: []int{1}}); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestHandleDownloadFailed_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("nil event: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleDownloadFailed_NoOpOnEmptyEpisodeIDs(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := &trackeddownloads.DownloadFailedEvent{SeriesID: 7}
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Errorf("empty EpisodeIDs: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("empty EpisodeIDs: writer should not be called; got %d", len(w.added))
	}
}

func TestHandleDownloadFailed_WritesPerEpisode(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11, 12, 13})
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if len(w.added) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(w.added))
	}
	for i, want := range []int{11, 12, 13} {
		if w.added[i].EpisodeID != want {
			t.Errorf("entry %d EpisodeID = %d, want %d", i, w.added[i].EpisodeID, want)
		}
		if w.added[i].SeriesID != 7 {
			t.Errorf("entry %d SeriesID = %d, want 7", i, w.added[i].SeriesID)
		}
		if w.added[i].DownloadID != "tok-failed-1" {
			t.Errorf("entry %d DownloadID = %q", i, w.added[i].DownloadID)
		}
		if w.added[i].SourceTitle != "Show.S01E01.1080p.WEB-DL-GROUP" {
			t.Errorf("entry %d SourceTitle = %q", i, w.added[i].SourceTitle)
		}
		if w.added[i].EventType == nil || history.EventType(*w.added[i].EventType) != history.EventTypeDownloadFailed {
			t.Errorf("entry %d EventType = %v, want DownloadFailed", i, w.added[i].EventType)
		}
		if w.added[i].Date.IsZero() {
			t.Errorf("entry %d Date is zero", i)
		}
	}
}

func TestHandleDownloadFailed_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11})
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	d := w.added[0].Data
	cases := []struct{ key, want string }{
		{"DownloadClient", "QBittorrent"},
		{"DownloadClientName", "Local QB"},
		{"Message", "Removed by user"},
		{"Source", "ManualOverride"},
		{"ReleaseGroup", "GROUP-X"},
		{"Size", "999888777"},
		{"Indexer", "Indexer-X"},
	}
	for _, c := range cases {
		if got := d[c.key]; got != c.want {
			t.Errorf("Data[%q] = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestHandleDownloadFailed_FallbackToData(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11})
	ev.TrackedDownload = nil
	ev.Data = map[string]string{
		"releaseGroup": "FALLBACK-GROUP",
		"size":         "12345",
		"indexer":      "FallbackIndexer",
	}
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	d := w.added[0].Data
	if d["ReleaseGroup"] != "FALLBACK-GROUP" {
		t.Errorf("ReleaseGroup fallback = %q", d["ReleaseGroup"])
	}
	if d["Size"] != "12345" {
		t.Errorf("Size fallback = %q", d["Size"])
	}
	if d["Indexer"] != "FallbackIndexer" {
		t.Errorf("Indexer fallback = %q", d["Indexer"])
	}
	if d["DownloadClientName"] != "" {
		t.Errorf("DownloadClientName (no tracked) = %q, want empty", d["DownloadClientName"])
	}
}

func TestHandleDownloadFailed_EmptyTrackedFieldsTreatedAsMissing(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11})
	// TrackedDownload exists but with empty strings on the chain ends.
	ev.TrackedDownload.RemoteEpisode.ParsedEpisodeInfo.ReleaseGroup = ""
	ev.TrackedDownload.RemoteEpisode.Release = &parser.ReleaseInfo{Indexer: ""}
	ev.Data = map[string]string{
		"releaseGroup": "FROM-DATA",
		"indexer":      "FROM-DATA-INDEXER",
	}
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	d := w.added[0].Data
	if d["ReleaseGroup"] != "FROM-DATA" {
		t.Errorf("empty TD ReleaseGroup → expected fallback, got %q", d["ReleaseGroup"])
	}
	if d["Indexer"] != "FROM-DATA-INDEXER" {
		t.Errorf("empty TD Indexer → expected fallback, got %q", d["Indexer"])
	}
}

func TestHandleDownloadFailed_TorrentReleaseIndexer(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11})
	ev.TrackedDownload.RemoteEpisode.Release = &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{Indexer: "TorrentIndexer"},
	}
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if got := w.added[0].Data["Indexer"]; got != "TorrentIndexer" {
		t.Errorf("Indexer (torrent) = %q, want TorrentIndexer", got)
	}
}

func TestHandleDownloadFailed_TrackedDownloadItemMissingClientInfo(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11})
	ev.TrackedDownload.DownloadItem.DownloadClientInfo = nil
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if got := w.added[0].Data["DownloadClientName"]; got != "" {
		t.Errorf("DownloadClientName (nil info) = %q, want empty", got)
	}
}

func TestHandleDownloadFailed_TrackedDownloadItemNil(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11})
	ev.TrackedDownload.DownloadItem = nil
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	d := w.added[0].Data
	if d["DownloadClientName"] != "" {
		t.Errorf("DownloadClientName (nil item) = %q", d["DownloadClientName"])
	}
	// Size: TrackedDownload.DownloadItem nil should fall through to Data["size"] (empty here).
	if d["Size"] != "" {
		t.Errorf("Size (nil item, no fallback) = %q, want empty", d["Size"])
	}
}

func TestHandleDownloadFailed_SizeZeroFromTrackedNotFallback(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11})
	ev.TrackedDownload.DownloadItem.TotalSize = 0
	ev.Data = map[string]string{"size": "999"}
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if got := w.added[0].Data["Size"]; got != "0" {
		t.Errorf("Size from tracked TotalSize=0 should NOT fall back to Data; got %q", got)
	}
}

func TestHandleDownloadFailed_LanguagesPerEpisodeIndependent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11, 12})
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if len(w.added) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(w.added))
	}
	w.added[0].Languages[0] = -999
	if w.added[1].Languages[0] == -999 {
		t.Errorf("Languages slice aliased between per-episode entries")
	}
}

func TestHandleDownloadFailed_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	ev := failedEvent([]int{11})
	err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev)
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing repo error: %v", err)
	}
}

func TestHandleDownloadFailed_QualityRoundTrip(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent([]int{11})
	if err := historyhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	q := w.added[0].Quality
	var m quality.Model
	if err := m.UnmarshalJSON([]byte(q)); err != nil {
		t.Fatalf("Quality JSON did not round-trip: %v (raw=%q)", err, q)
	}
	if m.Quality.ID != quality.Bluray1080p.ID {
		t.Errorf("Quality.ID after round-trip = %d, want %d", m.Quality.ID, quality.Bluray1080p.ID)
	}
}
