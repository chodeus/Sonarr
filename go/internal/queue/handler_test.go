package queue

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

func dur(d time.Duration) *time.Duration { return &d }

func makeTracked(downloadClient int, downloadID, title string, opts ...func(*trackeddownloads.TrackedDownload)) *trackeddownloads.TrackedDownload {
	td := &trackeddownloads.TrackedDownload{
		DownloadID:     downloadID,
		DownloadClient: downloadClient,
		IsTrackable:    true,
		Protocol:       int(indexers.DownloadProtocolUsenet),
		Indexer:        "TestIndexer",
		Status:         trackeddownloads.StatusOK,
		State:          trackeddownloads.StateDownloading,
		StatusMessages: []string{},
		DownloadItem: &downloadclients.Item{
			DownloadID:    downloadID,
			Title:         title,
			TotalSize:     1_000_000_000,
			RemainingSize: 500_000_000,
			Status:        downloadclients.DownloadItemStatusDownloading,
			DownloadClientInfo: &downloadclients.ItemClientInfo{
				ID:                    downloadClient,
				Name:                  "TestClient",
				Type:                  "Sabnzbd",
				HasPostImportCategory: false,
			},
		},
	}
	for _, opt := range opts {
		opt(td)
	}
	return td
}

func TestSubscribeOnNilBusIsNoOp(t *testing.T) {
	t.Parallel()
	s := NewService()
	s.SubscribeOn(nil) // must not panic
	if s.bus != nil {
		t.Errorf("bus captured despite nil input: %v", s.bus)
	}
}

func TestSubscribeOnNilReceiverIsNoOp(t *testing.T) {
	t.Parallel()
	var s *Service
	bus := messaging.New(slog.Default())
	defer bus.BeginShutdown()
	s.SubscribeOn(bus) // must not panic
}

func TestSubscribeOnCapturesBus(t *testing.T) {
	t.Parallel()
	s := NewService()
	bus := messaging.New(slog.Default())
	defer bus.BeginShutdown()
	s.SubscribeOn(bus)
	if s.bus != bus {
		t.Errorf("bus not captured")
	}
}

func TestHandleTrackedDownloadRefreshedNilReceiver(t *testing.T) {
	t.Parallel()
	var s *Service
	if err := s.HandleTrackedDownloadRefreshed(t.Context(), trackeddownloads.TrackedDownloadRefreshedEvent{}); err != nil {
		t.Errorf("nil receiver: %v", err)
	}
}

func TestHandleTrackedDownloadRefreshedEmpty(t *testing.T) {
	t.Parallel()
	s := NewService()
	if err := s.HandleTrackedDownloadRefreshed(t.Context(), trackeddownloads.TrackedDownloadRefreshedEvent{}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if got := s.GetQueue(); len(got) != 0 {
		t.Errorf("expected empty queue, got %d", len(got))
	}
}

func TestHandleTrackedDownloadRefreshedFiltersIsTrackable(t *testing.T) {
	t.Parallel()
	s := NewService()
	tracked := makeTracked(1, "tok-1", "show-S01E01.mkv")
	untracked := makeTracked(2, "tok-2", "show-S01E02.mkv", func(td *trackeddownloads.TrackedDownload) {
		td.IsTrackable = false
	})
	ev := trackeddownloads.TrackedDownloadRefreshedEvent{
		TrackedDownloads: []*trackeddownloads.TrackedDownload{tracked, untracked},
	}
	if err := s.HandleTrackedDownloadRefreshed(t.Context(), ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	got := s.GetQueue()
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].DownloadID != "tok-1" {
		t.Errorf("expected tok-1, got %q", got[0].DownloadID)
	}
}

func TestHandleTrackedDownloadRefreshedSortsByRemainingTime(t *testing.T) {
	t.Parallel()
	s := NewService()
	slow := makeTracked(1, "slow", "slow.mkv", func(td *trackeddownloads.TrackedDownload) {
		td.DownloadItem.RemainingTime = dur(10 * time.Hour)
	})
	fast := makeTracked(2, "fast", "fast.mkv", func(td *trackeddownloads.TrackedDownload) {
		td.DownloadItem.RemainingTime = dur(1 * time.Hour)
	})
	noETA := makeTracked(3, "noETA", "noETA.mkv", func(td *trackeddownloads.TrackedDownload) {
		td.DownloadItem.RemainingTime = nil
	})
	ev := trackeddownloads.TrackedDownloadRefreshedEvent{
		TrackedDownloads: []*trackeddownloads.TrackedDownload{slow, noETA, fast},
	}
	if err := s.HandleTrackedDownloadRefreshed(t.Context(), ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	got := s.GetQueue()
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	// fast (1h) → slow (10h) → noETA (nil pushed to end per Go-port divergence)
	if got[0].DownloadID != "fast" || got[1].DownloadID != "slow" || got[2].DownloadID != "noETA" {
		t.Errorf("sort order wrong: %v", []string{got[0].DownloadID, got[1].DownloadID, got[2].DownloadID})
	}
}

func TestMapQueueItemBasicFields(t *testing.T) {
	t.Parallel()
	td := makeTracked(7, "tok-1", "Show.S01E01.WEB-DL-GROUP.mkv")
	td.RemoteEpisode = &decisionengine.Subject{
		Series:             &series.Series{ID: 42, Path: "/tv/Show"},
		MappedSeasonNumber: 1,
		Episodes:           []episodes.Episode{{ID: 11, SeriesID: 42, EpisodeNumber: 1}, {ID: 12, SeriesID: 42, EpisodeNumber: 2}},
		Languages:          []language.Language{language.English},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality: quality.Model{Quality: quality.Bluray1080p, Revision: quality.Revision{Version: 1}},
		},
	}
	td.DownloadItem.RemainingTime = dur(2 * time.Hour)

	rows := mapQueue(td)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	q := rows[0]

	if q.ID == 0 {
		t.Errorf("ID should be non-zero hash")
	}
	if q.Title != "Show.S01E01.WEB-DL-GROUP" {
		t.Errorf("Title (extension stripped) = %q", q.Title)
	}
	if q.Size != 1_000_000_000 {
		t.Errorf("Size = %d", q.Size)
	}
	if q.SizeLeft != 500_000_000 {
		t.Errorf("SizeLeft = %d", q.SizeLeft)
	}
	if q.TimeLeft == nil || *q.TimeLeft != 2*time.Hour {
		t.Errorf("TimeLeft = %v", q.TimeLeft)
	}
	if q.EstimatedCompletionTime == nil {
		t.Errorf("EstimatedCompletionTime is nil")
	}
	if q.Status != QueueStatusDownloading {
		t.Errorf("Status = %v, want Downloading", q.Status)
	}
	if q.Series == nil || q.Series.ID != 42 {
		t.Errorf("Series = %+v", q.Series)
	}
	if q.SeasonNumber == nil || *q.SeasonNumber != 1 {
		t.Errorf("SeasonNumber = %v", q.SeasonNumber)
	}
	if len(q.Episodes) != 2 {
		t.Errorf("Episodes len = %d, want 2", len(q.Episodes))
	}
	if len(q.Languages) != 1 || q.Languages[0].ID != language.English.ID {
		t.Errorf("Languages = %v", q.Languages)
	}
	if q.Quality.Quality.ID != quality.Bluray1080p.ID {
		t.Errorf("Quality.ID = %d, want %d", q.Quality.Quality.ID, quality.Bluray1080p.ID)
	}
	if q.DownloadID != "tok-1" {
		t.Errorf("DownloadID = %q", q.DownloadID)
	}
	if q.DownloadClient != "TestClient" {
		t.Errorf("DownloadClient = %q", q.DownloadClient)
	}
	if q.Indexer != "TestIndexer" {
		t.Errorf("Indexer = %q", q.Indexer)
	}
	if q.Protocol != indexers.DownloadProtocolUsenet {
		t.Errorf("Protocol = %v", q.Protocol)
	}
	if q.TrackedDownloadStatus == nil || *q.TrackedDownloadStatus != trackeddownloads.StatusOK {
		t.Errorf("TrackedDownloadStatus = %v", q.TrackedDownloadStatus)
	}
	if q.TrackedDownloadState == nil || *q.TrackedDownloadState != trackeddownloads.StateDownloading {
		t.Errorf("TrackedDownloadState = %v", q.TrackedDownloadState)
	}
}

func TestMapQueueItemNilRemoteEpisodeDefaults(t *testing.T) {
	t.Parallel()
	td := makeTracked(7, "tok-1", "show.mkv")
	// No RemoteEpisode attached.
	rows := mapQueue(td)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	q := rows[0]
	if q.Series != nil {
		t.Errorf("Series should be nil for nil RemoteEpisode")
	}
	if q.SeasonNumber != nil {
		t.Errorf("SeasonNumber should be nil")
	}
	if len(q.Episodes) != 0 {
		t.Errorf("Episodes should be empty, got %d", len(q.Episodes))
	}
	if len(q.Languages) != 1 || q.Languages[0].ID != language.Unknown.ID {
		t.Errorf("Languages should default to [Unknown], got %v", q.Languages)
	}
	if q.Quality.Quality.ID != quality.Unknown.ID {
		t.Errorf("Quality should default to Unknown, got %+v", q.Quality)
	}
}

func TestMapQueueItemNilDownloadItemSafe(t *testing.T) {
	t.Parallel()
	td := &trackeddownloads.TrackedDownload{
		DownloadID:     "tok-1",
		DownloadClient: 7,
		IsTrackable:    true,
		Status:         trackeddownloads.StatusOK,
		State:          trackeddownloads.StateDownloading,
		StatusMessages: []string{},
	}
	rows := mapQueue(td)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	q := rows[0]
	if q.Title != "" {
		t.Errorf("Title (nil DownloadItem) = %q", q.Title)
	}
	if q.Size != 0 || q.SizeLeft != 0 {
		t.Errorf("Size/SizeLeft (nil DownloadItem) = %d/%d", q.Size, q.SizeLeft)
	}
	if q.DownloadClient != "" {
		t.Errorf("DownloadClient (nil DownloadItem) = %q", q.DownloadClient)
	}
	if q.Status != QueueStatusUnknown {
		t.Errorf("Status (nil DownloadItem) = %v, want Unknown", q.Status)
	}
}

func TestMapStatusCovers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   downloadclients.DownloadItemStatus
		want QueueStatus
	}{
		{downloadclients.DownloadItemStatusQueued, QueueStatusQueued},
		{downloadclients.DownloadItemStatusPaused, QueueStatusPaused},
		{downloadclients.DownloadItemStatusDownloading, QueueStatusDownloading},
		{downloadclients.DownloadItemStatusCompleted, QueueStatusCompleted},
		{downloadclients.DownloadItemStatusFailed, QueueStatusFailed},
		{downloadclients.DownloadItemStatusWarning, QueueStatusWarning},
	}
	for _, c := range cases {
		td := makeTracked(1, "tok", "x.mkv")
		td.DownloadItem.Status = c.in
		got := mapStatus(td)
		if got != c.want {
			t.Errorf("mapStatus(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTrackedDownloadHashIDStableAndPositive(t *testing.T) {
	t.Parallel()
	td := makeTracked(7, "tok-abc", "x.mkv")
	id1 := trackedDownloadHashID(td)
	id2 := trackedDownloadHashID(td)
	if id1 != id2 {
		t.Errorf("hash not stable: %d vs %d", id1, id2)
	}
	if id1 < 0 {
		t.Errorf("hash is negative (sign-bit not masked): %d", id1)
	}
	if id1 == 0 {
		t.Errorf("hash should be non-zero for a real tracked download")
	}
	// Different DownloadID → different hash.
	other := makeTracked(7, "tok-different", "x.mkv")
	if trackedDownloadHashID(other) == id1 {
		t.Errorf("hash collision on different DownloadID")
	}
}

func TestRemoveFileExtension(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"Show.S01E01.mkv", "Show.S01E01"},
		{"Show.S01E01", "Show.S01E01"}, // no extension
		{"", ""},
		{"file.txt", "file"},
		{"my.long.file.name.mp4", "my.long.file.name"},
		{"trailing.", "trailing."}, // trailing dot stays
		{"Show.S01E01.weird-format-no-real-ext", "Show.S01E01.weird-format-no-real-ext"}, // suffix not alpha
	}
	for _, c := range cases {
		if got := removeFileExtension(c.in); got != c.want {
			t.Errorf("removeFileExtension(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHandleTrackedDownloadRefreshedPublishesQueueUpdated(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	defer bus.BeginShutdown()
	s := NewService()
	s.SubscribeOn(bus)

	publishedCount := 0
	messaging.Subscribe(bus, "test.queue.updated", func(ctx context.Context, _ QueueUpdatedEvent) error {
		publishedCount++
		return nil
	})

	td := makeTracked(7, "tok-1", "show.mkv")
	ev := trackeddownloads.TrackedDownloadRefreshedEvent{
		TrackedDownloads: []*trackeddownloads.TrackedDownload{td},
	}
	if err := s.HandleTrackedDownloadRefreshed(t.Context(), ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if publishedCount != 1 {
		t.Errorf("QueueUpdatedEvent published %d times, want 1", publishedCount)
	}
}
