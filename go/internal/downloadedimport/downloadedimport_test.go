package downloadedimport_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// --- stubs ---------------------------------------------------------

type stubDisk struct {
	folders     map[string]bool
	files       map[string]bool
	dirs        map[string][]string
	allFiles    map[string][]string
	sizes       map[string]int64
	getFilesErr error
	sizeErr     map[string]error
	locked      map[string]bool
	deleted     []string
}

func (s *stubDisk) FolderExists(path string) bool { return s.folders[path] }
func (s *stubDisk) FileExists(path string) bool   { return s.files[path] }
func (s *stubDisk) GetDirectories(path string) ([]string, error) {
	return s.dirs[path], nil
}
func (s *stubDisk) GetFiles(path string, _ bool) ([]string, error) {
	if s.getFilesErr != nil {
		return nil, s.getFilesErr
	}
	return s.allFiles[path], nil
}
func (s *stubDisk) GetFileSize(path string) (int64, error) {
	if perr, ok := s.sizeErr[path]; ok {
		return 0, perr
	}
	return s.sizes[path], nil
}
func (s *stubDisk) IsFileLocked(path string) bool { return s.locked[path] }
func (s *stubDisk) DeleteFolder(path string, _ bool) error {
	s.deleted = append(s.deleted, path)
	return nil
}

type stubScanner struct {
	videos    map[string][]string
	filterRet func(string, []string) []string
}

func (s *stubScanner) GetVideoFiles(path string, _ bool) []string {
	return s.videos[path]
}
func (s *stubScanner) FilterPaths(rootPath string, paths []string) []string {
	if s.filterRet != nil {
		return s.filterRet(rootPath, paths)
	}
	return paths
}

type stubSeriesPaths struct{ exists map[string]bool }

func (s *stubSeriesPaths) SeriesPathExists(p string) bool { return s.exists[p] }

type stubParser struct{ resolveTo *series.Series }

func (s *stubParser) GetSeriesFromTitle(_ string) *series.Series { return s.resolveTo }

type stubDecider struct{}

func (stubDecider) GetImportDecisions(
	_ []string, _ *series.Series, _ *downloadclients.Item,
	_ *parser.ParsedEpisodeInfo, _ *parser.ParsedEpisodeInfo, _ bool,
) []*episodeimport.ImportDecision {
	return nil
}

type stubRunner struct{}

func (stubRunner) Import(
	_ []*episodeimport.ImportDecision, _ bool, _ *downloadclients.Item, _ episodeimport.ImportMode,
) []*episodeimport.ImportResult {
	return nil
}

type stubSample struct {
	verdict episodeimport.DetectSampleResult
	calls   int
}

func (s *stubSample) IsSample(_ *series.Series, _ string, _ bool) episodeimport.DetectSampleResult {
	s.calls++
	return s.verdict
}

type stubCfg struct{ rejected string }

func (s stubCfg) UserRejectedExtensions() string { return s.rejected }

func newSvc(t *testing.T, disk *stubDisk, scanner *stubScanner, sample *stubSample) *downloadedimport.Service {
	t.Helper()
	if scanner == nil {
		scanner = &stubScanner{}
	}
	if sample == nil {
		sample = &stubSample{verdict: episodeimport.DetectSampleSample}
	}
	svc := downloadedimport.NewService(
		disk,
		scanner,
		&stubSeriesPaths{},
		&stubParser{},
		stubDecider{},
		stubRunner{},
		sample,
		stubCfg{},
		slog.Default(),
	)
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	return svc
}

// --- NewService ----------------------------------------------------

func TestNewService_NilRequiredDeps(t *testing.T) {
	t.Parallel()
	if got := downloadedimport.NewService(
		nil, &stubScanner{}, &stubSeriesPaths{}, &stubParser{},
		stubDecider{}, stubRunner{}, &stubSample{}, stubCfg{}, nil,
	); got != nil {
		t.Errorf("NewService(nil disk) = %v, want nil", got)
	}
	if got := downloadedimport.NewService(
		&stubDisk{}, nil, &stubSeriesPaths{}, &stubParser{},
		stubDecider{}, stubRunner{}, &stubSample{}, stubCfg{}, nil,
	); got != nil {
		t.Errorf("NewService(nil scanner) = %v, want nil", got)
	}
}

// --- ProcessPath ---------------------------------------------------

func TestProcessPath_FolderRoute(t *testing.T) {
	t.Parallel()
	d := &stubDisk{folders: map[string]bool{"/drop/show": true}}
	svc := newSvc(t, d, nil, nil)
	got := svc.ProcessPath("/drop/show", episodeimport.ImportModeAuto, nil, nil)
	if got == nil {
		t.Fatal("ProcessPath returned nil — should always be non-nil slice")
	}
	// Folder route routes to processFolder which delegates to the
	// processFolderWithSeries stub when the parser returns nil.
	// Either way, the dispatcher returns a non-nil result list; the
	// exact contents are an implementation detail of the deferred
	// stubs.
}

func TestProcessPath_FileRoute(t *testing.T) {
	t.Parallel()
	d := &stubDisk{files: map[string]bool{"/drop/show.mkv": true}}
	svc := newSvc(t, d, nil, nil)
	got := svc.ProcessPath("/drop/show.mkv", episodeimport.ImportModeAuto, nil, nil)
	if got == nil {
		t.Fatal("ProcessPath returned nil")
	}
}

func TestProcessPath_FolderRouteWithSeries(t *testing.T) {
	t.Parallel()
	d := &stubDisk{folders: map[string]bool{"/drop/show": true}}
	svc := newSvc(t, d, nil, nil)
	ser := &series.Series{ID: 1}
	got := svc.ProcessPath("/drop/show", episodeimport.ImportModeAuto, ser, nil)
	if got == nil {
		t.Fatal("ProcessPath returned nil")
	}
	// With ser != nil, dispatcher routes to processFolderWithSeries
	// (deferred stub) — single result with deferred error message.
	if len(got) != 1 {
		t.Errorf("expected 1 result from deferred processFolderWithSeries, got %d", len(got))
	}
}

func TestProcessPath_InaccessiblePath(t *testing.T) {
	t.Parallel()
	d := &stubDisk{}
	svc := newSvc(t, d, nil, nil)
	got := svc.ProcessPath("/drop/missing", episodeimport.ImportModeAuto, nil, nil)
	if got == nil {
		t.Fatal("ProcessPath returned nil — should be non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice on inaccessible path, got %d results", len(got))
	}
}

// --- ShouldDeleteFolder --------------------------------------------

func TestShouldDeleteFolder_AllSamples_ReturnsTrue(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	video := filepath.Join(dir, "Show.S01E01.sample.mkv")
	d := &stubDisk{
		allFiles: map[string][]string{dir: {video}},
	}
	scanner := &stubScanner{
		videos: map[string][]string{dir: {video}},
	}
	sample := &stubSample{verdict: episodeimport.DetectSampleSample}
	svc := newSvc(t, d, scanner, sample)
	if !svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = false, want true (all samples)")
	}
	if sample.calls != 1 {
		t.Errorf("sample detector called %d times, want 1", sample.calls)
	}
}

func TestShouldDeleteFolder_UnparseableVideo_ReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	d := &stubDisk{allFiles: map[string][]string{dir: {}}}
	scanner := &stubScanner{
		// "ep01.mkv" doesn't carry a series title or season number
		// so parser.ParseTitle returns nil.
		videos: map[string][]string{dir: {filepath.Join(dir, "ep01.mkv")}},
	}
	svc := newSvc(t, d, scanner, nil)
	if svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = true on unparseable video, want false")
	}
}

func TestShouldDeleteFolder_NotASample_ReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	d := &stubDisk{
		allFiles: map[string][]string{dir: {}},
	}
	scanner := &stubScanner{
		videos: map[string][]string{dir: {filepath.Join(dir, "Show.S01E01.1080p.x264.mkv")}},
	}
	sample := &stubSample{verdict: episodeimport.DetectSampleNotSample}
	svc := newSvc(t, d, scanner, sample)
	if svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = true, want false (non-sample present)")
	}
}

func TestShouldDeleteFolder_IndeterminateSample_ReturnsFalse(t *testing.T) {
	t.Parallel()
	// Mirrors C# `:131-136` strict-not-equal: Indeterminate also
	// blocks the delete.
	dir := "/drop/show"
	d := &stubDisk{allFiles: map[string][]string{dir: {}}}
	scanner := &stubScanner{
		videos: map[string][]string{dir: {filepath.Join(dir, "Show.S01E01.mkv")}},
	}
	sample := &stubSample{verdict: episodeimport.DetectSampleIndeterminate}
	svc := newSvc(t, d, scanner, sample)
	if svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = true on Indeterminate, want false")
	}
}

func TestShouldDeleteFolder_LargeRarFile_ReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	rar := filepath.Join(dir, "show.rar")
	d := &stubDisk{
		allFiles: map[string][]string{dir: {rar}},
		sizes:    map[string]int64{rar: 11 * 1024 * 1024}, // 11 MB > 10 MB threshold
	}
	scanner := &stubScanner{videos: map[string][]string{dir: nil}}
	svc := newSvc(t, d, scanner, nil)
	if svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = true with >10MB rar, want false")
	}
}

func TestShouldDeleteFolder_SmallRarFile_ReturnsTrue(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	rar := filepath.Join(dir, "small.rar")
	d := &stubDisk{
		allFiles: map[string][]string{dir: {rar}},
		sizes:    map[string]int64{rar: 1024 * 1024}, // 1 MB, well under
	}
	scanner := &stubScanner{videos: map[string][]string{dir: nil}}
	svc := newSvc(t, d, scanner, nil)
	if !svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = false with <10MB rar, want true")
	}
}

func TestShouldDeleteFolder_RarExtensionCaseInsensitive(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	bigRar := filepath.Join(dir, "show.RAR")
	d := &stubDisk{
		allFiles: map[string][]string{dir: {bigRar}},
		sizes:    map[string]int64{bigRar: 11 * 1024 * 1024},
	}
	scanner := &stubScanner{videos: map[string][]string{dir: nil}}
	svc := newSvc(t, d, scanner, nil)
	if svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = true for .RAR uppercase, should be case-insensitive")
	}
}

func TestShouldDeleteFolder_DirectoryNotFound_ReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := "/drop/missing"
	d := &stubDisk{getFilesErr: fs.ErrNotExist}
	scanner := &stubScanner{videos: map[string][]string{}}
	svc := newSvc(t, d, scanner, nil)
	if svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = true on missing dir, want false")
	}
}

func TestShouldDeleteFolder_RarFileSizeError_ReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	rar := filepath.Join(dir, "broken.rar")
	d := &stubDisk{
		allFiles: map[string][]string{dir: {rar}},
		sizeErr:  map[string]error{rar: errors.New("EACCES")},
	}
	scanner := &stubScanner{videos: map[string][]string{dir: nil}}
	svc := newSvc(t, d, scanner, nil)
	if svc.ShouldDeleteFolder(dir, &series.Series{ID: 1}) {
		t.Error("ShouldDeleteFolder = true on size-stat error, want false")
	}
}

// --- ProcessRootFolder (deferred) ----------------------------------

func TestProcessRootFolder_Deferred(t *testing.T) {
	t.Parallel()
	svc := newSvc(t, &stubDisk{}, nil, nil)
	got, err := svc.ProcessRootFolder("/drop")
	if got != nil {
		t.Errorf("expected nil slice for deferred method, got %v", got)
	}
	if err == nil {
		t.Fatal("expected error from deferred method")
	}
}
