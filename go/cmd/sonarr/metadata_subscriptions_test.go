package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type fakeMetadataDriver struct {
	mu                sync.Mutex
	importSeriesID    int
	importFileID      int
	coverSeriesID     int
	scanSeriesID      int
	scanFileCount     int
	folderSeriesID    int
	folderSeries      string
	folderSeason      string
	renameSeriesID    int
	renameFileCount   int
	importedBatch     []int
	failEpisodeImport bool
}

func (f *fakeMetadataDriver) CreateAfterEpisodeImport(_ context.Context, sr *series.Series, file *episodefiles.File) ([]metadatafiles.MetadataFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.importSeriesID = sr.ID
	f.importFileID = file.ID
	if f.failEpisodeImport {
		return nil, errors.New("boom")
	}
	return nil, nil
}

func (f *fakeMetadataDriver) CreateAfterMediaCoverUpdate(_ context.Context, sr *series.Series) ([]metadatafiles.MetadataFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.coverSeriesID = sr.ID
	return nil, nil
}

func (f *fakeMetadataDriver) CreateAfterSeriesScan(_ context.Context, sr *series.Series, list []episodefiles.File) ([]metadatafiles.MetadataFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scanSeriesID = sr.ID
	f.scanFileCount = len(list)
	return nil, nil
}

func (f *fakeMetadataDriver) CreateAfterEpisodeFolder(_ context.Context, sr *series.Series, seriesFolder, seasonFolder string) ([]metadatafiles.MetadataFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folderSeriesID = sr.ID
	f.folderSeries = seriesFolder
	f.folderSeason = seasonFolder
	return nil, nil
}

func (f *fakeMetadataDriver) MoveFilesAfterRename(_ context.Context, sr *series.Series, list []episodefiles.File) ([]metadatafiles.MetadataFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renameSeriesID = sr.ID
	f.renameFileCount = len(list)
	return nil, nil
}

func (f *fakeMetadataDriver) CreateAfterEpisodesImported(_ context.Context, sr *series.Series) ([]metadatafiles.MetadataFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.importedBatch = append(f.importedBatch, sr.ID)
	return nil, nil
}

type fakeScanFileLister struct {
	files []episodefiles.File
	err   error
}

func (f fakeScanFileLister) GetFilesBySeries(context.Context, int) ([]episodefiles.File, error) {
	return f.files, f.err
}

func newTestBus() *messaging.Bus {
	return messaging.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSubscribeMetadataWriters_EpisodeImported(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	subscribeMetadataWriters(bus, drv, fakeScanFileLister{}, discardLogger())

	ev := episodeimport.EpisodeImportedEvent{
		LocalEpisode: &episodeimport.LocalEpisode{Series: &series.Series{ID: 42}},
		EpisodeFile:  &episodefiles.File{ID: 7},
	}
	if err := messaging.Publish(context.Background(), bus, ev); err != nil {
		t.Fatal(err)
	}
	if drv.importSeriesID != 42 || drv.importFileID != 7 {
		t.Errorf("CreateAfterEpisodeImport got series=%d file=%d, want 42/7", drv.importSeriesID, drv.importFileID)
	}
}

func TestSubscribeMetadataWriters_NilGuards(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	subscribeMetadataWriters(bus, drv, fakeScanFileLister{}, discardLogger())

	// nil LocalEpisode / nil Series / nil file must not call the driver.
	_ = messaging.Publish(context.Background(), bus, episodeimport.EpisodeImportedEvent{})
	_ = messaging.Publish(context.Background(), bus, episodeimport.EpisodeImportedEvent{LocalEpisode: &episodeimport.LocalEpisode{}})
	_ = messaging.Publish(context.Background(), bus, mediacover.CoversUpdatedEvent{})
	_ = messaging.Publish(context.Background(), bus, mediafiles.SeriesScannedEvent{})
	if drv.importSeriesID != 0 || drv.coverSeriesID != 0 || drv.scanSeriesID != 0 {
		t.Errorf("nil-guarded events should not call driver: %+v", drv)
	}
}

func TestSubscribeMetadataWriters_CoversUpdated(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	subscribeMetadataWriters(bus, drv, fakeScanFileLister{}, discardLogger())

	if err := messaging.Publish(context.Background(), bus, mediacover.CoversUpdatedEvent{Series: &series.Series{ID: 9}}); err != nil {
		t.Fatal(err)
	}
	if drv.coverSeriesID != 9 {
		t.Errorf("CreateAfterMediaCoverUpdate got %d, want 9", drv.coverSeriesID)
	}
}

func TestSubscribeMetadataWriters_SeriesScanned_LoadsFiles(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	lister := fakeScanFileLister{files: []episodefiles.File{{ID: 1}, {ID: 2}, {ID: 3}}}
	subscribeMetadataWriters(bus, drv, lister, discardLogger())

	if err := messaging.Publish(context.Background(), bus, mediafiles.SeriesScannedEvent{Series: &series.Series{ID: 5}}); err != nil {
		t.Fatal(err)
	}
	if drv.scanSeriesID != 5 || drv.scanFileCount != 3 {
		t.Errorf("CreateAfterSeriesScan got series=%d files=%d, want 5/3", drv.scanSeriesID, drv.scanFileCount)
	}
}

func TestSubscribeMetadataWriters_SeriesScanned_FileLoadError_NoCall(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	lister := fakeScanFileLister{err: errors.New("db down")}
	subscribeMetadataWriters(bus, drv, lister, discardLogger())

	_ = messaging.Publish(context.Background(), bus, mediafiles.SeriesScannedEvent{Series: &series.Series{ID: 5}})
	if drv.scanSeriesID != 0 {
		t.Errorf("file-load error must skip CreateAfterSeriesScan, got series=%d", drv.scanSeriesID)
	}
}

func TestSubscribeMetadataWriters_EpisodeFolderCreated_J452(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	subscribeMetadataWriters(bus, drv, fakeScanFileLister{}, discardLogger())

	ev := episodefilemoving.EpisodeFolderCreatedEvent{
		Series:       &series.Series{ID: 11},
		SeriesFolder: "/tv/Show",
		SeasonFolder: "/tv/Show/Season 01",
	}
	if err := messaging.Publish(context.Background(), bus, ev); err != nil {
		t.Fatal(err)
	}
	if drv.folderSeriesID != 11 || drv.folderSeries != "/tv/Show" || drv.folderSeason != "/tv/Show/Season 01" {
		t.Errorf("CreateAfterEpisodeFolder got series=%d seriesFolder=%q seasonFolder=%q, want 11//tv/Show//tv/Show/Season 01",
			drv.folderSeriesID, drv.folderSeries, drv.folderSeason)
	}
}

func TestSubscribeMetadataWriters_EpisodeFolderCreated_NilSeries_J452(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	subscribeMetadataWriters(bus, drv, fakeScanFileLister{}, discardLogger())

	_ = messaging.Publish(context.Background(), bus, episodefilemoving.EpisodeFolderCreatedEvent{})
	if drv.folderSeriesID != 0 {
		t.Errorf("nil-series EpisodeFolderCreatedEvent should not call driver, got series=%d", drv.folderSeriesID)
	}
}

func TestSubscribeMetadataWriters_SeriesRenamed_J453(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	lister := fakeScanFileLister{files: []episodefiles.File{{ID: 1}, {ID: 2}}}
	subscribeMetadataWriters(bus, drv, lister, discardLogger())

	ev := episodefiles.SeriesRenamedEvent{Series: &series.Series{ID: 7}}
	if err := messaging.Publish(context.Background(), bus, ev); err != nil {
		t.Fatal(err)
	}
	// C# reloads ALL series files (not the renamed subset) → 2 here.
	if drv.renameSeriesID != 7 || drv.renameFileCount != 2 {
		t.Errorf("MoveFilesAfterRename got series=%d files=%d, want 7/2", drv.renameSeriesID, drv.renameFileCount)
	}
}

func TestSubscribeMetadataWriters_SeriesRenamed_NilSeries_J453(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	subscribeMetadataWriters(bus, drv, fakeScanFileLister{}, discardLogger())

	_ = messaging.Publish(context.Background(), bus, episodefiles.SeriesRenamedEvent{})
	if drv.renameSeriesID != 0 {
		t.Errorf("nil-series SeriesRenamedEvent should not call driver, got series=%d", drv.renameSeriesID)
	}
}

func TestSubscribeMetadataWriters_DownloadsProcessed_DrainsAccumulator_J454(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{}
	subscribeMetadataWriters(bus, drv, fakeScanFileLister{}, discardLogger())
	ctx := context.Background()

	// Two imports for series 5, one for series 9 → 2 distinct series.
	imp := func(seriesID, fileID int) episodeimport.EpisodeImportedEvent {
		return episodeimport.EpisodeImportedEvent{
			LocalEpisode: &episodeimport.LocalEpisode{Series: &series.Series{ID: seriesID}},
			EpisodeFile:  &episodefiles.File{ID: fileID},
		}
	}
	for _, ev := range []episodeimport.EpisodeImportedEvent{imp(5, 1), imp(5, 2), imp(9, 3)} {
		if err := messaging.Publish(ctx, bus, ev); err != nil {
			t.Fatal(err)
		}
	}

	if err := messaging.Publish(ctx, bus, trackeddownloads.DownloadsProcessedEvent{}); err != nil {
		t.Fatal(err)
	}
	// CreateAfterEpisodesImported called once per distinct series (5, 9).
	if len(drv.importedBatch) != 2 {
		t.Fatalf("CreateAfterEpisodesImported calls = %v, want 2 distinct series", drv.importedBatch)
	}
	got := map[int]bool{drv.importedBatch[0]: true, drv.importedBatch[1]: true}
	if !got[5] || !got[9] {
		t.Errorf("imported batch = %v, want series {5,9}", drv.importedBatch)
	}

	// A second DownloadsProcessedEvent with no new imports drains nothing.
	drv.importedBatch = nil
	if err := messaging.Publish(ctx, bus, trackeddownloads.DownloadsProcessedEvent{}); err != nil {
		t.Fatal(err)
	}
	if len(drv.importedBatch) != 0 {
		t.Errorf("second pass should drain nothing, got %v", drv.importedBatch)
	}
}

func TestSubscribeMetadataWriters_HandlerErrorSwallowed(t *testing.T) {
	t.Parallel()
	bus := newTestBus()
	drv := &fakeMetadataDriver{failEpisodeImport: true}
	subscribeMetadataWriters(bus, drv, fakeScanFileLister{}, discardLogger())

	// A driver error must not propagate out of Publish (logged + swallowed).
	ev := episodeimport.EpisodeImportedEvent{
		LocalEpisode: &episodeimport.LocalEpisode{Series: &series.Series{ID: 1}},
		EpisodeFile:  &episodefiles.File{ID: 1},
	}
	if err := messaging.Publish(context.Background(), bus, ev); err != nil {
		t.Errorf("Publish should not return handler error: %v", err)
	}
}
