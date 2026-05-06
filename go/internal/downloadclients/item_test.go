package downloadclients_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

func TestDownloadItemStatus_NumericOrder(t *testing.T) {
	t.Parallel()
	// Wire-stable contract: the v3 React frontend serialises this
	// enum as int. A reorder breaks SignalR payload diffs.
	cases := []struct {
		got  downloadclients.DownloadItemStatus
		want int
	}{
		{downloadclients.DownloadItemStatusQueued, 0},
		{downloadclients.DownloadItemStatusPaused, 1},
		{downloadclients.DownloadItemStatusDownloading, 2},
		{downloadclients.DownloadItemStatusCompleted, 3},
		{downloadclients.DownloadItemStatusFailed, 4},
		{downloadclients.DownloadItemStatusWarning, 5},
	}
	for _, c := range cases {
		if int(c.got) != c.want {
			t.Errorf("status %v = %d, want %d", c.got, int(c.got), c.want)
		}
	}
}

func TestItem_CloneShallow(t *testing.T) {
	t.Parallel()
	dur := 5 * time.Minute
	ratio := 1.25
	info := &downloadclients.ItemClientInfo{
		Protocol: indexers.DownloadProtocolTorrent,
		Type:     "QBittorrent",
		ID:       42,
		Name:     "Local QB",
	}
	orig := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         "abc123",
		Category:           "tv-sonarr",
		Title:              "Series.S01E01",
		TotalSize:          1_073_741_824,
		RemainingSize:      0,
		RemainingTime:      &dur,
		SeedRatio:          &ratio,
		OutputPath:         ospath.New("/downloads/complete/Series.S01E01"),
		Status:             downloadclients.DownloadItemStatusCompleted,
		CanMoveFiles:       true,
		CanBeRemoved:       true,
	}

	clone := orig.Clone()
	if clone == orig {
		t.Errorf("Clone returned same pointer; want fresh value")
	}
	if clone.DownloadClientInfo != info {
		t.Errorf("Clone.DownloadClientInfo = %p, want shared %p (shallow)", clone.DownloadClientInfo, info)
	}
	if clone.DownloadID != "abc123" || clone.Title != "Series.S01E01" {
		t.Errorf("Clone field copy lost values: %+v", clone)
	}

	// Mutating clone scalars must not touch original.
	clone.Status = downloadclients.DownloadItemStatusFailed
	if orig.Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("Clone scalar mutation leaked into original: %v", orig.Status)
	}
}

func TestItem_CloneNil(t *testing.T) {
	t.Parallel()
	var orig *downloadclients.Item
	if got := orig.Clone(); got != nil {
		t.Errorf("nil.Clone() = %v, want nil", got)
	}
}
