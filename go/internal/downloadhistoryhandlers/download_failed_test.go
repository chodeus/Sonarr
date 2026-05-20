package downloadhistoryhandlers_test

import (
	"encoding/json"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/downloadhistoryhandlers"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

func failedTrackedDownload() *trackeddownloads.TrackedDownload {
	return &trackeddownloads.TrackedDownload{
		DownloadID:     "tok-fail-1",
		DownloadClient: 23,
		Protocol:       int(indexers.DownloadProtocolTorrent),
		DownloadItem: &downloadclients.Item{
			DownloadID: "tok-fail-1",
			DownloadClientInfo: &downloadclients.ItemClientInfo{
				Type: "QBittorrent",
				Name: "Main QBittorrent",
			},
		},
	}
}

func failedEvent(td *trackeddownloads.TrackedDownload) *trackeddownloads.DownloadFailedEvent {
	return &trackeddownloads.DownloadFailedEvent{
		SeriesID:        7,
		EpisodeIDs:      []int{11, 12},
		SourceTitle:     "Show.S01E01.1080p.WEB-DL-GROUP",
		DownloadID:      "tok-fail-1",
		Message:         "retry-exhausted",
		Source:          "FailedDownloadService",
		TrackedDownload: td,
	}
}

func TestFailed_NilWriter(t *testing.T) {
	t.Parallel()
	if err := downloadhistoryhandlers.HandleDownloadFailed(t.Context(), nil, failedEvent(failedTrackedDownload())); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestFailed_NilEventIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadFailed(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called; got %d", len(w.added))
	}
}

func TestFailed_NilTrackedDownloadIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent(nil)
	if err := downloadhistoryhandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Errorf("nil TrackedDownload: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called when TrackedDownload nil; got %d", len(w.added))
	}
}

func TestFailed_WritesEntry(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadFailed(t.Context(), w, failedEvent(failedTrackedDownload())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 row, got %d", len(w.added))
	}
	got := w.added[0]
	if downloadhistory.EventType(got.EventType) != downloadhistory.EventTypeFailed {
		t.Errorf("EventType = %v, want Failed", got.EventType)
	}
	if got.SeriesID != 7 || got.DownloadID != "tok-fail-1" {
		t.Errorf("got %+v", got)
	}
	if got.Protocol != int(indexers.DownloadProtocolTorrent) {
		t.Errorf("Protocol = %d", got.Protocol)
	}
	if got.DownloadClientID != 23 {
		t.Errorf("DownloadClientID = %d, want 23", got.DownloadClientID)
	}
	if got.SourceTitle != "Show.S01E01.1080p.WEB-DL-GROUP" {
		t.Errorf("SourceTitle = %q", got.SourceTitle)
	}
}

func TestFailed_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadFailed(t.Context(), w, failedEvent(failedTrackedDownload())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(w.added[0].Data), &data); err != nil {
		t.Fatalf("Data did not round-trip: %v", err)
	}
	if data["DownloadClient"] != "QBittorrent" || data["DownloadClientName"] != "Main QBittorrent" {
		t.Errorf("Data = %v", data)
	}
	if len(data) != 2 {
		t.Errorf("Data has %d keys, want 2", len(data))
	}
}

func TestFailed_NilDownloadItemNilClientInfoZeroData(t *testing.T) {
	t.Parallel()
	cases := []*trackeddownloads.TrackedDownload{
		{DownloadClient: 23, Protocol: 1, DownloadItem: nil},
		{DownloadClient: 23, Protocol: 1, DownloadItem: &downloadclients.Item{DownloadClientInfo: nil}},
	}
	for i, td := range cases {
		w := &stubWriter{}
		if err := downloadhistoryhandlers.HandleDownloadFailed(t.Context(), w, failedEvent(td)); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if len(w.added) != 1 {
			t.Fatalf("case %d: expected 1 row, got %d", i, len(w.added))
		}
		var data map[string]string
		_ = json.Unmarshal([]byte(w.added[0].Data), &data)
		if data["DownloadClient"] != "" || data["DownloadClientName"] != "" {
			t.Errorf("case %d: nil-link should yield empty Data; got %v", i, data)
		}
		// Typed columns still come from TrackedDownload root fields.
		if w.added[0].DownloadClientID != 23 || w.added[0].Protocol != 1 {
			t.Errorf("case %d: TrackedDownload root fields should still surface; got %+v", i, w.added[0])
		}
	}
}

func TestFailed_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	err := downloadhistoryhandlers.HandleDownloadFailed(t.Context(), w, failedEvent(failedTrackedDownload()))
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing source: %v", err)
	}
}
