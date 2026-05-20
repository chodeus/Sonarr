package downloadhistoryhandlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/downloadhistoryhandlers"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubWriter captures every Add call.
type stubWriter struct {
	added []*downloadhistory.Entry
	err   error
}

func (s *stubWriter) Add(_ context.Context, e *downloadhistory.Entry) (*downloadhistory.Entry, error) {
	if s.err != nil {
		return nil, s.err
	}
	clone := *e
	clone.ID = len(s.added) + 1
	s.added = append(s.added, &clone)
	return &clone, nil
}

func makeRelease() *parser.ReleaseInfo {
	return &parser.ReleaseInfo{
		Title:            "Show.S01E01.1080p.BluRay-GROUP",
		Indexer:          "MyIndexer",
		IndexerID:        42,
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	}
}

func makeSubject() *decisionengine.Subject {
	return &decisionengine.Subject{
		Series:            &series.Series{ID: 7},
		Release:           makeRelease(),
		CustomFormatScore: 125,
	}
}

func makeEvent() *decisionengine.EpisodeGrabbedEvent {
	return &decisionengine.EpisodeGrabbedEvent{
		Episode:            makeSubject(),
		DownloadClientID:   17,
		DownloadClient:     "Sabnzbd",
		DownloadClientName: "Main Sabnzbd",
		DownloadID:         "tok-grab-1",
	}
}

func TestHandleEpisodeGrabbed_NilWriter(t *testing.T) {
	t.Parallel()
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), nil, makeEvent()); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestHandleEpisodeGrabbed_NilEventIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called; got %d", len(w.added))
	}
}

func TestHandleEpisodeGrabbed_EmptyDownloadIDSkipped(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := makeEvent()
	ev.DownloadID = ""
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Errorf("empty downloadID: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called for empty downloadID; got %d", len(w.added))
	}
}

func TestHandleEpisodeGrabbed_NilEpisodeSubjectIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := makeEvent()
	ev.Episode = nil
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Errorf("nil Episode: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called for nil Episode; got %d", len(w.added))
	}
}

func TestHandleEpisodeGrabbed_NilSeriesIsNoOp(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := makeEvent()
	ev.Episode.Series = nil
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Errorf("nil Series: %v", err)
	}
	if len(w.added) != 0 {
		t.Errorf("writer should not be called for nil Series; got %d", len(w.added))
	}
}

func TestHandleEpisodeGrabbed_WritesEntry(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := makeEvent()
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	if len(w.added) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(w.added))
	}
	got := w.added[0]
	if downloadhistory.EventType(got.EventType) != downloadhistory.EventTypeGrabbed {
		t.Errorf("EventType = %v, want Grabbed", got.EventType)
	}
	if got.SeriesID != 7 {
		t.Errorf("SeriesID = %d, want 7", got.SeriesID)
	}
	if got.DownloadID != "tok-grab-1" {
		t.Errorf("DownloadID = %q", got.DownloadID)
	}
	if got.SourceTitle != "Show.S01E01.1080p.BluRay-GROUP" {
		t.Errorf("SourceTitle = %q", got.SourceTitle)
	}
	if got.Protocol != int(indexers.DownloadProtocolUsenet) {
		t.Errorf("Protocol = %d", got.Protocol)
	}
	if got.IndexerID != 42 {
		t.Errorf("IndexerID = %d, want 42", got.IndexerID)
	}
	if got.DownloadClientID != 17 {
		t.Errorf("DownloadClientID = %d, want 17", got.DownloadClientID)
	}
	if got.Date.IsZero() {
		t.Errorf("Date is zero")
	}
}

func TestHandleEpisodeGrabbed_ReleaseJSONRoundTrips(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, makeEvent()); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	var back parser.ReleaseInfo
	if err := json.Unmarshal([]byte(w.added[0].Release), &back); err != nil {
		t.Fatalf("Release JSON did not round-trip: %v (raw=%q)", err, w.added[0].Release)
	}
	if back.Title != "Show.S01E01.1080p.BluRay-GROUP" || back.IndexerID != 42 {
		t.Errorf("Release after round-trip = %+v", back)
	}
}

func TestHandleEpisodeGrabbed_DataMapShape(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, makeEvent()); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(w.added[0].Data), &data); err != nil {
		t.Fatalf("Data JSON did not round-trip: %v", err)
	}
	for _, c := range []struct{ key, want string }{
		{"Indexer", "MyIndexer"},
		{"DownloadClient", "Sabnzbd"},
		{"DownloadClientName", "Main Sabnzbd"},
		{"CustomFormatScore", "125"},
	} {
		if got := data[c.key]; got != c.want {
			t.Errorf("Data[%q] = %q, want %q", c.key, got, c.want)
		}
	}
	if len(data) != 4 {
		t.Errorf("Data has %d keys, want exactly 4; got %v", len(data), data)
	}
}

func TestHandleEpisodeGrabbed_TorrentReleaseUnwrapsToReleaseInfo(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	// Use a TorrentInfo (which embeds ReleaseInfo) — the handler must
	// reach the embedded fields for IndexerID + Protocol.
	torrent := &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            "Show.S01E01.1080p.WEB-DL-GROUP",
			Indexer:          "Torznab",
			IndexerID:        99,
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
		InfoHash: "deadbeef",
	}
	ev := makeEvent()
	ev.Episode.Release = torrent
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	got := w.added[0]
	if got.IndexerID != 99 {
		t.Errorf("IndexerID (torrent unwrap) = %d, want 99", got.IndexerID)
	}
	if got.Protocol != int(indexers.DownloadProtocolTorrent) {
		t.Errorf("Protocol (torrent unwrap) = %d", got.Protocol)
	}
}

func TestHandleEpisodeGrabbed_NilReleaseProducesZeroValueColumns(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := makeEvent()
	ev.Episode.Release = nil
	if err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleEpisodeGrabbed: %v", err)
	}
	got := w.added[0]
	if got.SourceTitle != "" || got.IndexerID != 0 || got.Protocol != 0 {
		t.Errorf("nil release should yield zero-valued typed columns; got %+v", got)
	}
	if got.Release != "" {
		t.Errorf("Release JSON for nil should be empty, got %q", got.Release)
	}
}

func TestHandleEpisodeGrabbed_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	err := downloadhistoryhandlers.HandleEpisodeGrabbed(t.Context(), w, makeEvent())
	if err == nil {
		t.Fatal("expected error from stub writer")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing source: %v", err)
	}
}

// Compile-time assertion: *downloadhistory.Service satisfies EntryWriter.
var _ downloadhistoryhandlers.EntryWriter = (*downloadhistory.Service)(nil)
