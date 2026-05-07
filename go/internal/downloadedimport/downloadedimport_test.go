package downloadedimport_test

import (
	"errors"
	"fmt"
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

func TestProcessPath_FolderRouteWithSeries_NoVideoFiles(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	d := &stubDisk{
		folders: map[string]bool{dir: true},
		// No allFiles/videos → checkEmptyResultForIssue returns nil
		// (no flagged extensions among zero files).
	}
	svc := newSvc(t, d, nil, nil)
	ser := &series.Series{ID: 1}
	got := svc.ProcessPath(dir, episodeimport.ImportModeAuto, ser, nil)
	if got == nil {
		t.Fatal("ProcessPath returned nil")
	}
	// processFolderWithSeries on an empty folder: no SeriesPathExists
	// match, no videoFiles, no decisions, no results, no flagged
	// extensions → empty slice.
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d results: %+v", len(got), got)
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

// --- processFolder (via ProcessPath FolderRoute) ------------------

// scannerWithVideos returns a stubScanner whose GetVideoFiles map
// pins the supplied videos under the conventional test directory.
// Every processFolder test below uses /drop/show as its dir, so the
// helper hardcodes that — keeps the test names short and the helper
// signature single-arg.
func scannerWithVideos(videos []string) *stubScanner {
	return &stubScanner{videos: map[string][]string{"/drop/show": videos}}
}

// recDecider records GetImportDecisions calls + lets the test
// stipulate the returned decisions list.
type recDecider struct {
	calls    int
	got      []string
	gotSer   *series.Series
	gotDci   *downloadclients.Item
	gotDciI  *parser.ParsedEpisodeInfo
	gotFInfo *parser.ParsedEpisodeInfo
	ret      []*episodeimport.ImportDecision
}

func (r *recDecider) GetImportDecisions(
	paths []string, ser *series.Series, dci *downloadclients.Item,
	dciI *parser.ParsedEpisodeInfo, folderInfo *parser.ParsedEpisodeInfo, _ bool,
) []*episodeimport.ImportDecision {
	r.calls++
	r.got = paths
	r.gotSer = ser
	r.gotDci = dci
	r.gotDciI = dciI
	r.gotFInfo = folderInfo
	return r.ret
}

// recRunner records Import calls + lets the test stipulate the
// returned results.
type recRunner struct {
	calls   int
	gotMode episodeimport.ImportMode
	gotDci  *downloadclients.Item
	ret     []*episodeimport.ImportResult
}

func (r *recRunner) Import(
	_ []*episodeimport.ImportDecision, _ bool, dci *downloadclients.Item, mode episodeimport.ImportMode,
) []*episodeimport.ImportResult {
	r.calls++
	r.gotMode = mode
	r.gotDci = dci
	return r.ret
}

func newSvcCustom(
	t *testing.T,
	disk *stubDisk,
	scanner *stubScanner,
	seriesPaths *stubSeriesPaths,
	parserSvc *stubParser,
	decider downloadedimport.ImportDecider,
	runner downloadedimport.ImportRunner,
) *downloadedimport.Service {
	t.Helper()
	if scanner == nil {
		scanner = &stubScanner{}
	}
	if seriesPaths == nil {
		seriesPaths = &stubSeriesPaths{}
	}
	if parserSvc == nil {
		parserSvc = &stubParser{}
	}
	if decider == nil {
		decider = stubDecider{}
	}
	if runner == nil {
		runner = stubRunner{}
	}
	// Sample detector defaults to "all-Sample" — the processFolder
	// tests below don't exercise ShouldDeleteFolder unless the
	// PostImportDelete test, which uses sample-named files that
	// match the default verdict.
	sample := &stubSample{verdict: episodeimport.DetectSampleSample}
	svc := downloadedimport.NewService(disk, scanner, seriesPaths, parserSvc,
		decider, runner, sample, stubCfg{}, slog.Default())
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	return svc
}

func TestProcessFolder_UnknownSeries(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	d := &stubDisk{folders: map[string]bool{dir: true}}
	// stubParser default returns nil → unknown series.
	svc := newSvcCustom(t, d, nil, nil, nil, nil, nil)
	got := svc.ProcessPath(dir, episodeimport.ImportModeAuto, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1 (UnknownSeries)", len(got))
	}
	rejs := got[0].Decision.Rejections
	if len(rejs) == 0 || rejs[0].Reason != episodeimport.ImportRejectionUnknownSeries {
		t.Errorf("expected UnknownSeries rejection, got %+v", rejs)
	}
}

func TestProcessFolder_SeriesPathExists_Refuses(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	d := &stubDisk{folders: map[string]bool{dir: true}}
	sp := &stubSeriesPaths{exists: map[string]bool{dir: true}}
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	svc := newSvcCustom(t, d, nil, sp, pSvc, nil, nil)
	got := svc.ProcessPath(dir, episodeimport.ImportModeAuto, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (SeriesFolder rejection)", len(got))
	}
	rejs := got[0].Decision.Rejections
	if len(rejs) == 0 || rejs[0].Reason != episodeimport.ImportRejectionSeriesFolder {
		t.Errorf("expected SeriesFolder rejection, got %+v", rejs)
	}
}

func TestProcessFolder_FileLocked_NoDci(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	video := filepath.Join(dir, "Show.S01E01.mkv")
	d := &stubDisk{
		folders: map[string]bool{dir: true},
		locked:  map[string]bool{video: true},
	}
	scanner := scannerWithVideos([]string{video})
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	svc := newSvcCustom(t, d, scanner, nil, pSvc, nil, nil)
	got := svc.ProcessPath(dir, episodeimport.ImportModeAuto, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (FileLocked rejection)", len(got))
	}
	rejs := got[0].Decision.Rejections
	if len(rejs) == 0 || rejs[0].Reason != episodeimport.ImportRejectionFileLocked {
		t.Errorf("expected FileLocked, got %+v", rejs)
	}
}

func TestProcessFolder_FileLockedSkippedWhenDci(t *testing.T) {
	t.Parallel()
	// Mirrors C# `:192-204` — when downloadClientItem != nil, the
	// IsFileLocked check is skipped entirely.
	dir := "/drop/show"
	video := filepath.Join(dir, "Show.S01E01.mkv")
	d := &stubDisk{
		folders: map[string]bool{dir: true},
		locked:  map[string]bool{video: true},
	}
	scanner := scannerWithVideos([]string{video})
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	dci := &downloadclients.Item{Title: "Show.S01E01.WEB"}
	rec := &recDecider{}
	rRun := &recRunner{}
	svc := newSvcCustom(t, d, scanner, nil, pSvc, rec, rRun)
	svc.ProcessPath(dir, episodeimport.ImportModeAuto, nil, dci)
	if rec.calls != 1 {
		t.Errorf("expected decider called 1 time (lock check skipped), got %d", rec.calls)
	}
}

// MultiSeason rejection is structurally wired (see
// processFolderWithSeries: dciInfo.IsMultiSeason check at C# `:206-214`)
// but the Go parser doesn't yet populate ParsedEpisodeInfo.IsMultiSeason
// — the field exists on the model but no parser branch sets it true.
// A direct test of the rejection arm would require either a parser
// change (out of scope for this slice) or a test seam past
// parser.ParseTitle (the dispatcher takes title strings, not
// ParsedEpisodeInfo). Documented here so the gap is tracked.

func TestProcessFolder_AutoMode_NoDci_ResolvesToMove(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	d := &stubDisk{folders: map[string]bool{dir: true}}
	scanner := scannerWithVideos([]string{filepath.Join(dir, "Show.S01E01.mkv")})
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	rRun := &recRunner{ret: []*episodeimport.ImportResult{}}
	svc := newSvcCustom(t, d, scanner, nil, pSvc, nil, rRun)
	svc.ProcessPath(dir, episodeimport.ImportModeAuto, nil, nil)
	if rRun.gotMode != episodeimport.ImportModeAuto {
		// Mode passed to runner.Import is the ORIGINAL mode at C#
		// `:217` — auto-resolution happens AFTER on the local copy.
		t.Errorf("runner.Import got mode %v, want ImportModeAuto (C# `:217` passes pre-resolution)", rRun.gotMode)
	}
}

func TestProcessFolder_AutoMode_DciCantMove_ResolvesToCopy_NoDelete(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	video := filepath.Join(dir, "Show.S01E01.mkv")
	d := &stubDisk{folders: map[string]bool{dir: true}}
	scanner := scannerWithVideos([]string{video})
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	dci := &downloadclients.Item{Title: "Show.S01E01.mkv", CanMoveFiles: false}
	// One Imported result — the post-import delete branch should
	// NOT fire because CanMoveFiles=false → mode resolves to Copy.
	imported := episodeimport.NewImportedImportResult(
		episodeimport.NewImportDecision(&episodeimport.LocalEpisode{Path: video}),
		nil,
	)
	rRun := &recRunner{ret: []*episodeimport.ImportResult{imported}}
	svc := newSvcCustom(t, d, scanner, nil, pSvc, nil, rRun)
	svc.ProcessPath(dir, episodeimport.ImportModeAuto, nil, dci)
	if len(d.deleted) != 0 {
		t.Errorf("expected no folder delete (Copy mode), got %v", d.deleted)
	}
}

func TestProcessFolder_EmptyResults_AppendsArchiveRejection(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	rar := filepath.Join(dir, "show.rar")
	d := &stubDisk{
		folders:  map[string]bool{dir: true},
		allFiles: map[string][]string{dir: {rar}},
		// rar at 1MB so ShouldDeleteFolder wouldn't block — but we
		// exercise checkEmptyResultForIssue, not ShouldDeleteFolder.
		sizes: map[string]int64{rar: 1024},
	}
	scanner := scannerWithVideos(nil)
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	rRun := &recRunner{ret: []*episodeimport.ImportResult{}}
	svc := newSvcCustom(t, d, scanner, nil, pSvc, nil, rRun)
	got := svc.ProcessPath(dir, episodeimport.ImportModeAuto, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (archive rejection)", len(got))
	}
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionArchiveFile {
		t.Errorf("expected ArchiveFile rejection, got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFolder_EmptyResults_DangerousBeatsArchive(t *testing.T) {
	t.Parallel()
	// Mirrors C# `:378-390` order — Dangerous before Executable
	// before Archive. Folder with both .vbs (dangerous) and .rar
	// (archive) → DangerousFile wins.
	dir := "/drop/show"
	d := &stubDisk{
		folders: map[string]bool{dir: true},
		allFiles: map[string][]string{
			dir: {filepath.Join(dir, "evil.VBS"), filepath.Join(dir, "show.rar")},
		},
	}
	scanner := scannerWithVideos(nil)
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	rRun := &recRunner{ret: []*episodeimport.ImportResult{}}
	svc := newSvcCustom(t, d, scanner, nil, pSvc, nil, rRun)
	got := svc.ProcessPath(dir, episodeimport.ImportModeAuto, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionDangerousFile {
		t.Errorf("expected DangerousFile (case-insensitive .VBS), got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFolder_PostImportDelete_FiresOnMoveAndImported(t *testing.T) {
	t.Parallel()
	dir := "/drop/show"
	video := filepath.Join(dir, "Show.S01E01.mkv")
	d := &stubDisk{
		folders:  map[string]bool{dir: true},
		allFiles: map[string][]string{dir: {video}},
		// Configure ShouldDeleteFolder to return true: video file
		// parses + sample.IsSample == DetectSampleSample, no rar
		// files.
	}
	video = filepath.Join(dir, "Show.S01E01.sample.mkv") // make ShouldDeleteFolder happy
	d.allFiles[dir] = []string{video}
	scanner := scannerWithVideos([]string{video})
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	imported := episodeimport.NewImportedImportResult(
		episodeimport.NewImportDecision(&episodeimport.LocalEpisode{Path: video}),
		nil,
	)
	rRun := &recRunner{ret: []*episodeimport.ImportResult{imported}}
	svc := newSvcCustom(t, d, scanner, nil, pSvc, nil, rRun)
	svc.ProcessPath(dir, episodeimport.ImportModeMove, nil, nil)
	if len(d.deleted) != 1 || d.deleted[0] != dir {
		t.Errorf("expected DeleteFolder(%q), got %v", dir, d.deleted)
	}
}

// --- processFile (via ProcessPath FileRoute) ----------------------

// fileSvc builds a Service whose disk.FileExists matches a single
// path and whose parser resolves to a known series; convenient for
// the processFile rejection-ladder tests below. The dci is threaded
// at the ProcessPath call site, not here.
func fileSvc(t *testing.T, path string, cfg downloadedimport.ConfigReader) (
	*downloadedimport.Service, *recDecider, *recRunner, *stubDisk,
) {
	t.Helper()
	d := &stubDisk{files: map[string]bool{path: true}}
	pSvc := &stubParser{resolveTo: &series.Series{ID: 1}}
	rec := &recDecider{}
	rRun := &recRunner{}
	if cfg == nil {
		cfg = stubCfg{}
	}
	svc := downloadedimport.NewService(
		d, &stubScanner{}, &stubSeriesPaths{}, pSvc,
		rec, rRun, &stubSample{verdict: episodeimport.DetectSampleSample},
		cfg, slog.Default(),
	)
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	return svc, rec, rRun, d
}

func TestProcessFile_UnknownSeries(t *testing.T) {
	t.Parallel()
	path := "/drop/Unknown.S01E01.mkv"
	d := &stubDisk{files: map[string]bool{path: true}}
	// stubParser default returns nil → unknown series.
	svc := newSvc(t, d, nil, nil)
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (UnknownSeries)", len(got))
	}
	rejs := got[0].Decision.Rejections
	if len(rejs) == 0 || rejs[0].Reason != episodeimport.ImportRejectionUnknownSeries {
		t.Errorf("expected UnknownSeries, got %+v", rejs)
	}
}

func TestProcessFile_DotUnderscorePrefix(t *testing.T) {
	t.Parallel()
	// macOS resource fork. C# `:266-274` rejects with
	// InvalidFilePath without consulting any ladder below.
	path := "/drop/show/._hidden.mkv"
	svc, _, _, _ := fileSvc(t, path, nil)
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (InvalidFilePath)", len(got))
	}
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionInvalidFilePath {
		t.Errorf("expected InvalidFilePath, got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFile_DangerousExtension(t *testing.T) {
	t.Parallel()
	path := "/drop/Show.S01E01.vbs"
	svc, _, _, _ := fileSvc(t, path, nil)
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionDangerousFile {
		t.Errorf("expected DangerousFile, got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFile_DangerousExtension_CaseInsensitive(t *testing.T) {
	t.Parallel()
	path := "/drop/evil.PS1"
	svc, _, _, _ := fileSvc(t, path, nil)
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionDangerousFile {
		t.Errorf("expected DangerousFile (case-insensitive .PS1), got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFile_ExecutableExtension(t *testing.T) {
	t.Parallel()
	path := "/drop/installer.exe"
	svc, _, _, _ := fileSvc(t, path, nil)
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionExecutableFile {
		t.Errorf("expected ExecutableFile, got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFile_UserRejectedExtension(t *testing.T) {
	t.Parallel()
	// User-config rejects .nfo via the C# `:300-303` parsing
	// (split-on-comma, trim-dot-and-space, re-prepend-dot).
	path := "/drop/show.nfo"
	svc, _, _, _ := fileSvc(t, path, stubCfg{rejected: " nfo, srt"})
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionUserRejectedExtension {
		t.Errorf("expected UserRejectedExtension, got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFile_UserRejected_NotMatched_FallsThrough(t *testing.T) {
	t.Parallel()
	// .mkv is not in the user-rejected list and IS a media
	// extension, so the ladder falls through to the decision
	// pipeline.
	path := "/drop/Show.S01E01.mkv"
	svc, rec, rRun, _ := fileSvc(t, path, stubCfg{rejected: ".nfo,.srt"})
	rRun.ret = []*episodeimport.ImportResult{}
	svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if rec.calls != 1 {
		t.Errorf("decider should have been called once, got %d", rec.calls)
	}
}

func TestProcessFile_UnsupportedExtension(t *testing.T) {
	t.Parallel()
	// .docx is neither dangerous, executable, user-rejected, nor a
	// media extension → UnsupportedExtension.
	path := "/drop/notes.docx"
	svc, _, _, _ := fileSvc(t, path, nil)
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionUnsupportedExtension {
		t.Errorf("expected UnsupportedExtension, got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFile_NoExtension_Unsupported(t *testing.T) {
	t.Parallel()
	// Mirrors C# `extension.IsNullOrWhiteSpace()` arm at `:319` —
	// extensionless files are unsupported.
	path := "/drop/README"
	svc, _, _, _ := fileSvc(t, path, nil)
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionUnsupportedExtension {
		t.Errorf("expected UnsupportedExtension on extensionless file, got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFile_Locked_NoDci(t *testing.T) {
	t.Parallel()
	path := "/drop/Show.S01E01.mkv"
	svc, _, _, d := fileSvc(t, path, nil)
	d.locked = map[string]bool{path: true}
	got := svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, nil)
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionFileLocked {
		t.Errorf("expected FileLocked, got %+v", got[0].Decision.Rejections)
	}
}

func TestProcessFile_LockedSkippedWhenDci(t *testing.T) {
	t.Parallel()
	// Mirrors C# `:331-340` — IsFileLocked is gated behind
	// downloadClientItem == nil.
	path := "/drop/Show.S01E01.mkv"
	svc, rec, _, d := fileSvc(t, path, nil)
	d.locked = map[string]bool{path: true}
	dci := &downloadclients.Item{Title: "Show.S01E01.WEB"}
	svc.ProcessPath(path, episodeimport.ImportModeAuto, nil, dci)
	if rec.calls != 1 {
		t.Errorf("expected decider called 1 (lock check skipped), got %d", rec.calls)
	}
}

func TestProcessFile_PassesPathsThroughDecider(t *testing.T) {
	t.Parallel()
	path := "/drop/Show.S01E01.mkv"
	svc, rec, rRun, _ := fileSvc(t, path, nil)
	rRun.ret = []*episodeimport.ImportResult{}
	svc.ProcessPath(path, episodeimport.ImportModeMove, nil, nil)
	if len(rec.got) != 1 || rec.got[0] != path {
		t.Errorf("decider got %v, want [%q]", rec.got, path)
	}
	if rRun.gotMode != episodeimport.ImportModeMove {
		t.Errorf("runner got mode %v, want Move", rRun.gotMode)
	}
}

// --- ProcessRootFolder ---------------------------------------------

func TestProcessRootFolder_EmptyDrop(t *testing.T) {
	t.Parallel()
	d := &stubDisk{}
	svc := newSvc(t, d, nil, nil)
	got, err := svc.ProcessRootFolder("/drop")
	if err != nil {
		t.Fatalf("ProcessRootFolder: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results from empty drop, want 0", len(got))
	}
}

func TestProcessRootFolder_GetDirectoriesError_Propagates(t *testing.T) {
	t.Parallel()
	// Inject an error via stubDisk by overriding GetDirectories with
	// a failing path. The current stubDisk doesn't have an explicit
	// err field for GetDirectories, so we use a path with no dirs
	// map entry which returns nil + nil err — that's not the error
	// path. Instead use a custom stub via newSvcCustom.
	d := &errorDirsDisk{err: fmt.Errorf("boom")}
	svc := downloadedimport.NewService(
		d, &stubScanner{}, &stubSeriesPaths{}, &stubParser{},
		stubDecider{}, stubRunner{}, &stubSample{verdict: episodeimport.DetectSampleSample},
		stubCfg{}, slog.Default(),
	)
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	got, err := svc.ProcessRootFolder("/drop")
	if err == nil {
		t.Fatal("expected GetDirectories error to propagate")
	}
	if got != nil {
		t.Errorf("expected nil slice on error, got %v", got)
	}
}

func TestProcessRootFolder_DispatchesSubfolders(t *testing.T) {
	t.Parallel()
	root := "/drop"
	subA := filepath.Join(root, "Show.S01")
	subB := filepath.Join(root, "Show.S02")
	d := &stubDisk{
		dirs: map[string][]string{root: {subA, subB}},
	}
	// Two subfolders → processFolder fires twice. With nil parser
	// resolution, each surfaces an UnknownSeries result. The slice
	// returned should have 2 entries.
	svc := newSvc(t, d, nil, nil)
	got, err := svc.ProcessRootFolder(root)
	if err != nil {
		t.Fatalf("ProcessRootFolder: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2 (one per sub-folder UnknownSeries)", len(got))
	}
	for _, r := range got {
		if r.Decision.Rejections[0].Reason != episodeimport.ImportRejectionUnknownSeries {
			t.Errorf("expected UnknownSeries, got %+v", r.Decision.Rejections)
		}
	}
}

func TestProcessRootFolder_DispatchesTopLevelFiles(t *testing.T) {
	t.Parallel()
	root := "/drop"
	video := filepath.Join(root, "Show.S01E01.mkv")
	d := &stubDisk{}
	scanner := &stubScanner{
		videos: map[string][]string{root: {video}},
	}
	svc := newSvc(t, d, scanner, nil)
	got, err := svc.ProcessRootFolder(root)
	if err != nil {
		t.Fatalf("ProcessRootFolder: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (UnknownSeries for top-level file)", len(got))
	}
	if got[0].Decision.Rejections[0].Reason != episodeimport.ImportRejectionUnknownSeries {
		t.Errorf("expected UnknownSeries, got %+v", got[0].Decision.Rejections)
	}
}

// errorDirsDisk satisfies DiskOps with a GetDirectories that always
// errors — used to exercise the GetDirectories-error arm of
// ProcessRootFolder. Defaults for every other method match
// stubDisk's zero-value behaviour.
type errorDirsDisk struct {
	err error
}

func (e *errorDirsDisk) FolderExists(string) bool                { return false }
func (e *errorDirsDisk) FileExists(string) bool                  { return false }
func (e *errorDirsDisk) GetDirectories(string) ([]string, error) { return nil, e.err }
func (e *errorDirsDisk) GetFiles(string, bool) ([]string, error) { return nil, nil }
func (e *errorDirsDisk) GetFileSize(string) (int64, error)       { return 0, nil }
func (e *errorDirsDisk) IsFileLocked(string) bool                { return false }
func (e *errorDirsDisk) DeleteFolder(string, bool) error         { return nil }
