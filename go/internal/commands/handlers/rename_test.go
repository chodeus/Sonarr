package handlers_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/series"
)

type fakeRenameSeries struct{ byID map[int]*series.Series }

func (f fakeRenameSeries) GetSeries(_ context.Context, id int) (*series.Series, error) {
	return f.byID[id], nil
}

type fakeRenameFiles struct {
	byIDs    []episodefiles.File
	bySeries map[int][]episodefiles.File
}

func (f fakeRenameFiles) GetFiles(_ context.Context, _ []int) ([]episodefiles.File, error) {
	return f.byIDs, nil
}
func (f fakeRenameFiles) GetFilesBySeries(_ context.Context, seriesID int) ([]episodefiles.File, error) {
	return f.bySeries[seriesID], nil
}

type fakeMover struct {
	moved     []int
	sameForID map[int]bool
}

func (f *fakeMover) MoveEpisodeFile(_ context.Context, file *episodefiles.File, _ *series.Series) (*episodefiles.File, error) {
	if f.sameForID[file.ID] {
		return nil, episodefilemoving.ErrSameFilename
	}
	f.moved = append(f.moved, file.ID)
	cp := *file
	cp.RelativePath = "renamed/" + file.RelativePath
	return &cp, nil
}

type fakeRowUpdater struct{ updated []int }

func (f *fakeRowUpdater) Update(_ context.Context, file *episodefiles.File) error {
	f.updated = append(f.updated, file.ID)
	return nil
}

func renameDeps(mover *fakeMover, upd *fakeRowUpdater, fl fakeRenameFiles) handlers.RenameDeps {
	return handlers.RenameDeps{
		Series:  fakeRenameSeries{byID: map[int]*series.Series{1: {ID: 1, Path: "/tv/Show"}}},
		Files:   fl,
		Mover:   mover,
		Updater: upd,
	}
}

type fakeRenamePublisher struct {
	fileEvents     []episodefiles.EpisodeFileRenamedEvent
	seriesEvents   []episodefiles.SeriesRenamedEvent
	completedCount int
}

func (p *fakeRenamePublisher) PublishEpisodeFileRenamed(_ context.Context, ev episodefiles.EpisodeFileRenamedEvent) error {
	p.fileEvents = append(p.fileEvents, ev)
	return nil
}

func (p *fakeRenamePublisher) PublishSeriesRenamed(_ context.Context, ev episodefiles.SeriesRenamedEvent) error {
	p.seriesEvents = append(p.seriesEvents, ev)
	return nil
}

func (p *fakeRenamePublisher) PublishRenameCompleted(_ context.Context, _ episodefiles.RenameCompletedEvent) error {
	p.completedCount++
	return nil
}

type fakeEmptySubfolderRemover struct {
	paths []string
	err   error
}

func (f *fakeEmptySubfolderRemover) RemoveEmptySubfolders(path string) error {
	f.paths = append(f.paths, path)
	return f.err
}

// TestRenameFiles_RemovesEmptySubfolders_J463 pins the C# `:173`
// post-rename cleanup: after at least one file is renamed,
// RemoveEmptySubfolders(series.Path) runs exactly once, before the
// SeriesRenamedEvent (the ordering C# uses).
func TestRenameFiles_RemovesEmptySubfolders_J463(t *testing.T) {
	t.Parallel()
	mover := &fakeMover{}
	upd := &fakeRowUpdater{}
	fl := fakeRenameFiles{byIDs: []episodefiles.File{{ID: 10, SeriesID: 1, RelativePath: "a.mkv"}}}
	disk := &fakeEmptySubfolderRemover{}
	pub := &fakeRenamePublisher{}
	deps := renameDeps(mover, upd, fl)
	deps.Disk = disk
	deps.Publisher = pub
	h, _ := handlers.NewRenameFiles(deps, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":1,"files":[10]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(disk.paths) != 1 || disk.paths[0] != "/tv/Show" {
		t.Errorf("RemoveEmptySubfolders calls = %v, want [/tv/Show]", disk.paths)
	}
	if len(pub.seriesEvents) != 1 {
		t.Errorf("SeriesRenamedEvent should still fire after cleanup; got %d", len(pub.seriesEvents))
	}
}

// TestRenameFiles_NoRename_NoSubfolderCleanup_J463 pins that the cleanup
// is gated on at least one rename (C# `:172` `if (renamed.Any())`).
func TestRenameFiles_NoRename_NoSubfolderCleanup_J463(t *testing.T) {
	t.Parallel()
	mover := &fakeMover{sameForID: map[int]bool{10: true}} // same-filename → no move
	upd := &fakeRowUpdater{}
	fl := fakeRenameFiles{byIDs: []episodefiles.File{{ID: 10, SeriesID: 1, RelativePath: "a.mkv"}}}
	disk := &fakeEmptySubfolderRemover{}
	deps := renameDeps(mover, upd, fl)
	deps.Disk = disk
	h, _ := handlers.NewRenameFiles(deps, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":1,"files":[10]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(disk.paths) != 0 {
		t.Errorf("no rename → no subfolder cleanup; got %v", disk.paths)
	}
}

func TestRename_NilDeps(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewRenameFiles(handlers.RenameDeps{}, nil); err == nil {
		t.Fatal("expected error from nil deps (RenameFiles)")
	}
	if _, err := handlers.NewRenameSeries(handlers.RenameDeps{}, nil); err == nil {
		t.Fatal("expected error from nil deps (RenameSeries)")
	}
}

func TestRenameFiles_MovesAndUpdatesEach(t *testing.T) {
	t.Parallel()
	mover := &fakeMover{}
	upd := &fakeRowUpdater{}
	fl := fakeRenameFiles{byIDs: []episodefiles.File{{ID: 10, SeriesID: 1, RelativePath: "a.mkv"}, {ID: 11, SeriesID: 1, RelativePath: "b.mkv"}}}
	h, _ := handlers.NewRenameFiles(renameDeps(mover, upd, fl), slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":1,"files":[10,11]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(mover.moved) != 2 || len(upd.updated) != 2 {
		t.Errorf("moved=%v updated=%v, want both [10 11]", mover.moved, upd.updated)
	}
}

func TestRenameFiles_PublishesEvents_J453(t *testing.T) {
	t.Parallel()
	mover := &fakeMover{}
	upd := &fakeRowUpdater{}
	fl := fakeRenameFiles{byIDs: []episodefiles.File{{ID: 10, SeriesID: 1, RelativePath: "a.mkv"}, {ID: 11, SeriesID: 1, RelativePath: "b.mkv"}}}
	pub := &fakeRenamePublisher{}
	deps := renameDeps(mover, upd, fl)
	deps.Publisher = pub
	h, _ := handlers.NewRenameFiles(deps, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":1,"files":[10,11]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// One per-file event each, with the absolute pre-move path.
	if len(pub.fileEvents) != 2 {
		t.Fatalf("file events = %d, want 2", len(pub.fileEvents))
	}
	if pub.fileEvents[0].OriginalPath != "/tv/Show/a.mkv" || pub.fileEvents[1].OriginalPath != "/tv/Show/b.mkv" {
		t.Errorf("file event OriginalPaths = %q,%q", pub.fileEvents[0].OriginalPath, pub.fileEvents[1].OriginalPath)
	}
	// One per-series event carrying both renamed files + prior paths.
	if len(pub.seriesEvents) != 1 {
		t.Fatalf("series events = %d, want 1", len(pub.seriesEvents))
	}
	se := pub.seriesEvents[0]
	if se.Series == nil || se.Series.ID != 1 || len(se.RenamedFiles) != 2 {
		t.Fatalf("series event = %+v, want series 1 + 2 renamed files", se)
	}
	if se.RenamedFiles[0].PreviousRelativePath != "a.mkv" || se.RenamedFiles[0].PreviousPath != "/tv/Show/a.mkv" {
		t.Errorf("renamed[0] prior paths = %q / %q", se.RenamedFiles[0].PreviousRelativePath, se.RenamedFiles[0].PreviousPath)
	}
	if pub.completedCount != 1 {
		t.Errorf("RenameCompletedEvent fired %d times, want 1", pub.completedCount)
	}
}

func TestRenameFiles_NoFilesRenamed_NoSeriesEvent_J453(t *testing.T) {
	t.Parallel()
	mover := &fakeMover{sameForID: map[int]bool{10: true, 11: true}} // both same-filename → no move
	upd := &fakeRowUpdater{}
	fl := fakeRenameFiles{byIDs: []episodefiles.File{{ID: 10, SeriesID: 1, RelativePath: "a.mkv"}, {ID: 11, SeriesID: 1, RelativePath: "b.mkv"}}}
	pub := &fakeRenamePublisher{}
	deps := renameDeps(mover, upd, fl)
	deps.Publisher = pub
	h, _ := handlers.NewRenameFiles(deps, slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":1,"files":[10,11]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pub.fileEvents) != 0 || len(pub.seriesEvents) != 0 {
		t.Errorf("no files renamed → expected no events, got file=%d series=%d", len(pub.fileEvents), len(pub.seriesEvents))
	}
	// RenameCompletedEvent fires UNCONDITIONALLY at the end of the command
	// (C# publishes it even when nothing was renamed).
	if pub.completedCount != 1 {
		t.Errorf("RenameCompletedEvent should fire once even with no renames, got %d", pub.completedCount)
	}
}

func TestRenameFiles_SameFilenameSkipsUpdate(t *testing.T) {
	t.Parallel()
	mover := &fakeMover{sameForID: map[int]bool{10: true}}
	upd := &fakeRowUpdater{}
	fl := fakeRenameFiles{byIDs: []episodefiles.File{{ID: 10, SeriesID: 1, RelativePath: "a.mkv"}, {ID: 11, SeriesID: 1, RelativePath: "b.mkv"}}}
	h, _ := handlers.NewRenameFiles(renameDeps(mover, upd, fl), slog.Default())
	if err := h.Execute(t.Context(), `{"seriesId":1,"files":[10,11]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(mover.moved) != 1 || mover.moved[0] != 11 {
		t.Errorf("moved=%v, want [11] (10 was same-filename)", mover.moved)
	}
	if len(upd.updated) != 1 || upd.updated[0] != 11 {
		t.Errorf("updated=%v, want [11]", upd.updated)
	}
}

func TestRenameFiles_EmptyBodyNoop(t *testing.T) {
	t.Parallel()
	mover := &fakeMover{}
	h, _ := handlers.NewRenameFiles(renameDeps(mover, &fakeRowUpdater{}, fakeRenameFiles{}), slog.Default())
	for _, body := range []string{``, `{}`, `{"seriesId":1}`, `{"files":[10]}`} {
		if err := h.Execute(t.Context(), body); err != nil {
			t.Fatalf("Execute(%q): %v", body, err)
		}
	}
	if len(mover.moved) != 0 {
		t.Errorf("no moves expected; got %v", mover.moved)
	}
}

func TestRenameSeries_RenamesAllFilesPerSeries(t *testing.T) {
	t.Parallel()
	mover := &fakeMover{}
	upd := &fakeRowUpdater{}
	fl := fakeRenameFiles{bySeries: map[int][]episodefiles.File{
		1: {{ID: 20, SeriesID: 1, RelativePath: "x.mkv"}, {ID: 21, SeriesID: 1, RelativePath: "y.mkv"}},
	}}
	h, _ := handlers.NewRenameSeries(renameDeps(mover, upd, fl), slog.Default())
	if err := h.Execute(t.Context(), `{"seriesIds":[1]}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(mover.moved) != 2 || len(upd.updated) != 2 {
		t.Errorf("moved=%v updated=%v, want both files", mover.moved, upd.updated)
	}
}
