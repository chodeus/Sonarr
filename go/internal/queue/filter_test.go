package queue_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
)

func TestFilterQueue_ExcludesSeriesLessByDefault(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Series: &series.Series{ID: 10}},
		{ID: 2, Series: nil},
	}
	// Default (IncludeUnknownSeriesItems=false) drops the series-less row.
	got := queue.FilterQueue(rows, queue.FilterCriteria{})
	eq(t, ids(got), []int{1})
	// Explicit include keeps both.
	got = queue.FilterQueue(rows, queue.FilterCriteria{IncludeUnknownSeriesItems: true})
	eq(t, ids(got), []int{1, 2})
}

func TestFilterQueue_SeriesIDs(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Series: &series.Series{ID: 10}},
		{ID: 2, Series: &series.Series{ID: 20}},
		{ID: 3, Series: &series.Series{ID: 30}},
	}
	got := queue.FilterQueue(rows, queue.FilterCriteria{SeriesIDs: map[int]bool{10: true, 30: true}})
	eq(t, ids(got), []int{1, 3})
}

func TestFilterQueue_Protocol(t *testing.T) {
	t.Parallel()
	usenet := indexers.DownloadProtocolUsenet
	rows := []queue.Queue{
		{ID: 1, Series: &series.Series{ID: 1}, Protocol: indexers.DownloadProtocolUsenet},
		{ID: 2, Series: &series.Series{ID: 2}, Protocol: indexers.DownloadProtocolTorrent},
	}
	got := queue.FilterQueue(rows, queue.FilterCriteria{Protocol: &usenet})
	eq(t, ids(got), []int{1})
}

func TestFilterQueue_Languages(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Series: &series.Series{ID: 1}, Languages: []language.Language{{ID: 1}}},
		{ID: 2, Series: &series.Series{ID: 2}, Languages: []language.Language{{ID: 2}, {ID: 3}}},
		{ID: 3, Series: &series.Series{ID: 3}, Languages: []language.Language{{ID: 5}}},
	}
	// Keep rows with ANY language in {2,5}.
	got := queue.FilterQueue(rows, queue.FilterCriteria{Languages: map[int]bool{2: true, 5: true}})
	eq(t, ids(got), []int{2, 3})
}

func TestFilterQueue_Quality(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Series: &series.Series{ID: 1}, Quality: quality.Model{Quality: quality.Quality{ID: 7}}},
		{ID: 2, Series: &series.Series{ID: 2}, Quality: quality.Model{Quality: quality.Quality{ID: 9}}},
	}
	got := queue.FilterQueue(rows, queue.FilterCriteria{Quality: map[int]bool{9: true}})
	eq(t, ids(got), []int{2})
}

func TestFilterQueue_Status(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Series: &series.Series{ID: 1}, Status: queue.QueueStatusDownloading},
		{ID: 2, Series: &series.Series{ID: 2}, Status: queue.QueueStatusCompleted},
		{ID: 3, Series: &series.Series{ID: 3}, Status: queue.QueueStatusPaused},
	}
	got := queue.FilterQueue(rows, queue.FilterCriteria{Status: map[queue.QueueStatus]bool{
		queue.QueueStatusDownloading: true, queue.QueueStatusPaused: true,
	}})
	eq(t, ids(got), []int{1, 3})
}

func TestFilterQueue_Combined(t *testing.T) {
	t.Parallel()
	usenet := indexers.DownloadProtocolUsenet
	rows := []queue.Queue{
		{ID: 1, Series: &series.Series{ID: 10}, Protocol: indexers.DownloadProtocolUsenet, Status: queue.QueueStatusDownloading},
		{ID: 2, Series: &series.Series{ID: 10}, Protocol: indexers.DownloadProtocolTorrent, Status: queue.QueueStatusDownloading},
		{ID: 3, Series: &series.Series{ID: 20}, Protocol: indexers.DownloadProtocolUsenet, Status: queue.QueueStatusDownloading},
	}
	got := queue.FilterQueue(rows, queue.FilterCriteria{
		SeriesIDs: map[int]bool{10: true},
		Protocol:  &usenet,
		Status:    map[queue.QueueStatus]bool{queue.QueueStatusDownloading: true},
	})
	eq(t, ids(got), []int{1})
}

func TestFilterQueue_NoMutation(t *testing.T) {
	t.Parallel()
	rows := []queue.Queue{
		{ID: 1, Series: &series.Series{ID: 1}},
		{ID: 2, Series: nil},
	}
	_ = queue.FilterQueue(rows, queue.FilterCriteria{})
	eq(t, ids(rows), []int{1, 2}) // input untouched
}
