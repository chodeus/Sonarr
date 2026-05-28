package main

import (
	"context"
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
)

func TestQueuePagedAdapter_EmptyQueue_ReturnsEmptyPage(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	adapter := queuePagedAdapter{svc}
	records, total, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if total != 0 {
		t.Errorf("total: got %d want 0", total)
	}
	if len(records) != 0 {
		t.Errorf("records: got %d want 0", len(records))
	}
}

func TestQueuePagedAdapter_ScalarProjection_J347(t *testing.T) {
	t.Parallel()
	tl := 90 * time.Minute
	added := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{{
		ID:             42,
		Title:          "Show.S01E02.1080p.WEB-DL",
		Status:         queue.QueueStatusDownloading,
		DownloadID:     "abc123",
		Protocol:       indexers.DownloadProtocolTorrent,
		Size:           2000,
		SizeLeft:       500,
		TimeLeft:       &tl,
		Indexer:        "TestIndexer",
		DownloadClient: "TestClient",
		OutputPath:     "/downloads/x",
		Added:          &added,
		Series:         &series.Series{ID: 7, Title: "Show"},
		Episodes:       []episodes.Episode{{ID: 11, SeasonNumber: 1, EpisodeNumber: 2}},
		Languages:      []language.Language{{ID: 1, Name: "English"}},
	}})
	adapter := queuePagedAdapter{svc}
	records, total, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Fatalf("total=%d records=%d, want 1/1", total, len(records))
	}
	r := records[0]
	if r.ID != 42 {
		t.Errorf("ID: got %d want 42", r.ID)
	}
	if r.SeriesID == nil || *r.SeriesID != 7 {
		t.Errorf("SeriesID: got %v want 7", r.SeriesID)
	}
	if r.EpisodeID == nil || *r.EpisodeID != 11 {
		t.Errorf("EpisodeID: got %v want 11", r.EpisodeID)
	}
	if r.Status != "downloading" {
		t.Errorf("Status: got %q want downloading (camelCase enum)", r.Status)
	}
	if r.Protocol != "torrent" {
		t.Errorf("Protocol: got %q want torrent", r.Protocol)
	}
	if r.TimeLeft != "01:30:00" {
		t.Errorf("TimeLeft: got %q want 01:30:00", r.TimeLeft)
	}
	if len(r.Languages) != 1 || r.Languages[0].ID != 1 {
		t.Errorf("Languages: got %+v want [{1 English}]", r.Languages)
	}
	// Subresources DEFERRED at J347 — must be nil.
	if r.Series != nil || r.Episode != nil || r.Episodes != nil {
		t.Errorf("J347 subresources must be nil; got series=%v episode=%v episodes=%v", r.Series, r.Episode, r.Episodes)
	}
}

func TestQueuePagedAdapter_Pagination_J347(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	rows := make([]queue.Queue, 0, 25)
	for i := 1; i <= 25; i++ {
		rows = append(rows, queue.Queue{ID: i, Title: "row", Status: queue.QueueStatusQueued})
	}
	svc.SetQueue(rows)
	adapter := queuePagedAdapter{svc}
	// Page 2 of size 10 → 10 records, total 25.
	records, total, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 2, PageSize: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 25 {
		t.Errorf("total: got %d want 25", total)
	}
	if len(records) != 10 {
		t.Errorf("page 2 size: got %d want 10", len(records))
	}
	if records[0].ID != 11 {
		t.Errorf("page 2 first ID: got %d want 11", records[0].ID)
	}
}
