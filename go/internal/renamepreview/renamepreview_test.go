package renamepreview

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

func intp(i int) *int { return &i }

// fakeData implements all three getter interfaces over in-memory maps.
type fakeData struct {
	series map[int]*series.Series
	eps    map[int][]episodes.Episode // by seriesID
	files  map[int][]episodefiles.File
	err    error
}

func (f *fakeData) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.series[id], nil
}
func (f *fakeData) GetMany(_ context.Context, ids []int) ([]series.Series, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []series.Series
	for _, id := range ids {
		if s := f.series[id]; s != nil {
			out = append(out, *s)
		}
	}
	return out, nil
}
func (f *fakeData) GetEpisodesBySeries(_ context.Context, seriesID int) ([]episodes.Episode, error) {
	return f.eps[seriesID], f.err
}
func (f *fakeData) GetEpisodesBySeason(_ context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error) {
	var out []episodes.Episode
	for _, e := range f.eps[seriesID] {
		if e.SeasonNumber == seasonNumber {
			out = append(out, e)
		}
	}
	return out, f.err
}
func (f *fakeData) GetEpisodesByMultipleSeries(_ context.Context, seriesIDs []int) ([]episodes.Episode, error) {
	var out []episodes.Episode
	for _, id := range seriesIDs {
		out = append(out, f.eps[id]...)
	}
	return out, f.err
}
func (f *fakeData) GetFilesBySeries(_ context.Context, seriesID int) ([]episodefiles.File, error) {
	return f.files[seriesID], f.err
}
func (f *fakeData) GetFilesBySeason(_ context.Context, seriesID, seasonNumber int) ([]episodefiles.File, error) {
	var out []episodefiles.File
	for _, fl := range f.files[seriesID] {
		if fl.SeasonNumber == seasonNumber {
			out = append(out, fl)
		}
	}
	return out, f.err
}
func (f *fakeData) GetFilesBySeriesIDs(_ context.Context, seriesIDs []int) ([]episodefiles.File, error) {
	var out []episodefiles.File
	for _, id := range seriesIDs {
		out = append(out, f.files[id]...)
	}
	return out, f.err
}

// renameAllBuilder returns a fixed target relative to the series
// folder, prefixed "renamed/" — so every file with a linked episode
// "needs" renaming unless its RelativePath already starts with it.
type renameAllBuilder struct{}

func (renameAllBuilder) BuildFilePath(eps []episodes.Episode, s *series.Series, file *episodefiles.File, extension string) (string, error) {
	return filepath.Join(s.Path, "renamed", file.RelativePath), nil
}

// identityBuilder returns the current path unchanged — nothing renames.
type identityBuilder struct{}

func (identityBuilder) BuildFilePath(_ []episodes.Episode, s *series.Series, file *episodefiles.File, _ string) (string, error) {
	return filepath.Join(s.Path, file.RelativePath), nil
}

func newFake() *fakeData {
	return &fakeData{
		series: map[int]*series.Series{
			1: {ID: 1, Path: "/tv/Show"},
		},
		eps: map[int][]episodes.Episode{
			1: {
				{ID: 10, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 1, EpisodeFileID: intp(100)},
				{ID: 11, SeriesID: 1, SeasonNumber: 1, EpisodeNumber: 2, EpisodeFileID: intp(101)},
				{ID: 12, SeriesID: 1, SeasonNumber: 2, EpisodeNumber: 1, EpisodeFileID: intp(102)},
				{ID: 13, SeriesID: 1, SeasonNumber: 2, EpisodeNumber: 2, EpisodeFileID: nil},
			},
		},
		files: map[int][]episodefiles.File{
			1: {
				{ID: 100, SeriesID: 1, SeasonNumber: 1, RelativePath: "S01/e01.mkv"},
				{ID: 101, SeriesID: 1, SeasonNumber: 1, RelativePath: "S01/e02.mkv"},
				{ID: 102, SeriesID: 1, SeasonNumber: 2, RelativePath: "S02/e01.mkv"},
				{ID: 199, SeriesID: 1, SeasonNumber: 2, RelativePath: "S02/orphan.mkv"}, // no linked episode
			},
		},
	}
}

func TestGetBySeries_DiffsAndSorts(t *testing.T) {
	t.Parallel()
	f := newFake()
	svc := NewService(f, f, f, renameAllBuilder{}, nil)
	got, err := svc.GetBySeries(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	// 3 files have a linked episode (100,101,102); the orphan (199) is
	// skipped (logged warning). All three differ → 3 previews.
	if len(got) != 3 {
		t.Fatalf("got %d previews, want 3: %+v", len(got), got)
	}
	// Sorted desc by season then first-episode: S02E01, S01E02, S01E01.
	wantOrder := []struct {
		season, file int
	}{{2, 102}, {1, 101}, {1, 100}}
	for i, w := range wantOrder {
		if got[i].SeasonNumber != w.season || got[i].EpisodeFileID != w.file {
			t.Errorf("row %d: got season=%d file=%d, want season=%d file=%d", i, got[i].SeasonNumber, got[i].EpisodeFileID, w.season, w.file)
		}
	}
	// NewPath is relative to series folder.
	if got[0].NewPath != filepath.Join("renamed", "S02/e01.mkv") {
		t.Errorf("NewPath = %q", got[0].NewPath)
	}
	if got[0].ExistingPath != "S02/e01.mkv" {
		t.Errorf("ExistingPath = %q", got[0].ExistingPath)
	}
	if len(got[0].EpisodeNumbers) != 1 || got[0].EpisodeNumbers[0] != 1 {
		t.Errorf("EpisodeNumbers = %v", got[0].EpisodeNumbers)
	}
}

func TestGetBySeries_NoRenamesWhenPathsMatch(t *testing.T) {
	t.Parallel()
	f := newFake()
	svc := NewService(f, f, f, identityBuilder{}, nil)
	got, err := svc.GetBySeries(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d previews, want 0 (paths identical): %+v", len(got), got)
	}
}

func TestGetBySeason_FiltersAndSorts(t *testing.T) {
	t.Parallel()
	f := newFake()
	svc := NewService(f, f, f, renameAllBuilder{}, nil)
	got, err := svc.GetBySeason(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d previews, want 2 (season 1 only): %+v", len(got), got)
	}
	// Desc by first episode: e02 (file 101) before e01 (file 100).
	if got[0].EpisodeFileID != 101 || got[1].EpisodeFileID != 100 {
		t.Errorf("order: %d, %d", got[0].EpisodeFileID, got[1].EpisodeFileID)
	}
}

func TestGetBulk_SortsBySeriesThenSeasonThenEpisode(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.series[2] = &series.Series{ID: 2, Path: "/tv/Other"}
	f.eps[2] = []episodes.Episode{
		{ID: 20, SeriesID: 2, SeasonNumber: 1, EpisodeNumber: 1, EpisodeFileID: intp(200)},
	}
	f.files[2] = []episodefiles.File{
		{ID: 200, SeriesID: 2, SeasonNumber: 1, RelativePath: "S01/e01.mkv"},
	}
	svc := NewService(f, f, f, renameAllBuilder{}, nil)
	got, err := svc.GetBulk(context.Background(), []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d previews, want 4: %+v", len(got), got)
	}
	// Series 2 sorts first (desc by SeriesId).
	if got[0].SeriesID != 2 {
		t.Errorf("first row SeriesID = %d, want 2", got[0].SeriesID)
	}
	if got[1].SeriesID != 1 || got[1].SeasonNumber != 2 {
		t.Errorf("second row = series %d season %d, want series 1 season 2", got[1].SeriesID, got[1].SeasonNumber)
	}
}

func TestGetBySeries_PropagatesError(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.err = errors.New("db down")
	svc := NewService(f, f, f, renameAllBuilder{}, nil)
	if _, err := svc.GetBySeries(context.Background(), 1); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetBySeries_NilSeriesYieldsEmpty(t *testing.T) {
	t.Parallel()
	f := newFake()
	delete(f.series, 1)
	svc := NewService(f, f, f, renameAllBuilder{}, nil)
	got, err := svc.GetBySeries(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d previews for missing series, want 0", len(got))
	}
}
