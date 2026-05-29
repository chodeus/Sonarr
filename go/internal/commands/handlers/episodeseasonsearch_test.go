package handlers

import (
	"context"
	"errors"
	"testing"
)

type fakeSearchGrabber struct {
	epCalls     []int
	seasonCalls []struct {
		series, season int
		missingOnly    bool
	}
	epDecisions int
	seasonDecs  int
	epErrOn     int // episode id that errors (0 = none)
	seasonErr   error
}

func (f *fakeSearchGrabber) EpisodeSearch(_ context.Context, id int) (int, error) {
	f.epCalls = append(f.epCalls, id)
	if id == f.epErrOn {
		return 0, errors.New("boom")
	}
	return f.epDecisions, nil
}
func (f *fakeSearchGrabber) SeasonSearch(_ context.Context, series, season int, missingOnly bool) (int, error) {
	f.seasonCalls = append(f.seasonCalls, struct {
		series, season int
		missingOnly    bool
	}{series, season, missingOnly})
	return f.seasonDecs, f.seasonErr
}

func TestEpisodeSearch_SearchesEachIDAndGrabs(t *testing.T) {
	t.Parallel()
	f := &fakeSearchGrabber{epDecisions: 2}
	if err := NewEpisodeSearch(f, nil).Execute(context.Background(), `{"episodeIds":[10,20,30]}`); err != nil {
		t.Fatal(err)
	}
	if len(f.epCalls) != 3 || f.epCalls[0] != 10 || f.epCalls[2] != 30 {
		t.Errorf("EpisodeSearch calls = %v, want [10 20 30]", f.epCalls)
	}
}

func TestEpisodeSearch_PerEpisodeErrorAbsorbed(t *testing.T) {
	t.Parallel()
	f := &fakeSearchGrabber{epErrOn: 20}
	// One failing id must not abort the run or fail Execute.
	if err := NewEpisodeSearch(f, nil).Execute(context.Background(), `{"episodeIds":[10,20,30]}`); err != nil {
		t.Fatalf("Execute should absorb per-episode errors: %v", err)
	}
	if len(f.epCalls) != 3 {
		t.Errorf("all 3 ids should be attempted, got %v", f.epCalls)
	}
}

func TestEpisodeSearch_EmptyAndNil(t *testing.T) {
	t.Parallel()
	f := &fakeSearchGrabber{}
	if err := NewEpisodeSearch(f, nil).Execute(context.Background(), `{"episodeIds":[]}`); err != nil {
		t.Fatal(err)
	}
	if len(f.epCalls) != 0 {
		t.Errorf("empty episodeIds → no search calls, got %v", f.epCalls)
	}
	// nil searcher → stub no-op.
	if err := NewEpisodeSearch(nil, nil).Execute(context.Background(), `{"episodeIds":[1]}`); err != nil {
		t.Fatalf("nil searcher should no-op: %v", err)
	}
}

func TestSeasonSearch_SearchesSeasonMissingOnlyFalse(t *testing.T) {
	t.Parallel()
	f := &fakeSearchGrabber{seasonDecs: 1}
	if err := NewSeasonSearch(f, nil).Execute(context.Background(), `{"seriesId":5,"seasonNumber":3}`); err != nil {
		t.Fatal(err)
	}
	if len(f.seasonCalls) != 1 || f.seasonCalls[0].series != 5 || f.seasonCalls[0].season != 3 || f.seasonCalls[0].missingOnly {
		t.Errorf("SeasonSearch call = %+v, want {5 3 false}", f.seasonCalls)
	}
}

func TestSeasonSearch_MissingSeriesIDNoOp_AndErrorPropagates(t *testing.T) {
	t.Parallel()
	f := &fakeSearchGrabber{}
	if err := NewSeasonSearch(f, nil).Execute(context.Background(), `{"seasonNumber":3}`); err != nil {
		t.Fatal(err)
	}
	if len(f.seasonCalls) != 0 {
		t.Errorf("missing seriesId → no search, got %v", f.seasonCalls)
	}
	fe := &fakeSearchGrabber{seasonErr: errors.New("db down")}
	if err := NewSeasonSearch(fe, nil).Execute(context.Background(), `{"seriesId":5,"seasonNumber":1}`); err == nil {
		t.Error("SeasonSearch error should propagate")
	}
}
