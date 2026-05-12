package metadataservice_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/metadataservice"
	"sonarr.tv/sonarr/internal/series"
)

// classifyProvider is a fakeProvider variant whose FindMetadataFile
// returns a canned MetadataType for paths matching a registered
// suffix. Used by the importer tests to programme a writer's
// classification without writing real file content.
type classifyProvider struct {
	fakeProvider
	classify func(path string) (metadata.MetadataType, bool)
	consumer string
}

func (p *classifyProvider) FindMetadataFile(_ *series.Series, path string) (*metadatafiles.MetadataFile, error) {
	if p.classify == nil {
		return nil, nil
	}
	t, ok := p.classify(path)
	if !ok {
		return nil, nil
	}
	return &metadatafiles.MetadataFile{
		SeriesID:     1,
		Consumer:     p.consumer,
		RelativePath: path, // tests don't care about series-relative; the importer overwrites Extension only.
		Type:         int(t),
	}, nil
}

// fakeAggregator implements [metadataservice.AggregationService]
// with a programmable per-path return.
type fakeAggregator struct {
	byPath map[string][]episodes.Episode
	err    error
}

func (f *fakeAggregator) Augment(_ context.Context, _ *series.Series, path string) ([]episodes.Episode, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byPath[path], nil
}

func newImporter(t *testing.T, deps metadataservice.ImporterDeps) *metadataservice.ExistingImporter {
	t.Helper()
	imp, err := metadataservice.NewImporter(deps)
	if err != nil {
		t.Fatalf("NewImporter: %v", err)
	}
	return imp
}

func TestNewImporter_RejectsNilRequiredDeps(t *testing.T) {
	t.Parallel()
	if _, err := metadataservice.NewImporter(metadataservice.ImporterDeps{Files: &fakeFiles{}}); err == nil {
		t.Errorf("nil Writers should error")
	}
	if _, err := metadataservice.NewImporter(metadataservice.ImporterDeps{Writers: &fakeWriters{}}); err == nil {
		t.Errorf("nil Files should error")
	}
	if _, err := metadataservice.NewImporter(metadataservice.ImporterDeps{Writers: &fakeWriters{}, Files: &fakeFiles{}}); err != nil {
		t.Errorf("Writers+Files only should succeed: %v", err)
	}
}

func TestImporter_Order(t *testing.T) {
	t.Parallel()
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{}, Files: &fakeFiles{}})
	if got := imp.Order(); got != 0 {
		t.Errorf("Order = %d, want 0", got)
	}
}

func TestImporter_ProcessFiles_NoCandidates(t *testing.T) {
	t.Parallel()
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{}, Files: &fakeFiles{}})
	rows, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, nil, nil, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 0 {
		t.Errorf("no candidates → no rows, got %+v", rows)
	}
}

func TestImporter_ProcessFiles_SkipsSubtitleExtensions(t *testing.T) {
	t.Parallel()
	provider := &classifyProvider{
		fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"},
		consumer:     "XbmcMetadata",
		classify: func(path string) (metadata.MetadataType, bool) {
			return metadata.MetadataTypeSeriesImage, true
		},
	}
	files := &fakeFiles{}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{all: []metadata.Provider{provider}}, Files: files})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	rows, err := imp.ProcessFiles(t.Context(), sr, []string{
		"/tv/Show/poster.jpg",
		"/tv/Show/S01E02.srt",
		"/tv/Show/S01E02.ass",
		"/tv/Show/banner.jpg",
		"/tv/Show/eng.SRT", // case-insensitive
	}, nil, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Subtitle extensions should be skipped — 2 .jpg paths remain.
	// Both classified as SeriesImage by the fake writer.
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (subtitles skipped), got %d", len(rows))
	}
}

func TestImporter_ProcessFiles_ClassifiesSeriesImage(t *testing.T) {
	t.Parallel()
	provider := &classifyProvider{
		fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"},
		consumer:     "XbmcMetadata",
		classify: func(path string) (metadata.MetadataType, bool) {
			if path == "/tv/Show/poster.jpg" {
				return metadata.MetadataTypeSeriesImage, true
			}
			return 0, false
		},
	}
	files := &fakeFiles{}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{all: []metadata.Provider{provider}}, Files: files})
	rows, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/poster.jpg"}, nil, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if metadata.MetadataType(rows[0].Type) != metadata.MetadataTypeSeriesImage {
		t.Errorf("Type = %s", metadata.MetadataType(rows[0].Type))
	}
	if rows[0].Extension != ".jpg" {
		t.Errorf("Extension = %q, want .jpg", rows[0].Extension)
	}
	if len(files.upserts) != 1 {
		t.Errorf("Upsert: %+v", files.upserts)
	}
}

func TestImporter_ProcessFiles_EpisodeImageNeedsAggregator(t *testing.T) {
	t.Parallel()
	provider := &classifyProvider{
		fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"},
		consumer:     "XbmcMetadata",
		classify: func(_ string) (metadata.MetadataType, bool) {
			return metadata.MetadataTypeEpisodeImage, true
		},
	}
	files := &fakeFiles{}
	// nil Aggregator → EpisodeImage classification SKIPPED.
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{all: []metadata.Provider{provider}}, Files: files})
	rows, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/S01E02-thumb.jpg"}, nil, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 0 {
		t.Errorf("nil aggregator should drop EpisodeImage rows, got %+v", rows)
	}
}

func TestImporter_ProcessFiles_EpisodeImageWithAggregator(t *testing.T) {
	t.Parallel()
	provider := &classifyProvider{
		fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"},
		consumer:     "XbmcMetadata",
		classify: func(_ string) (metadata.MetadataType, bool) {
			return metadata.MetadataTypeEpisodeImage, true
		},
	}
	files := &fakeFiles{}
	fileID := 42
	agg := &fakeAggregator{byPath: map[string][]episodes.Episode{
		"/tv/Show/S01E02-thumb.jpg": {{ID: 100, SeasonNumber: 1, EpisodeFileID: &fileID}},
	}}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{all: []metadata.Provider{provider}}, Files: files, Aggregator: agg})
	rows, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/S01E02-thumb.jpg"}, nil, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].SeasonNumber == nil || *rows[0].SeasonNumber != 1 {
		t.Errorf("SeasonNumber = %v, want 1", rows[0].SeasonNumber)
	}
	if rows[0].EpisodeFileID == nil || *rows[0].EpisodeFileID != 42 {
		t.Errorf("EpisodeFileID = %v, want 42", rows[0].EpisodeFileID)
	}
}

func TestImporter_ProcessFiles_AggregatorErrorSkipsRow(t *testing.T) {
	t.Parallel()
	provider := &classifyProvider{
		fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"},
		consumer:     "XbmcMetadata",
		classify: func(_ string) (metadata.MetadataType, bool) {
			return metadata.MetadataTypeEpisodeMetadata, true
		},
	}
	files := &fakeFiles{}
	agg := &fakeAggregator{err: errors.New("AugmentingFailedException equivalent")}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{all: []metadata.Provider{provider}}, Files: files, Aggregator: agg})
	rows, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/Season 1/S01E02.nfo"}, nil, false)
	if err != nil {
		t.Errorf("aggregator error should be non-fatal: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("aggregator error should skip the row, got %+v", rows)
	}
}

func TestImporter_ProcessFiles_AggregatorEmptyEpisodesSkipsRow(t *testing.T) {
	t.Parallel()
	provider := &classifyProvider{
		fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"},
		consumer:     "XbmcMetadata",
		classify: func(_ string) (metadata.MetadataType, bool) {
			return metadata.MetadataTypeEpisodeImage, true
		},
	}
	agg := &fakeAggregator{} // empty byPath → no episodes resolved
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{all: []metadata.Provider{provider}}, Files: &fakeFiles{}, Aggregator: agg})
	rows, _ := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/S01E02.jpg"}, nil, false)
	if len(rows) != 0 {
		t.Errorf("no episodes resolved should skip the row: %+v", rows)
	}
}

func TestImporter_ProcessFiles_MultiFileSpanningEpisodesSkipsRow(t *testing.T) {
	t.Parallel()
	provider := &classifyProvider{
		fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"},
		consumer:     "XbmcMetadata",
		classify: func(_ string) (metadata.MetadataType, bool) {
			return metadata.MetadataTypeEpisodeImage, true
		},
	}
	a := 42
	b := 43
	// Two episodes pointing at DIFFERENT EpisodeFileIDs — the C#
	// `:87` "spans multiple files" skip should fire.
	agg := &fakeAggregator{byPath: map[string][]episodes.Episode{
		"/tv/Show/S01E02.jpg": {
			{ID: 1, SeasonNumber: 1, EpisodeFileID: &a},
			{ID: 2, SeasonNumber: 1, EpisodeFileID: &b},
		},
	}}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{all: []metadata.Provider{provider}}, Files: &fakeFiles{}, Aggregator: agg})
	rows, _ := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/S01E02.jpg"}, nil, false)
	if len(rows) != 0 {
		t.Errorf("multi-file-spanning should skip the row: %+v", rows)
	}
}

func TestImporter_ProcessFiles_PreviouslyImportedReturned(t *testing.T) {
	t.Parallel()
	files := &fakeFiles{bySeries: map[int][]metadatafiles.MetadataFile{
		7: {{ID: 100, SeriesID: 7, RelativePath: "tvshow.nfo", Consumer: "XbmcMetadata"}},
	}}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{}, Files: files})
	// Disk has the existing tvshow.nfo; the importer should
	// classify it as "previously imported" and return it without
	// re-classifying.
	rows, _ := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/tvshow.nfo"}, nil, false)
	if len(rows) != 1 {
		t.Fatalf("expected previously-imported row to be returned: %+v", rows)
	}
	if rows[0].ID != 100 {
		t.Errorf("previously-imported row should keep its ID: %+v", rows[0])
	}
}

func TestImporter_ProcessFiles_CleanDeletesGoneFromDisk(t *testing.T) {
	t.Parallel()
	files := &fakeFiles{bySeries: map[int][]metadatafiles.MetadataFile{
		7: {
			{ID: 100, SeriesID: 7, RelativePath: "tvshow.nfo"},
			{ID: 200, SeriesID: 7, RelativePath: "deleted.jpg"}, // not on disk
		},
	}}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{}, Files: files})
	_, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/tvshow.nfo"}, nil, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Row 200 should be in DeleteMany (gone from disk).
	if len(files.deleted) != 1 || files.deleted[0] != 200 {
		t.Errorf("expected delete of gone-from-disk row, got %+v", files.deleted)
	}
}

func TestImporter_ProcessFiles_CleanDeletesAlreadyImportedElsewhere(t *testing.T) {
	t.Parallel()
	files := &fakeFiles{bySeries: map[int][]metadatafiles.MetadataFile{
		7: {{ID: 100, SeriesID: 7, RelativePath: "tvshow.nfo"}},
	}}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{}, Files: files})
	// importedFiles allow-list contains the file → row 100 must be deleted.
	_, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/tvshow.nfo"}, []string{"/tv/Show/tvshow.nfo"}, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(files.deleted) != 1 || files.deleted[0] != 100 {
		t.Errorf("expected delete of imported-elsewhere row, got %+v", files.deleted)
	}
}

func TestImporter_ProcessFiles_KeepExistingEntriesDeletesIncomplete(t *testing.T) {
	t.Parallel()
	files := &fakeFiles{bySeries: map[int][]metadatafiles.MetadataFile{
		7: {
			{ID: 100, SeriesID: 7, RelativePath: "tvshow.nfo"},
			{ID: 200, SeriesID: 7, RelativePath: "deleted.jpg"},
		},
	}}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{}, Files: files})
	// keepExistingEntries=true → delete rows whose paths are still
	// on disk (mid-rename incomplete imports); preserve gone-from-
	// disk rows.
	_, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/tvshow.nfo"}, nil, true)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Row 100 should be deleted (incomplete-rename trigger).
	// Row 200 should NOT be touched.
	if len(files.deleted) != 1 || files.deleted[0] != 100 {
		t.Errorf("keepExistingEntries=true: expected delete of [100], got %+v", files.deleted)
	}
}

func TestImporter_ProcessFiles_CaseInsensitivePathCompare(t *testing.T) {
	t.Parallel()
	files := &fakeFiles{bySeries: map[int][]metadatafiles.MetadataFile{
		7: {{ID: 100, SeriesID: 7, RelativePath: "tvshow.nfo"}},
	}}
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{}, Files: files})
	// Disk path uses mixed case; should still match the stored row.
	rows, _ := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/TV/Show"}, []string{"/TV/Show/TvShow.Nfo"}, nil, false)
	if len(rows) != 1 || rows[0].ID != 100 {
		t.Errorf("case-insensitive compare should match: %+v", rows)
	}
}

func TestImporter_ProcessFiles_WriterFactoryErrorPropagates(t *testing.T) {
	t.Parallel()
	imp := newImporter(t, metadataservice.ImporterDeps{Writers: &fakeWriters{allErr: errors.New("registry boom")}, Files: &fakeFiles{}})
	_, err := imp.ProcessFiles(t.Context(), &series.Series{ID: 7, Path: "/tv/Show"}, []string{"/tv/Show/poster.jpg"}, nil, false)
	if err == nil {
		t.Errorf("expected error from WriterFactory.All")
	}
}

func TestImporter_ProcessFiles_NilReceiver(t *testing.T) {
	t.Parallel()
	var imp *metadataservice.ExistingImporter
	rows, err := imp.ProcessFiles(t.Context(), &series.Series{}, nil, nil, false)
	if err != nil || rows != nil {
		t.Errorf("nil receiver: %+v / %v", rows, err)
	}
}

// Sanity: importer uses the EpisodeFileID-aware narrow type without
// importing internal/episodefiles in the test file. Quiet the unused
// import warning via a forward reference.
var _ = episodefiles.File{}
