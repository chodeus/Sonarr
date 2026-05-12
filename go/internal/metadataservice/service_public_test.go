package metadataservice_test

import (
	"errors"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/metadataservice"
	"sonarr.tv/sonarr/internal/series"
)

// withSeriesFolder pre-stamps a series folder so FolderExists
// returns true + registers a nil FilesBySeries entry. Used by the
// per-event tests below. Helper kept for clarity even though every
// caller passes nil today.
func withSeriesFolder(t *testing.T, files *fakeFiles, disk *fakeDisk, sr *series.Series) {
	t.Helper()
	if files.bySeries == nil {
		files.bySeries = map[int][]metadatafiles.MetadataFile{}
	}
	files.bySeries[sr.ID] = nil
	disk.folders[sr.Path] = true
}

func TestService_Order(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil)
	if got := svc.Order(); got != 0 {
		t.Errorf("Order = %d, want 0", got)
	}
}

func TestCreateAfterMediaCoverUpdate_HappyPath(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesImgs:   []metadata.ImageFileResult{metadata.NewImageFileResult("poster.jpg", "https://cdn/p.jpg")},
	}
	cleaner := &fakeCleaner{}
	svc, disk, http, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
		d.Cleaner = cleaner
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	withSeriesFolder(t, files, disk, sr)

	rows, err := svc.CreateAfterMediaCoverUpdate(t.Context(), sr)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
	if len(cleaner.calls) != 1 || cleaner.calls[0] != 7 {
		t.Errorf("Cleaner should have run: %+v", cleaner.calls)
	}
	if len(http.downloads) != 1 {
		t.Errorf("expected http download: %+v", http.downloads)
	}
	if len(files.upserts) != 1 || len(files.upserts[0]) != 1 {
		t.Errorf("Upsert: %+v", files.upserts)
	}
}

func TestCreateAfterMediaCoverUpdate_FolderMissing_Skips(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{consumerImpl: "XbmcMetadata"}
	svc, _, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	// Note: folder NOT registered in disk.folders.
	files.bySeries = map[int][]metadatafiles.MetadataFile{7: nil}

	rows, err := svc.CreateAfterMediaCoverUpdate(t.Context(), sr)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if rows != nil {
		t.Errorf("folder missing should return nil, got %+v", rows)
	}
	if len(files.upserts) != 0 {
		t.Errorf("no upsert should occur")
	}
}

func TestCreateAfterMediaCoverUpdate_CleanerErrorNonFatal(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{consumerImpl: "XbmcMetadata"}
	cleaner := &fakeCleaner{err: errors.New("cleaner exploded")}
	svc, disk, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
		d.Cleaner = cleaner
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	withSeriesFolder(t, files, disk, sr)
	_, err := svc.CreateAfterMediaCoverUpdate(t.Context(), sr)
	if err != nil {
		t.Errorf("cleaner errors should be non-fatal: %v", err)
	}
}

func TestCreateAfterMediaCoverUpdate_WriterFactoryErrorPropagates(t *testing.T) {
	t.Parallel()
	svc, disk, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabledErr: errors.New("registry boom")}
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	withSeriesFolder(t, files, disk, sr)
	_, err := svc.CreateAfterMediaCoverUpdate(t.Context(), sr)
	if err == nil {
		t.Errorf("expected error from WriterFactory.Enabled")
	}
}

func TestCreateAfterSeriesScan_FullPipeline(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesMeta:   metadata.NewMetadataFileResult("tvshow.nfo", "<x/>"),
		seriesImgs:   []metadata.ImageFileResult{metadata.NewImageFileResult("poster.jpg", "https://cdn/p.jpg")},
		seasonImgs:   map[int][]metadata.ImageFileResult{1: {metadata.NewImageFileResult("Season 1/poster.jpg", "https://cdn/s1.jpg")}},
		episodeMeta:  metadata.NewMetadataFileResult("Season 1/S01E02.nfo", "<ep/>"),
		episodeImgs:  []metadata.ImageFileResult{metadata.NewImageFileResult("Season 1/S01E02-thumb.jpg", "https://cdn/thumb.jpg")},
	}
	seasons := &fakeSeasons{seasons: []metadata.Season{{SeasonNumber: 1}}}
	svc, disk, http, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
		d.Seasons = seasons
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	withSeriesFolder(t, files, disk, sr)

	rows, err := svc.CreateAfterSeriesScan(t.Context(), sr, []episodefiles.File{{ID: 42, SeasonNumber: 1, RelativePath: "Season 1/S01E02.mkv"}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	// 1 series meta + 1 series image + 1 season image + 1 episode meta + 1 episode image = 5.
	if len(rows) != 5 {
		t.Errorf("expected 5 rows, got %d: %+v", len(rows), rows)
	}
	// 3 image downloads (series poster + season poster + episode thumb).
	if len(http.downloads) != 3 {
		t.Errorf("expected 3 downloads, got %+v", http.downloads)
	}
	// Series + episode metadata bodies written.
	if _, ok := disk.files[filepath.Join(sr.Path, "tvshow.nfo")]; !ok {
		t.Errorf("series metadata not written")
	}
	if _, ok := disk.files[filepath.Join(sr.Path, "Season 1/S01E02.nfo")]; !ok {
		t.Errorf("episode metadata not written")
	}
}

func TestCreateAfterEpisodesImported_OnlyEmitsSeriesMetadata(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesMeta:   metadata.NewMetadataFileResult("tvshow.nfo", "<x/>"),
		// Images / episode emitters intentionally set; should NOT run.
		seriesImgs:  []metadata.ImageFileResult{metadata.NewImageFileResult("poster.jpg", "https://cdn/p.jpg")},
		episodeMeta: metadata.NewMetadataFileResult("S01E02.nfo", "<ep/>"),
	}
	svc, disk, http, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	withSeriesFolder(t, files, disk, sr)

	rows, err := svc.CreateAfterEpisodesImported(t.Context(), sr)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row (series metadata only), got %d", len(rows))
	}
	if len(http.downloads) != 0 {
		t.Errorf("no images should be downloaded: %+v", http.downloads)
	}
}

func TestCreateAfterEpisodeImport_NoCleanerNoFolderCheck(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		episodeMeta:  metadata.NewMetadataFileResult("Season 1/S01E02.nfo", "<ep/>"),
	}
	cleaner := &fakeCleaner{}
	svc, _, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
		d.Cleaner = cleaner
	})
	// Note: series folder NOT registered — but CreateAfterEpisodeImport
	// per C# `:143-156` skips both the cleaner + folder-exists check.
	sr := &series.Series{ID: 7, Path: "/tv/Show"}

	rows, err := svc.CreateAfterEpisodeImport(t.Context(), sr, &episodefiles.File{ID: 42, SeasonNumber: 1, RelativePath: "Season 1/S01E02.mkv"})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
	if len(cleaner.calls) != 0 {
		t.Errorf("cleaner should NOT have run per C# parity: %+v", cleaner.calls)
	}
	// FilesBySeries should NOT be called either — C# passes a new empty list.
	if len(files.upserts) != 1 {
		t.Errorf("expected 1 upsert, got %+v", files.upserts)
	}
}

func TestCreateAfterEpisodeFolder_BothEmpty_NoOp(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{consumerImpl: "XbmcMetadata", seriesMeta: metadata.NewMetadataFileResult("tvshow.nfo", "<x/>")}
	svc, _, _, _, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	rows, err := svc.CreateAfterEpisodeFolder(t.Context(), sr, "", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if rows != nil {
		t.Errorf("both-empty should early-return nil: %+v", rows)
	}
}

func TestCreateAfterEpisodeFolder_SeriesFolderOnly(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesMeta:   metadata.NewMetadataFileResult("tvshow.nfo", "<x/>"),
		seriesImgs:   []metadata.ImageFileResult{metadata.NewImageFileResult("poster.jpg", "https://cdn/p.jpg")},
		seasonImgs:   map[int][]metadata.ImageFileResult{1: {metadata.NewImageFileResult("Season 1/poster.jpg", "https://cdn/s1.jpg")}},
	}
	seasons := &fakeSeasons{seasons: []metadata.Season{{SeasonNumber: 1}}}
	svc, _, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
		d.Seasons = seasons
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	files.bySeries = map[int][]metadatafiles.MetadataFile{7: nil}

	rows, err := svc.CreateAfterEpisodeFolder(t.Context(), sr, "Show", "")
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Series metadata + series image. NO season image because seasonFolder empty.
	if len(rows) != 2 {
		t.Errorf("expected 2 rows (series meta + series image), got %d", len(rows))
	}
}

func TestCreateAfterEpisodeFolder_SeasonFolderOnly(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesMeta:   metadata.NewMetadataFileResult("tvshow.nfo", "<x/>"),
		seriesImgs:   []metadata.ImageFileResult{metadata.NewImageFileResult("poster.jpg", "https://cdn/p.jpg")},
		seasonImgs:   map[int][]metadata.ImageFileResult{1: {metadata.NewImageFileResult("Season 1/poster.jpg", "https://cdn/s1.jpg")}},
	}
	seasons := &fakeSeasons{seasons: []metadata.Season{{SeasonNumber: 1}}}
	svc, _, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: []metadata.Provider{provider}}
		d.Seasons = seasons
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	files.bySeries = map[int][]metadatafiles.MetadataFile{7: nil}

	rows, err := svc.CreateAfterEpisodeFolder(t.Context(), sr, "", "Season 1")
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Only season image emitted.
	if len(rows) != 1 {
		t.Errorf("expected 1 row (season image only), got %d", len(rows))
	}
}

func TestMoveFilesAfterRename_RelocatesMismatchedPaths(t *testing.T) {
	t.Parallel()
	// A writer whose GetFilenameAfterMove rewrites the stored path.
	provider := &renameProvider{fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"}, newName: "/tv/Show/Season 1/S01E02.nfo"}
	cleaner := &fakeCleaner{}
	svc, disk, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{all: []metadata.Provider{provider}}
		d.Cleaner = cleaner
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	// Existing row at the OLD path; place a file there so the move is visible.
	files.bySeries = map[int][]metadatafiles.MetadataFile{7: {{
		ID: 100, Consumer: "XbmcMetadata",
		Type:          int(metadata.MetadataTypeEpisodeMetadata),
		EpisodeFileID: intPtr(42),
		RelativePath:  "Season 1/S01E02-old.nfo",
	}}}
	disk.files["/tv/Show/Season 1/S01E02-old.nfo"] = "old contents"

	rows, err := svc.MoveFilesAfterRename(t.Context(), sr, []episodefiles.File{{ID: 42, RelativePath: "Season 1/S01E02.mkv"}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 moved row, got %d: %+v", len(rows), rows)
	}
	if rows[0].RelativePath != "Season 1/S01E02.nfo" {
		t.Errorf("RelativePath after move = %q", rows[0].RelativePath)
	}
	// Source file should be gone; target file should exist.
	if disk.FileExists("/tv/Show/Season 1/S01E02-old.nfo") {
		t.Errorf("old file should have been moved away")
	}
	if !disk.FileExists("/tv/Show/Season 1/S01E02.nfo") {
		t.Errorf("new file should exist after move")
	}
}

func TestMoveFilesAfterRename_MoveErrorIsNonFatal(t *testing.T) {
	t.Parallel()
	provider := &renameProvider{fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"}, newName: "/tv/Show/Season 1/S01E02.nfo"}
	svc, disk, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{all: []metadata.Provider{provider}}
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	files.bySeries = map[int][]metadatafiles.MetadataFile{7: {{
		ID: 100, Consumer: "XbmcMetadata",
		Type:          int(metadata.MetadataTypeEpisodeMetadata),
		EpisodeFileID: intPtr(42),
		RelativePath:  "Season 1/S01E02-old.nfo",
	}}}
	disk.files["/tv/Show/Season 1/S01E02-old.nfo"] = "old"
	disk.moveErr["/tv/Show/Season 1/S01E02-old.nfo"] = errors.New("permission denied")

	rows, err := svc.MoveFilesAfterRename(t.Context(), sr, []episodefiles.File{{ID: 42, RelativePath: "Season 1/S01E02.mkv"}})
	if err != nil {
		t.Errorf("move errors should be non-fatal: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("failed move should not produce a moved row: %+v", rows)
	}
}

func TestMoveFilesAfterRename_UsesAllNotEnabled(t *testing.T) {
	t.Parallel()
	// `all` populated but `enabled` empty — verifies the renamer
	// path uses the broader writer set per C# `:198`.
	provider := &renameProvider{fakeProvider: fakeProvider{consumerImpl: "XbmcMetadata"}, newName: "/tv/Show/Season 1/S01E02.nfo"}
	svc, disk, _, files, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Writers = &fakeWriters{enabled: nil, all: []metadata.Provider{provider}}
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	files.bySeries = map[int][]metadatafiles.MetadataFile{7: {{
		ID: 100, Consumer: "XbmcMetadata",
		Type:          int(metadata.MetadataTypeEpisodeMetadata),
		EpisodeFileID: intPtr(42),
		RelativePath:  "Season 1/S01E02-old.nfo",
	}}}
	disk.files["/tv/Show/Season 1/S01E02-old.nfo"] = "old"
	rows, err := svc.MoveFilesAfterRename(t.Context(), sr, []episodefiles.File{{ID: 42, RelativePath: "Season 1/S01E02.mkv"}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 {
		t.Errorf("disabled-but-registered writer should still move files: %+v", rows)
	}
}

func TestCanImportFile_AlwaysFalse(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil)
	if svc.CanImportFile(nil, &episodefiles.File{}, "/tv/x.mkv", ".mkv", false) {
		t.Errorf("CanImportFile must always return false per C# parity")
	}
}

func TestImportFiles_AlwaysEmpty(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil)
	rows, err := svc.ImportFiles(t.Context(), nil, &episodefiles.File{}, []string{"/tv/a", "/tv/b"}, false)
	if err != nil {
		t.Errorf("ImportFiles error: %v", err)
	}
	if rows != nil {
		t.Errorf("ImportFiles must always return empty per C# parity: %+v", rows)
	}
}

// renameProvider is a fakeProvider variant that overrides
// GetFilenameAfterMove to return a hard-coded path. Used by the
// MoveFilesAfterRename tests since the base fakeProvider returns
// the empty string.
type renameProvider struct {
	fakeProvider
	newName string
}

func newRenameProvider(impl, name string) *renameProvider {
	return &renameProvider{fakeProvider: fakeProvider{consumerImpl: impl}, newName: name}
}

func (p *renameProvider) GetFilenameAfterMove(_ *series.Series, _ *episodefiles.File, _ *metadatafiles.MetadataFile) string {
	return p.newName
}

// silence unused complaints for renameProvider's constructor (kept
// for documentation; the struct literal is what tests use today).
var _ = newRenameProvider
