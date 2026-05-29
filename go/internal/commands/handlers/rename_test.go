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
