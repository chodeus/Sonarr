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

func ignoredClientInfo() *downloadclients.ItemClientInfo {
	return &downloadclients.ItemClientInfo{
		ID:       31,
		Type:     "Deluge",
		Name:     "Main Deluge",
		Protocol: indexers.DownloadProtocolTorrent,
	}
}

func ignoredEvent(info *downloadclients.ItemClientInfo) *trackeddownloads.DownloadIgnoredEvent {
	return &trackeddownloads.DownloadIgnoredEvent{
		SeriesID:           7,
		EpisodeIDs:         []int{11, 12},
		SourceTitle:        "Show.S01E01",
		DownloadID:         "tok-ignored-1",
		Message:            "user-ignored",
		DownloadClientInfo: info,
	}
}

func TestIgnored_NilWriter(t *testing.T) {
	t.Parallel()
	if err := downloadhistoryhandlers.HandleDownloadIgnored(t.Context(), nil, ignoredEvent(ignoredClientInfo())); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestIgnored_NilEventIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadIgnored(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called; got %d", len(w.added))
	}
}

func TestIgnored_WritesEntry(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadIgnored(t.Context(), w, ignoredEvent(ignoredClientInfo())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 row (per-event), got %d", len(w.added))
	}
	got := w.added[0]
	if downloadhistory.EventType(got.EventType) != downloadhistory.EventTypeIgnored {
		t.Errorf("EventType = %v, want Ignored", got.EventType)
	}
	if got.SeriesID != 7 || got.DownloadID != "tok-ignored-1" {
		t.Errorf("got %+v", got)
	}
	if got.Protocol != int(indexers.DownloadProtocolTorrent) {
		t.Errorf("Protocol = %d", got.Protocol)
	}
	if got.DownloadClientID != 31 {
		t.Errorf("DownloadClientID = %d, want 31", got.DownloadClientID)
	}
	if got.SourceTitle != "Show.S01E01" {
		t.Errorf("SourceTitle = %q", got.SourceTitle)
	}
}

func TestIgnored_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadIgnored(t.Context(), w, ignoredEvent(ignoredClientInfo())); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(w.added[0].Data), &data); err != nil {
		t.Fatalf("Data did not round-trip: %v", err)
	}
	if data["DownloadClient"] != "Deluge" || data["DownloadClientName"] != "Main Deluge" {
		t.Errorf("Data = %v", data)
	}
	if len(data) != 2 {
		t.Errorf("Data has %d keys, want 2 (DownloadHistory ignored shape is narrower than the History twin's 7-key)", len(data))
	}
}

func TestIgnored_NilClientInfoZeroFallback(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleDownloadIgnored(t.Context(), w, ignoredEvent(nil)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got := w.added[0]
	if got.Protocol != 0 || got.DownloadClientID != 0 {
		t.Errorf("nil DownloadClientInfo should yield zero Protocol/DownloadClientID; got %+v", got)
	}
	var data map[string]string
	_ = json.Unmarshal([]byte(got.Data), &data)
	if data["DownloadClient"] != "" || data["DownloadClientName"] != "" {
		t.Errorf("nil DownloadClientInfo should yield empty Data; got %v", data)
	}
}

func TestIgnored_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	err := downloadhistoryhandlers.HandleDownloadIgnored(t.Context(), w, ignoredEvent(ignoredClientInfo()))
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing source: %v", err)
	}
}
