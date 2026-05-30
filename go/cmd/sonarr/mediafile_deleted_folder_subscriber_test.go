package main

import (
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/series"
)

// fakeDeleteCleanupCfg stubs deletedFolderCleanupConfig.
type fakeDeleteCleanupCfg struct{ deleteEmpty bool }

func (f fakeDeleteCleanupCfg) DeleteEmptyFolders() bool { return f.deleteEmpty }

// fakeDeleteCleanupDisk records the folder-pruning calls.
type fakeDeleteCleanupDisk struct {
	exists     map[string]bool // FolderExists lookups
	emptyPaths map[string]bool // FolderEmpty returns
	swept      []string        // RemoveEmptySubfolders calls (in order)
	deleted    []string        // DeleteFolder calls
	sweepErr   error
	deleteErr  error
}

func (f *fakeDeleteCleanupDisk) FolderExists(p string) bool { return f.exists[p] }
func (f *fakeDeleteCleanupDisk) FolderEmpty(p string) bool  { return f.emptyPaths[p] }
func (f *fakeDeleteCleanupDisk) RemoveEmptySubfolders(p string) error {
	f.swept = append(f.swept, p)
	return f.sweepErr
}
func (f *fakeDeleteCleanupDisk) DeleteFolder(p string, _ bool) error {
	f.deleted = append(f.deleted, p)
	return f.deleteErr
}

func mkDeletedEvent(seriesPath, relPath string, reason episodefiles.DeleteMediaFileReason) episodefiles.EpisodeFileDeletedEvent {
	return episodefiles.EpisodeFileDeletedEvent{
		EpisodeFile: &episodefiles.File{RelativePath: relPath},
		Series:      &series.Series{ID: 1, Path: seriesPath},
		Reason:      reason,
	}
}

// J496: DeleteEmptyFolders off → no disk touch (C#:218).
func TestMediaFileDeletedCleanup_GateOff_NoOp(t *testing.T) {
	t.Parallel()
	disk := &fakeDeleteCleanupDisk{}
	ev := mkDeletedEvent("/tv/Show", "Season 01/ep.mkv", episodefiles.ReasonManual)
	handleMediaFileDeletedForFolderCleanup(t.Context(), ev, fakeDeleteCleanupCfg{deleteEmpty: false}, disk, newTestLogger())
	if len(disk.swept) != 0 || len(disk.deleted) != 0 {
		t.Errorf("expected no disk activity, swept=%v deleted=%v", disk.swept, disk.deleted)
	}
}

// J496: reason MissingFromDisk → no disk touch (C#:218); the file already
// vanished and the folder may still hold other files.
func TestMediaFileDeletedCleanup_MissingFromDisk_NoOp(t *testing.T) {
	t.Parallel()
	disk := &fakeDeleteCleanupDisk{}
	ev := mkDeletedEvent("/tv/Show", "Season 01/ep.mkv", episodefiles.ReasonMissingFromDisk)
	handleMediaFileDeletedForFolderCleanup(t.Context(), ev, fakeDeleteCleanupCfg{deleteEmpty: true}, disk, newTestLogger())
	if len(disk.swept) != 0 || len(disk.deleted) != 0 {
		t.Errorf("expected no disk activity, swept=%v deleted=%v", disk.swept, disk.deleted)
	}
}

// J496: nil Series → no-op (can't compute the series path).
func TestMediaFileDeletedCleanup_NilSeries_NoOp(t *testing.T) {
	t.Parallel()
	disk := &fakeDeleteCleanupDisk{}
	ev := episodefiles.EpisodeFileDeletedEvent{EpisodeFile: &episodefiles.File{RelativePath: "x.mkv"}, Reason: episodefiles.ReasonManual}
	handleMediaFileDeletedForFolderCleanup(t.Context(), ev, fakeDeleteCleanupCfg{deleteEmpty: true}, disk, newTestLogger())
	if len(disk.swept) != 0 || len(disk.deleted) != 0 {
		t.Errorf("expected no disk activity, swept=%v deleted=%v", disk.swept, disk.deleted)
	}
}

// J496: happy path — walk up from the file's parent to the series root,
// prune each level, then sweep the root. Series folder not empty → not
// deleted.
func TestMediaFileDeletedCleanup_PrunesUpToSeriesRoot(t *testing.T) {
	t.Parallel()
	disk := &fakeDeleteCleanupDisk{
		exists:     map[string]bool{"/tv/Show/Season 01": true},
		emptyPaths: map[string]bool{}, // series root NOT empty
	}
	ev := mkDeletedEvent("/tv/Show", "Season 01/ep.mkv", episodefiles.ReasonManual)
	handleMediaFileDeletedForFolderCleanup(t.Context(), ev, fakeDeleteCleanupCfg{deleteEmpty: true}, disk, newTestLogger())

	// "/tv/Show/Season 01" pruned (it's under the series root and exists),
	// then "/tv/Show" swept. The loop stops when folder reaches the root.
	want := []string{"/tv/Show/Season 01", "/tv/Show"}
	if len(disk.swept) != len(want) {
		t.Fatalf("swept = %v, want %v", disk.swept, want)
	}
	for i := range want {
		if disk.swept[i] != want[i] {
			t.Errorf("swept[%d] = %q, want %q", i, disk.swept[i], want[i])
		}
	}
	if len(disk.deleted) != 0 {
		t.Errorf("series root not empty → should not be deleted, got %v", disk.deleted)
	}
}

// J496: when the series folder ends up empty, it is deleted outright
// (C#:239-242).
func TestMediaFileDeletedCleanup_DeletesEmptySeriesRoot(t *testing.T) {
	t.Parallel()
	disk := &fakeDeleteCleanupDisk{
		exists:     map[string]bool{"/tv/Show/Season 01": true},
		emptyPaths: map[string]bool{"/tv/Show": true},
	}
	ev := mkDeletedEvent("/tv/Show", "Season 01/ep.mkv", episodefiles.ReasonManual)
	handleMediaFileDeletedForFolderCleanup(t.Context(), ev, fakeDeleteCleanupCfg{deleteEmpty: true}, disk, newTestLogger())

	if len(disk.deleted) != 1 || disk.deleted[0] != "/tv/Show" {
		t.Errorf("empty series root should be deleted, got %v", disk.deleted)
	}
}

// J496: a file directly in the series root prunes only the root (no
// intermediate subfolder to walk).
func TestMediaFileDeletedCleanup_FileInSeriesRoot(t *testing.T) {
	t.Parallel()
	disk := &fakeDeleteCleanupDisk{exists: map[string]bool{}, emptyPaths: map[string]bool{}}
	ev := mkDeletedEvent("/tv/Show", "ep.mkv", episodefiles.ReasonManual)
	handleMediaFileDeletedForFolderCleanup(t.Context(), ev, fakeDeleteCleanupCfg{deleteEmpty: true}, disk, newTestLogger())

	if len(disk.swept) != 1 || disk.swept[0] != "/tv/Show" {
		t.Errorf("only series root should be swept, got %v", disk.swept)
	}
}

// Compile-time guards: production deps satisfy the narrow interfaces.
var (
	_ deletedFolderCleanupConfig = fakeDeleteCleanupCfg{}
	_ deletedFolderCleanupDisk   = (*fakeDeleteCleanupDisk)(nil)
)
