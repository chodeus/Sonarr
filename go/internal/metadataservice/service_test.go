package metadataservice_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/metadataservice"
	"sonarr.tv/sonarr/internal/series"
)

// fakeDisk satisfies metadataservice.DiskWriter with an in-memory map.
type fakeDisk struct {
	files map[string]string
	// writeErr / copyErr injectable per-path.
	writeErr map[string]error
	copyErr  map[string]error
}

func newFakeDisk() *fakeDisk {
	return &fakeDisk{
		files:    map[string]string{},
		writeErr: map[string]error{},
		copyErr:  map[string]error{},
	}
}

func (f *fakeDisk) WriteAllText(path, contents string) error {
	if err, ok := f.writeErr[path]; ok {
		return err
	}
	f.files[path] = contents
	return nil
}

func (f *fakeDisk) FileExists(path string) bool {
	_, ok := f.files[path]
	return ok
}

func (f *fakeDisk) CopyFile(src, dst string) error {
	if err, ok := f.copyErr[src]; ok {
		return err
	}
	content, ok := f.files[src]
	if !ok {
		return fmt.Errorf("copy source not found: %s", src)
	}
	f.files[dst] = content
	return nil
}

// fakeHTTP records downloads in an in-memory map.
type fakeHTTP struct {
	downloads map[string]string // url → dst
	err       error
}

func (f *fakeHTTP) DownloadFile(_ context.Context, url, dst string) error {
	if f.err != nil {
		return f.err
	}
	if f.downloads == nil {
		f.downloads = map[string]string{}
	}
	f.downloads[url] = dst
	return nil
}

// fakeFiles records persistence calls.
type fakeFiles struct {
	inserts   []metadatafiles.MetadataFile
	updates   []metadatafiles.MetadataFile
	deleted   []int
	insertErr error
	updateErr error
	deleteErr error
}

func (f *fakeFiles) Insert(_ context.Context, m *metadatafiles.MetadataFile) (int, error) {
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	id := len(f.inserts) + 1000
	m.ID = id
	f.inserts = append(f.inserts, *m)
	return id, nil
}

func (f *fakeFiles) Update(_ context.Context, m *metadatafiles.MetadataFile) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, *m)
	return nil
}

func (f *fakeFiles) Delete(_ context.Context, id int) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, id)
	return nil
}

// fakeAttrs no-op + injectable err.
type fakeAttrs struct {
	calls []string
	err   error
}

func (f *fakeAttrs) SetFilePermissions(path string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, path)
	return nil
}

// fakeBin records deletes.
type fakeBin struct {
	deletes []string
	err     error
}

func (f *fakeBin) DeleteFile(path, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.deletes = append(f.deletes, path)
	return nil
}

// fakeRenamer is a no-op.
type fakeRenamer struct {
	calls []string
	err   error
}

func (f *fakeRenamer) RenameOtherExtraFile(_ *series.Series, fullPath string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, fullPath)
	return nil
}

// fakeTransfer records moves.
type fakeTransfer struct {
	moves []struct{ src, dst string }
	err   error
}

func (f *fakeTransfer) TransferFile(src, dst string, _ metadataservice.TransferModeT) error {
	if f.err != nil {
		return f.err
	}
	f.moves = append(f.moves, struct{ src, dst string }{src, dst})
	return nil
}

// fakeSeasons stub.
type fakeSeasons struct {
	seasons []metadata.Season
	err     error
}

func (f *fakeSeasons) SeasonsForSeries(_ *series.Series) ([]metadata.Season, error) {
	return f.seasons, f.err
}

// fakeProvider implements metadata.Provider.
type fakeProvider struct {
	consumerImpl string
	// programmed outputs:
	seriesMeta  metadata.MetadataFileResult
	episodeMeta metadata.MetadataFileResult
	seriesImgs  []metadata.ImageFileResult
	seasonImgs  map[int][]metadata.ImageFileResult
	episodeImgs []metadata.ImageFileResult
	// programmed errors:
	seriesMetaErr  error
	episodeMetaErr error
	seriesImgErr   error
}

func (p *fakeProvider) Name() string { return p.consumerImpl }
func (p *fakeProvider) Definition() *metadata.Metadata {
	return &metadata.Metadata{Implementation: p.consumerImpl, ConfigContract: p.consumerImpl + "Settings"}
}
func (p *fakeProvider) GetFilenameAfterMove(_ *series.Series, _ *episodefiles.File, _ *metadatafiles.MetadataFile) string {
	return ""
}
func (p *fakeProvider) FindMetadataFile(_ *series.Series, _ string) (*metadatafiles.MetadataFile, error) {
	return nil, nil
}
func (p *fakeProvider) SeriesMetadata(_ context.Context, _ *series.Series, _ metadata.SeriesMetadataReason) (metadata.MetadataFileResult, error) {
	return p.seriesMeta, p.seriesMetaErr
}
func (p *fakeProvider) EpisodeMetadata(_ context.Context, _ *series.Series, _ *episodefiles.File) (metadata.MetadataFileResult, error) {
	return p.episodeMeta, p.episodeMetaErr
}
func (p *fakeProvider) SeriesImages(_ context.Context, _ *series.Series) ([]metadata.ImageFileResult, error) {
	return p.seriesImgs, p.seriesImgErr
}
func (p *fakeProvider) SeasonImages(_ context.Context, _ *series.Series, season metadata.Season) ([]metadata.ImageFileResult, error) {
	return p.seasonImgs[season.SeasonNumber], nil
}
func (p *fakeProvider) EpisodeImages(_ context.Context, _ *series.Series, _ *episodefiles.File) ([]metadata.ImageFileResult, error) {
	return p.episodeImgs, nil
}
func (p *fakeProvider) Test(_ context.Context) ([]metadata.TestFailure, error) { return nil, nil }
func (p *fakeProvider) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

func newService(t *testing.T, extra func(deps *metadataservice.Deps)) (*metadataservice.Service, *fakeDisk, *fakeHTTP, *fakeFiles, *fakeAttrs, *fakeBin, *fakeRenamer, *fakeTransfer) {
	t.Helper()
	disk := newFakeDisk()
	http := &fakeHTTP{}
	files := &fakeFiles{}
	attrs := &fakeAttrs{}
	bin := &fakeBin{}
	renamer := &fakeRenamer{}
	transfer := &fakeTransfer{}
	deps := metadataservice.Deps{
		Disk:         disk,
		HTTP:         http,
		Files:        files,
		Attrs:        attrs,
		Bin:          bin,
		OtherRenamer: renamer,
		Transfer:     transfer,
	}
	if extra != nil {
		extra(&deps)
	}
	svc, err := metadataservice.NewService(deps)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, disk, http, files, attrs, bin, renamer, transfer
}

func TestNewService_RejectsNilRequiredDeps(t *testing.T) {
	t.Parallel()
	full := metadataservice.Deps{
		Disk:     newFakeDisk(),
		HTTP:     &fakeHTTP{},
		Files:    &fakeFiles{},
		Attrs:    &fakeAttrs{},
		Bin:      &fakeBin{},
		Transfer: &fakeTransfer{},
	}
	if _, err := metadataservice.NewService(full); err != nil {
		t.Fatalf("full deps should succeed: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(d *metadataservice.Deps)
	}{
		{"nil Disk", func(d *metadataservice.Deps) { d.Disk = nil }},
		{"nil HTTP", func(d *metadataservice.Deps) { d.HTTP = nil }},
		{"nil Files", func(d *metadataservice.Deps) { d.Files = nil }},
		{"nil Attrs", func(d *metadataservice.Deps) { d.Attrs = nil }},
		{"nil Bin", func(d *metadataservice.Deps) { d.Bin = nil }},
		{"nil Transfer", func(d *metadataservice.Deps) { d.Transfer = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := full
			tc.mutate(&d)
			if _, err := metadataservice.NewService(d); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestNewService_NilOtherRenamerOK(t *testing.T) {
	t.Parallel()
	deps := metadataservice.Deps{
		Disk: newFakeDisk(), HTTP: &fakeHTTP{}, Files: &fakeFiles{},
		Attrs: &fakeAttrs{}, Bin: &fakeBin{}, Transfer: &fakeTransfer{},
		OtherRenamer: nil,
	}
	if _, err := metadataservice.NewService(deps); err != nil {
		t.Errorf("nil OtherRenamer should be tolerated: %v", err)
	}
}

func TestGetMetadataFilesForConsumer(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil)
	provider := &fakeProvider{consumerImpl: "XbmcMetadata"}
	all := []metadatafiles.MetadataFile{
		{ID: 1, Consumer: "XbmcMetadata"},
		{ID: 2, Consumer: "RoksboxMetadata"},
		{ID: 3, Consumer: "XbmcMetadata"},
	}
	got := svc.GetMetadataFilesForConsumer(provider, all)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Errorf("filter: %+v", got)
	}
}

func TestGetMetadataFilesForConsumer_NilConsumer(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil)
	if got := svc.GetMetadataFilesForConsumer(nil, []metadatafiles.MetadataFile{{ID: 1}}); got != nil {
		t.Errorf("nil consumer: %+v", got)
	}
}

func TestGetMetadataFile_NoMatch(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil)
	got, err := svc.GetMetadataFile(t.Context(), &series.Series{}, []metadatafiles.MetadataFile{}, func(metadatafiles.MetadataFile) bool { return true })
	if err != nil || got != nil {
		t.Errorf("no match: got=%+v err=%v", got, err)
	}
}

func TestGetMetadataFile_DeduplicatesExtras(t *testing.T) {
	t.Parallel()
	svc, _, _, files, _, bin, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	existing := []metadatafiles.MetadataFile{
		{ID: 100, Type: int(metadata.MetadataTypeSeriesMetadata), RelativePath: "tvshow.nfo"},
		{ID: 101, Type: int(metadata.MetadataTypeSeriesMetadata), RelativePath: "tvshow.nfo"}, // dup
		{ID: 102, Type: int(metadata.MetadataTypeSeriesMetadata), RelativePath: "tvshow.nfo"}, // dup
	}
	got, err := svc.GetMetadataFile(t.Context(), sr, existing, func(f metadatafiles.MetadataFile) bool {
		return metadata.MetadataType(f.Type) == metadata.MetadataTypeSeriesMetadata
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got == nil || got.ID != 100 {
		t.Errorf("first match: %+v", got)
	}
	// Duplicates (ID 101, 102) should be deleted from both the store and recyclebin.
	if len(files.deleted) != 2 || files.deleted[0] != 101 || files.deleted[1] != 102 {
		t.Errorf("Store deletes: %+v, want [101, 102]", files.deleted)
	}
	if len(bin.deletes) != 2 {
		t.Errorf("Recyclebin deletes: %+v", bin.deletes)
	}
}

func TestGetMetadataFile_DupCleanupErrorsAreNonFatal(t *testing.T) {
	t.Parallel()
	svc, _, _, files, _, bin, _, _ := newService(t, nil)
	// Recyclebin errors are logged + ignored.
	bin.err = errors.New("recyclebin offline")
	files.deleteErr = errors.New("db down")
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	existing := []metadatafiles.MetadataFile{
		{ID: 100, Type: int(metadata.MetadataTypeSeriesMetadata)},
		{ID: 101, Type: int(metadata.MetadataTypeSeriesMetadata)},
	}
	got, err := svc.GetMetadataFile(t.Context(), sr, existing, func(f metadatafiles.MetadataFile) bool {
		return metadata.MetadataType(f.Type) == metadata.MetadataTypeSeriesMetadata
	})
	if err != nil {
		t.Errorf("dup-cleanup errors should be non-fatal: %v", err)
	}
	if got == nil || got.ID != 100 {
		t.Errorf("primary match should still return: %+v", got)
	}
}

func TestProcessSeriesMetadata_HappyPath_NewFile(t *testing.T) {
	t.Parallel()
	svc, disk, _, _, attrs, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesMeta:   metadata.NewMetadataFileResult("tvshow.nfo", "<tvshow><title>X</title></tvshow>"),
	}
	mf, err := svc.ProcessSeriesMetadata(t.Context(), provider, sr, nil, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if mf == nil {
		t.Fatal("expected MetadataFile row, got nil")
	}
	if mf.Consumer != "XbmcMetadata" || mf.RelativePath != "tvshow.nfo" {
		t.Errorf("row: %+v", mf)
	}
	if mf.Hash == "" {
		t.Errorf("Hash should be populated")
	}
	wantPath := filepath.Join(sr.Path, "tvshow.nfo")
	if disk.files[wantPath] != "<tvshow><title>X</title></tvshow>" {
		t.Errorf("body not written to %q: %q", wantPath, disk.files[wantPath])
	}
	if len(attrs.calls) != 1 || attrs.calls[0] != wantPath {
		t.Errorf("SetFilePermissions: %+v", attrs.calls)
	}
}

func TestProcessSeriesMetadata_HashUnchanged_PathUnchanged_ReturnsNil(t *testing.T) {
	t.Parallel()
	svc, disk, _, _, _, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	body := "<tvshow/>"
	// Compute the hash the service will see — sha256 of the body.
	// Build the existing row with that hash + same path.
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesMeta:   metadata.NewMetadataFileResult("tvshow.nfo", body),
	}
	// First call to populate the cached hash via writing a new row.
	mf, err := svc.ProcessSeriesMetadata(t.Context(), provider, sr, nil, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	existing := []metadatafiles.MetadataFile{*mf}
	// Reset the disk state to ensure no second write.
	delete(disk.files, filepath.Join(sr.Path, "tvshow.nfo"))

	got, err := svc.ProcessSeriesMetadata(t.Context(), provider, sr, existing, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != nil {
		t.Errorf("hash+path unchanged should return nil, got %+v", got)
	}
	if disk.FileExists(filepath.Join(sr.Path, "tvshow.nfo")) {
		t.Errorf("should not have written again")
	}
}

func TestProcessSeriesMetadata_HashUnchanged_PathChanged_UpdatesPathOnly(t *testing.T) {
	t.Parallel()
	svc, disk, _, _, _, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	body := "<tvshow/>"
	provider := &fakeProvider{consumerImpl: "XbmcMetadata", seriesMeta: metadata.NewMetadataFileResult("tvshow.nfo", body)}
	mf, err := svc.ProcessSeriesMetadata(t.Context(), provider, sr, nil, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Existing has same hash but different RelativePath.
	existing := []metadatafiles.MetadataFile{{ID: 100, Type: int(metadata.MetadataTypeSeriesMetadata), Hash: mf.Hash, RelativePath: "old.nfo"}}
	delete(disk.files, filepath.Join(sr.Path, "tvshow.nfo"))

	got, err := svc.ProcessSeriesMetadata(t.Context(), provider, sr, existing, metadata.SeriesMetadataReasonScan)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got == nil || got.RelativePath != "tvshow.nfo" {
		t.Errorf("expected path update, got %+v", got)
	}
	if disk.FileExists(filepath.Join(sr.Path, "tvshow.nfo")) {
		t.Errorf("should NOT have written body (hash unchanged)")
	}
}

func TestProcessSeriesMetadata_WriterSkipped(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil)
	provider := &fakeProvider{consumerImpl: "XbmcMetadata"} // empty result → skip
	got, err := svc.ProcessSeriesMetadata(t.Context(), provider, &series.Series{}, nil, metadata.SeriesMetadataReasonScan)
	if err != nil || got != nil {
		t.Errorf("writer-skip: got=%+v err=%v", got, err)
	}
}

func TestProcessSeriesMetadata_WriterErrorPropagates(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil)
	provider := &fakeProvider{consumerImpl: "XbmcMetadata", seriesMetaErr: errors.New("boom")}
	_, err := svc.ProcessSeriesMetadata(t.Context(), provider, &series.Series{}, nil, metadata.SeriesMetadataReasonScan)
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestProcessEpisodeMetadata_HappyPath_NewFile(t *testing.T) {
	t.Parallel()
	svc, disk, _, _, _, _, renamer, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	ef := &episodefiles.File{ID: 42, SeasonNumber: 1}
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		episodeMeta:  metadata.NewMetadataFileResult("Season 1/S01E02.nfo", "<episodedetails/>"),
	}
	mf, err := svc.ProcessEpisodeMetadata(t.Context(), provider, sr, ef, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if mf == nil {
		t.Fatal("expected row")
	}
	if mf.EpisodeFileID == nil || *mf.EpisodeFileID != 42 {
		t.Errorf("EpisodeFileID: %v", mf.EpisodeFileID)
	}
	if mf.SeasonNumber == nil || *mf.SeasonNumber != 1 {
		t.Errorf("SeasonNumber: %v", mf.SeasonNumber)
	}
	wantPath := filepath.Join(sr.Path, "Season 1/S01E02.nfo")
	if _, ok := disk.files[wantPath]; !ok {
		t.Errorf("body not written to %q", wantPath)
	}
	if len(renamer.calls) != 1 || renamer.calls[0] != wantPath {
		t.Errorf("renamer not called: %+v", renamer.calls)
	}
}

func TestProcessEpisodeMetadata_RelocatesWhenPathChanged(t *testing.T) {
	t.Parallel()
	svc, disk, _, _, _, _, _, transfer := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	ef := &episodefiles.File{ID: 42, SeasonNumber: 1}
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		episodeMeta:  metadata.NewMetadataFileResult("Season 1/S01E02-renamed.nfo", "<episodedetails/>"),
	}
	// Existing row at the OLD path. Place a "file" there so the move is visible.
	existing := []metadatafiles.MetadataFile{{
		ID: 100, Type: int(metadata.MetadataTypeEpisodeMetadata),
		EpisodeFileID: intPtr(42),
		RelativePath:  "Season 1/S01E02-old.nfo",
	}}
	disk.files[filepath.Join(sr.Path, "Season 1/S01E02-old.nfo")] = "old contents"

	mf, err := svc.ProcessEpisodeMetadata(t.Context(), provider, sr, ef, existing)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if mf == nil || mf.RelativePath != "Season 1/S01E02-renamed.nfo" {
		t.Errorf("expected renamed path: %+v", mf)
	}
	if len(transfer.moves) != 1 {
		t.Errorf("expected 1 move, got %+v", transfer.moves)
	}
}

func TestProcessEpisodeMetadata_HashUnchanged_NoWrite(t *testing.T) {
	t.Parallel()
	svc, disk, _, _, _, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	ef := &episodefiles.File{ID: 42, SeasonNumber: 1}
	provider := &fakeProvider{consumerImpl: "XbmcMetadata", episodeMeta: metadata.NewMetadataFileResult("Season 1/S01E02.nfo", "<x/>")}
	// First call to learn the hash.
	first, _ := svc.ProcessEpisodeMetadata(t.Context(), provider, sr, ef, nil)
	delete(disk.files, filepath.Join(sr.Path, "Season 1/S01E02.nfo"))

	existing := []metadatafiles.MetadataFile{*first}
	got, err := svc.ProcessEpisodeMetadata(t.Context(), provider, sr, ef, existing)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != nil {
		t.Errorf("hash unchanged should return nil, got %+v", got)
	}
}

func TestProcessSeriesImages_HappyPath(t *testing.T) {
	t.Parallel()
	svc, disk, http, _, _, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesImgs: []metadata.ImageFileResult{
			metadata.NewImageFileResult("poster.jpg", "https://cdn/poster.jpg"),
			metadata.NewImageFileResult("banner.jpg", "https://cdn/banner.jpg"),
		},
	}
	rows, err := svc.ProcessSeriesImages(t.Context(), provider, sr, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if len(http.downloads) != 2 {
		t.Errorf("expected 2 downloads, got %+v", http.downloads)
	}
	_ = disk // not asserting disk here — http.downloads carries the test signal
}

func TestProcessSeriesImages_SkipsExistingFiles(t *testing.T) {
	t.Parallel()
	svc, disk, http, _, _, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	disk.files[filepath.Join(sr.Path, "poster.jpg")] = "already there"
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seriesImgs:   []metadata.ImageFileResult{metadata.NewImageFileResult("poster.jpg", "https://cdn/poster.jpg")},
	}
	rows, err := svc.ProcessSeriesImages(t.Context(), provider, sr, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 0 {
		t.Errorf("existing file should be skipped, got rows: %+v", rows)
	}
	if len(http.downloads) != 0 {
		t.Errorf("should not have downloaded: %+v", http.downloads)
	}
}

func TestProcessSeasonImages_FanOutAcrossSeasons(t *testing.T) {
	t.Parallel()
	seasons := &fakeSeasons{seasons: []metadata.Season{
		{SeasonNumber: 1},
		{SeasonNumber: 2},
	}}
	svc, _, _, _, _, _, _, _ := newService(t, func(d *metadataservice.Deps) {
		d.Seasons = seasons
	})
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		seasonImgs: map[int][]metadata.ImageFileResult{
			1: {metadata.NewImageFileResult("Season 1/poster.jpg", "https://cdn/s1.jpg")},
			2: {metadata.NewImageFileResult("Season 2/poster.jpg", "https://cdn/s2.jpg")},
		},
	}
	rows, err := svc.ProcessSeasonImages(t.Context(), provider, sr, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 2 {
		t.Errorf("rows = %+v", rows)
	}
	// Verify SeasonNumber populated on each row.
	for _, r := range rows {
		if r.SeasonNumber == nil {
			t.Errorf("SeasonNumber should be set: %+v", r)
		}
	}
}

func TestProcessSeasonImages_NilSeasonsListerNoCrash(t *testing.T) {
	t.Parallel()
	svc, _, _, _, _, _, _, _ := newService(t, nil) // Seasons nil
	rows, err := svc.ProcessSeasonImages(t.Context(), &fakeProvider{consumerImpl: "X"}, &series.Series{}, nil)
	if err != nil || rows != nil {
		t.Errorf("nil seasons: %+v / %v", rows, err)
	}
}

func TestProcessEpisodeImages_HappyPath_NewFile(t *testing.T) {
	t.Parallel()
	svc, _, http, _, _, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	ef := &episodefiles.File{ID: 42, SeasonNumber: 1}
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		episodeImgs:  []metadata.ImageFileResult{metadata.NewImageFileResult("Season 1/S01E02-thumb.jpg", "https://cdn/thumb.jpg")},
	}
	rows, err := svc.ProcessEpisodeImages(t.Context(), provider, sr, ef, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].EpisodeFileID == nil || *rows[0].EpisodeFileID != 42 {
		t.Errorf("EpisodeFileID: %v", rows[0].EpisodeFileID)
	}
	if len(http.downloads) != 1 {
		t.Errorf("expected 1 download: %+v", http.downloads)
	}
}

func TestProcessEpisodeImages_MovesExistingWhenPathChanged(t *testing.T) {
	t.Parallel()
	svc, disk, http, _, _, _, _, transfer := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	ef := &episodefiles.File{ID: 42, SeasonNumber: 1}
	provider := &fakeProvider{
		consumerImpl: "XbmcMetadata",
		episodeImgs:  []metadata.ImageFileResult{metadata.NewImageFileResult("Season 1/S01E02-renamed-thumb.jpg", "https://cdn/x.jpg")},
	}
	existing := []metadatafiles.MetadataFile{{
		ID:            100,
		Type:          int(metadata.MetadataTypeEpisodeImage),
		EpisodeFileID: intPtr(42),
		RelativePath:  "Season 1/S01E02-old-thumb.jpg",
	}}
	disk.files[filepath.Join(sr.Path, "Season 1/S01E02-old-thumb.jpg")] = "old"
	rows, err := svc.ProcessEpisodeImages(t.Context(), provider, sr, ef, existing)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(rows) != 1 || rows[0].RelativePath != "Season 1/S01E02-renamed-thumb.jpg" {
		t.Errorf("expected single moved row: %+v", rows)
	}
	if len(transfer.moves) != 1 {
		t.Errorf("expected 1 move, got %+v", transfer.moves)
	}
	if len(http.downloads) != 0 {
		t.Errorf("should NOT have re-downloaded after move: %+v", http.downloads)
	}
}

func TestDownloadImage_HTTPPath(t *testing.T) {
	t.Parallel()
	svc, _, http, _, attrs, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	err := svc.DownloadImage(t.Context(), sr, metadata.NewImageFileResult("poster.jpg", "https://cdn/p.jpg"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(http.downloads) != 1 {
		t.Errorf("expected http download: %+v", http.downloads)
	}
	if len(attrs.calls) != 1 {
		t.Errorf("expected SetFilePermissions: %+v", attrs.calls)
	}
}

func TestDownloadImage_LocalFilePath(t *testing.T) {
	t.Parallel()
	svc, disk, http, _, attrs, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	disk.files["/var/cover.jpg"] = "cached bytes"
	err := svc.DownloadImage(t.Context(), sr, metadata.NewImageFileResult("poster.jpg", "/var/cover.jpg"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(http.downloads) != 0 {
		t.Errorf("should NOT have hit HTTP for local path: %+v", http.downloads)
	}
	if _, ok := disk.files[filepath.Join(sr.Path, "poster.jpg")]; !ok {
		t.Errorf("local copy not made")
	}
	if len(attrs.calls) != 1 {
		t.Errorf("expected SetFilePermissions: %+v", attrs.calls)
	}
}

func TestDownloadImage_NeitherHTTPNorLocalSilent(t *testing.T) {
	t.Parallel()
	svc, _, http, _, attrs, _, _, _ := newService(t, nil)
	sr := &series.Series{ID: 7, Path: "/tv/Show"}
	// URL is "" → not http, not existing file → silent skip per C# `:483-486`.
	err := svc.DownloadImage(t.Context(), sr, metadata.NewImageFileResult("poster.jpg", ""))
	if err != nil {
		t.Errorf("silent-skip path: %v", err)
	}
	if len(http.downloads) != 0 || len(attrs.calls) != 0 {
		t.Errorf("nothing should have been done")
	}
}

func TestSaveMetadataFile_WritesAndChmods(t *testing.T) {
	t.Parallel()
	svc, disk, _, _, attrs, _, _, _ := newService(t, nil)
	err := svc.SaveMetadataFile("/tv/Show/tvshow.nfo", "<x/>")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if disk.files["/tv/Show/tvshow.nfo"] != "<x/>" {
		t.Errorf("body not written: %q", disk.files["/tv/Show/tvshow.nfo"])
	}
	if len(attrs.calls) != 1 || attrs.calls[0] != "/tv/Show/tvshow.nfo" {
		t.Errorf("chmod: %+v", attrs.calls)
	}
}

func TestSaveMetadataFile_WriteErrorPropagates(t *testing.T) {
	t.Parallel()
	svc, disk, _, _, _, _, _, _ := newService(t, nil)
	disk.writeErr["/tv/Show/x.nfo"] = errors.New("disk full")
	err := svc.SaveMetadataFile("/tv/Show/x.nfo", "<x/>")
	if err == nil {
		t.Errorf("expected error")
	}
}

func intPtr(v int) *int { return &v }
