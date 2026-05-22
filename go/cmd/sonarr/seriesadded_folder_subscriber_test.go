package main

import (
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// fakeFolderCfg stubs seriesAddedFolderConfig with two-bool state.
type fakeFolderCfg struct {
	createEmpty bool
	deleteEmpty bool
}

func (f fakeFolderCfg) CreateEmptySeriesFolders() bool { return f.createEmpty }
func (f fakeFolderCfg) DeleteEmptyFolders() bool       { return f.deleteEmpty }

// fakeFolderDisk stubs seriesAddedFolderDisk. exists controls the
// FolderExists return; createErr is returned from CreateFolder when
// non-nil. Tracks invocations via the counter fields.
type fakeFolderDisk struct {
	exists      bool
	createErr   error
	folderCalls atomic.Int32
	createCalls atomic.Int32
	lastCreated atomic.Value // string
}

func (f *fakeFolderDisk) FolderExists(path string) bool {
	f.folderCalls.Add(1)
	_ = path
	return f.exists
}

func (f *fakeFolderDisk) CreateFolder(path string) error {
	f.createCalls.Add(1)
	f.lastCreated.Store(path)
	return f.createErr
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// J185: gate ON, folder missing, paths set → CreateFolder fires.
func TestSeriesAddedFolderCreate_HappyPath_CreatesFolder(t *testing.T) {
	t.Parallel()
	cfg := fakeFolderCfg{createEmpty: true, deleteEmpty: false}
	disk := &fakeFolderDisk{exists: false}
	ev := series.SeriesAddedEvent{Series: &series.Series{ID: 42, Path: "/tv/Some Show"}}

	handleSeriesAddedForFolderCreate(t.Context(), ev, cfg, disk, newTestLogger())

	if got := disk.createCalls.Load(); got != 1 {
		t.Errorf("CreateFolder calls = %d, want 1", got)
	}
	if got, _ := disk.lastCreated.Load().(string); got != "/tv/Some Show" {
		t.Errorf("CreateFolder path = %q, want /tv/Some Show", got)
	}
}

// J185: gate OFF (CreateEmptySeriesFolders=false, the default) →
// subscriber is a no-op. Mirrors the C# `:102` else-arm log-only
// posture.
func TestSeriesAddedFolderCreate_GateOff_NoOp(t *testing.T) {
	t.Parallel()
	cfg := fakeFolderCfg{createEmpty: false}
	disk := &fakeFolderDisk{}
	ev := series.SeriesAddedEvent{Series: &series.Series{ID: 42, Path: "/tv/Some Show"}}

	handleSeriesAddedForFolderCreate(t.Context(), ev, cfg, disk, newTestLogger())

	if got := disk.createCalls.Load(); got != 0 {
		t.Errorf("CreateFolder calls = %d, want 0 (gate off)", got)
	}
}

// J185: both flags on → no-op (folder would immediately be
// deleted by the CleanUpRecycleBin job; mirrors C# `:106-109`).
func TestSeriesAddedFolderCreate_DeleteEmptyOn_NoOp(t *testing.T) {
	t.Parallel()
	cfg := fakeFolderCfg{createEmpty: true, deleteEmpty: true}
	disk := &fakeFolderDisk{exists: false}
	ev := series.SeriesAddedEvent{Series: &series.Series{ID: 42, Path: "/tv/Some Show"}}

	handleSeriesAddedForFolderCreate(t.Context(), ev, cfg, disk, newTestLogger())

	if got := disk.createCalls.Load(); got != 0 {
		t.Errorf("CreateFolder calls = %d, want 0 (DeleteEmpty would drop the folder)", got)
	}
}

// J185: folder already exists → no CreateFolder call (idempotency
// at the subscriber level; underlying disk.Provider.CreateFolder is
// also idempotent via MkdirAll but skipping the call avoids a
// pointless syscall).
func TestSeriesAddedFolderCreate_FolderExists_NoCreate(t *testing.T) {
	t.Parallel()
	cfg := fakeFolderCfg{createEmpty: true}
	disk := &fakeFolderDisk{exists: true}
	ev := series.SeriesAddedEvent{Series: &series.Series{ID: 42, Path: "/tv/Some Show"}}

	handleSeriesAddedForFolderCreate(t.Context(), ev, cfg, disk, newTestLogger())

	if got := disk.createCalls.Load(); got != 0 {
		t.Errorf("CreateFolder calls = %d, want 0 (folder already exists)", got)
	}
	if got := disk.folderCalls.Load(); got != 1 {
		t.Errorf("FolderExists calls = %d, want 1", got)
	}
}

// J185: empty Path field → no-op (no on-disk folder to create).
func TestSeriesAddedFolderCreate_EmptyPath_NoOp(t *testing.T) {
	t.Parallel()
	cfg := fakeFolderCfg{createEmpty: true}
	disk := &fakeFolderDisk{}
	ev := series.SeriesAddedEvent{Series: &series.Series{ID: 42, Path: ""}}

	handleSeriesAddedForFolderCreate(t.Context(), ev, cfg, disk, newTestLogger())

	if got := disk.createCalls.Load(); got != 0 {
		t.Errorf("CreateFolder calls = %d, want 0 (no path)", got)
	}
	if got := disk.folderCalls.Load(); got != 0 {
		t.Errorf("FolderExists calls = %d, want 0 (short-circuit before disk check)", got)
	}
}

// J185: nil Series defensive guard (shouldn't happen — every emit
// site sets the field — but the subscriber should not panic).
func TestSeriesAddedFolderCreate_NilSeries_NoOp(t *testing.T) {
	t.Parallel()
	cfg := fakeFolderCfg{createEmpty: true}
	disk := &fakeFolderDisk{}
	ev := series.SeriesAddedEvent{Series: nil}

	handleSeriesAddedForFolderCreate(t.Context(), ev, cfg, disk, newTestLogger())

	if got := disk.createCalls.Load(); got != 0 {
		t.Errorf("CreateFolder calls = %d, want 0 (nil series)", got)
	}
}

// J185: CreateFolder errors are absorbed (warn log + return). The
// subscriber doesn't re-publish or fail the bus pipeline; a next
// scheduled DiskScan will retry. Mirrors the existing Phase 4
// "warn-and-continue" subscriber posture.
func TestSeriesAddedFolderCreate_CreateError_Absorbed(t *testing.T) {
	t.Parallel()
	cfg := fakeFolderCfg{createEmpty: true}
	disk := &fakeFolderDisk{createErr: errors.New("EACCES")}
	ev := series.SeriesAddedEvent{Series: &series.Series{ID: 42, Path: "/tv/Some Show"}}

	// Should NOT panic + should NOT bubble up an error.
	handleSeriesAddedForFolderCreate(t.Context(), ev, cfg, disk, newTestLogger())

	if got := disk.createCalls.Load(); got != 1 {
		t.Errorf("CreateFolder calls = %d, want 1", got)
	}
}
