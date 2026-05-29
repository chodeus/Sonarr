package main

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// minimal Provider fake for the tracker adapter test.
type trackerTestProvider struct {
	def      *downloadclients.DownloadClient
	protocol indexers.DownloadProtocol
}

func (p trackerTestProvider) Name() string                                { return "fake" }
func (p trackerTestProvider) Protocol() indexers.DownloadProtocol         { return p.protocol }
func (p trackerTestProvider) Definition() *downloadclients.DownloadClient { return p.def }
func (p trackerTestProvider) GetItems(context.Context) ([]*downloadclients.Item, error) {
	return nil, nil
}
func (p trackerTestProvider) Download(context.Context, *downloadclients.GrabRequest) (string, error) {
	return "", nil
}
func (p trackerTestProvider) GetStatus(context.Context) (*downloadclients.ClientInfo, error) {
	return nil, nil
}
func (p trackerTestProvider) RemoveItem(context.Context, *downloadclients.Item, bool) error {
	return nil
}
func (p trackerTestProvider) MarkItemAsImported(context.Context, *downloadclients.Item) error {
	return nil
}
func (p trackerTestProvider) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}
func (p trackerTestProvider) Test(context.Context) ([]downloadclients.TestFailure, error) {
	return nil, nil
}
func (p trackerTestProvider) RequestAction(context.Context, string, map[string]string) (any, error) {
	return nil, nil
}

// J394: TrackDownload builds + caches a TrackedDownload carrying the
// client config-id + protocol + live item, defaulting to Downloading.
// (parsing nil → RemoteEpisode stays nil; the unmatched-download path.)
func TestDownloadItemTracker_TracksNewItem_J394(t *testing.T) {
	t.Parallel()
	tracked := trackeddownloads.NewService(slog.Default())
	a := downloadItemTrackerAdapter{parsing: nil, tracked: tracked, log: slog.Default()}
	client := trackerTestProvider{def: &downloadclients.DownloadClient{ID: 5}, protocol: indexers.DownloadProtocolTorrent}
	item := &downloadclients.Item{DownloadID: "abc", Title: "Show.S01E01.WEBDL-1080p"}

	td := a.TrackDownload(t.Context(), client, item)
	if td == nil {
		t.Fatal("TrackDownload returned nil")
	}
	if td.DownloadID != "abc" || td.DownloadClient != 5 || td.Protocol != int(indexers.DownloadProtocolTorrent) {
		t.Errorf("td = %+v, want DownloadID=abc client=5 protocol=torrent", td)
	}
	if !td.IsTrackable || td.State != trackeddownloads.StateDownloading || td.DownloadItem != item {
		t.Errorf("td flags/state/item wrong: %+v", td)
	}
	if got := tracked.Find("abc"); got != td {
		t.Error("TrackDownload should have cached the tracked download")
	}
}

// J394: an already-tracked download keeps its prior state, refreshes the
// live item, and re-marks trackable (C# existing-item arm).
func TestDownloadItemTracker_PreservesExistingState_J394(t *testing.T) {
	t.Parallel()
	tracked := trackeddownloads.NewService(slog.Default())
	_ = tracked.Track(&trackeddownloads.TrackedDownload{DownloadID: "x", State: trackeddownloads.StateImported, IsTrackable: false})
	a := downloadItemTrackerAdapter{parsing: nil, tracked: tracked, log: slog.Default()}
	client := trackerTestProvider{def: &downloadclients.DownloadClient{ID: 9}, protocol: indexers.DownloadProtocolUsenet}
	newItem := &downloadclients.Item{DownloadID: "x", Title: "Show.S01E02"}

	td := a.TrackDownload(t.Context(), client, newItem)
	if td.State != trackeddownloads.StateImported {
		t.Errorf("State = %v, want preserved StateImported", td.State)
	}
	if !td.IsTrackable {
		t.Error("existing item should be re-marked trackable")
	}
	if td.DownloadItem != newItem {
		t.Error("existing item should refresh the live DownloadItem")
	}
}
