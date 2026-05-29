package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

type fakeMetadataDriver struct {
	mu                sync.Mutex
	importSeriesID    int
	importFileID      int
	coverSeriesID     int
	scanSeriesID      int
	scanFileCount     int
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
