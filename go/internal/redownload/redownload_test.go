package redownload_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/redownload"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type fakeCfg struct {
	auto        bool
	interactive bool
}

func (f fakeCfg) AutoRedownloadFailed() bool                      { return f.auto }
func (f fakeCfg) AutoRedownloadFailedFromInteractiveSearch() bool { return f.interactive }

type fakeEpisodes struct {
	first       *episodes.Episode
	season      []episodes.Episode
	getErr      error
	getSeasErr  error
	getCalls    int
	seasonCalls int
}

func (f *fakeEpisodes) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.first, nil
}

func (f *fakeEpisodes) GetEpisodesBySeason(_ context.Context, _, _ int) ([]episodes.Episode, error) {
	f.seasonCalls++
	if f.getSeasErr != nil {
		return nil, f.getSeasErr
	}
	return f.season, nil
}

type fakeEpSearch struct {
	got   []int
	calls int
	err   error
}

func (f *fakeEpSearch) SearchEpisodes(_ context.Context, ids []int) error {
	f.calls++
	f.got = ids
	return f.err
}

type fakeSeasonSearch struct {
	gotSeries int
	gotSeason int
	calls     int
}

func (f *fakeSeasonSearch) SearchSeason(_ context.Context, seriesID, seasonNumber int) error {
	f.calls++
	f.gotSeries = seriesID
	f.gotSeason = seasonNumber
	return nil
}

func newSvc(t *testing.T, cfg redownload.Config, eps redownload.EpisodeLookup, ep redownload.EpisodeSearcher, se redownload.SeasonSearcher) *redownload.Service {
	t.Helper()
	s, err := redownload.NewService(cfg, eps, ep, se, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

// SkipRedownload set → no search dispatched.
func TestHandle_SkipRedownload_NoSearch(t *testing.T) {
	t.Parallel()
	ep, se := &fakeEpSearch{}, &fakeSeasonSearch{}
	svc := newSvc(t, fakeCfg{auto: true}, &fakeEpisodes{}, ep, se)
	if err := svc.Handle(context.Background(), trackeddownloads.DownloadFailedEvent{EpisodeIDs: []int{1}, SkipRedownload: true}); err != nil {
		t.Fatal(err)
	}
	if ep.calls != 0 || se.calls != 0 {
		t.Errorf("searches dispatched ep=%d season=%d, want 0/0", ep.calls, se.calls)
	}
}

// AutoRedownloadFailed disabled → no search.
func TestHandle_AutoDisabled_NoSearch(t *testing.T) {
	t.Parallel()
	ep, se := &fakeEpSearch{}, &fakeSeasonSearch{}
	svc := newSvc(t, fakeCfg{auto: false}, &fakeEpisodes{}, ep, se)
	if err := svc.Handle(context.Background(), trackeddownloads.DownloadFailedEvent{EpisodeIDs: []int{1}}); err != nil {
		t.Fatal(err)
	}
	if ep.calls != 0 || se.calls != 0 {
		t.Errorf("searches dispatched ep=%d season=%d, want 0/0", ep.calls, se.calls)
	}
}

// InteractiveSearch source + interactive-redownload disabled → skip.
func TestHandle_InteractiveDisabled_NoSearch(t *testing.T) {
	t.Parallel()
	ep, se := &fakeEpSearch{}, &fakeSeasonSearch{}
	svc := newSvc(t, fakeCfg{auto: true, interactive: false}, &fakeEpisodes{}, ep, se)
	ev := trackeddownloads.DownloadFailedEvent{EpisodeIDs: []int{1}, ReleaseSource: decisionengine.ReleaseSourceInteractiveSearch}
	if err := svc.Handle(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if ep.calls != 0 {
		t.Errorf("ep searches=%d, want 0", ep.calls)
	}
}

// InteractiveSearch source + interactive-redownload enabled → searches.
func TestHandle_InteractiveEnabled_Searches(t *testing.T) {
	t.Parallel()
	ep, se := &fakeEpSearch{}, &fakeSeasonSearch{}
	svc := newSvc(t, fakeCfg{auto: true, interactive: true}, &fakeEpisodes{}, ep, se)
	ev := trackeddownloads.DownloadFailedEvent{EpisodeIDs: []int{9}, ReleaseSource: decisionengine.ReleaseSourceInteractiveSearch}
	if err := svc.Handle(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if ep.calls != 1 || len(ep.got) != 1 || ep.got[0] != 9 {
		t.Errorf("ep search calls=%d got=%v, want 1 / [9]", ep.calls, ep.got)
	}
}

// Single episode → per-episode search with that id; no episode lookup.
func TestHandle_SingleEpisode_EpisodeSearch(t *testing.T) {
	t.Parallel()
	ep, se := &fakeEpSearch{}, &fakeSeasonSearch{}
	eps := &fakeEpisodes{}
	svc := newSvc(t, fakeCfg{auto: true}, eps, ep, se)
	if err := svc.Handle(context.Background(), trackeddownloads.DownloadFailedEvent{SeriesID: 5, EpisodeIDs: []int{42}}); err != nil {
		t.Fatal(err)
	}
	if ep.calls != 1 || ep.got[0] != 42 || se.calls != 0 {
		t.Errorf("ep=%d got=%v season=%d, want ep search [42]", ep.calls, ep.got, se.calls)
	}
	if eps.getCalls != 0 || eps.seasonCalls != 0 {
		t.Errorf("episode lookups should be skipped for a single episode (get=%d season=%d)", eps.getCalls, eps.seasonCalls)
	}
}

// Whole-season failure (failed set == season size) → season search.
func TestHandle_WholeSeason_SeasonSearch(t *testing.T) {
	t.Parallel()
	ep, se := &fakeEpSearch{}, &fakeSeasonSearch{}
	eps := &fakeEpisodes{
		first:  &episodes.Episode{ID: 1, SeasonNumber: 3},
		season: []episodes.Episode{{ID: 1}, {ID: 2}, {ID: 3}},
	}
	svc := newSvc(t, fakeCfg{auto: true}, eps, ep, se)
	if err := svc.Handle(context.Background(), trackeddownloads.DownloadFailedEvent{SeriesID: 7, EpisodeIDs: []int{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if se.calls != 1 || se.gotSeries != 7 || se.gotSeason != 3 {
		t.Errorf("season search calls=%d series=%d season=%d, want 1/7/3", se.calls, se.gotSeries, se.gotSeason)
	}
	if ep.calls != 0 {
		t.Errorf("ep search calls=%d, want 0 (whole season)", ep.calls)
	}
}

// Partial multi-episode (double episode: set < season size) → episode search.
func TestHandle_PartialSeason_EpisodeSearch(t *testing.T) {
	t.Parallel()
	ep, se := &fakeEpSearch{}, &fakeSeasonSearch{}
	eps := &fakeEpisodes{
		first:  &episodes.Episode{ID: 1, SeasonNumber: 3},
		season: []episodes.Episode{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}},
	}
	svc := newSvc(t, fakeCfg{auto: true}, eps, ep, se)
	if err := svc.Handle(context.Background(), trackeddownloads.DownloadFailedEvent{SeriesID: 7, EpisodeIDs: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if ep.calls != 1 || len(ep.got) != 2 || se.calls != 0 {
		t.Errorf("ep=%d got=%v season=%d, want ep search [1 2]", ep.calls, ep.got, se.calls)
	}
}

// Empty episode id set → no search (Go-port guard over C#'s First()).
func TestHandle_NoEpisodes_NoSearch(t *testing.T) {
	t.Parallel()
	ep, se := &fakeEpSearch{}, &fakeSeasonSearch{}
	svc := newSvc(t, fakeCfg{auto: true}, &fakeEpisodes{}, ep, se)
	if err := svc.Handle(context.Background(), trackeddownloads.DownloadFailedEvent{EpisodeIDs: nil}); err != nil {
		t.Fatal(err)
	}
	if ep.calls != 0 || se.calls != 0 {
		t.Errorf("searches dispatched ep=%d season=%d, want 0/0", ep.calls, se.calls)
	}
}

// Episode-lookup error propagates.
func TestHandle_LookupError_Propagates(t *testing.T) {
	t.Parallel()
	eps := &fakeEpisodes{getErr: errors.New("boom")}
	svc := newSvc(t, fakeCfg{auto: true}, eps, &fakeEpSearch{}, &fakeSeasonSearch{})
	if err := svc.Handle(context.Background(), trackeddownloads.DownloadFailedEvent{SeriesID: 1, EpisodeIDs: []int{1, 2}}); err == nil {
		t.Error("want error from GetEpisode, got nil")
	}
}

// nil deps rejected.
func TestNewService_NilDeps(t *testing.T) {
	t.Parallel()
	if _, err := redownload.NewService(nil, &fakeEpisodes{}, &fakeEpSearch{}, &fakeSeasonSearch{}, nil); err == nil {
		t.Error("want error for nil config")
	}
}
