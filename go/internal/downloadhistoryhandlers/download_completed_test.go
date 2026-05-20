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

func completedTrackedDownload() *trackeddownloads.TrackedDownload {
	return &trackeddownloads.TrackedDownload{
		DownloadID:     "tok-cmpl-1",
		DownloadClient: 41,
		Protocol:       int(indexers.DownloadProtocolUsenet),
		DownloadItem: &downloadclients.Item{
			DownloadID: "tok-cmpl-1",
			Title:      "Show.S01E01.1080p.WEB-DL-GROUP",
			DownloadClientInfo: &downloadclients.ItemClientInfo{
				Type: "Sabnzbd",
				Name: "Backup Sab",
			},
		},
	}
}

func completedEvent(td *trackeddownloads.TrackedDownload) *trackeddownloads.DownloadCompletedEvent {
	return &trackeddownloads.DownloadCompletedEvent{
		TrackedDownload: td,
		SeriesID:        7,
	}
}

func TestCompleted_NilWriter(t *testing.T) {
	t.Parallel()
	if err := downloadhistoryhandlers.HandleDownloadCompleted(t.Context(), nil, completedEvent(completedTrackedDownload())); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestCompleted_NilEventIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadCompleted(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called; got %d", len(w.added))
	}
}

func TestCompleted_NilTrackedDownloadIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadCompleted(t.Context(), w, completedEvent(nil)); err != nil {
		t.Errorf("nil TrackedDownload: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called when TrackedDownload nil; got %d", len(w.added))
	}
}

func TestCompleted_NilDownloadItemIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	td := &trackeddownloads.TrackedDownload{DownloadID: "tok-cmpl-1", DownloadItem: nil}
	if err := downloadhistoryhandlers.HandleDownloadCompleted(t.Context(), w, completedEvent(td)); err != nil {
		t.Errorf("nil DownloadItem: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called when DownloadItem nil; got %d", len(w.added))
	}
}

func TestCompleted_WritesEntry(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadCompleted(t.Context(), w, completedEvent(completedTrackedDownload())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 row, got %d", len(w.added))
	}
	got := w.added[0]
	if downloadhistory.EventType(got.EventType) != downloadhistory.EventTypeImported {
		t.Errorf("EventType = %v, want Imported (the DownloadImported ordinal)", got.EventType)
	}
	if got.SeriesID != 7 || got.DownloadID != "tok-cmpl-1" {
		t.Errorf("got %+v", got)
	}
	if got.SourceTitle != "Show.S01E01.1080p.WEB-DL-GROUP" {
		t.Errorf("SourceTitle = %q (must come from DownloadItem.Title)", got.SourceTitle)
	}
	if got.Protocol != int(indexers.DownloadProtocolUsenet) {
		t.Errorf("Protocol = %d", got.Protocol)
	}
	if got.DownloadClientID != 41 {
		t.Errorf("DownloadClientID = %d, want 41", got.DownloadClientID)
	}
}

func TestCompleted_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadCompleted(t.Context(), w, completedEvent(completedTrackedDownload())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(w.added[0].Data), &data); err != nil {
		t.Fatalf("Data did not round-trip: %v", err)
	}
	if data["DownloadClient"] != "Sabnzbd" || data["DownloadClientName"] != "Backup Sab" {
		t.Errorf("Data = %v", data)
	}
	if len(data) != 2 {
		t.Errorf("Data has %d keys, want 2", len(data))
	}
}

func TestCompleted_NilClientInfoZeroData(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	td := completedTrackedDownload()
	td.DownloadItem.DownloadClientInfo = nil
	if err := downloadhistoryhandlers.HandleDownloadCompleted(t.Context(), w, completedEvent(td)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 row, got %d", len(w.added))
	}
	var data map[string]string
	_ = json.Unmarshal([]byte(w.added[0].Data), &data)
	if data["DownloadClient"] != "" || data["DownloadClientName"] != "" {
		t.Errorf("nil ClientInfo should yield empty Data; got %v", data)
	}
	// Typed columns still surface from TrackedDownload root.
	if w.added[0].DownloadClientID != 41 || w.added[0].Protocol != int(indexers.DownloadProtocolUsenet) {
		t.Errorf("TrackedDownload root fields should still surface; got %+v", w.added[0])
	}
}

func TestCompleted_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	err := downloadhistoryhandlers.HandleDownloadCompleted(t.Context(), w, completedEvent(completedTrackedDownload()))
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing source: %v", err)
	}
}
