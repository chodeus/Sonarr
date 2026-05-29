package main

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/series"
)

type fakeMoveDisk struct {
	folders map[string]bool
	moved   [][2]string
	moveErr error
	created []string
}

func (f *fakeMoveDisk) FolderExists(p string) bool { return f.folders[p] }
func (f *fakeMoveDisk) CreateFolder(p string) error {
	f.created = append(f.created, p)
	return nil
}
func (f *fakeMoveDisk) MoveFolder(src, dst string) error {
	if f.moveErr != nil {
		return f.moveErr
	}
	f.moved = append(f.moved, [2]string{src, dst})
	return nil
}

type fakeMoveNaming struct{}

func (fakeMoveNaming) GetConfig(context.Context) (*namingconfig.Config, error) {
	return &namingconfig.Config{SeriesFolderFormat: "{Series Title}"}, nil
}

func newMoveSeriesSvc(t *testing.T) (*series.Service, *series.Series) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	svc, err := series.NewService(repo, messaging.New(slog.Default()))
	if err != nil {
		t.Fatalf("series.NewService: %v", err)
	}
	ser := &series.Series{TvdbID: 11, Title: "Show", CleanTitle: "show", Path: "/old/Show", Monitored: true, Runtime: 30}
	if _, err := repo.Insert(t.Context(), ser); err != nil {
		t.Fatalf("insert series: %v", err)
	}
	return svc, ser
}

// J393: MoveSeries moves the folder and persists the new Path.
func TestSeriesMoveAdapter_MoveSeries_J393(t *testing.T) {
	t.Parallel()
	svc, ser := newMoveSeriesSvc(t)
	disk := &fakeMoveDisk{folders: map[string]bool{"/old/Show": true}}
	a := &seriesMoveAdapter{series: svc, naming: fakeMoveNaming{}, disk: disk, log: slog.Default()}
	if err := a.MoveSeries(t.Context(), ser.ID, "/old/Show", "/new/Show"); err != nil {
		t.Fatalf("MoveSeries: %v", err)
	}
	if len(disk.moved) != 1 || disk.moved[0] != [2]string{"/old/Show", "/new/Show"} {
		t.Errorf("moved = %v", disk.moved)
	}
	got, _ := svc.GetSeries(t.Context(), ser.ID)
	if got.Path != "/new/Show" {
		t.Errorf("series Path = %q, want /new/Show", got.Path)
	}
}

// J393: a failed move reverts the persisted Path to the source.
func TestSeriesMoveAdapter_MoveFailReverts_J393(t *testing.T) {
	t.Parallel()
	svc, ser := newMoveSeriesSvc(t) // seeded Path=/old/Show
	disk := &fakeMoveDisk{folders: map[string]bool{"/old/Show": true}, moveErr: context.DeadlineExceeded}
	a := &seriesMoveAdapter{series: svc, naming: fakeMoveNaming{}, disk: disk, log: slog.Default()}
	if err := a.MoveSeries(t.Context(), ser.ID, "/old/Show", "/new/Show"); err != nil {
		t.Fatalf("MoveSeries: %v", err)
	}
	got, _ := svc.GetSeries(t.Context(), ser.ID)
	if got.Path != "/old/Show" {
		t.Errorf("series Path = %q, want reverted to /old/Show", got.Path)
	}
}

// J393: source folder missing → skip (no move, Path unchanged).
func TestSeriesMoveAdapter_SourceMissingSkips_J393(t *testing.T) {
	t.Parallel()
	svc, ser := newMoveSeriesSvc(t)
	disk := &fakeMoveDisk{folders: map[string]bool{}} // source absent
	a := &seriesMoveAdapter{series: svc, naming: fakeMoveNaming{}, disk: disk, log: slog.Default()}
	if err := a.MoveSeries(t.Context(), ser.ID, "/old/Show", "/new/Show"); err != nil {
		t.Fatalf("MoveSeries: %v", err)
	}
	if len(disk.moved) != 0 {
		t.Errorf("no move expected; got %v", disk.moved)
	}
}

// J393: BulkMoveSeries computes dest = destRoot + GetSeriesFolder.
func TestSeriesMoveAdapter_BulkMove_J393(t *testing.T) {
	t.Parallel()
	svc, ser := newMoveSeriesSvc(t) // Title "Show", Path /old/Show
	disk := &fakeMoveDisk{folders: map[string]bool{"/old/Show": true}}
	a := &seriesMoveAdapter{series: svc, naming: fakeMoveNaming{}, disk: disk, log: slog.Default()}
	items := []handlers.BulkMoveSeriesItem{{SeriesID: ser.ID, SourcePath: "/old/Show"}}
	if err := a.BulkMoveSeries(t.Context(), items, "/newroot"); err != nil {
		t.Fatalf("BulkMoveSeries: %v", err)
	}
	if len(disk.moved) != 1 || disk.moved[0] != [2]string{"/old/Show", "/newroot/Show"} {
		t.Errorf("moved = %v, want [/old/Show -> /newroot/Show]", disk.moved)
	}
}
