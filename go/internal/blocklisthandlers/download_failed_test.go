package blocklisthandlers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/blocklisthandlers"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// stubWriter implements EntryWriter; captures the last Add call.
type stubWriter struct {
	gotAdd     *blocklist.Entry
	insertedID int
	err        error
}

func (s *stubWriter) Add(_ context.Context, e *blocklist.Entry) (*blocklist.Entry, error) {
	if s.err != nil {
		return nil, s.err
	}
	clone := *e
	if s.insertedID > 0 {
		clone.ID = s.insertedID
	} else {
		clone.ID = 1
	}
	s.gotAdd = &clone
	return &clone, nil
}

func failedEvent() *trackeddownloads.DownloadFailedEvent {
	return &trackeddownloads.DownloadFailedEvent{
		SeriesID:    7,
		EpisodeIDs:  []int{11, 12},
		Quality:     quality.Model{Quality: quality.Bluray1080p, Revision: quality.Revision{Version: 1}},
		SourceTitle: "Show.S01E01.BluRay-GROUP",
		DownloadID:  "tok-1",
		Message:     "Removed by user",
		Source:      "ManualOverride",
		Languages:   []language.Language{language.English},
		Data: map[string]string{
			"publishedDate":   "2025-04-01T10:30:00Z",
			"size":            "1500000000",
			"indexer":         "Indexer-Z",
			"protocol":        "1",
			"torrentInfoHash": "fallback-hash",
			"indexerFlags":    "Freeleech, Internal",
			"releaseType":     "SingleEpisode",
		},
	}
}

func TestHandleDownloadFailed_NilWriter(t *testing.T) {
	t.Parallel()
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), nil, failedEvent()); err == nil {
		t.Errorf("expected error on nil writer")
	}
}

func TestHandleDownloadFailed_NoOpOnNilEvent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if w.gotAdd != nil {
		t.Errorf("nil event: writer should not be called")
	}
}

func TestHandleDownloadFailed_BuildsEntryFromEventDirect(t *testing.T) {
	t.Parallel()
	w := &stubWriter{insertedID: 99}
	ev := failedEvent()
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	got := w.gotAdd
	if got == nil {
		t.Fatal("expected Add to be called")
	}
	if got.SeriesID != 7 {
		t.Errorf("SeriesID = %d, want 7", got.SeriesID)
	}
	if len(got.EpisodeIDs) != 2 || got.EpisodeIDs[0] != 11 || got.EpisodeIDs[1] != 12 {
		t.Errorf("EpisodeIDs = %v, want [11 12]", got.EpisodeIDs)
	}
	if got.SourceTitle != "Show.S01E01.BluRay-GROUP" {
		t.Errorf("SourceTitle = %q", got.SourceTitle)
	}
	if got.Message != "Removed by user" {
		t.Errorf("Message = %q", got.Message)
	}
	if got.Source != "ManualOverride" {
		t.Errorf("Source = %q", got.Source)
	}
	if got.Date.IsZero() {
		t.Errorf("Date is zero")
	}
	if got.Indexer != "Indexer-Z" {
		t.Errorf("Indexer = %q", got.Indexer)
	}
	if got.IndexerFlags != int(parser.IndexerFlagFreeleech|parser.IndexerFlagInternal) {
		t.Errorf("IndexerFlags = %d, want %d", got.IndexerFlags, parser.IndexerFlagFreeleech|parser.IndexerFlagInternal)
	}
	if got.ReleaseType != int(parser.ReleaseTypeSingleEpisode) {
		t.Errorf("ReleaseType = %d, want %d", got.ReleaseType, parser.ReleaseTypeSingleEpisode)
	}
}

func TestHandleDownloadFailed_DataDictParsing(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	got := w.gotAdd
	if got.PublishedDate == nil {
		t.Fatal("PublishedDate is nil")
	}
	if !got.PublishedDate.Equal(time.Date(2025, 4, 1, 10, 30, 0, 0, time.UTC)) {
		t.Errorf("PublishedDate = %v", got.PublishedDate)
	}
	if got.Size == nil || *got.Size != 1_500_000_000 {
		t.Errorf("Size = %v, want 1500000000", got.Size)
	}
	if got.Protocol == nil || *got.Protocol != int(indexers.DownloadProtocolUsenet) {
		t.Errorf("Protocol = %v, want %d", got.Protocol, indexers.DownloadProtocolUsenet)
	}
}

func TestHandleDownloadFailed_TorrentInfoHashFromTrackedDownload(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	ev.TrackedDownload = &trackeddownloads.TrackedDownload{
		Protocol: int(indexers.DownloadProtocolTorrent),
		DownloadItem: &downloadclients.Item{
			DownloadID: "tracked-hash",
		},
	}
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if got := w.gotAdd.TorrentInfoHash; got != "tracked-hash" {
		t.Errorf("TorrentInfoHash (tracked-torrent) = %q, want tracked-hash", got)
	}
}

func TestHandleDownloadFailed_TorrentInfoHashFromDataFallback(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	ev.TrackedDownload = nil
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if got := w.gotAdd.TorrentInfoHash; got != "fallback-hash" {
		t.Errorf("TorrentInfoHash (fallback) = %q", got)
	}
}

func TestHandleDownloadFailed_TorrentInfoHashUsenetFallsBack(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	// Tracked but Usenet protocol — should hit the Data fallback.
	ev.TrackedDownload = &trackeddownloads.TrackedDownload{
		Protocol: int(indexers.DownloadProtocolUsenet),
		DownloadItem: &downloadclients.Item{
			DownloadID: "should-not-use",
		},
	}
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if got := w.gotAdd.TorrentInfoHash; got != "fallback-hash" {
		t.Errorf("TorrentInfoHash (Usenet → fallback) = %q", got)
	}
}

func TestHandleDownloadFailed_TorrentItemNilCollapsesToEmpty(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	ev.TrackedDownload = &trackeddownloads.TrackedDownload{
		Protocol:     int(indexers.DownloadProtocolTorrent),
		DownloadItem: nil,
	}
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if got := w.gotAdd.TorrentInfoHash; got != "" {
		t.Errorf("TorrentInfoHash (nil DownloadItem) = %q, want empty", got)
	}
}

func TestHandleDownloadFailed_NumericIndexerFlagsAndReleaseType(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	ev.Data["indexerFlags"] = "9" // Freeleech + Internal
	ev.Data["releaseType"] = "2"  // MultiEpisode
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	got := w.gotAdd
	if got.IndexerFlags != 9 {
		t.Errorf("IndexerFlags (numeric) = %d, want 9", got.IndexerFlags)
	}
	if got.ReleaseType != int(parser.ReleaseTypeMultiEpisode) {
		t.Errorf("ReleaseType (numeric) = %d", got.ReleaseType)
	}
}

func TestHandleDownloadFailed_EmptyDataDict(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	ev.Data = nil
	ev.TrackedDownload = nil
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	got := w.gotAdd
	if got.PublishedDate != nil {
		t.Errorf("PublishedDate (empty data) = %v, want nil", got.PublishedDate)
	}
	if got.Size != nil {
		t.Errorf("Size (empty data) = %v, want nil", got.Size)
	}
	if got.Protocol != nil {
		t.Errorf("Protocol (empty data) = %v, want nil", got.Protocol)
	}
	if got.IndexerFlags != 0 {
		t.Errorf("IndexerFlags (empty data) = %d, want 0", got.IndexerFlags)
	}
	if got.ReleaseType != int(parser.ReleaseTypeUnknown) {
		t.Errorf("ReleaseType (empty data) = %d, want Unknown", got.ReleaseType)
	}
}

func TestHandleDownloadFailed_BadPublishedDateCollapses(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	ev.Data["publishedDate"] = "not-a-date"
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	if got := w.gotAdd.PublishedDate; got != nil {
		t.Errorf("PublishedDate (bad input) = %v, want nil", got)
	}
}

func TestHandleDownloadFailed_SizeZeroIsRecorded(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	ev.Data["size"] = "0"
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	got := w.gotAdd
	if got.Size == nil || *got.Size != 0 {
		t.Errorf("Size (=0) = %v, want pointer to 0", got.Size)
	}
}

func TestHandleDownloadFailed_LanguagesEmpty(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	ev := failedEvent()
	ev.Languages = nil
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, ev); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	got := w.gotAdd.Languages
	if got == nil || len(got) != 0 {
		t.Errorf("Languages (nil) = %v, want []int{}", got)
	}
}

func TestHandleDownloadFailed_AddErrorPropagates(t *testing.T) {
	t.Parallel()
	w := &stubWriter{err: errors.New("repo down")}
	err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, failedEvent())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, w.err) {
		t.Errorf("error chain missing repo err: %v", err)
	}
}

func TestHandleDownloadFailed_QualityRoundTrip(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	if err := blocklisthandlers.HandleDownloadFailed(t.Context(), w, failedEvent()); err != nil {
		t.Fatalf("HandleDownloadFailed: %v", err)
	}
	q := w.gotAdd.Quality
	var m quality.Model
	if err := m.UnmarshalJSON([]byte(q)); err != nil {
		t.Fatalf("Quality JSON did not round-trip: %v (raw=%q)", err, q)
	}
	if m.Quality.ID != quality.Bluray1080p.ID {
		t.Errorf("Quality.ID after round-trip = %d, want %d", m.Quality.ID, quality.Bluray1080p.ID)
	}
}
