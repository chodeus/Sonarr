package main

import (
	"context"
	"log/slog"
	"testing"

	v3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

type fakeEFSeries struct{ ser *series.Series }

func (f fakeEFSeries) GetSeries(context.Context, int) (*series.Series, error) { return f.ser, nil }

type fakeEFRoot struct{ path string }

func (f fakeEFRoot) GetBestRootFolderPath(context.Context, string) (string, error) {
	return f.path, nil
}

type fakeEFRecycle struct{ moved []string }

func (f *fakeEFRecycle) DeleteFile(path, _ string) (string, error) {
	f.moved = append(f.moved, path)
	return "", nil
}

type fakeEFDisk struct {
	folders map[string]bool
	files   map[string]bool
	dirs    map[string][]string
}

func (f fakeEFDisk) FolderExists(p string) bool { return f.folders[p] }
func (f fakeEFDisk) FileExists(p string) bool   { return f.files[p] }
func (f fakeEFDisk) GetDirectories(p string) ([]string, error) {
	return f.dirs[p], nil
}

// realEpisodeFilesSvc builds a SQLite-backed episodefiles.Service and
// seeds one file, returning the service + the stored row.
func realEpisodeFilesSvc(t *testing.T) (*episodefiles.Service, *episodefiles.File) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("episodefiles.NewRepo: %v", err)
	}
	svc, err := episodefiles.NewService(repo, messaging.New(slog.Default()))
	if err != nil {
		t.Fatalf("episodefiles.NewService: %v", err)
	}
	stored, err := svc.Add(t.Context(), &episodefiles.File{
		SeriesID: 1, SeasonNumber: 1, RelativePath: "Show.S01E01.mkv",
	})
	if err != nil {
		t.Fatalf("seed episodefile: %v", err)
	}
	return svc, stored
}

// J390: happy path — the physical file is recycled, then the DB row is
// removed. Asserts the recycle-bin received the full path and the row
// is gone afterward.
func TestEpisodeFileDeleteAdapter_RecyclesThenDeletes_J390(t *testing.T) {
	t.Parallel()
	svc, row := realEpisodeFilesSvc(t)
	ser := &series.Series{ID: 1, Path: "/tv/Show"}
	full := "/tv/Show/Show.S01E01.mkv"
	recycle := &fakeEFRecycle{}
	a := &episodeFileDeleteAdapter{
		Service:     svc,
		series:      fakeEFSeries{ser: ser},
		rootFolders: fakeEFRoot{path: "/tv"},
		recycleBin:  recycle,
		disk: fakeEFDisk{
			folders: map[string]bool{"/tv": true, "/tv/Show": true},
			files:   map[string]bool{full: true},
			dirs:    map[string][]string{"/tv": {"/tv/Show"}},
		},
		log: slog.Default(),
	}
	if err := a.DeleteEpisodeFileWithDisk(t.Context(), row, episodefiles.ReasonManual); err != nil {
		t.Fatalf("DeleteEpisodeFileWithDisk: %v", err)
	}
	if len(recycle.moved) != 1 || recycle.moved[0] != full {
		t.Errorf("recycled = %v, want [%s]", recycle.moved, full)
	}
	if _, err := svc.GetFile(t.Context(), row.ID); err == nil {
		t.Error("DB row should be deleted after recycle")
	}
}

// J390: a missing root folder aborts before any recycle or DB delete,
// returning the 409 sentinel.
func TestEpisodeFileDeleteAdapter_RootFolderMissing_J390(t *testing.T) {
	t.Parallel()
	svc, row := realEpisodeFilesSvc(t)
	recycle := &fakeEFRecycle{}
	a := &episodeFileDeleteAdapter{
		Service:     svc,
		series:      fakeEFSeries{ser: &series.Series{ID: 1, Path: "/tv/Show"}},
		rootFolders: fakeEFRoot{path: "/tv"},
		recycleBin:  recycle,
		disk:        fakeEFDisk{folders: map[string]bool{}}, // /tv does not exist
		log:         slog.Default(),
	}
	if err := a.DeleteEpisodeFileWithDisk(t.Context(), row, episodefiles.ReasonManual); err != v3.ErrEpisodeFileRootFolderInvalid {
		t.Fatalf("err = %v, want ErrEpisodeFileRootFolderInvalid", err)
	}
	if len(recycle.moved) != 0 {
		t.Errorf("should not recycle when root invalid; got %v", recycle.moved)
	}
	if _, err := svc.GetFile(t.Context(), row.ID); err != nil {
		t.Error("DB row should survive when root folder invalid")
	}
}

// J390: empty root folder (exists but no subdirectories) → 409 sentinel.
func TestEpisodeFileDeleteAdapter_RootFolderEmpty_J390(t *testing.T) {
	t.Parallel()
	svc, row := realEpisodeFilesSvc(t)
	a := &episodeFileDeleteAdapter{
		Service:     svc,
		series:      fakeEFSeries{ser: &series.Series{ID: 1, Path: "/tv/Show"}},
		rootFolders: fakeEFRoot{path: "/tv"},
		recycleBin:  &fakeEFRecycle{},
		disk:        fakeEFDisk{folders: map[string]bool{"/tv": true}, dirs: map[string][]string{"/tv": {}}},
		log:         slog.Default(),
	}
	if err := a.DeleteEpisodeFileWithDisk(t.Context(), row, episodefiles.ReasonManual); err != v3.ErrEpisodeFileRootFolderInvalid {
		t.Fatalf("err = %v, want ErrEpisodeFileRootFolderInvalid", err)
	}
}

// J390: when the physical file isn't present the DB row is still removed
// (C# "clean up even if the file was already deleted").
func TestEpisodeFileDeleteAdapter_FileAbsentStillDeletesRow_J390(t *testing.T) {
	t.Parallel()
	svc, row := realEpisodeFilesSvc(t)
	recycle := &fakeEFRecycle{}
	a := &episodeFileDeleteAdapter{
		Service:     svc,
		series:      fakeEFSeries{ser: &series.Series{ID: 1, Path: "/tv/Show"}},
		rootFolders: fakeEFRoot{path: "/tv"},
		recycleBin:  recycle,
		disk: fakeEFDisk{
			folders: map[string]bool{"/tv": true, "/tv/Show": true},
			files:   map[string]bool{}, // file not on disk
			dirs:    map[string][]string{"/tv": {"/tv/Show"}},
		},
		log: slog.Default(),
	}
	if err := a.DeleteEpisodeFileWithDisk(t.Context(), row, episodefiles.ReasonManual); err != nil {
		t.Fatalf("DeleteEpisodeFileWithDisk: %v", err)
	}
	if len(recycle.moved) != 0 {
		t.Errorf("no recycle expected when file absent; got %v", recycle.moved)
	}
	if _, err := svc.GetFile(t.Context(), row.ID); err == nil {
		t.Error("DB row should be deleted even when file absent")
	}
}
