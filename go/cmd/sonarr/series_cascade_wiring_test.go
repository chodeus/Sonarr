package main

import (
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// fakeCascadeRecycleBin records DeleteFolder calls and can fail on
// demand. Satisfies seriesFolderRecycler.
type fakeCascadeRecycleBin struct {
	deleted []string
	err     error
}

func (f *fakeCascadeRecycleBin) DeleteFolder(path string) error {
	f.deleted = append(f.deleted, path)
	return f.err
}

// fakeCascadeDisk records FolderExists lookups (true for any path in the
// set). MoveFolder/DeleteFolder are unused by DeleteCascade after J495 but
// must exist to satisfy seriesFolderCleaner.
type fakeCascadeDisk struct {
	exists map[string]bool
}

func (f *fakeCascadeDisk) FolderExists(p string) bool      { return f.exists[p] }
func (f *fakeCascadeDisk) DeleteFolder(string, bool) error { return nil }
func (f *fakeCascadeDisk) MoveFolder(_, _ string) error    { return nil }

// newCascadeAdapter builds a seriesCascadeAdapter over a real SQLite DB
// with real series/episodes/episodefiles services, plus the supplied disk
// + recycle-bin fakes. Returns the adapter and the inserted series rows
// keyed by title for id lookups.
func newCascadeAdapter(t *testing.T, disk seriesFolderCleaner, rb seriesFolderRecycler, rows ...*series.Series) (*seriesCascadeAdapter, map[string]int) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	bus := messaging.New(slog.Default())

	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	seriesSvc, err := series.NewService(seriesRepo, bus)
	if err != nil {
		t.Fatalf("series.NewService: %v", err)
	}
	episodesRepo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("episodes.NewRepo: %v", err)
	}
	episodesSvc, err := episodes.NewService(episodesRepo, bus)
	if err != nil {
		t.Fatalf("episodes.NewService: %v", err)
	}
	efRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("episodefiles.NewRepo: %v", err)
	}
	efSvc, err := episodefiles.NewService(efRepo, bus)
	if err != nil {
		t.Fatalf("episodefiles.NewService: %v", err)
	}

	ids := map[string]int{}
	for _, r := range rows {
		inserted, err := seriesRepo.Insert(t.Context(), r)
		if err != nil {
			t.Fatalf("insert series %q: %v", r.Title, err)
		}
		ids[r.Title] = inserted.ID
	}

	a := &seriesCascadeAdapter{
		Service:      seriesSvc,
		episodes:     episodesSvc,
		episodefiles: efSvc,
		disk:         disk,
		recycleBin:   rb,
		logger:       slog.Default(),
	}
	return a, ids
}

func mkSeries(tvdb int, title, clean, path string) *series.Series {
	return &series.Series{TvdbID: tvdb, Title: title, CleanTitle: clean, Path: path, Monitored: true, Runtime: 30}
}

// J495: the happy path — a standalone series folder is routed through the
// recycle bin and the DB row is removed.
func TestDeleteCascade_RecyclesFolder_AndDeletesRow(t *testing.T) {
	t.Parallel()
	disk := &fakeCascadeDisk{exists: map[string]bool{"/tv/Show": true}}
	rb := &fakeCascadeRecycleBin{}
	a, ids := newCascadeAdapter(t, disk, rb, mkSeries(1, "Show", "show", "/tv/Show"))

	if err := a.DeleteCascade(t.Context(), ids["Show"]); err != nil {
		t.Fatalf("DeleteCascade: %v", err)
	}
	if len(rb.deleted) != 1 || rb.deleted[0] != "/tv/Show" {
		t.Fatalf("recycle DeleteFolder = %v, want [/tv/Show]", rb.deleted)
	}
	if got, _ := a.GetSeries(t.Context(), ids["Show"]); got != nil {
		t.Errorf("series row still present after cascade: %+v", got)
	}
}

// J495: when the series path is a PARENT of another series' path, the
// folder must NOT be deleted (would clobber the child series) — but the DB
// cascade still proceeds. Ports MediaFileDeletionService.cs:192-196.
func TestDeleteCascade_ParentOfAnotherSeries_SkipsFolderDelete(t *testing.T) {
	t.Parallel()
	disk := &fakeCascadeDisk{exists: map[string]bool{"/tv": true}}
	rb := &fakeCascadeRecycleBin{}
	a, ids := newCascadeAdapter(t, disk, rb,
		mkSeries(1, "Parent", "parent", "/tv"),
		mkSeries(2, "Child", "child", "/tv/Child"),
	)

	if err := a.DeleteCascade(t.Context(), ids["Parent"]); err != nil {
		t.Fatalf("DeleteCascade: %v", err)
	}
	if len(rb.deleted) != 0 {
		t.Errorf("folder should NOT be recycled when parent of another series, got %v", rb.deleted)
	}
	// DB row still removed.
	if got, _ := a.GetSeries(t.Context(), ids["Parent"]); got != nil {
		t.Errorf("series row should still be deleted from DB, got %+v", got)
	}
}

// J495: when the series path EQUALS another series' path, the folder must
// NOT be deleted. Ports MediaFileDeletionService.cs:198-202.
func TestDeleteCascade_SamePathAsAnotherSeries_SkipsFolderDelete(t *testing.T) {
	t.Parallel()
	disk := &fakeCascadeDisk{exists: map[string]bool{"/tv/Show": true}}
	rb := &fakeCascadeRecycleBin{}
	a, ids := newCascadeAdapter(t, disk, rb,
		mkSeries(1, "A", "a", "/tv/Show"),
		mkSeries(2, "B", "b", "/tv/Show/"), // same after filepath.Clean
	)

	if err := a.DeleteCascade(t.Context(), ids["A"]); err != nil {
		t.Fatalf("DeleteCascade: %v", err)
	}
	if len(rb.deleted) != 0 {
		t.Errorf("folder should NOT be recycled when sharing a path with another series, got %v", rb.deleted)
	}
}

// J495: a missing on-disk folder (FolderExists=false) is tolerated — the
// recycle step is skipped (C#:205) and the DB cascade still runs.
func TestDeleteCascade_MissingFolder_SkipsRecycle_StillCascades(t *testing.T) {
	t.Parallel()
	disk := &fakeCascadeDisk{exists: map[string]bool{}} // folder absent
	rb := &fakeCascadeRecycleBin{}
	a, ids := newCascadeAdapter(t, disk, rb, mkSeries(1, "Gone", "gone", "/tv/Gone"))

	if err := a.DeleteCascade(t.Context(), ids["Gone"]); err != nil {
		t.Fatalf("DeleteCascade: %v", err)
	}
	if len(rb.deleted) != 0 {
		t.Errorf("recycle should be skipped for a missing folder, got %v", rb.deleted)
	}
	if got, _ := a.GetSeries(t.Context(), ids["Gone"]); got != nil {
		t.Errorf("series row should be deleted, got %+v", got)
	}
}

// J495: a recycle-bin error is best-effort — logged, not propagated, and
// the DB cascade still completes. Mirrors the C# DiskProvider failure
// posture (on-disk delete failures don't abort the row cleanup).
func TestDeleteCascade_RecycleError_IsBestEffort(t *testing.T) {
	t.Parallel()
	disk := &fakeCascadeDisk{exists: map[string]bool{"/tv/Boom": true}}
	rb := &fakeCascadeRecycleBin{err: errors.New("permission denied")}
	a, ids := newCascadeAdapter(t, disk, rb, mkSeries(1, "Boom", "boom", "/tv/Boom"))

	if err := a.DeleteCascade(t.Context(), ids["Boom"]); err != nil {
		t.Fatalf("DeleteCascade should swallow recycle errors, got %v", err)
	}
	if len(rb.deleted) != 1 {
		t.Errorf("recycle should have been attempted, got %v", rb.deleted)
	}
	if got, _ := a.GetSeries(t.Context(), ids["Boom"]); got != nil {
		t.Errorf("series row should be deleted despite recycle error, got %+v", got)
	}
}

// pathEquals: cleaned-string equality (trailing slash / dot segments).
func TestPathEquals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"/tv/Show", "/tv/Show", true},
		{"/tv/Show", "/tv/Show/", true},
		{"/tv/Show", "/tv/./Show", true},
		{"/tv/Show", "/tv/Other", false},
		{"/tv/Show", "/tv/Show/S01", false},
	}
	for _, c := range cases {
		if got := pathEquals(c.a, c.b); got != c.want {
			t.Errorf("pathEquals(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Compile-time guards: the production deps satisfy the narrow interfaces.
var (
	_ seriesFolderRecycler = (*fakeCascadeRecycleBin)(nil)
	_ seriesFolderCleaner  = (*fakeCascadeDisk)(nil)
)
