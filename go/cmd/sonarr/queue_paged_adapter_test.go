package main

import (
	"context"
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
)

func TestQueuePagedAdapter_EmptyQueue_ReturnsEmptyPage(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	adapter := queuePagedAdapter{svc: svc}
	records, total, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
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
	adapter := queuePagedAdapter{svc: svc}
	records, total, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
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
	// J348/J352 — the adapter hydrates Series + Episode (singular) +
	// Episodes (plural) unconditionally; the v3/v5 handlers gate them.
	if r.Series == nil || r.Series.ID != 7 {
		t.Errorf("Series subresource: got %v want id=7", r.Series)
	}
	if r.Episode == nil || r.Episode.ID != 11 {
		t.Errorf("Episode subresource: got %v want id=11", r.Episode)
	}
	if r.Episodes == nil || len(*r.Episodes) != 1 || (*r.Episodes)[0].ID != 11 {
		t.Errorf("Episodes plural subresource: got %v want [{id=11}]", r.Episodes)
	}
}

func TestQueuePagedAdapter_CustomFormatsAndScore_J349(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{{
		ID:     1,
		Title:  "row",
		Status: queue.QueueStatusDownloading,
		RemoteEpisode: &decisionengine.Subject{
			CustomFormats:     []customformats.CustomFormat{{ID: 5, Name: "x265"}, {ID: 6, Name: "HDR"}},
			CustomFormatScore: 37,
		},
	}})
	adapter := queuePagedAdapter{svc: svc}
	records, _, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	r := records[0]
	if len(r.CustomFormats) != 2 || r.CustomFormats[0].ID != 5 || r.CustomFormats[1].Name != "HDR" {
		t.Errorf("CustomFormats projection: got %+v", r.CustomFormats)
	}
	if r.CustomFormatScore != 37 {
		t.Errorf("CustomFormatScore: got %d want 37", r.CustomFormatScore)
	}
}

func TestQueuePagedAdapter_NoRemoteEpisode_OmitsCustomFormats_J349(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{{ID: 1, Title: "row", Status: queue.QueueStatusQueued}})
	adapter := queuePagedAdapter{svc: svc}
	records, _, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if records[0].CustomFormats != nil {
		t.Errorf("no RemoteEpisode → CustomFormats must be nil (omitempty drops key); got %+v", records[0].CustomFormats)
	}
	if records[0].CustomFormatScore != 0 {
		t.Errorf("no RemoteEpisode → CustomFormatScore must be 0; got %d", records[0].CustomFormatScore)
	}
}

func TestQueuePagedAdapter_V5ListFields_J351(t *testing.T) {
	t.Parallel()
	sn := 3
	f1 := 100
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{{
		ID:           1,
		Title:        "row",
		Status:       queue.QueueStatusDownloading,
		SeasonNumber: &sn,
		Episodes: []episodes.Episode{
			{ID: 11, EpisodeFileID: &f1}, // has file
			{ID: 12},                     // no file
		},
	}})
	adapter := queuePagedAdapter{svc: svc}
	records, _, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	r := records[0]
	if r.EpisodeIDs == nil || len(*r.EpisodeIDs) != 2 || (*r.EpisodeIDs)[0] != 11 || (*r.EpisodeIDs)[1] != 12 {
		t.Errorf("EpisodeIDs: got %v want [11 12]", r.EpisodeIDs)
	}
	if r.SeasonNumbers == nil || len(*r.SeasonNumbers) != 1 || (*r.SeasonNumbers)[0] != 3 {
		t.Errorf("SeasonNumbers: got %v want [3]", r.SeasonNumbers)
	}
	if r.EpisodesWithFilesCount != 1 {
		t.Errorf("EpisodesWithFilesCount: got %d want 1", r.EpisodesWithFilesCount)
	}
}

func TestQueuePagedAdapter_V5ListFields_EmptyNonNil_J351(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	// No episodes, no SeasonNumber → C# emits `[]` for both lists.
	svc.SetQueue([]queue.Queue{{ID: 1, Title: "row", Status: queue.QueueStatusQueued}})
	adapter := queuePagedAdapter{svc: svc}
	records, _, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	r := records[0]
	if r.EpisodeIDs == nil || len(*r.EpisodeIDs) != 0 {
		t.Errorf("empty EpisodeIDs must be non-nil len-0 (C# = []); got %v", r.EpisodeIDs)
	}
	if r.SeasonNumbers == nil || len(*r.SeasonNumbers) != 0 {
		t.Errorf("empty SeasonNumbers must be non-nil len-0 (C# = []); got %v", r.SeasonNumbers)
	}
	if r.EpisodesWithFilesCount != 0 {
		t.Errorf("EpisodesWithFilesCount: got %d want 0", r.EpisodesWithFilesCount)
	}
}

func TestQueuePagedAdapter_StatusMessages_J350(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{{
		ID:             1,
		Title:          "row",
		Status:         queue.QueueStatusWarning,
		StatusMessages: []string{"Episode already imported", "Unable to parse"},
	}})
	adapter := queuePagedAdapter{svc: svc}
	records, _, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	r := records[0]
	if r.StatusMessages == nil {
		t.Fatalf("StatusMessages pointer must be non-nil")
	}
	got := *r.StatusMessages
	if len(got) != 2 {
		t.Fatalf("StatusMessages len: got %d want 2", len(got))
	}
	if got[0].Title != "Episode already imported" || len(got[0].Messages) != 0 {
		t.Errorf("entry 0: got %+v want {Title:'Episode already imported', Messages:[]}", got[0])
	}
	if got[1].Title != "Unable to parse" {
		t.Errorf("entry 1 title: got %q", got[1].Title)
	}
}

func TestQueuePagedAdapter_StatusMessages_EmptyEmitsNonNilList_J350(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{{ID: 1, Title: "row", Status: queue.QueueStatusQueued}})
	adapter := queuePagedAdapter{svc: svc}
	records, _, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Mirrors C# non-null list — empty domain slice still emits a
	// non-nil pointer so the wire carries "statusMessages":[].
	if records[0].StatusMessages == nil {
		t.Errorf("empty StatusMessages must still be a non-nil pointer (C# non-null list)")
	}
	if len(*records[0].StatusMessages) != 0 {
		t.Errorf("empty StatusMessages must project to len 0; got %d", len(*records[0].StatusMessages))
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
	adapter := queuePagedAdapter{svc: svc}
	// Page 2 of size 10 → 10 records, total 25.
	records, total, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 2, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
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

// TestQueuePagedAdapter_Filter_J358 exercises the wire→domain filter
// mapping through the adapter: default (no includeUnknownSeriesItems)
// excludes series-less rows, and the protocol + status string params
// map to the domain enums.
func TestQueuePagedAdapter_Filter_J358(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{
		{ID: 1, Series: &series.Series{ID: 10}, Protocol: indexers.DownloadProtocolUsenet, Status: queue.QueueStatusDownloading},
		{ID: 2, Series: &series.Series{ID: 20}, Protocol: indexers.DownloadProtocolTorrent, Status: queue.QueueStatusDownloading},
		{ID: 3, Series: nil, Protocol: indexers.DownloadProtocolUsenet, Status: queue.QueueStatusCompleted},
	})
	adapter := queuePagedAdapter{svc: svc}

	// Default filter (IncludeUnknownSeriesItems=false) drops the
	// series-less row (ID 3).
	recs, total, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 2 {
		t.Errorf("default filter total: got %d want 2 (series-less excluded)", total)
	}

	// Protocol=usenet keeps only ID 1 (ID 3 series-less still excluded).
	recs, total, err = adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{Protocol: "usenet"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(recs) != 1 || recs[0].ID != 1 {
		t.Errorf("protocol filter: got total=%d recs=%+v", total, recs)
	}

	// Status=completed with includeUnknownSeriesItems keeps only ID 3.
	recs, total, err = adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{Status: []string{"completed"}, IncludeUnknownSeriesItems: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(recs) != 1 || recs[0].ID != 3 {
		t.Errorf("status filter: got total=%d recs=%+v", total, recs)
	}
}

// TestQueuePagedAdapter_CustomFormatScoreFallback_J359 pins the
// defensive fallback: when the adapter has no qualityProfiles service
// wired (or the profile can't be resolved), the decision-time
// precomputed CustomFormatScore is preserved rather than zeroed.
func TestQueuePagedAdapter_CustomFormatScoreFallback_J359(t *testing.T) {
	t.Parallel()
	svc := queue.NewService()
	svc.SetQueue([]queue.Queue{{
		ID:     1,
		Title:  "row",
		Status: queue.QueueStatusDownloading,
		Series: &series.Series{ID: 7, QualityProfileID: 3},
		RemoteEpisode: &decisionengine.Subject{
			CustomFormats:     []customformats.CustomFormat{{ID: 5, Name: "x265"}},
			CustomFormatScore: 37,
		},
	}})
	// qualityProfiles intentionally nil → recompute returns (0,false) →
	// precomputed 37 is kept.
	adapter := queuePagedAdapter{svc: svc}
	records, _, err := adapter.PagedQueue(context.Background(), apiv3.PagingRequest{Page: 1, PageSize: 10}, apiv3.QueueFilter{IncludeUnknownSeriesItems: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if records[0].CustomFormatScore != 37 {
		t.Errorf("fallback must preserve precomputed score; got %d want 37", records[0].CustomFormatScore)
	}
}
