package housekeeping_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/extrafiles"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/mediafiles"
)

// fakeExtraFileSweep records every method call + serves canned data.
type fakeExtraFileSweep struct {
	bySeries  map[int][]extrafiles.ExtraFile
	allErr    error
	deleted   []int
	deleteErr error
}

func (f *fakeExtraFileSweep) AllBySeriesID(_ context.Context, seriesID int) ([]extrafiles.ExtraFile, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.bySeries[seriesID], nil
}

func (f *fakeExtraFileSweep) DeleteByIDs(_ context.Context, ids []int) (int64, error) {
	if f.deleteErr != nil {
		return 0, f.deleteErr
	}
	f.deleted = append(f.deleted, ids...)
	return int64(len(ids)), nil
}

type fakeSeriesPaths struct {
	paths map[int]string
	err   error
}

func (f *fakeSeriesPaths) AllSeriesPaths(_ context.Context) (map[int]string, error) {
	return f.paths, f.err
}

// realFilter wires the production mediafiles.FilterPaths into the
// housekeeper's PathFilter shape. Pinning filterExtras=true matches
// the C# 2-arg overload's default.
func realFilter(base string, paths []string) ([]string, error) {
	return mediafiles.FilterPaths(base, paths, true)
}

func TestCleanupExtraFilesExcluded_DeletesExtrasInExcludedSubfolder(t *testing.T) {
	t.Parallel()
	repo := &fakeExtraFileSweep{
		bySeries: map[int][]extrafiles.ExtraFile{
			7: {
				{ID: 1, SeriesID: 7, RelativePath: "Season 01/poster.jpg"},      // kept
				{ID: 2, SeriesID: 7, RelativePath: "Extras/trailer.mp4"},        // excluded subfolder
				{ID: 3, SeriesID: 7, RelativePath: "Season 01/.DS_Store"},       // excluded file
				{ID: 4, SeriesID: 7, RelativePath: "Season 02/episode-art.jpg"}, // kept
			},
		},
	}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}

	task, err := housekeeping.NewCleanupExtraFilesInExcludedFolders(repo, paths, realFilter, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := task.Name(); got != "CleanupExtraFilesInExcludedFolders" {
		t.Errorf("Name() = %q", got)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 2 {
		t.Fatalf("deleted = %v, want 2 ids", repo.deleted)
	}
	got := map[int]bool{}
	for _, id := range repo.deleted {
		got[id] = true
	}
	if !got[2] || !got[3] {
		t.Errorf("expected ids 2 + 3 deleted; got %v", repo.deleted)
	}
}

func TestCleanupExtraFilesExcluded_NoExtras_IsNoOp(t *testing.T) {
	t.Parallel()
	repo := &fakeExtraFileSweep{bySeries: map[int][]extrafiles.ExtraFile{7: {}}}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}

	task, err := housekeeping.NewCleanupExtraFilesInExcludedFolders(repo, paths, realFilter, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted should be empty; got %v", repo.deleted)
	}
}

func TestCleanupExtraFilesExcluded_AllExtrasSurvive_IsNoOp(t *testing.T) {
	t.Parallel()
	// All extras live in non-excluded folders → kept-set equals input
	// → no DeleteByIDs call. Verifies the early-continue gate at the
	// per-series body.
	repo := &fakeExtraFileSweep{
		bySeries: map[int][]extrafiles.ExtraFile{
			7: {
				{ID: 1, RelativePath: "Season 01/poster.jpg"},
				{ID: 2, RelativePath: "Season 02/banner.png"},
			},
		},
	}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	task, err := housekeeping.NewCleanupExtraFilesInExcludedFolders(repo, paths, realFilter, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("deleted should be empty when nothing is filtered out; got %v", repo.deleted)
	}
}

func TestCleanupExtraFilesExcluded_CSharpBugIsFixed(t *testing.T) {
	t.Parallel()
	// Regression guard for the C# bug documented in the package
	// doc-comment. The C# implementation would mark ALL extras for
	// deletion when any extra is in an excluded folder; the fix
	// only deletes the ones that actually failed the filter.
	repo := &fakeExtraFileSweep{
		bySeries: map[int][]extrafiles.ExtraFile{
			7: {
				{ID: 1, RelativePath: "Extras/trailer.mp4"}, // filtered out
				{ID: 2, RelativePath: "Season 01/poster.jpg"},
				{ID: 3, RelativePath: "Season 01/banner.png"},
			},
		},
	}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	task, err := housekeeping.NewCleanupExtraFilesInExcludedFolders(repo, paths, realFilter, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 1 {
		t.Errorf("deleted = %v, want [1] (C# would have deleted all three)", repo.deleted)
	}
}

func TestCleanupExtraFilesExcluded_AllSeriesPathsErrorPropagates(t *testing.T) {
	t.Parallel()
	repo := &fakeExtraFileSweep{}
	paths := &fakeSeriesPaths{err: errors.New("EIO")}
	task, err := housekeeping.NewCleanupExtraFilesInExcludedFolders(repo, paths, realFilter, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from AllSeriesPaths")
	}
	if !errors.Is(err, paths.err) {
		t.Errorf("error chain missing source: %v", err)
	}
}

func TestCleanupExtraFilesExcluded_AllBySeriesIDErrorPropagates(t *testing.T) {
	t.Parallel()
	repo := &fakeExtraFileSweep{allErr: errors.New("EIO")}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	task, _ := housekeeping.NewCleanupExtraFilesInExcludedFolders(repo, paths, realFilter, slog.Default())
	err := task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from AllBySeriesID")
	}
	if !errors.Is(err, repo.allErr) {
		t.Errorf("error chain missing source: %v", err)
	}
}

func TestCleanupExtraFilesExcluded_DeleteErrorPropagates(t *testing.T) {
	t.Parallel()
	repo := &fakeExtraFileSweep{
		bySeries: map[int][]extrafiles.ExtraFile{
			7: {{ID: 1, RelativePath: "Extras/trailer.mp4"}},
		},
		deleteErr: errors.New("EIO"),
	}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/Show"}}
	task, _ := housekeeping.NewCleanupExtraFilesInExcludedFolders(repo, paths, realFilter, slog.Default())
	err := task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from DeleteByIDs")
	}
	if !errors.Is(err, repo.deleteErr) {
		t.Errorf("error chain missing source: %v", err)
	}
}

func TestCleanupExtraFilesExcluded_FilterPaths_BadBase_Propagates(t *testing.T) {
	t.Parallel()
	repo := &fakeExtraFileSweep{
		bySeries: map[int][]extrafiles.ExtraFile{
			7: {{ID: 1, RelativePath: "x"}},
		},
	}
	// Series path "/tv/A" but the filter would be asked to relativise
	// fullPaths under "/tv/A" — mismatch only happens when a different
	// base is injected. Inject a deliberately-broken filter to assert
	// the error propagates without surfacing as deletion.
	broken := func(_ string, _ []string) ([]string, error) {
		return nil, errors.New("filter exploded")
	}
	paths := &fakeSeriesPaths{paths: map[int]string{7: "/tv/A"}}
	task, _ := housekeeping.NewCleanupExtraFilesInExcludedFolders(repo, paths, broken, slog.Default())
	err := task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from broken filter")
	}
	if !strings.Contains(err.Error(), "filter exploded") {
		t.Errorf("error chain missing filter err: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Errorf("nothing should have been deleted on filter error; got %v", repo.deleted)
	}
}

func TestCleanupExtraFilesExcluded_ConstructorGuards(t *testing.T) {
	t.Parallel()
	repo := &fakeExtraFileSweep{}
	paths := &fakeSeriesPaths{}
	cases := []struct {
		name        string
		repo        housekeeping.ExtraFileSweepRepo
		paths       housekeeping.SeriesPaths
		filter      housekeeping.PathFilter
		expectError bool
	}{
		{"nil repo", nil, paths, realFilter, true},
		{"nil paths", repo, nil, realFilter, true},
		{"nil filter", repo, paths, nil, true},
		{"all set", repo, paths, realFilter, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := housekeeping.NewCleanupExtraFilesInExcludedFolders(c.repo, c.paths, c.filter, slog.Default())
			if c.expectError && err == nil {
				t.Errorf("%s: expected error", c.name)
			}
			if !c.expectError && err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
		})
	}
}

// Sanity check: the realFilter wiring actually filters something.
func TestRealFilter_SmokeTest(t *testing.T) {
	t.Parallel()
	base := "/tv/Show"
	in := []string{
		filepath.Join(base, "Extras/trailer.mp4"),
		filepath.Join(base, "Season 01/poster.jpg"),
	}
	out, err := realFilter(base, in)
	if err != nil {
		t.Fatalf("realFilter: %v", err)
	}
	if len(out) != 1 || out[0] != in[1] {
		t.Errorf("realFilter dropped wrong set: in=%v out=%v", in, out)
	}
}
