package recyclebin_test

import (
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/recyclebin"
	"sonarr.tv/sonarr/internal/upgrademediafiles"
)

type stubConfig struct {
	bin string
}

func (s stubConfig) RecycleBin() string { return s.bin }

type fakeDisk struct {
	deleted     []string
	created     []string
	moves       []move
	exists      map[string]bool
	deleteErr   error
	createErr   error
	moveErr     error
	existsHooks func(path string) bool
}

type move struct{ src, dst string }

func (f *fakeDisk) DeleteFile(path string) error {
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
