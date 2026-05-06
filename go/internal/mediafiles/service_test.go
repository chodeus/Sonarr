package mediafiles

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// fakeDisk is a fully-recording DiskProvider stub. Each method records
// the call args; some return values are configurable. The default
// zero-value form returns "false" for FolderExists / FolderEmpty,
// nil-error + nil-files for GetFiles, etc. Tests override fields
// per-case.
type fakeDisk struct {
	mu sync.Mutex

	// Programmable returns.
	folderExistsByPath map[string]bool
	folderEmptyByPath  map[string]bool
	filesByPath        map[string][]string
	getFilesErr        error
	createFolderErr    error
	removeEmptySubErr  error
	deleteFolderErr    error
	setPermissionsErr  error
	getFileSizeByPath  map[string]int64
	getFileSizeErrPath map[string]error

	// Recorded calls.
	folderExistsCalls   []string
	folderEmptyCalls    []string
	getFilesCalls       []getFilesCall
	createFolderCalls   []string
	removeEmptySubCalls []string
	deleteFolderCalls   []deleteFolderCall
	setPermissionsCalls []setPermissionsCall
	getFileSizeCalls    []string
}

type getFilesCall struct {
	path      string
	recursive bool
}
type deleteFolderCall struct {
	path      string
	recursive bool
}
type setPermissionsCall struct {
	path, mask, group string
}

func (f *fakeDisk) FolderExists(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folderExistsCalls = append(f.folderExistsCalls, path)
	return f.folderExistsByPath[path]
}
func (f *fakeDisk) FolderEmpty(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folderEmptyCalls = append(f.folderEmptyCalls, path)
	return f.folderEmptyByPath[path]
}
func (f *fakeDisk) GetFiles(path string, recursive bool) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getFilesCalls = append(f.getFilesCalls, getFilesCall{path: path, recursive: recursive})
	if f.getFilesErr != nil {
		return nil, f.getFilesErr
	}
	return f.filesByPath[path], nil
}
func (f *fakeDisk) CreateFolder(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createFolderCalls = append(f.createFolderCalls, path)
	return f.createFolderErr
}
func (f *fakeDisk) RemoveEmptySubfolders(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeEmptySubCalls = append(f.removeEmptySubCalls, path)
	return f.removeEmptySubErr
}
func (f *fakeDisk) DeleteFolder(path string, recursive bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteFolderCalls = append(f.deleteFolderCalls, deleteFolderCall{path: path, recursive: recursive})
	return f.deleteFolderErr
}
func (f *fakeDisk) SetPermissions(path, mask, group string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setPermissionsCalls = append(f.setPermissionsCalls, setPermissionsCall{path: path, mask: mask, group: group})
	return f.setPermissionsErr
}
func (f *fakeDisk) GetFileSize(path string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getFileSizeCalls = append(f.getFileSizeCalls, path)
	if e, ok := f.getFileSizeErrPath[path]; ok {
		return 0, e
	}
	return f.getFileSizeByPath[path], nil
}

// fakeConfig is a Config stub.
type fakeConfig struct {
	createEmpty bool
	deleteEmpty bool
	setPerms    bool
	chmod       string
	chown       string
}

func (f *fakeConfig) CreateEmptySeriesFolders() bool { return f.createEmpty }
func (f *fakeConfig) DeleteEmptyFolders() bool       { return f.deleteEmpty }
func (f *fakeConfig) SetPermissionsLinux() bool      { return f.setPerms }
func (f *fakeConfig) ChmodFolder() string            { return f.chmod }
func (f *fakeConfig) ChownGroup() string             { return f.chown }

// fakeRootFolders is a RootFolderResolver stub.
type fakeRootFolders struct {
	bestPath string
	err      error
	calls    []string
}

func (f *fakeRootFolders) GetBestRootFolderPath(_ context.Context, p string) (string, error) {
	f.calls = append(f.calls, p)
	return f.bestPath, f.err
}

// fakeSeriesGet is a SeriesGetter stub.
type fakeSeriesGet struct {
	getByID  map[int]*series.Series
	all      []series.Series
	getErr   error
	allErr   error
	getCalls []int
	allCalls int
}

func (f *fakeSeriesGet) GetSeries(_ context.Context, id int) (*series.Series, error) {
	f.getCalls = append(f.getCalls, id)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getByID[id], nil
}
func (f *fakeSeriesGet) GetAllSeries(_ context.Context) ([]series.Series, error) {
	f.allCalls++
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.all, nil
}

// fakeEpisodeFiles is an EpisodeFileService stub.
type fakeEpisodeFiles struct {
	bySeries    map[int][]episodefiles.File
	bySeriesErr error
	updateErr   error
	updates     [][]*episodefiles.File
	filterFn    func(candidates []string, parent *series.Series) []string
	filterErr   error
	getCalls    []int
	filterCalls int
}

func (f *fakeEpisodeFiles) GetFilesBySeries(_ context.Context, seriesID int) ([]episodefiles.File, error) {
	f.getCalls = append(f.getCalls, seriesID)
	if f.bySeriesErr != nil {
		return nil, f.bySeriesErr
	}
	return f.bySeries[seriesID], nil
}
func (f *fakeEpisodeFiles) UpdateMany(_ context.Context, files []*episodefiles.File) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	cp := make([]*episodefiles.File, len(files))
	copy(cp, files)
	f.updates = append(f.updates, cp)
	return nil
}
func (f *fakeEpisodeFiles) FilterExistingFiles(_ context.Context, candidates []string, parent *series.Series) ([]string, error) {
	f.filterCalls++
	if f.filterErr != nil {
		return nil, f.filterErr
	}
	if f.filterFn == nil {
		return candidates, nil
	}
	return f.filterFn(candidates, parent), nil
}

// fakeCleanup is a MediaFileTableCleanup stub.
type fakeCleanup struct {
	err   error
	calls []cleanupCall
}
type cleanupCall struct {
	seriesID    int
	filesOnDisk []string
}

func (f *fakeCleanup) Clean(_ context.Context, parent *series.Series, filesOnDisk []string) (int, int, error) {
	cp := append([]string(nil), filesOnDisk...)
	f.calls = append(f.calls, cleanupCall{seriesID: parent.ID, filesOnDisk: cp})
	return 0, 0, f.err
}

// fakeImportDecide / fakeImportApprove / fakeMediaInfo cover the
// three deferred-stub axes.
type fakeImportDecide struct {
	decisions []any
	err       error
	calls     []importDecideCall
}
type importDecideCall struct {
	files       []string
	seriesID    int
	filterExist bool
}

func (f *fakeImportDecide) GetImportDecisions(_ context.Context, files []string, parent *series.Series, filterExist bool) ([]any, error) {
	cp := append([]string(nil), files...)
	f.calls = append(f.calls, importDecideCall{files: cp, seriesID: parent.ID, filterExist: filterExist})
	return f.decisions, f.err
}

type fakeImportApprove struct {
	err   error
	calls []importApproveCall
}
type importApproveCall struct {
	count    int
	copyOnly bool
}

func (f *fakeImportApprove) Import(_ context.Context, decisions []any, copyOnly bool) error {
	f.calls = append(f.calls, importApproveCall{count: len(decisions), copyOnly: copyOnly})
	return f.err
}

type fakeMediaInfo struct {
	updatedByPath map[string]bool
	errByPath     map[string]error
	calls         []string
}

func (f *fakeMediaInfo) Update(_ context.Context, file *episodefiles.File, _ *series.Series) (bool, error) {
	f.calls = append(f.calls, file.RelativePath)
	if e, ok := f.errByPath[file.RelativePath]; ok {
		return false, e
	}
	return f.updatedByPath[file.RelativePath], nil
}

// stubs is the bag of every dependency a test typically wants
// preloaded with sensible zero behaviours. Tests reach into individual
// fields to override.
type stubs struct {
	disk          *fakeDisk
	cfg           *fakeConfig
	rootFolders   *fakeRootFolders
	seriesGet     *fakeSeriesGet
	episodeFiles  *fakeEpisodeFiles
	cleanup       *fakeCleanup
	importDecide  *fakeImportDecide
	importApprove *fakeImportApprove
	mediaInfo     *fakeMediaInfo
}

func newStubs() *stubs {
	return &stubs{
		disk: &fakeDisk{
			folderExistsByPath: map[string]bool{},
			folderEmptyByPath:  map[string]bool{},
			filesByPath:        map[string][]string{},
			getFileSizeByPath:  map[string]int64{},
			getFileSizeErrPath: map[string]error{},
		},
		cfg:           &fakeConfig{},
		rootFolders:   &fakeRootFolders{},
		seriesGet:     &fakeSeriesGet{getByID: map[int]*series.Series{}},
		episodeFiles:  &fakeEpisodeFiles{bySeries: map[int][]episodefiles.File{}},
		cleanup:       &fakeCleanup{},
		importDecide:  &fakeImportDecide{},
		importApprove: &fakeImportApprove{},
		mediaInfo: &fakeMediaInfo{
			updatedByPath: map[string]bool{},
			errByPath:     map[string]error{},
		},
	}
}

func (s *stubs) build(t *testing.T, bus *messaging.Bus) *Service {
	t.Helper()
	svc, err := NewService(
		s.disk, s.cfg, s.rootFolders, s.seriesGet, s.episodeFiles,
		s.cleanup, s.importDecide, s.importApprove, s.mediaInfo,
		bus, nil,
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// captureBus subscribes to both scan events and lets a test assert
// what was published in what order.
type captureBus struct {
	scanned []SeriesScannedEvent
	skipped []SeriesScanSkippedEvent
}

func newCaptureBus(t *testing.T) (*messaging.Bus, *captureBus) {
	t.Helper()
	bus := messaging.New(nil)
	cap := &captureBus{}
	messaging.Subscribe(bus, "test.mediafiles.scanned", func(_ context.Context, ev SeriesScannedEvent) error {
		cap.scanned = append(cap.scanned, ev)
		return nil
	})
	messaging.Subscribe(bus, "test.mediafiles.skipped", func(_ context.Context, ev SeriesScanSkippedEvent) error {
		cap.skipped = append(cap.skipped, ev)
		return nil
	})
	t.Cleanup(func() {
		bus.BeginShutdown()
		bus.Wait()
	})
	return bus, cap
}

// TestNewService_RejectsNilDeps pins the constructor's nil-dep
// validation. Each axis gets its own subtest so the failure message
// names which dep was actually rejected.
func TestNewService_RejectsNilDeps(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo)
		want   string
	}{
		{
			name: "nil disk", want: "DiskProvider",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return nil, s.cfg, s.rootFolders, s.seriesGet, s.episodeFiles, s.cleanup, s.importDecide, s.importApprove, s.mediaInfo
			},
		},
		{
			name: "nil cfg", want: "Config",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return s.disk, nil, s.rootFolders, s.seriesGet, s.episodeFiles, s.cleanup, s.importDecide, s.importApprove, s.mediaInfo
			},
		},
		{
			name: "nil rootFolders", want: "RootFolderResolver",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return s.disk, s.cfg, nil, s.seriesGet, s.episodeFiles, s.cleanup, s.importDecide, s.importApprove, s.mediaInfo
			},
		},
		{
			name: "nil seriesGet", want: "SeriesGetter",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return s.disk, s.cfg, s.rootFolders, nil, s.episodeFiles, s.cleanup, s.importDecide, s.importApprove, s.mediaInfo
			},
		},
		{
			name: "nil episodeFiles", want: "EpisodeFileService",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return s.disk, s.cfg, s.rootFolders, s.seriesGet, nil, s.cleanup, s.importDecide, s.importApprove, s.mediaInfo
			},
		},
		{
			name: "nil cleanup", want: "MediaFileTableCleanup",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return s.disk, s.cfg, s.rootFolders, s.seriesGet, s.episodeFiles, nil, s.importDecide, s.importApprove, s.mediaInfo
			},
		},
		{
			name: "nil importDecide", want: "ImportDecisionMaker",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return s.disk, s.cfg, s.rootFolders, s.seriesGet, s.episodeFiles, s.cleanup, nil, s.importApprove, s.mediaInfo
			},
		},
		{
			name: "nil importApprove", want: "ImportApprovedEpisodes",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return s.disk, s.cfg, s.rootFolders, s.seriesGet, s.episodeFiles, s.cleanup, s.importDecide, nil, s.mediaInfo
			},
		},
		{
			name: "nil mediaInfo", want: "UpdateMediaInfo",
			mutate: func(s *stubs) (DiskProvider, Config, RootFolderResolver, SeriesGetter, EpisodeFileService, MediaFileTableCleanup, ImportDecisionMaker, ImportApprovedEpisodes, UpdateMediaInfo) {
				return s.disk, s.cfg, s.rootFolders, s.seriesGet, s.episodeFiles, s.cleanup, s.importDecide, s.importApprove, nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStubs()
			disk, cfg, rf, sg, ef, cl, id, ia, mi := tc.mutate(s)
			_, err := NewService(disk, cfg, rf, sg, ef, cl, id, ia, mi, nil, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("err %q must mention %q", err.Error(), tc.want)
			}
		})
	}
}

// TestScan_NilSeries pins the defensive guard.
func TestScan_NilSeries(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	if err := svc.Scan(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil series")
	}
}

// TestScan_RootFolderDoesNotExist pins branch 1: missing series
// folder + missing root folder → SeriesScanSkippedEvent published
// with the correct reason; no further disk work.
func TestScan_RootFolderDoesNotExist(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 1, Title: "Show", Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = false
	s.disk.folderExistsByPath["/data"] = false

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(capb.skipped) != 1 || capb.skipped[0].Reason != SeriesScanSkippedReasonRootFolderDoesNotExist {
		t.Fatalf("expected RootFolderDoesNotExist; got %+v", capb.skipped)
	}
	if len(capb.scanned) != 0 {
		t.Fatalf("expected no scanned event; got %+v", capb.scanned)
	}
	if len(s.disk.getFilesCalls) != 0 {
		t.Fatalf("expected no GetFiles call; got %+v", s.disk.getFilesCalls)
	}
	if len(s.cleanup.calls) != 0 {
		t.Fatalf("expected no cleanup call on root-missing path; got %+v", s.cleanup.calls)
	}
}

// TestScan_RootFolderIsEmpty pins branch 2.
func TestScan_RootFolderIsEmpty(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 1, Title: "Show", Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = false
	s.disk.folderExistsByPath["/data"] = true
	s.disk.folderEmptyByPath["/data"] = true

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(capb.skipped) != 1 || capb.skipped[0].Reason != SeriesScanSkippedReasonRootFolderIsEmpty {
		t.Fatalf("expected RootFolderIsEmpty; got %+v", capb.skipped)
	}
}

// TestScan_RootFolderResolverError pins error propagation.
func TestScan_RootFolderResolverError(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	wantErr := errors.New("root resolve boom")
	s.rootFolders.err = wantErr
	if err := svc.Scan(context.Background(), &series.Series{ID: 1, Path: "/x"}); !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped %v; got %v", wantErr, err)
	}
}

// TestScan_MissingFolder_CreateAndDeleteEmpty pins branch 3a:
// CreateEmptySeriesFolders true + DeleteEmptyFolders true → no
// CreateFolder; cleanMediaFiles called with empty slice;
// SeriesScannedEvent with empty extras.
func TestScan_MissingFolder_CreateAndDeleteEmpty(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 7, Title: "Show", Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = false
	s.disk.folderExistsByPath["/data"] = true
	s.cfg.createEmpty = true
	s.cfg.deleteEmpty = true

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.disk.createFolderCalls) != 0 {
		t.Fatalf("expected no CreateFolder; got %+v", s.disk.createFolderCalls)
	}
	if len(s.cleanup.calls) != 1 || s.cleanup.calls[0].seriesID != 7 || len(s.cleanup.calls[0].filesOnDisk) != 0 {
		t.Fatalf("cleanup expected (id=7, empty files); got %+v", s.cleanup.calls)
	}
	if len(capb.scanned) != 1 || len(capb.scanned[0].PossibleExtraFiles) != 0 {
		t.Fatalf("expected scanned event with empty extras; got %+v", capb.scanned)
	}
	if len(capb.skipped) != 0 {
		t.Fatalf("expected no skipped event; got %+v", capb.skipped)
	}
}

// TestScan_MissingFolder_CreateOnly pins branch 3b: CreateEmptyTrue +
// DeleteEmptyFalse → CreateFolder called, applyPermissions invoked,
// then clean+complete with empty extras.
func TestScan_MissingFolder_CreateOnly(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 7, Title: "Show", Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = false
	s.disk.folderExistsByPath["/data"] = true
	s.cfg.createEmpty = true
	s.cfg.deleteEmpty = false
	s.cfg.setPerms = true
	s.cfg.chmod = "0775"
	s.cfg.chown = "media"

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.disk.createFolderCalls) != 1 || s.disk.createFolderCalls[0] != "/data/Show" {
		t.Fatalf("CreateFolder calls: %+v", s.disk.createFolderCalls)
	}
	want := setPermissionsCall{path: "/data/Show", mask: "0775", group: "media"}
	if len(s.disk.setPermissionsCalls) != 1 || s.disk.setPermissionsCalls[0] != want {
		t.Fatalf("SetPermissions calls: %+v want %+v", s.disk.setPermissionsCalls, want)
	}
	if len(capb.scanned) != 1 {
		t.Fatalf("expected scanned event; got %+v", capb.scanned)
	}
}

// TestScan_MissingFolder_NoCreate pins branch 3c: CreateEmpty=false →
// no CreateFolder; clean+complete still happen.
func TestScan_MissingFolder_NoCreate(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 9, Title: "Show", Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = false
	s.disk.folderExistsByPath["/data"] = true
	s.cfg.createEmpty = false

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.disk.createFolderCalls) != 0 {
		t.Fatalf("expected no CreateFolder; got %+v", s.disk.createFolderCalls)
	}
	if len(s.disk.setPermissionsCalls) != 0 {
		t.Fatalf("expected no SetPermissions; got %+v", s.disk.setPermissionsCalls)
	}
	if len(capb.scanned) != 1 {
		t.Fatalf("expected scanned event; got %+v", capb.scanned)
	}
}

// TestScan_MissingFolder_CreateOnly_NoSetPerms pins that
// SetPermissionsLinux=false short-circuits the permission set.
func TestScan_MissingFolder_CreateOnly_NoSetPerms(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	ser := &series.Series{ID: 7, Title: "Show", Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = false
	s.disk.folderExistsByPath["/data"] = true
	s.cfg.createEmpty = true
	s.cfg.deleteEmpty = false
	s.cfg.setPerms = false

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.disk.createFolderCalls) != 1 {
		t.Fatalf("expected one CreateFolder; got %+v", s.disk.createFolderCalls)
	}
	if len(s.disk.setPermissionsCalls) != 0 {
		t.Fatalf("expected no SetPermissions when SetPermissionsLinux=false; got %+v", s.disk.setPermissionsCalls)
	}
}

// TestScan_SetPermissionsErrorSwallowed pins the C# `try/catch`
// behaviour: SetPermissions errors don't fail the scan.
func TestScan_SetPermissionsErrorSwallowed(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 7, Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = false
	s.disk.folderExistsByPath["/data"] = true
	s.cfg.createEmpty = true
	s.cfg.setPerms = true
	s.disk.setPermissionsErr = errors.New("perm denied")

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan should not fail on SetPermissions error: %v", err)
	}
	if len(capb.scanned) != 1 {
		t.Fatalf("expected scanned event; got %+v", capb.scanned)
	}
}

// TestScan_HappyPath pins the full successful scan: video filter,
// FilterPaths, GetFilesBySeries, FilterExistingFiles, decisions,
// import, no size changes (loop short-circuit), no removeEmpty
// (DeleteEmpty=false), extras pass.
func TestScan_HappyPath(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 5, Title: "Show", Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = true
	s.disk.filesByPath["/data/Show"] = []string{
		"/data/Show/S01/episode.mkv",
		"/data/Show/S01/episode.nfo",
		"/data/Show/S01/poster.jpg",
		"/data/Show/Trailers/show-trailer.mkv", // dropped by extras filter
	}
	// Existing file already at the right size — size loop should not queue.
	existing := episodefiles.File{ID: 1, SeriesID: 5, Size: 100, RelativePath: "S01/existing.mkv"}
	s.episodeFiles.bySeries[5] = []episodefiles.File{existing}
	s.disk.getFileSizeByPath["/data/Show/S01/existing.mkv"] = 100
	s.importDecide.decisions = []any{"d1", "d2"}

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Cleanup is called with the FilterPaths-survived video files.
	if len(s.cleanup.calls) != 1 {
		t.Fatalf("expected 1 cleanup call; got %+v", s.cleanup.calls)
	}
	wantClean := []string{"/data/Show/S01/episode.mkv"} // only video, extras dropped
	if !reflect.DeepEqual(s.cleanup.calls[0].filesOnDisk, wantClean) {
		t.Fatalf("cleanup files: got %v want %v", s.cleanup.calls[0].filesOnDisk, wantClean)
	}

	// FilterExistingFiles called once with the same media list + parent.
	if s.episodeFiles.filterCalls != 1 {
		t.Fatalf("expected 1 FilterExistingFiles; got %d", s.episodeFiles.filterCalls)
	}

	// Import decisions invoked with filterExistingFiles=false (matches C#).
	if len(s.importDecide.calls) != 1 || s.importDecide.calls[0].filterExist != false {
		t.Fatalf("ImportDecide call: %+v want filterExist=false", s.importDecide.calls)
	}
	// ImportApprove called with copyOnly=false.
	if len(s.importApprove.calls) != 1 || s.importApprove.calls[0].copyOnly != false || s.importApprove.calls[0].count != 2 {
		t.Fatalf("ImportApprove call: %+v want copyOnly=false count=2", s.importApprove.calls)
	}

	// Size loop: same size → no UpdateMediaInfo, no UpdateMany.
	if len(s.mediaInfo.calls) != 0 {
		t.Fatalf("expected no UpdateMediaInfo; got %+v", s.mediaInfo.calls)
	}
	if len(s.episodeFiles.updates) != 0 {
		t.Fatalf("expected no UpdateMany; got %+v", s.episodeFiles.updates)
	}

	// DeleteEmpty=false → no RemoveEmptySubfolders / DeleteFolder.
	if len(s.disk.removeEmptySubCalls) != 0 || len(s.disk.deleteFolderCalls) != 0 {
		t.Fatalf("expected no remove/delete folder; got remove=%v delete=%v",
			s.disk.removeEmptySubCalls, s.disk.deleteFolderCalls)
	}

	// Extras pass yields the non-video survivors.
	if len(capb.scanned) != 1 {
		t.Fatalf("expected 1 scanned event; got %+v", capb.scanned)
	}
	got := capb.scanned[0].PossibleExtraFiles
	want := []string{"/data/Show/S01/episode.nfo", "/data/Show/S01/poster.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("possible extras: got %v want %v", got, want)
	}
}

// TestScan_SizeLoop_DifferentSize_MediaInfoTrue pins that when a size
// changes AND UpdateMediaInfo returns true, the row is NOT queued for
// the bulk Update.
func TestScan_SizeLoop_DifferentSize_MediaInfoTrue(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	ser := &series.Series{ID: 5, Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = true
	s.disk.filesByPath["/data/Show"] = nil
	s.episodeFiles.bySeries[5] = []episodefiles.File{
		{ID: 1, SeriesID: 5, Size: 100, RelativePath: "S01/a.mkv"},
	}
	s.disk.getFileSizeByPath["/data/Show/S01/a.mkv"] = 200
	s.mediaInfo.updatedByPath["S01/a.mkv"] = true

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.mediaInfo.calls) != 1 {
		t.Fatalf("expected 1 mediaInfo call; got %+v", s.mediaInfo.calls)
	}
	if len(s.episodeFiles.updates) != 0 {
		t.Fatalf("expected no UpdateMany when MediaInfo=true; got %+v", s.episodeFiles.updates)
	}
}

// TestScan_SizeLoop_DifferentSize_MediaInfoFalse pins that when
// UpdateMediaInfo returns false the row IS queued and the bulk
// UpdateMany fires once with the new size.
func TestScan_SizeLoop_DifferentSize_MediaInfoFalse(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	ser := &series.Series{ID: 5, Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = true
	s.disk.filesByPath["/data/Show"] = nil
	s.episodeFiles.bySeries[5] = []episodefiles.File{
		{ID: 1, SeriesID: 5, Size: 100, RelativePath: "S01/a.mkv"},
		{ID: 2, SeriesID: 5, Size: 200, RelativePath: "S01/b.mkv"},
	}
	s.disk.getFileSizeByPath["/data/Show/S01/a.mkv"] = 150 // changed
	s.disk.getFileSizeByPath["/data/Show/S01/b.mkv"] = 200 // unchanged
	s.mediaInfo.updatedByPath["S01/a.mkv"] = false         // forces fallback

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.episodeFiles.updates) != 1 {
		t.Fatalf("expected 1 UpdateMany; got %+v", s.episodeFiles.updates)
	}
	updated := s.episodeFiles.updates[0]
	if len(updated) != 1 || updated[0].ID != 1 || updated[0].Size != 150 {
		t.Fatalf("expected only file 1 with new size 150; got %+v", updated)
	}
}

// TestScan_SizeLoop_MediaInfoErrorFallback pins that an error from
// UpdateMediaInfo is treated as updated=false and the row queued
// for the bulk fallback.
func TestScan_SizeLoop_MediaInfoErrorFallback(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	ser := &series.Series{ID: 5, Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = true
	s.episodeFiles.bySeries[5] = []episodefiles.File{
		{ID: 1, SeriesID: 5, Size: 100, RelativePath: "S01/a.mkv"},
	}
	s.disk.getFileSizeByPath["/data/Show/S01/a.mkv"] = 999
	s.mediaInfo.errByPath["S01/a.mkv"] = errors.New("media-info offline")

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan should not fail on MediaInfo error: %v", err)
	}
	if len(s.episodeFiles.updates) != 1 || len(s.episodeFiles.updates[0]) != 1 {
		t.Fatalf("expected fallback UpdateMany on MediaInfo error; got %+v", s.episodeFiles.updates)
	}
}

// TestScan_SizeLoop_GetFileSizeErrorSkips pins that a GetFileSize
// error logs+skips the file rather than failing the scan.
func TestScan_SizeLoop_GetFileSizeErrorSkips(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	ser := &series.Series{ID: 5, Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = true
	s.episodeFiles.bySeries[5] = []episodefiles.File{
		{ID: 1, Size: 100, RelativePath: "S01/missing.mkv"},
	}
	s.disk.getFileSizeErrPath["/data/Show/S01/missing.mkv"] = errors.New("ENOENT")

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan should not fail on GetFileSize error: %v", err)
	}
	if len(s.mediaInfo.calls) != 0 {
		t.Fatalf("expected no UpdateMediaInfo on size-error skip; got %+v", s.mediaInfo.calls)
	}
	if len(s.episodeFiles.updates) != 0 {
		t.Fatalf("expected no UpdateMany on size-error skip; got %+v", s.episodeFiles.updates)
	}
}

// TestScan_RemoveEmpty_FolderEndsEmpty pins that DeleteEmptyFolders=true
// AND post-sweep FolderEmpty=true → DeleteFolder fires and the extras
// pass is skipped (the post-delete FolderExists check returns false).
func TestScan_RemoveEmpty_FolderEndsEmpty(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	ser := &series.Series{ID: 5, Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"

	// FolderExists returns true on the first call (initial gate) and
	// false on the post-RemoveEmpty extras-pass call. We model this
	// with a counter via the disk.folderExistsByPath but the simple
	// per-path map can't model history — switch to a per-call closure.
	folderExistsCalls := 0
	wrappedDisk := &countingDisk{base: s.disk, onFolderExists: func(p string) bool {
		folderExistsCalls++
		if p != "/data/Show" {
			return false
		}
		// First call: initial gate. Second call: post-cleanup extras gate.
		return folderExistsCalls == 1
	}}
	// Construct the service with the wrapping disk.
	svc, err := NewService(wrappedDisk, s.cfg, s.rootFolders, s.seriesGet, s.episodeFiles,
		s.cleanup, s.importDecide, s.importApprove, s.mediaInfo, bus, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	s.cfg.deleteEmpty = true
	s.disk.folderEmptyByPath["/data/Show"] = true // empty after RemoveEmptySub

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.disk.removeEmptySubCalls) != 1 {
		t.Fatalf("expected 1 RemoveEmptySubfolders; got %+v", s.disk.removeEmptySubCalls)
	}
	if len(s.disk.deleteFolderCalls) != 1 || s.disk.deleteFolderCalls[0].path != "/data/Show" {
		t.Fatalf("expected DeleteFolder on /data/Show recursive; got %+v", s.disk.deleteFolderCalls)
	}
	if len(s.disk.deleteFolderCalls) != 0 && !s.disk.deleteFolderCalls[0].recursive {
		t.Fatalf("DeleteFolder must be recursive=true; got %v", s.disk.deleteFolderCalls)
	}
	if len(capb.scanned) != 1 || len(capb.scanned[0].PossibleExtraFiles) != 0 {
		t.Fatalf("expected scanned event with no extras (folder gone); got %+v", capb.scanned)
	}
}

// TestScan_RemoveEmpty_FolderHasContent pins that DeleteEmptyFolders=true
// but FolderEmpty=false leaves the folder alone and runs the extras
// pass.
func TestScan_RemoveEmpty_FolderHasContent(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 5, Path: "/data/Show"}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Show"] = true
	s.disk.folderEmptyByPath["/data/Show"] = false
	s.cfg.deleteEmpty = true
	s.disk.filesByPath["/data/Show"] = []string{"/data/Show/poster.jpg"}

	if err := svc.Scan(context.Background(), ser); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(s.disk.removeEmptySubCalls) != 1 {
		t.Fatalf("expected 1 RemoveEmptySubfolders; got %+v", s.disk.removeEmptySubCalls)
	}
	if len(s.disk.deleteFolderCalls) != 0 {
		t.Fatalf("expected no DeleteFolder (non-empty); got %+v", s.disk.deleteFolderCalls)
	}
	if len(capb.scanned) != 1 || !reflect.DeepEqual(capb.scanned[0].PossibleExtraFiles, []string{"/data/Show/poster.jpg"}) {
		t.Fatalf("extras pass: got %+v", capb.scanned)
	}
}

// TestScan_HappyPath_DepErrors pins that disk + repo errors propagate
// in the order Scan invokes them. Each subtest stubs one axis to
// fail and asserts the wrapped error.
func TestScan_HappyPath_DepErrors(t *testing.T) {
	type setup func(s *stubs)

	cases := []struct {
		name string
		set  setup
		want string
	}{
		{
			name: "GetFiles error", want: "GetVideoFiles",
			set: func(s *stubs) {
				s.disk.folderExistsByPath["/data/Show"] = true
				s.disk.getFilesErr = errors.New("disk read")
			},
		},
		{
			name: "GetFilesBySeries error", want: "GetFilesBySeries",
			set: func(s *stubs) {
				s.disk.folderExistsByPath["/data/Show"] = true
				s.episodeFiles.bySeriesErr = errors.New("repo down")
			},
		},
		{
			name: "FilterExistingFiles error", want: "FilterExistingFiles",
			set: func(s *stubs) {
				s.disk.folderExistsByPath["/data/Show"] = true
				s.episodeFiles.filterErr = errors.New("filter boom")
			},
		},
		{
			name: "GetImportDecisions error", want: "GetImportDecisions",
			set: func(s *stubs) {
				s.disk.folderExistsByPath["/data/Show"] = true
				s.importDecide.err = errors.New("decide boom")
			},
		},
		{
			name: "Import error", want: "Import",
			set: func(s *stubs) {
				s.disk.folderExistsByPath["/data/Show"] = true
				s.importApprove.err = errors.New("import boom")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStubs()
			s.rootFolders.bestPath = "/data"
			tc.set(s)
			svc := s.build(t, nil)
			err := svc.Scan(context.Background(), &series.Series{ID: 5, Path: "/data/Show"})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("err %q must mention %q", err.Error(), tc.want)
			}
		})
	}
}

// countingDisk wraps a fakeDisk with a per-call FolderExists hook,
// used only by [TestScan_RemoveEmpty_FolderEndsEmpty] which needs
// to model the "folder exists on first check, gone on second"
// transition the C# tree creates by deleting the folder mid-scan.
type countingDisk struct {
	base           *fakeDisk
	onFolderExists func(string) bool
}

func (c *countingDisk) FolderExists(path string) bool {
	if c.onFolderExists != nil {
		return c.onFolderExists(path)
	}
	return c.base.FolderExists(path)
}
func (c *countingDisk) FolderEmpty(path string) bool { return c.base.FolderEmpty(path) }
func (c *countingDisk) GetFiles(path string, r bool) ([]string, error) {
	return c.base.GetFiles(path, r)
}
func (c *countingDisk) CreateFolder(path string) error { return c.base.CreateFolder(path) }
func (c *countingDisk) RemoveEmptySubfolders(path string) error {
	return c.base.RemoveEmptySubfolders(path)
}
func (c *countingDisk) DeleteFolder(path string, r bool) error { return c.base.DeleteFolder(path, r) }
func (c *countingDisk) SetPermissions(p, m, g string) error    { return c.base.SetPermissions(p, m, g) }
func (c *countingDisk) GetFileSize(p string) (int64, error)    { return c.base.GetFileSize(p) }

// contains is a tiny helper to keep error-text assertions readable.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestExecute_PopulatedSeriesID pins the single-series branch — looks
// up via GetSeries and runs Scan once.
func TestExecute_PopulatedSeriesID(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	ser := &series.Series{ID: 42, Title: "Found", Path: "/data/Found"}
	s.seriesGet.getByID[42] = ser
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/Found"] = true

	id := 42
	if err := svc.Execute(context.Background(), RescanSeriesCommand{SeriesID: &id}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(s.seriesGet.getCalls) != 1 || s.seriesGet.getCalls[0] != 42 {
		t.Fatalf("expected GetSeries(42); got %v", s.seriesGet.getCalls)
	}
	if s.seriesGet.allCalls != 0 {
		t.Fatalf("expected no GetAllSeries; got %d", s.seriesGet.allCalls)
	}
	if len(capb.scanned) != 1 {
		t.Fatalf("expected 1 scanned event; got %+v", capb.scanned)
	}
}

// TestExecute_NilSeriesID pins the all-series branch — iterates
// GetAllSeries and calls Scan per row in input order.
func TestExecute_NilSeriesID(t *testing.T) {
	s := newStubs()
	bus, capb := newCaptureBus(t)
	svc := s.build(t, bus)
	s.seriesGet.all = []series.Series{
		{ID: 1, Title: "A", Path: "/data/A"},
		{ID: 2, Title: "B", Path: "/data/B"},
		{ID: 3, Title: "C", Path: "/data/C"},
	}
	s.rootFolders.bestPath = "/data"
	s.disk.folderExistsByPath["/data/A"] = true
	s.disk.folderExistsByPath["/data/B"] = true
	s.disk.folderExistsByPath["/data/C"] = true

	if err := svc.Execute(context.Background(), RescanSeriesCommand{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if s.seriesGet.allCalls != 1 {
		t.Fatalf("expected 1 GetAllSeries; got %d", s.seriesGet.allCalls)
	}
	if len(s.seriesGet.getCalls) != 0 {
		t.Fatalf("expected no GetSeries; got %v", s.seriesGet.getCalls)
	}
	if len(capb.scanned) != 3 {
		t.Fatalf("expected 3 scanned events; got %d", len(capb.scanned))
	}
	for i, want := range []int{1, 2, 3} {
		if capb.scanned[i].Series.ID != want {
			t.Fatalf("scanned[%d].ID: got %d want %d", i, capb.scanned[i].Series.ID, want)
		}
	}
}

// TestExecute_NotFoundReturnsError pins the not-found path: GetSeries
// returns nil → Execute returns a wrapped error rather than silently
// no-op'ing. The C# tree throws ModelNotFoundException; Go surfaces
// the equivalent at this boundary.
func TestExecute_NotFoundReturnsError(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	id := 999
	err := svc.Execute(context.Background(), RescanSeriesCommand{SeriesID: &id})
	if err == nil {
		t.Fatal("expected error for missing series")
	}
	if !contains(err.Error(), "999") {
		t.Fatalf("err must mention id 999; got %q", err.Error())
	}
}

// TestExecute_GetSeriesError pins error propagation from the lookup.
func TestExecute_GetSeriesError(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	wantErr := errors.New("repo offline")
	s.seriesGet.getErr = wantErr
	id := 1
	err := svc.Execute(context.Background(), RescanSeriesCommand{SeriesID: &id})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped %v; got %v", wantErr, err)
	}
}

// TestExecute_GetAllSeriesError pins error propagation from the
// nil-SeriesID iteration branch.
func TestExecute_GetAllSeriesError(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	wantErr := errors.New("repo offline")
	s.seriesGet.allErr = wantErr
	err := svc.Execute(context.Background(), RescanSeriesCommand{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped %v; got %v", wantErr, err)
	}
}

// TestExecute_PerSeriesScanErrorStopsBatch pins the C# foreach posture:
// a Scan error on series K aborts the iteration; subsequent series
// are not scanned.
func TestExecute_PerSeriesScanErrorStopsBatch(t *testing.T) {
	s := newStubs()
	svc := s.build(t, nil)
	s.seriesGet.all = []series.Series{
		{ID: 1, Path: "/data/A"},
		{ID: 2, Path: "/data/B"},
		{ID: 3, Path: "/data/C"},
	}
	wantErr := errors.New("root resolve boom")
	// The first Scan succeeds (rootFolders.GetBestRootFolderPath returns ""+nil),
	// but we'll wire the rootFolders error to fire on the second call.
	s.rootFolders.err = wantErr // every call fails

	err := svc.Execute(context.Background(), RescanSeriesCommand{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected first-Scan error; got %v", err)
	}
	if !contains(err.Error(), "seriesId=1") {
		t.Fatalf("error should name the failing series; got %q", err.Error())
	}
}
