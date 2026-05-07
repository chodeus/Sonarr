package recyclebin_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/recyclebin"
	"sonarr.tv/sonarr/internal/upgrademediafiles"
)

type stubConfig struct {
	bin         string
	cleanupDays int
}

func (s stubConfig) RecycleBin() string         { return s.bin }
func (s stubConfig) RecycleBinCleanupDays() int { return s.cleanupDays }

type fakeDisk struct {
	deleted        []string
	deletedFolders []folderDel
	created        []string
	moves          []move
	exists         map[string]bool
	files          []string             // returned by GetFiles
	mtimes         map[string]time.Time // for FileGetLastWrite
	swept          []string             // RemoveEmptySubfolders calls
	dirs           []string             // returned by GetDirectories

	deleteErr        error
	createErr        error
	moveErr          error
	getFilesErr      error
	getDirsErr       error
	deleteFolderErr  error
	deleteFolderErrs map[string]error // per-path err for DeleteFolder
	mtimeErr         map[string]error // per-path err for FileGetLastWrite
	deleteErrs       map[string]error // per-path err for DeleteFile (for partial-failure tests)
	sweepErr         error
	existsHooks      func(path string) bool
}

type folderDel struct {
	path      string
	recursive bool
}

type move struct{ src, dst string }

func (f *fakeDisk) DeleteFile(path string) error {
	if perr, ok := f.deleteErrs[path]; ok {
		return perr
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, path)
	return nil
}

func (f *fakeDisk) CreateFolder(path string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, path)
	return nil
}

func (f *fakeDisk) FileExists(path string) bool {
	if f.existsHooks != nil {
		return f.existsHooks(path)
	}
	return f.exists[path]
}

func (f *fakeDisk) MoveFile(src, dst string) error {
	if f.moveErr != nil {
		return f.moveErr
	}
	f.moves = append(f.moves, move{src, dst})
	return nil
}

func (f *fakeDisk) GetFiles(_ string, _ bool) ([]string, error) {
	if f.getFilesErr != nil {
		return nil, f.getFilesErr
	}
	return f.files, nil
}

func (f *fakeDisk) FileGetLastWrite(path string) (time.Time, error) {
	if perr, ok := f.mtimeErr[path]; ok {
		return time.Time{}, perr
	}
	t, ok := f.mtimes[path]
	if !ok {
		return time.Time{}, nil
	}
	return t, nil
}

func (f *fakeDisk) RemoveEmptySubfolders(path string) error {
	if f.sweepErr != nil {
		return f.sweepErr
	}
	f.swept = append(f.swept, path)
	return nil
}

func (f *fakeDisk) GetDirectories(_ string) ([]string, error) {
	if f.getDirsErr != nil {
		return nil, f.getDirsErr
	}
	return f.dirs, nil
}

func (f *fakeDisk) DeleteFolder(path string, recursive bool) error {
	if perr, ok := f.deleteFolderErrs[path]; ok {
		return perr
	}
	if f.deleteFolderErr != nil {
		return f.deleteFolderErr
	}
	f.deletedFolders = append(f.deletedFolders, folderDel{path: path, recursive: recursive})
	return nil
}

func TestNewService_NilConfigOrDisk(t *testing.T) {
	t.Parallel()
	if got := recyclebin.NewService(nil, &fakeDisk{}, nil); got != nil {
		t.Errorf("NewService(nil cfg) = %v, want nil", got)
	}
	if got := recyclebin.NewService(stubConfig{}, nil, nil); got != nil {
		t.Errorf("NewService(nil disk) = %v, want nil", got)
	}
	if got := recyclebin.NewService(stubConfig{}, &fakeDisk{}, nil); got == nil {
		t.Error("NewService(non-nil deps) = nil, want non-nil")
	}
}

func TestDeleteFile_FeatureDisabled_HardDeletes(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{}
	svc := recyclebin.NewService(stubConfig{bin: ""}, d, slog.Default())
	got, err := svc.DeleteFile("/series/show/Episode.mkv", "show")
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if got != "" {
		t.Errorf("recyclePath = %q, want \"\" (feature disabled)", got)
	}
	if len(d.deleted) != 1 || d.deleted[0] != "/series/show/Episode.mkv" {
		t.Errorf("deleted = %v, want one hard-delete of /series/show/Episode.mkv", d.deleted)
	}
	if len(d.moves) != 0 {
		t.Errorf("moves = %v, want 0", d.moves)
	}
}

func TestDeleteFile_FeatureDisabled_WhitespaceTreatedAsEmpty(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{}
	svc := recyclebin.NewService(stubConfig{bin: "   "}, d, slog.Default())
	got, err := svc.DeleteFile("/x/y.mkv", "x")
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if got != "" {
		t.Errorf("recyclePath = %q, want \"\" (whitespace == disabled)", got)
	}
	if len(d.deleted) != 1 {
		t.Errorf("expected hard-delete on whitespace-only RecycleBin")
	}
}

func TestDeleteFile_RecyclesIntoSubfolder(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	src := "/series/MyShow/Season 1/Episode.mkv"
	got, err := svc.DeleteFile(src, "MyShow/Season 1")
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	wantDest := filepath.Join("/recycle", "MyShow/Season 1", "Episode.mkv")
	if got != wantDest {
		t.Errorf("recyclePath = %q, want %q", got, wantDest)
	}
	if len(d.created) != 1 || d.created[0] != filepath.Join("/recycle", "MyShow/Season 1") {
		t.Errorf("created = %v, want one folder", d.created)
	}
	if len(d.moves) != 1 {
		t.Fatalf("moves = %v, want 1", d.moves)
	}
	if d.moves[0] != (move{src: src, dst: wantDest}) {
		t.Errorf("move = %+v, want src=%q dst=%q", d.moves[0], src, wantDest)
	}
	if len(d.deleted) != 0 {
		t.Errorf("hard-delete called when feature is enabled (deleted=%v)", d.deleted)
	}
}

func TestDeleteFile_CollisionAppendsSuffix(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{
		exists: map[string]bool{
			filepath.Join("/recycle/show", "Episode.mkv"):   true,
			filepath.Join("/recycle/show", "Episode_2.mkv"): true,
			// Episode_3.mkv free
		},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	got, err := svc.DeleteFile("/series/show/Episode.mkv", "show")
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	want := filepath.Join("/recycle/show", "Episode_3.mkv")
	if got != want {
		t.Errorf("recyclePath = %q, want %q", got, want)
	}
	if len(d.moves) != 1 || d.moves[0].dst != want {
		t.Errorf("move dst = %v, want %q", d.moves, want)
	}
}

func TestDeleteFile_CollisionWithExtensionlessFile(t *testing.T) {
	t.Parallel()
	// Mirrors C# `:110-113` — when the file has no extension, the
	// suffix appends to the full name.
	d := &fakeDisk{
		exists: map[string]bool{
			filepath.Join("/recycle/show", "README"): true,
		},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	got, err := svc.DeleteFile("/series/show/README", "show")
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	want := filepath.Join("/recycle/show", "README_2")
	if got != want {
		t.Errorf("recyclePath = %q, want %q (no-ext suffix form)", got, want)
	}
}

func TestDeleteFile_CreateFolderError(t *testing.T) {
	t.Parallel()
	bottom := errors.New("permission denied")
	d := &fakeDisk{createErr: bottom}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	got, err := svc.DeleteFile("/x/y.mkv", "x")
	if got != "" {
		t.Errorf("recyclePath = %q, want \"\" on error", got)
	}
	if err == nil {
		t.Fatal("DeleteFile returned nil, want error")
	}
	if !errors.Is(err, recyclebin.ErrRecycleBin) {
		t.Errorf("err not classified as ErrRecycleBin: %v", err)
	}
	if !errors.Is(err, bottom) {
		t.Errorf("err lost the underlying CreateFolder error: %v", err)
	}
	var rbErr *recyclebin.RecycleBinError
	if !errors.As(err, &rbErr) {
		t.Errorf("err not a *RecycleBinError: %v", err)
	} else if rbErr.Op != "create-folder" {
		t.Errorf("rbErr.Op = %q, want %q", rbErr.Op, "create-folder")
	}
}

func TestDeleteFile_MoveError(t *testing.T) {
	t.Parallel()
	bottom := errors.New("disk full")
	d := &fakeDisk{moveErr: bottom}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	got, err := svc.DeleteFile("/x/y.mkv", "x")
	if got != "" {
		t.Errorf("recyclePath = %q, want \"\" on move error", got)
	}
	if !errors.Is(err, recyclebin.ErrRecycleBin) {
		t.Errorf("err not classified as ErrRecycleBin: %v", err)
	}
	if !errors.Is(err, bottom) {
		t.Errorf("err lost underlying disk error: %v", err)
	}
	var rbErr *recyclebin.RecycleBinError
	if !errors.As(err, &rbErr) {
		t.Errorf("err not a *RecycleBinError: %v", err)
	} else if rbErr.Op != "move-file" {
		t.Errorf("rbErr.Op = %q, want %q", rbErr.Op, "move-file")
	}
}

func TestDeleteFile_HardDeleteErrorPropagates(t *testing.T) {
	t.Parallel()
	bottom := errors.New("read-only")
	d := &fakeDisk{deleteErr: bottom}
	svc := recyclebin.NewService(stubConfig{bin: ""}, d, slog.Default())
	_, err := svc.DeleteFile("/x/y.mkv", "x")
	if err == nil {
		t.Fatal("DeleteFile returned nil, want hard-delete error")
	}
	if !errors.Is(err, bottom) {
		t.Errorf("err = %v, want it to wrap %v (C# `:84` doesn't wrap)", err, bottom)
	}
	// Hard-delete arm does NOT classify as ErrRecycleBin per
	// the C# `:84` behaviour — the recycle-bin feature is disabled,
	// so a sentinel about "recycle failed" would be misleading.
	if errors.Is(err, recyclebin.ErrRecycleBin) {
		t.Errorf("err mis-classified as ErrRecycleBin: %v", err)
	}
}

// Compile-time check: Service satisfies the upgrademediafiles
// RecycleBinProvider seam. Without this guard, a refactor to either
// the interface or the Service would fail at the production wire-in
// rather than at build time. Mirrors the convention in
// `mediafileattributes/service_test.go`.
func TestService_SatisfiesUpgradeMediafilesRecycleBinProvider(t *testing.T) {
	t.Parallel()
	var _ upgrademediafiles.RecycleBinProvider = (*recyclebin.Service)(nil)
}

// Compile-time check: *disk.Provider satisfies the local DiskOps
// seam. Lives in the test package so the production binary doesn't
// import disk just to assert.
func TestDiskProvider_SatisfiesDiskOps(t *testing.T) {
	t.Parallel()
	var _ recyclebin.DiskOps = (*disk.Provider)(nil)
}

// --- Cleanup --------------------------------------------------------

func TestCleanup_RecycleBinNotConfigured(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{}
	svc := recyclebin.NewService(stubConfig{bin: ""}, d, slog.Default())
	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// Mirrors C# `:164-167` — early-return without hitting disk.
	if len(d.deleted) != 0 || len(d.swept) != 0 {
		t.Errorf("disk touched: deleted=%v swept=%v", d.deleted, d.swept)
	}
}

func TestCleanup_CleanupDaysZeroIsDisabled(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle", cleanupDays: 0}, d, slog.Default())
	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// Mirrors C# `:172-176` — cleanupDays=0 → return without sweep.
	if len(d.deleted) != 0 || len(d.swept) != 0 {
		t.Errorf("disk touched: deleted=%v swept=%v", d.deleted, d.swept)
	}
}

func TestCleanup_RemovesExpiredFiles(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour)  // 10 days old → expired (cleanupDays=7)
	fresh := now.Add(-2 * 24 * time.Hour) // 2 days old → kept

	d := &fakeDisk{
		files: []string{"/recycle/old.mkv", "/recycle/fresh.mkv"},
		mtimes: map[string]time.Time{
			"/recycle/old.mkv":   old,
			"/recycle/fresh.mkv": fresh,
		},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle", cleanupDays: 7}, d, slog.Default())
	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(d.deleted) != 1 || d.deleted[0] != "/recycle/old.mkv" {
		t.Errorf("deleted = %v, want [/recycle/old.mkv]", d.deleted)
	}
	if len(d.swept) != 1 || d.swept[0] != "/recycle" {
		t.Errorf("swept = %v, want [/recycle]", d.swept)
	}
}

func TestCleanup_BoundaryCondition_ExactlyAtThreshold(t *testing.T) {
	t.Parallel()
	// File mtime exactly at the threshold (mtime + retention == now).
	// Mirrors C# `mtime.AddDays(cleanupDays) > now` — strict greater,
	// so equal-to-threshold means EXPIRED (the > is false, so we don't
	// take the skip branch). The Go port's `mtime.Add(expiry).After(now)`
	// preserves that strict-greater semantics.
	now := time.Now().UTC()
	d := &fakeDisk{
		files:  []string{"/recycle/at.mkv"},
		mtimes: map[string]time.Time{"/recycle/at.mkv": now.Add(-7 * 24 * time.Hour)},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle", cleanupDays: 7}, d, slog.Default())
	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// At-threshold → expired → deleted.
	if len(d.deleted) != 1 {
		t.Errorf("deleted = %v, want one delete (at-threshold should expire)", d.deleted)
	}
}

func TestCleanup_GetFilesError_Propagates(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{getFilesErr: errors.New("boom")}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle", cleanupDays: 7}, d, slog.Default())
	err := svc.Cleanup(context.Background())
	if err == nil {
		t.Fatal("Cleanup returned nil, want GetFiles error")
	}
	if !errors.Is(err, d.getFilesErr) {
		t.Errorf("err = %v, want it to wrap GetFiles error", err)
	}
}

func TestCleanup_PerFileMtimeError_SkipsAndContinues(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour)
	d := &fakeDisk{
		files:    []string{"/recycle/broken.mkv", "/recycle/old.mkv"},
		mtimeErr: map[string]error{"/recycle/broken.mkv": errors.New("stat failed")},
		mtimes:   map[string]time.Time{"/recycle/old.mkv": old},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle", cleanupDays: 7}, d, slog.Default())
	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// broken.mkv skipped, old.mkv deleted.
	if len(d.deleted) != 1 || d.deleted[0] != "/recycle/old.mkv" {
		t.Errorf("deleted = %v, want only /recycle/old.mkv", d.deleted)
	}
}

func TestCleanup_PerFileDeleteError_ContinuesSweep(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour)
	d := &fakeDisk{
		files: []string{"/recycle/locked.mkv", "/recycle/old.mkv"},
		mtimes: map[string]time.Time{
			"/recycle/locked.mkv": old,
			"/recycle/old.mkv":    old,
		},
		deleteErrs: map[string]error{"/recycle/locked.mkv": errors.New("EBUSY")},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle", cleanupDays: 7}, d, slog.Default())
	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// old.mkv deleted; locked.mkv NOT in deleted (perr returned before append).
	if len(d.deleted) != 1 || d.deleted[0] != "/recycle/old.mkv" {
		t.Errorf("deleted = %v, want only /recycle/old.mkv (locked.mkv should fail without aborting)", d.deleted)
	}
	// Sweep still ran.
	if len(d.swept) != 1 {
		t.Errorf("swept = %v, want one (loop should continue past per-file failure)", d.swept)
	}
}

func TestCleanup_RemoveEmptySubfoldersError_Propagates(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{
		files:    []string{},
		sweepErr: errors.New("permission denied"),
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle", cleanupDays: 7}, d, slog.Default())
	err := svc.Cleanup(context.Background())
	if err == nil {
		t.Fatal("Cleanup returned nil, want RemoveEmptySubfolders error")
	}
	if !errors.Is(err, d.sweepErr) {
		t.Errorf("err = %v, want wrapping sweep error", err)
	}
}

func TestCleanup_ContextCancelled(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour)
	d := &fakeDisk{
		files: []string{"/recycle/a.mkv", "/recycle/b.mkv"},
		mtimes: map[string]time.Time{
			"/recycle/a.mkv": old,
			"/recycle/b.mkv": old,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	svc := recyclebin.NewService(stubConfig{bin: "/recycle", cleanupDays: 7}, d, slog.Default())
	err := svc.Cleanup(ctx)
	if err == nil {
		t.Fatal("Cleanup returned nil after cancel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	// No files should have been deleted.
	if len(d.deleted) != 0 {
		t.Errorf("deleted = %v on cancelled ctx, want 0", d.deleted)
	}
}

// CleanupRunner is a test-only assertion that *Service satisfies a
// one-method `Cleanup(ctx) error` interface — the seam the
// CleanUpRecycleBin handler reaches for. Mirrors the convention in
// `mediafileattributes/service_test.go`.
type cleanupRunnerSeam interface {
	Cleanup(ctx context.Context) error
}

func TestService_SatisfiesCleanupRunnerSeam(t *testing.T) {
	t.Parallel()
	var _ cleanupRunnerSeam = (*recyclebin.Service)(nil)
}

// --- Empty -----------------------------------------------------------

func TestEmpty_RecycleBinNotConfigured(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{}
	svc := recyclebin.NewService(stubConfig{bin: ""}, d, slog.Default())
	if err := svc.Empty(context.Background()); err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if len(d.deleted) != 0 || len(d.deletedFolders) != 0 {
		t.Errorf("disk touched: deleted=%v deletedFolders=%v", d.deleted, d.deletedFolders)
	}
}

func TestEmpty_DeletesFoldersAndFiles(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{
		dirs:  []string{"/recycle/show1", "/recycle/show2"},
		files: []string{"/recycle/loose1.mkv", "/recycle/loose2.mkv"},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	if err := svc.Empty(context.Background()); err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if len(d.deletedFolders) != 2 {
		t.Fatalf("deletedFolders = %v, want 2", d.deletedFolders)
	}
	for _, fd := range d.deletedFolders {
		if !fd.recursive {
			t.Errorf("DeleteFolder(%q) recursive=false, want true (matches C# `:151`)", fd.path)
		}
	}
	if len(d.deleted) != 2 {
		t.Errorf("deleted = %v, want 2 files", d.deleted)
	}
}

func TestEmpty_GetDirectoriesError_Propagates(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{getDirsErr: errors.New("boom")}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	err := svc.Empty(context.Background())
	if err == nil {
		t.Fatal("Empty returned nil, want GetDirectories error")
	}
	if !errors.Is(err, d.getDirsErr) {
		t.Errorf("err = %v, want it to wrap GetDirectories error", err)
	}
}

func TestEmpty_GetFilesError_Propagates(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{getFilesErr: errors.New("boom")}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	err := svc.Empty(context.Background())
	if err == nil {
		t.Fatal("Empty returned nil, want GetFiles error")
	}
	if !errors.Is(err, d.getFilesErr) {
		t.Errorf("err = %v, want it to wrap GetFiles error", err)
	}
}

func TestEmpty_PerFolderError_ContinuesSweep(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{
		dirs:             []string{"/recycle/locked", "/recycle/ok"},
		deleteFolderErrs: map[string]error{"/recycle/locked": errors.New("EBUSY")},
		files:            []string{"/recycle/loose.mkv"},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	if err := svc.Empty(context.Background()); err != nil {
		t.Fatalf("Empty: %v", err)
	}
	// /recycle/ok should be deleted; /recycle/locked should NOT be in
	// the delete log (perr returned before append).
	if len(d.deletedFolders) != 1 || d.deletedFolders[0].path != "/recycle/ok" {
		t.Errorf("deletedFolders = %v, want only /recycle/ok", d.deletedFolders)
	}
	// File-sweep arm still runs after the per-folder failure.
	if len(d.deleted) != 1 {
		t.Errorf("deleted = %v, want 1 (file sweep should continue)", d.deleted)
	}
}

func TestEmpty_PerFileError_ContinuesSweep(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{
		dirs:       []string{"/recycle/show"},
		files:      []string{"/recycle/locked.mkv", "/recycle/ok.mkv"},
		deleteErrs: map[string]error{"/recycle/locked.mkv": errors.New("EBUSY")},
	}
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	if err := svc.Empty(context.Background()); err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if len(d.deleted) != 1 || d.deleted[0] != "/recycle/ok.mkv" {
		t.Errorf("deleted = %v, want only /recycle/ok.mkv", d.deleted)
	}
}

func TestEmpty_ContextCancelled(t *testing.T) {
	t.Parallel()
	d := &fakeDisk{
		dirs:  []string{"/recycle/a", "/recycle/b"},
		files: []string{"/recycle/loose.mkv"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := recyclebin.NewService(stubConfig{bin: "/recycle"}, d, slog.Default())
	err := svc.Empty(ctx)
	if err == nil {
		t.Fatal("Empty returned nil after cancel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if len(d.deletedFolders) != 0 {
		t.Errorf("deletedFolders = %v on cancelled ctx, want 0", d.deletedFolders)
	}
}

// GetFiles list-only top-level usage is exercised through the Empty
// recursive=false path — when Empty calls GetFiles(bin, false), the
// fakeDisk's GetFiles ignores the recursive flag because it just
// returns f.files directly. The real disk.Provider's GetFiles
// already enforces recursive=false at top-level (provider.go:93).
