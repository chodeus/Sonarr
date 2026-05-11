package parsingservice_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/scenemappings"
	"sonarr.tv/sonarr/internal/series"
)

// --- stubs ---------------------------------------------------------

type stubFinder struct {
	sceneCallReturns []episodes.Episode
	sceneCallErr     error
	sceneCalled      bool
	sceneCallArgs    [3]int

	titleCallReturns *episodes.Episode
	titleCallErr     error
	titleCalled      bool
	titleCallArgs    struct {
		seriesID, seasonNumber int
		releaseTitle           string
	}

	// Per-method canned outputs for the GetEpisodes pipeline. Each
	// is a [4]int → result map so multiple calls with different
	// args can return different results in a single test.
	findEpisodeMap                 map[[3]int]*episodes.Episode
	findEpisodeErr                 error
	findEpisodeCalls               [][3]int
	findEpisodeByAbsoluteOut       *episodes.Episode
	findEpisodeByAbsoluteErr       error
	findEpisodesBySceneAbsoluteOut []episodes.Episode
	findEpisodesBySceneAbsoluteErr error
	findEpisodeByAirDateOut        *episodes.Episode
	findEpisodeByAirDateErr        error
	findEpisodeByAirDateCallArgs   struct {
		seriesID int
		date     string
		part     *int
	}
	getEpisodesBySeasonOut        []episodes.Episode
	getEpisodesBySeasonErr        error
	getEpisodesBySeasonCalls      [][2]int
	getEpisodesBySceneSeasonOut   []episodes.Episode
	getEpisodesBySceneSeasonErr   error
	getEpisodesBySceneSeasonCalls [][2]int
}

func (s *stubFinder) FindEpisodesBySceneNumbering(_ context.Context, seriesID, sceneSeason, sceneEpisode int) ([]episodes.Episode, error) {
	s.sceneCalled = true
	s.sceneCallArgs = [3]int{seriesID, sceneSeason, sceneEpisode}
	return s.sceneCallReturns, s.sceneCallErr
}

func (s *stubFinder) FindEpisodeByTitle(_ context.Context, seriesID, seasonNumber int, releaseTitle string) (*episodes.Episode, error) {
	s.titleCalled = true
	s.titleCallArgs.seriesID = seriesID
	s.titleCallArgs.seasonNumber = seasonNumber
	s.titleCallArgs.releaseTitle = releaseTitle
	return s.titleCallReturns, s.titleCallErr
}

func (s *stubFinder) FindEpisodesBySceneAbsolute(_ context.Context, _, _ int) ([]episodes.Episode, error) {
	return s.findEpisodesBySceneAbsoluteOut, s.findEpisodesBySceneAbsoluteErr
}

func (s *stubFinder) FindEpisode(_ context.Context, seriesID, seasonNumber, episodeNumber int) (*episodes.Episode, error) {
	s.findEpisodeCalls = append(s.findEpisodeCalls, [3]int{seriesID, seasonNumber, episodeNumber})
	if s.findEpisodeErr != nil {
		return nil, s.findEpisodeErr
	}
	if v, ok := s.findEpisodeMap[[3]int{seriesID, seasonNumber, episodeNumber}]; ok {
		return v, nil
	}
	return nil, nil
}

func (s *stubFinder) FindEpisodeByAbsolute(_ context.Context, _, _ int) (*episodes.Episode, error) {
	return s.findEpisodeByAbsoluteOut, s.findEpisodeByAbsoluteErr
}

func (s *stubFinder) FindEpisodeByAirDate(_ context.Context, seriesID int, date string, part *int) (*episodes.Episode, error) {
	s.findEpisodeByAirDateCallArgs.seriesID = seriesID
	s.findEpisodeByAirDateCallArgs.date = date
	s.findEpisodeByAirDateCallArgs.part = part
	return s.findEpisodeByAirDateOut, s.findEpisodeByAirDateErr
}

func (s *stubFinder) GetEpisodesBySeason(_ context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error) {
	s.getEpisodesBySeasonCalls = append(s.getEpisodesBySeasonCalls, [2]int{seriesID, seasonNumber})
	return s.getEpisodesBySeasonOut, s.getEpisodesBySeasonErr
}

func (s *stubFinder) GetEpisodesBySceneSeason(_ context.Context, seriesID, sceneSeason int) ([]episodes.Episode, error) {
	s.getEpisodesBySceneSeasonCalls = append(s.getEpisodesBySceneSeasonCalls, [2]int{seriesID, sceneSeason})
	return s.getEpisodesBySceneSeasonOut, s.getEpisodesBySceneSeasonErr
}

// stubScene satisfies parsingservice.SceneMappingFinder. Both
// methods return canned values (default nil/nil mirrors the
// production no-op).
type stubScene struct {
	mapping *scenemappings.Mapping
	season  *int
}

func (s stubScene) FindSceneMapping(_, _ string, _ int) *scenemappings.Mapping { return s.mapping }
func (s stubScene) GetSceneSeasonNumber(_, _ string) *int                      { return s.season }

type stubInfos struct {
	q  quality.Model
	rg string
	ls []language.Language

	parseQualityCalls      []string
	parseReleaseGroupCalls []string
	parseLanguagesCalls    []string
}

func (s *stubInfos) ParseQuality(t string) quality.Model {
	s.parseQualityCalls = append(s.parseQualityCalls, t)
	return s.q
}

func (s *stubInfos) ParseReleaseGroup(t string) string {
	s.parseReleaseGroupCalls = append(s.parseReleaseGroupCalls, t)
	return s.rg
}

func (s *stubInfos) ParseLanguages(t string) []language.Language {
	s.parseLanguagesCalls = append(s.parseLanguagesCalls, t)
	return s.ls
}

func mkSvc(t *testing.T, ef parsingservice.EpisodeFinder, ib parsingservice.ParsedEpisodeInfoBuilder) *parsingservice.Service {
	t.Helper()
	s, err := parsingservice.NewService(ef, ib, nil, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

// mkSvcScene constructs a Service with an explicit SceneMappingFinder
// stub. Tests that exercise the sceneSource=true path use this; the
// simpler ParseSpecialEpisodeTitle tests use mkSvc which binds the
// no-op default.
func mkSvcScene(t *testing.T, ef parsingservice.EpisodeFinder, ib parsingservice.ParsedEpisodeInfoBuilder, sm parsingservice.SceneMappingFinder) *parsingservice.Service {
	t.Helper()
	s, err := parsingservice.NewService(ef, ib, sm, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

// mkSvcAll constructs a Service with explicit SceneMappingFinder +
// SeriesFinder stubs. Used by the GetSeriesFromTitle test suite
// (slice 3.81) which exercises the full 5-arg construction path.
func mkSvcAll(t *testing.T, ef parsingservice.EpisodeFinder, ib parsingservice.ParsedEpisodeInfoBuilder, sm parsingservice.SceneMappingFinder, sf parsingservice.SeriesFinder) *parsingservice.Service {
	t.Helper()
	s, err := parsingservice.NewService(ef, ib, sm, sf, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func ts() *series.Series {
	return &series.Series{ID: 7, Title: "Test Show", UseSceneNumbering: false}
}

// --- constructor ---------------------------------------------------

func TestNewServiceRejectsNilFinder(t *testing.T) {
	t.Parallel()
	_, err := parsingservice.NewService(nil, &stubInfos{}, nil, nil, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "nil EpisodeFinder") {
		t.Errorf("got %v, want error mentioning nil EpisodeFinder", err)
	}
}

func TestNewServiceNilOptionalsUseDefaults(t *testing.T) {
	t.Parallel()
	s, err := parsingservice.NewService(&stubFinder{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s == nil {
		t.Fatalf("nil svc")
	}
}

// --- ParseSpecialEpisodeTitle: nil-receiver / nil-series guards ----

func TestParseSpecialEpisodeTitleNilSeriesErrors(t *testing.T) {
	t.Parallel()
	svc := mkSvc(t, &stubFinder{}, &stubInfos{})
	_, err := svc.ParseSpecialEpisodeTitle(context.Background(), nil, "anything", nil)
	if err == nil || !strings.Contains(err.Error(), "nil series") {
		t.Errorf("got %v, want nil-series error", err)
	}
}

// --- ParseSpecialEpisodeTitle: TheXEM scene-numbered short-circuit -

func TestParseSpecialEpisodeTitleSceneNumberedShortCircuitReturnsInputUnchanged(t *testing.T) {
	t.Parallel()

	// Input that triggers IsPossibleSceneSeasonSpecial: SeasonNumber!=0
	// AND single E0.
	in := &parser.ParsedEpisodeInfo{
		ReleaseTitle:   "Show.S03E00",
		SeasonNumber:   3,
		EpisodeNumbers: []int{0},
	}

	finder := &stubFinder{
		sceneCallReturns: []episodes.Episode{{ID: 11, SeasonNumber: 0, EpisodeNumber: 1}},
	}
	infos := &stubInfos{}
	svc := mkSvc(t, finder, infos)

	s := ts()
	s.UseSceneNumbering = true

	got, err := svc.ParseSpecialEpisodeTitle(context.Background(), in, "Show.S03E00", s)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != in {
		t.Errorf("expected pass-through of input pointer; got %p, want %p", got, in)
	}

	// FindEpisodesBySceneNumbering called with (seriesID=7, season=3, episode=0)
	if !finder.sceneCalled {
		t.Errorf("FindEpisodesBySceneNumbering should have been called")
	}
	if finder.sceneCallArgs != [3]int{7, 3, 0} {
		t.Errorf("scene args = %v, want [7 3 0]", finder.sceneCallArgs)
	}

	// FindEpisodeByTitle should NOT have been called (short-circuit)
	if finder.titleCalled {
		t.Errorf("FindEpisodeByTitle should not have been called on short-circuit")
	}

	// Builder methods NOT called (no synthetic ParsedEpisodeInfo built)
	if len(infos.parseQualityCalls)+len(infos.parseReleaseGroupCalls)+len(infos.parseLanguagesCalls) != 0 {
		t.Errorf("ParsedEpisodeInfoBuilder methods called on short-circuit")
	}
}

func TestParseSpecialEpisodeTitleSceneNumberedReturnsErrPropagates(t *testing.T) {
	t.Parallel()

	in := &parser.ParsedEpisodeInfo{
		SeasonNumber:   3,
		EpisodeNumbers: []int{0},
	}
	finder := &stubFinder{sceneCallErr: errors.New("EIO")}
	svc := mkSvc(t, finder, &stubInfos{})

	s := ts()
	s.UseSceneNumbering = true

	got, err := svc.ParseSpecialEpisodeTitle(context.Background(), in, "rt", s)
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

func TestParseSpecialEpisodeTitleSceneNumberedNoMatchFallsThroughToTitle(t *testing.T) {
	t.Parallel()

	in := &parser.ParsedEpisodeInfo{
		SeasonNumber:   3,
		EpisodeNumbers: []int{0},
	}
	finder := &stubFinder{
		// Scene-numbered lookup returns empty → no short-circuit.
		sceneCallReturns: nil,
		// Title lookup returns nil too → final return nil.
		titleCallReturns: nil,
	}
	svc := mkSvc(t, finder, &stubInfos{})

	s := ts()
	s.UseSceneNumbering = true

	got, err := svc.ParseSpecialEpisodeTitle(context.Background(), in, "rt", s)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil (no match)", got)
	}

	if !finder.sceneCalled {
		t.Errorf("FindEpisodesBySceneNumbering should have been called")
	}
	if !finder.titleCalled {
		t.Errorf("FindEpisodeByTitle should have been called after scene-numbered no-match")
	}
}

func TestParseSpecialEpisodeTitleSceneNumberingDisabledSkipsSceneCheck(t *testing.T) {
	t.Parallel()

	in := &parser.ParsedEpisodeInfo{
		SeasonNumber:   3,
		EpisodeNumbers: []int{0},
	}
	finder := &stubFinder{}
	svc := mkSvc(t, finder, &stubInfos{})

	// Series.UseSceneNumbering = false (default from ts())
	got, err := svc.ParseSpecialEpisodeTitle(context.Background(), in, "rt", ts())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil (no title match either)", got)
	}
	if finder.sceneCalled {
		t.Errorf("scene check should be skipped when UseSceneNumbering=false")
	}
	if !finder.titleCalled {
		t.Errorf("title-lookup should still run")
	}
}

func TestParseSpecialEpisodeTitleNilParsedInfoSkipsSceneCheck(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{}
	svc := mkSvc(t, finder, &stubInfos{})

	s := ts()
	s.UseSceneNumbering = true

	got, err := svc.ParseSpecialEpisodeTitle(context.Background(), nil, "rt", s)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
	if finder.sceneCalled {
		t.Errorf("scene check should be skipped when input parsedEpisodeInfo is nil")
	}
}

// --- ParseSpecialEpisodeTitle: title-lookup happy + edge paths ----

func TestParseSpecialEpisodeTitleTitleMatchBuildsSyntheticInfo(t *testing.T) {
	t.Parallel()

	hd, _ := quality.FindByID(4) // some real quality entry
	wantQuality := quality.Model{Quality: hd}
	wantReleaseGroup := "RARBG"
	wantLanguages := []language.Language{language.English}

	finder := &stubFinder{
		titleCallReturns: &episodes.Episode{
			ID:            42,
			SeasonNumber:  0,
			EpisodeNumber: 7,
			Title:         "The Special Episode",
		},
	}
	infos := &stubInfos{
		q:  wantQuality,
		rg: wantReleaseGroup,
		ls: wantLanguages,
	}
	svc := mkSvc(t, finder, infos)

	in := &parser.ParsedEpisodeInfo{
		ReleaseTitle: "doesn't matter — comes from arg",
	}

	got, err := svc.ParseSpecialEpisodeTitle(context.Background(), in, "Show.The.Special.Episode.1080p", ts())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatalf("got nil, want a synthetic ParsedEpisodeInfo")
	}
	if got == in {
		t.Errorf("expected fresh ParsedEpisodeInfo pointer, got input pointer")
	}

	// The synthetic info pulls SeriesTitle + SeriesTitleInfo.Title
	// from the series, NOT from input parsedInfo. Verifies we don't
	// accidentally propagate stale fields.
	if got.SeriesTitle != "Test Show" {
		t.Errorf("SeriesTitle = %q, want Test Show", got.SeriesTitle)
	}
	if got.SeriesTitleInfo == nil || got.SeriesTitleInfo.Title != "Test Show" {
		t.Errorf("SeriesTitleInfo = %+v, want Title=Test Show", got.SeriesTitleInfo)
	}
	if got.ReleaseTitle != "Show.The.Special.Episode.1080p" {
		t.Errorf("ReleaseTitle = %q, want from-arg", got.ReleaseTitle)
	}
	if got.SeasonNumber != 0 || len(got.EpisodeNumbers) != 1 || got.EpisodeNumbers[0] != 7 {
		t.Errorf("season/episode = %d / %v, want 0 / [7]", got.SeasonNumber, got.EpisodeNumbers)
	}
	if got.FullSeason {
		t.Errorf("FullSeason = true, want false")
	}
	if !got.Special {
		t.Errorf("Special = false, want true")
	}
	if got.Quality != wantQuality {
		t.Errorf("Quality = %+v, want %+v", got.Quality, wantQuality)
	}
	if got.ReleaseGroup != wantReleaseGroup {
		t.Errorf("ReleaseGroup = %q, want %q", got.ReleaseGroup, wantReleaseGroup)
	}
	if len(got.Languages) != 1 || got.Languages[0] != language.English {
		t.Errorf("Languages = %+v, want [English]", got.Languages)
	}

	// Builder methods called exactly once each, with the supplied
	// release title.
	if len(infos.parseQualityCalls) != 1 || infos.parseQualityCalls[0] != "Show.The.Special.Episode.1080p" {
		t.Errorf("parseQualityCalls = %v", infos.parseQualityCalls)
	}
	if len(infos.parseReleaseGroupCalls) != 1 {
		t.Errorf("parseReleaseGroupCalls = %v", infos.parseReleaseGroupCalls)
	}
	if len(infos.parseLanguagesCalls) != 1 {
		t.Errorf("parseLanguagesCalls = %v", infos.parseLanguagesCalls)
	}
}

func TestParseSpecialEpisodeTitleNoTitleMatchReturnsNil(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{titleCallReturns: nil}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.ParseSpecialEpisodeTitle(context.Background(), nil, "no.match", ts())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestParseSpecialEpisodeTitleTitleErrPropagates(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{titleCallErr: errors.New("EIO")}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.ParseSpecialEpisodeTitle(context.Background(), nil, "rt", ts())
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

func TestParseSpecialEpisodeTitleTitleLookupGetsSeason0Pinned(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{}
	svc := mkSvc(t, finder, &stubInfos{})

	_, _ = svc.ParseSpecialEpisodeTitle(context.Background(), nil, "rt", ts())

	if finder.titleCallArgs.seasonNumber != 0 {
		t.Errorf("title-lookup season = %d, want 0", finder.titleCallArgs.seasonNumber)
	}
	if finder.titleCallArgs.seriesID != 7 {
		t.Errorf("title-lookup seriesID = %d, want 7", finder.titleCallArgs.seriesID)
	}
	if finder.titleCallArgs.releaseTitle != "rt" {
		t.Errorf("title-lookup releaseTitle = %q, want rt", finder.titleCallArgs.releaseTitle)
	}
}

// --- GetEpisodes: nil-receiver / nil-arg guards --------------------

func TestGetEpisodesNilParsedInfoErrors(t *testing.T) {
	t.Parallel()
	svc := mkSvc(t, &stubFinder{}, &stubInfos{})
	_, err := svc.GetEpisodes(context.Background(), nil, ts(), false)
	if err == nil || !strings.Contains(err.Error(), "nil parsedEpisodeInfo") {
		t.Errorf("got %v, want nil-parsedInfo error", err)
	}
}

func TestGetEpisodesNilSeriesErrors(t *testing.T) {
	t.Parallel()
	svc := mkSvc(t, &stubFinder{}, &stubInfos{})
	_, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{}, nil, false)
	if err == nil || !strings.Contains(err.Error(), "nil series") {
		t.Errorf("got %v, want nil-series error", err)
	}
}

// --- GetEpisodes: FullSeason path ---------------------------------

func TestGetEpisodesFullSeasonNonScene(t *testing.T) {
	t.Parallel()

	want := []episodes.Episode{{ID: 1}, {ID: 2}, {ID: 3}}
	finder := &stubFinder{
		getEpisodesBySeasonOut: want,
	}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		FullSeason:   true,
		SeasonNumber: 4,
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d, want 3", len(got))
	}
	if len(finder.getEpisodesBySeasonCalls) != 1 || finder.getEpisodesBySeasonCalls[0] != [2]int{7, 4} {
		t.Errorf("GetEpisodesBySeason calls = %v, want [[7 4]]", finder.getEpisodesBySeasonCalls)
	}
}

func TestGetEpisodesFullSeasonSceneNumberedHits(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{
		getEpisodesBySceneSeasonOut: []episodes.Episode{{ID: 99}},
	}
	svc := mkSvc(t, finder, &stubInfos{})

	s := ts()
	s.UseSceneNumbering = true

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		FullSeason:   true,
		SeasonNumber: 2,
	}, s, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// sceneSource=false → SceneSeason path NOT taken even though
	// UseSceneNumbering=true (the AND with sceneSource is what gates).
	if len(got) != 0 {
		t.Errorf("got %d, want 0 (sceneSource=false should skip scene-season)", len(got))
	}
	if len(finder.getEpisodesBySceneSeasonCalls) != 0 {
		t.Errorf("GetEpisodesBySceneSeason should not have been called")
	}
}

// --- GetEpisodes: Daily path --------------------------------------

func TestGetEpisodesDailyMatchReturnsSingleEpisode(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{
		findEpisodeByAirDateOut: &episodes.Episode{ID: 50},
	}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		AirDate: "2024-01-02",
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].ID != 50 {
		t.Errorf("got %+v, want one episode ID=50", got)
	}
	if finder.findEpisodeByAirDateCallArgs.date != "2024-01-02" {
		t.Errorf("date passed = %q, want 2024-01-02", finder.findEpisodeByAirDateCallArgs.date)
	}
}

func TestGetEpisodesDailyNoMatchReturnsEmpty(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{findEpisodeByAirDateOut: nil}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		AirDate: "2024-01-02",
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

// --- GetEpisodes: Standard path -----------------------------------

func TestGetEpisodesStandardPicksByEpisodeNumber(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{
		findEpisodeMap: map[[3]int]*episodes.Episode{
			{7, 3, 5}: {ID: 105, SeasonNumber: 3, EpisodeNumber: 5},
			{7, 3, 6}: {ID: 106, SeasonNumber: 3, EpisodeNumber: 6},
		},
	}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		SeasonNumber:   3,
		EpisodeNumbers: []int{5, 6},
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
	if got[0].ID != 105 || got[1].ID != 106 {
		t.Errorf("ordering wrong: %+v", got)
	}
}

func TestGetEpisodesStandardNilEpisodeNumbersReturnsEmpty(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		SeasonNumber:   3,
		EpisodeNumbers: nil,
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
	if len(finder.findEpisodeCalls) != 0 {
		t.Errorf("FindEpisode should not be called for nil EpisodeNumbers")
	}
}

func TestGetEpisodesStandardSceneNumberingMatchOverridesTvdb(t *testing.T) {
	t.Parallel()

	// UseSceneNumbering=true + sceneSource=true → scene lookup wins
	finder := &stubFinder{
		sceneCallReturns: []episodes.Episode{{ID: 99, SeasonNumber: 1, EpisodeNumber: 1}},
	}
	svc := mkSvcScene(t, finder, &stubInfos{}, stubScene{}) // no scene mapping → mappedSeason = parsed season

	s := ts()
	s.UseSceneNumbering = true

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		SeasonNumber:   3,
		EpisodeNumbers: []int{5},
	}, s, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].ID != 99 {
		t.Errorf("got %+v, want scene-numbered ID=99", got)
	}
	// FindEpisode (TVDB-numbered) should NOT have been called when scene path matches.
	if len(finder.findEpisodeCalls) != 0 {
		t.Errorf("FindEpisode should be skipped when scene path returns matches")
	}
}

// --- GetEpisodes: IsPossibleSceneSeasonSpecial recursion ----------

func TestGetEpisodesPossibleSceneSeasonSpecialFallsThroughToStandard(t *testing.T) {
	t.Parallel()

	// IsPossibleSceneSeasonSpecial requires SeasonNumber!=0 AND single E0.
	// With UseSceneNumbering=false, ParseSpecialEpisodeTitle goes
	// straight to the title lookup. We arrange for it to find an
	// episode (so the synthetic ParsedEpisodeInfo gets built with
	// season=0/ep=12), and then for getStandardEpisodes(spec, season=0,
	// sceneSource=false) to succeed.
	finder := &stubFinder{
		// FindEpisodeByTitle hit → ParseSpecialEpisodeTitle returns
		// synthetic info with SeasonNumber=0, EpisodeNumber=12.
		titleCallReturns: &episodes.Episode{
			ID: 12, SeasonNumber: 0, EpisodeNumber: 12, Title: "Behind The Scenes",
		},
		// FindEpisode for (seriesID=7, season=0, episode=12) →
		// returns the episode.
		findEpisodeMap: map[[3]int]*episodes.Episode{
			{7, 0, 12}: {ID: 12, SeasonNumber: 0, EpisodeNumber: 12},
		},
	}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		ReleaseTitle:   "Show.Behind.The.Scenes",
		SeasonNumber:   3,
		EpisodeNumbers: []int{0},
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].ID != 12 {
		t.Errorf("got %+v, want one episode ID=12", got)
	}
}

// --- GetEpisodes: Special && mappedSeasonNumber != 0 returns empty -

func TestGetEpisodesSpecialAgainstNonZeroMappedSeasonReturnsEmpty(t *testing.T) {
	t.Parallel()

	// Special with mappedSeason != 0 → C# `:305-308` returns empty.
	finder := &stubFinder{}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		Special:      true,
		SeasonNumber: 5,
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want 0", got)
	}
	if len(finder.findEpisodeCalls) != 0 {
		t.Errorf("FindEpisode should not be called")
	}
}

// --- GetEpisodes: AbsoluteNumbering / Anime path ------------------

func TestGetEpisodesAbsoluteNumberingFallbackToTvdb(t *testing.T) {
	t.Parallel()

	// IsAbsoluteNumbering=true (AbsoluteEpisodeNumbers non-empty),
	// Special=false, sceneSource=false → no scene-season number is
	// queried. All scene-source arms skipped, default fallback
	// FindEpisodeByAbsolute.
	finder := &stubFinder{
		findEpisodeByAbsoluteOut: &episodes.Episode{ID: 80, SeasonNumber: 2, EpisodeNumber: 3},
	}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		AbsoluteEpisodeNumbers: []int{42},
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].ID != 80 {
		t.Errorf("got %+v, want one ID=80", got)
	}
}

func TestGetEpisodesAbsoluteSpecialUsesSeason0FindEpisode(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{
		findEpisodeMap: map[[3]int]*episodes.Episode{
			{7, 0, 42}: {ID: 142, SeasonNumber: 0, EpisodeNumber: 42},
		},
	}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		AbsoluteEpisodeNumbers: []int{42},
		Special:                true,
	}, ts(), false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].ID != 142 {
		t.Errorf("got %+v, want one ID=142", got)
	}
}

// --- mapForSeries (sceneSource=true) scene-mapping arms -----------

func TestGetEpisodesSceneSourceTrueWithoutMappingUsesParsedSeason(t *testing.T) {
	t.Parallel()

	// No SceneMapping → mappedSeason stays at parsedInfo.SeasonNumber.
	// With UseSceneNumbering=false the standard path runs.
	finder := &stubFinder{
		findEpisodeMap: map[[3]int]*episodes.Episode{
			{7, 3, 5}: {ID: 1, SeasonNumber: 3, EpisodeNumber: 5},
		},
	}
	svc := mkSvcScene(t, finder, &stubInfos{}, stubScene{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		SeasonNumber:   3,
		EpisodeNumbers: []int{5},
	}, ts(), true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("got %+v, want ID=1 from mappedSeason=3", got)
	}
}

func TestGetEpisodesSceneSourceTvdbOriginFlipsSceneSourceFalse(t *testing.T) {
	t.Parallel()

	// SceneOrigin == "tvdb" → sceneSource flipped to false →
	// even though UseSceneNumbering=true, scene-numbering path is skipped.
	finder := &stubFinder{
		// FindEpisode for the TVDB-numbered (mappedSeason=3) succeeds.
		findEpisodeMap: map[[3]int]*episodes.Episode{
			{7, 3, 5}: {ID: 305, SeasonNumber: 3, EpisodeNumber: 5},
		},
	}
	svc := mkSvcScene(t, finder, &stubInfos{}, stubScene{
		mapping: &scenemappings.Mapping{SceneOrigin: "tvdb"},
	})

	s := ts()
	s.UseSceneNumbering = true

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		SeasonNumber:   3,
		EpisodeNumbers: []int{5},
	}, s, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].ID != 305 {
		t.Errorf("got %+v, want TVDB-numbered ID=305 (scene path bypassed)", got)
	}
	// Scene-numbering path must NOT have been called.
	if finder.sceneCalled {
		t.Errorf("scene path was taken; tvdb origin should have skipped it")
	}
}

func TestGetEpisodesSceneSourceFailsValidationReturnsEmpty(t *testing.T) {
	t.Parallel()

	// SeriesType=Standard (0) + IsDaily parsed info → validation fails
	finder := &stubFinder{}
	svc := mkSvcScene(t, finder, &stubInfos{}, stubScene{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		SeasonNumber: 1,
		AirDate:      "2024-01-01", // makes IsDaily()=true
	}, ts(), true) // SeriesType=Standard via ts()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want empty (validation should fail Daily-on-Standard)", got)
	}
}

// --- error propagation -------------------------------------------

func TestGetEpisodesEpisodeFinderErrPropagates(t *testing.T) {
	t.Parallel()

	finder := &stubFinder{
		getEpisodesBySeasonErr: errors.New("EIO"),
	}
	svc := mkSvc(t, finder, &stubInfos{})

	got, err := svc.GetEpisodes(context.Background(), &parser.ParsedEpisodeInfo{
		FullSeason:   true,
		SeasonNumber: 4,
	}, ts(), false)
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

// --- GetSeriesFromTitle (slice 3.81) ------------------------------

// stubSeriesFinder satisfies parsingservice.SeriesFinder. Each method
// pulls from a per-key map keyed on the (cleaned-or-raw) input the
// production binding would consume; this keeps fixtures explicit and
// makes call-tracking easy. Errors are returned verbatim when set.
type stubSeriesFinder struct {
	byTitle      map[string]*series.Series
	byTitleErr   error
	byTitleCalls []string

	byTitleAndYear      map[string]*series.Series
	byTitleAndYearErr   error
	byTitleAndYearCalls []struct {
		title string
		year  int
	}

	byTvdbID      map[int]*series.Series
	byTvdbIDErr   error
	byTvdbIDCalls []int

	byTvRageID      map[int]*series.Series
	byTvRageIDErr   error
	byTvRageIDCalls []int

	byImdbID      map[string]*series.Series
	byImdbIDErr   error
	byImdbIDCalls []string
}

func (s *stubSeriesFinder) FindByTitle(_ context.Context, title string) (*series.Series, error) {
	s.byTitleCalls = append(s.byTitleCalls, title)
	if s.byTitleErr != nil {
		return nil, s.byTitleErr
	}
	if v, ok := s.byTitle[title]; ok {
		return v, nil
	}
	return nil, series.ErrNotFound
}

func (s *stubSeriesFinder) FindByTitleAndYear(_ context.Context, title string, year int) (*series.Series, error) {
	s.byTitleAndYearCalls = append(s.byTitleAndYearCalls, struct {
		title string
		year  int
	}{title, year})
	if s.byTitleAndYearErr != nil {
		return nil, s.byTitleAndYearErr
	}
	key := title + "\x00" + fmtIntForKey(year)
	if v, ok := s.byTitleAndYear[key]; ok {
		return v, nil
	}
	return nil, series.ErrNotFound
}

func (s *stubSeriesFinder) FindByTvdbID(_ context.Context, tvdbID int) (*series.Series, error) {
	s.byTvdbIDCalls = append(s.byTvdbIDCalls, tvdbID)
	if s.byTvdbIDErr != nil {
		return nil, s.byTvdbIDErr
	}
	if v, ok := s.byTvdbID[tvdbID]; ok {
		return v, nil
	}
	return nil, series.ErrNotFound
}

func (s *stubSeriesFinder) FindByTvRageID(_ context.Context, tvRageID int) (*series.Series, error) {
	s.byTvRageIDCalls = append(s.byTvRageIDCalls, tvRageID)
	if s.byTvRageIDErr != nil {
		return nil, s.byTvRageIDErr
	}
	if v, ok := s.byTvRageID[tvRageID]; ok {
		return v, nil
	}
	return nil, series.ErrNotFound
}

func (s *stubSeriesFinder) FindByImdbID(_ context.Context, imdbID string) (*series.Series, error) {
	s.byImdbIDCalls = append(s.byImdbIDCalls, imdbID)
	if s.byImdbIDErr != nil {
		return nil, s.byImdbIDErr
	}
	if v, ok := s.byImdbID[imdbID]; ok {
		return v, nil
	}
	return nil, series.ErrNotFound
}

// fmtIntForKey is a deliberately tiny helper so the byTitleAndYear
// map keys are readable in tests. Avoids dragging strconv into a
// fixture-heavy test file when the inputs are always small ints.
func fmtIntForKey(n int) string {
	switch n {
	case 0:
		return "0"
	default:
		// Tiny stringification — keeps the test file's dep surface
		// minimal. Year values in tests are always 4-digit positive
		// integers, so the simple loop suffices.
		var buf [16]byte
		i := len(buf)
		neg := n < 0
		if neg {
			n = -n
		}
		for n > 0 {
			i--
			buf[i] = byte('0' + n%10)
			n /= 10
		}
		if neg {
			i--
			buf[i] = '-'
		}
		return string(buf[i:])
	}
}

func TestGetSeriesFromTitleNilReceiverErrors(t *testing.T) {
	t.Parallel()
	var svc *parsingservice.Service
	_, err := svc.GetSeriesFromTitle(context.Background(), "Show.S01E01")
	if err == nil || !strings.Contains(err.Error(), "nil receiver") {
		t.Errorf("got %v, want nil-receiver error", err)
	}
}

func TestGetSeriesFromTitleNilSeriesFinderReturnsNil(t *testing.T) {
	// nil seriesFinder → noopSeriesFinder → every lookup returns
	// (nil, nil) → GetSeriesFromTitle returns (nil, nil) without error.
	t.Parallel()
	svc := mkSvc(t, &stubFinder{}, &stubInfos{})
	got, err := svc.GetSeriesFromTitle(context.Background(), "Some.Show.S01E01.WEB.x264")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestGetSeriesFromTitleParseFailedFallsBackToFindByTitle(t *testing.T) {
	// An input that ParseTitle can't make sense of falls through to
	// seriesFinder.FindByTitle(rawTitle). C# `:46-49`.
	t.Parallel()
	want := &series.Series{ID: 5, Title: "Bare Title", TvdbID: 12345}
	sf := &stubSeriesFinder{
		byTitle: map[string]*series.Series{
			"???NOT_A_PARSEABLE_TITLE???": want,
		},
	}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, nil, sf)
	got, err := svc.GetSeriesFromTitle(context.Background(), "???NOT_A_PARSEABLE_TITLE???")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(sf.byTitleCalls) != 1 || sf.byTitleCalls[0] != "???NOT_A_PARSEABLE_TITLE???" {
		t.Errorf("FindByTitle calls = %v, want one call with raw title", sf.byTitleCalls)
	}
}

func TestGetSeriesFromTitleParseFailedNotFoundIsNil(t *testing.T) {
	// Parser miss + FindByTitle(ErrNotFound) → (nil, nil), not an
	// error — ErrNotFound is the C# null arm.
	t.Parallel()
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, nil, sf)
	got, err := svc.GetSeriesFromTitle(context.Background(), "?")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestGetSeriesFromTitleParseFailedFinderErrorPropagates(t *testing.T) {
	// A real DB error (not ErrNotFound) must propagate wrapped.
	t.Parallel()
	sf := &stubSeriesFinder{byTitleErr: errors.New("EIO")}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, nil, sf)
	_, err := svc.GetSeriesFromTitle(context.Background(), "?")
	if err == nil || !strings.Contains(err.Error(), "FindByTitle") {
		t.Errorf("got %v, want wrapped FindByTitle error", err)
	}
}

func TestGetSeriesFromTitleSceneMappingHitsFindByTvdbID(t *testing.T) {
	// Scene mapping with non-zero TvdbId → FindByTvdbID dispatch.
	// Mirrors C# `:51-56`.
	t.Parallel()
	want := &series.Series{ID: 9, Title: "Mapped Show", TvdbID: 7777}
	sf := &stubSeriesFinder{
		byTvdbID: map[int]*series.Series{7777: want},
	}
	sm := stubScene{
		mapping: &scenemappings.Mapping{TvdbID: 7777},
	}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, sm, sf)
	got, err := svc.GetSeriesFromTitle(context.Background(), "Mapped.Show.S01E01.WEB.x264")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(sf.byTvdbIDCalls) != 1 || sf.byTvdbIDCalls[0] != 7777 {
		t.Errorf("FindByTvdbID calls = %v, want [7777]", sf.byTvdbIDCalls)
	}
	// FindByTitle should NOT have been called — the scene-mapping arm
	// short-circuits.
	if len(sf.byTitleCalls) != 0 {
		t.Errorf("FindByTitle calls = %v, want none", sf.byTitleCalls)
	}
}

func TestGetSeriesFromTitleSceneMappingZeroTvdbIdFallsThrough(t *testing.T) {
	// A mapping with TvdbID==0 (sentinel for "no real id") falls
	// through to the FindByTitle arm — matches the C# `?.TvdbId`
	// nullable-coalesce path at `:51`.
	t.Parallel()
	want := &series.Series{ID: 11, Title: "Some Show", TvdbID: 4242}
	sf := &stubSeriesFinder{
		byTitle: map[string]*series.Series{"Some Show": want},
	}
	sm := stubScene{mapping: &scenemappings.Mapping{TvdbID: 0}}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, sm, sf)
	got, err := svc.GetSeriesFromTitle(context.Background(), "Some.Show.S01E01.WEB.x264")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(sf.byTvdbIDCalls) != 0 {
		t.Errorf("FindByTvdbID called %d times; want 0 (zero-tvdb mapping should fall through)", len(sf.byTvdbIDCalls))
	}
}

func TestGetSeriesFromTitleByParsedSeriesTitle(t *testing.T) {
	// No scene mapping → FindByTitle(parsedInfo.SeriesTitle) wins.
	t.Parallel()
	want := &series.Series{ID: 3, Title: "Some Show", TvdbID: 99}
	sf := &stubSeriesFinder{
		byTitle: map[string]*series.Series{"Some Show": want},
	}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, nil, sf)
	got, err := svc.GetSeriesFromTitle(context.Background(), "Some.Show.S01E01.WEB.x264")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetSeriesFromTitleByTitleAndYearFallback(t *testing.T) {
	// Parsed title doesn't match in FindByTitle, and AllTitles is
	// empty → step 5 falls back to FindByTitleAndYear with
	// (TitleWithoutYear, Year). Construct an input whose
	// SeriesTitleInfo carries a year so the splitter populates the
	// TitleWithoutYear / Year fields.
	t.Parallel()
	// "Some Show 2020" → SeriesTitleInfo.Title="Some Show 2020",
	//                    TitleWithoutYear="Some Show", Year=2020.
	want := &series.Series{ID: 4, Title: "Some Show", TvdbID: 200}
	sf := &stubSeriesFinder{
		byTitleAndYear: map[string]*series.Series{
			"Some Show\x002020": want,
		},
	}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, nil, sf)
	got, err := svc.GetSeriesFromTitle(context.Background(), "Some.Show.2020.S01E01.WEB.x264")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(sf.byTitleAndYearCalls) != 1 ||
		sf.byTitleAndYearCalls[0].title != "Some Show" ||
		sf.byTitleAndYearCalls[0].year != 2020 {
		t.Errorf("FindByTitleAndYear calls = %+v, want one call (Some Show, 2020)", sf.byTitleAndYearCalls)
	}
}

func TestGetSeriesFromTitleAllTitlesSceneMappingFallback(t *testing.T) {
	// Direct unit-test of the getSeriesByAllTitles scene-mapping
	// fallback at C# `:107` — when FindByTitle misses but the scene
	// mapping resolves a TvdbId, foundTvdbId is set and the trailing
	// FindByTvdbID lookup at C# `:131-134` produces the series.
	//
	// We synthesise this by constructing an input the parser turns
	// into a non-nil ParsedEpisodeInfo with a non-empty AllTitles
	// list. To make the test deterministic we force FindByTitle to
	// miss EVERY title (no map entries) and have the SceneMappingFinder
	// return a non-nil mapping unconditionally.
	t.Parallel()
	want := &series.Series{ID: 33, Title: "Mapped By All", TvdbID: 5555}
	sf := &stubSeriesFinder{
		byTvdbID: map[int]*series.Series{5555: want},
	}
	sm := stubScene{mapping: &scenemappings.Mapping{TvdbID: 5555}}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, sm, sf)
	// Note: the top-level FindSceneMapping in step 2 of
	// GetSeriesFromTitle ALSO sees this stub, so it short-circuits
	// before we even get to AllTitles. That's actually the correct
	// production behaviour: scene-mapping always wins. So this test
	// effectively re-asserts the step-2 path with a fresh harness.
	got, err := svc.GetSeriesFromTitle(context.Background(), "Some.Show.S01E01.WEB.x264")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetSeriesFromTitleStep3FinderErrorPropagates(t *testing.T) {
	// A non-ErrNotFound error from the step-3 FindByTitle propagates.
	t.Parallel()
	sf := &stubSeriesFinder{byTitleErr: errors.New("EIO")}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, nil, sf)
	_, err := svc.GetSeriesFromTitle(context.Background(), "Some.Show.S01E01.WEB.x264")
	if err == nil || !strings.Contains(err.Error(), "FindByTitle") {
		t.Errorf("got %v, want wrapped FindByTitle error", err)
	}
}

// ============================================================================
// Service.Map (slice 4.S — queue [11] sub-arc 11a)
// ============================================================================
//
// Ports the C# MapFixture cases at
// `src/NzbDrone.Core.Test/ParserTests/ParsingServiceTests/MapFixture.cs`.
// Each test pins a specific findSeries arm + verifies the resolved
// *decisionengine.Subject's Series + SeriesMatchType + (where
// relevant) Episodes/EpisodeRequested/MappedSeasonNumber.

// mapTestSubject builds the parsedInfo used across MapFixture cases.
// Mirrors the C# `Setup()` at MapFixture.cs:27-56 — series "30 Stone"
// with cleanTitle="stone", one episode (id=1), and a parsedInfo for
// S01E01 against the series title.
func mapTestParsedInfo() *parser.ParsedEpisodeInfo {
	return &parser.ParsedEpisodeInfo{
		SeriesTitle:     "30 Stone",
		SeriesTitleInfo: &parser.SeriesTitleInfo{Title: "30 Stone"},
		SeasonNumber:    1,
		EpisodeNumbers:  []int{1},
		Languages:       []language.Language{language.English},
	}
}

func mapTestSeries() *series.Series {
	year := 2020
	return &series.Series{
		ID:         42,
		Title:      "30 Stone",
		CleanTitle: parser.CleanSeriesTitle("30 Stone"),
		TvdbID:     1234,
		TvRageID:   5678,
		ImdbID:     "tt9999",
		Year:       year,
		SeriesType: series.SeriesTypeStandard,
	}
}

func TestMapNilReceiverErrors(t *testing.T) {
	t.Parallel()
	var svc *parsingservice.Service
	_, err := svc.Map(context.Background(), mapTestParsedInfo(), 0, 0, "", nil)
	if err == nil || !strings.Contains(err.Error(), "nil receiver") {
		t.Errorf("got %v, want nil-receiver error", err)
	}
}

func TestMapNilParsedInfoErrors(t *testing.T) {
	t.Parallel()
	svc := mkSvc(t, &stubFinder{}, &stubInfos{})
	_, got := svc.Map(context.Background(), nil, 0, 0, "", nil)
	if got == nil || !strings.Contains(got.Error(), "nil parsedEpisodeInfo") {
		t.Errorf("got %v, want nil-parsedEpisodeInfo error", got)
	}
}

// MapFixture.cs:85-93 — should_lookup_series_by_name. FindByTitle hits.
func TestMapResolvesSeriesByTitle(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{byTitle: map[string]*series.Series{"30 Stone": ser}}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)

	sub, err := svc.Map(context.Background(), mapTestParsedInfo(), ser.TvdbID, ser.TvRageID, ser.ImdbID, nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.Series != ser {
		t.Errorf("Series = %v, want %v", sub.Series, ser)
	}
	if sub.SeriesMatchType != series.MatchTypeTitle {
		t.Errorf("SeriesMatchType = %v, want Title", sub.SeriesMatchType)
	}
	if len(sf.byTitleCalls) != 1 {
		t.Errorf("FindByTitle calls = %v, want 1 call", sf.byTitleCalls)
	}
}

// MapFixture.cs:96-104 — should_use_tvdbid_when_series_title_lookup_fails.
func TestMapFallsBackToTvdbID(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{byTvdbID: map[int]*series.Series{ser.TvdbID: ser}}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)

	sub, err := svc.Map(context.Background(), mapTestParsedInfo(), ser.TvdbID, ser.TvRageID, ser.ImdbID, nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.Series != ser {
		t.Errorf("Series = %v, want %v", sub.Series, ser)
	}
	if sub.SeriesMatchType != series.MatchTypeID {
		t.Errorf("SeriesMatchType = %v, want ID", sub.SeriesMatchType)
	}
	if len(sf.byTvdbIDCalls) != 1 {
		t.Errorf("FindByTvdbID calls = %v, want 1", sf.byTvdbIDCalls)
	}
}

// MapFixture.cs:107-115 — should_use_tvrageid_when_series_title_lookup_fails.
// Critical: tvdbId argument is 0 so step 7 actually fires.
func TestMapFallsBackToTvRageIDWhenTvdbAbsent(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{byTvRageID: map[int]*series.Series{ser.TvRageID: ser}}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)

	sub, err := svc.Map(context.Background(), mapTestParsedInfo(), 0, ser.TvRageID, "", nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.Series != ser {
		t.Errorf("Series = %v, want %v", sub.Series, ser)
	}
	if sub.SeriesMatchType != series.MatchTypeID {
		t.Errorf("SeriesMatchType = %v, want ID", sub.SeriesMatchType)
	}
	if len(sf.byTvRageIDCalls) != 1 {
		t.Errorf("FindByTvRageID calls = %v, want 1", sf.byTvRageIDCalls)
	}
}

// MapFixture.cs:118-132 — should_not_use_tvrageid_when_scene_naming_exception_exists.
// Scene mapping wins, FindByTvRageID never called. Note: scene mapping's
// TvdbID is unknown to the seriesFinder so the series stays nil.
func TestMapSceneMappingShortCircuitsAndSkipsTvRageFallback(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	mapping := &scenemappings.Mapping{TvdbID: 10}
	sf := &stubSeriesFinder{byTvRageID: map[int]*series.Series{ser.TvRageID: ser}}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{mapping: mapping}, sf)

	sub, err := svc.Map(context.Background(), mapTestParsedInfo(), ser.TvdbID, ser.TvRageID, ser.ImdbID, nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.Series != nil {
		t.Errorf("Series = %v, want nil", sub.Series)
	}
	if len(sf.byTvRageIDCalls) != 0 {
		t.Errorf("FindByTvRageID calls = %v, want 0", sf.byTvRageIDCalls)
	}
	// Step 1 dispatched FindByTvdbID(10), which misses → ser=nil.
	if len(sf.byTvdbIDCalls) != 1 || sf.byTvdbIDCalls[0] != 10 {
		t.Errorf("FindByTvdbID calls = %v, want [10]", sf.byTvdbIDCalls)
	}
}

// MapFixture.cs:135-143 — should_use_search_criteria_series_title.
// searchCriteria.Series CleanTitle matches parsedInfo → no FindByTitle.
func TestMapSearchCriteriaCleanTitleShortCircuit(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)
	crit := &decisionengine.SearchCriteria{Series: ser}

	sub, err := svc.Map(context.Background(), mapTestParsedInfo(), ser.TvdbID, ser.TvRageID, ser.ImdbID, crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.Series != ser {
		t.Errorf("Series = %v, want %v", sub.Series, ser)
	}
	if sub.SeriesMatchType != series.MatchTypeTitle {
		t.Errorf("SeriesMatchType = %v, want Title", sub.SeriesMatchType)
	}
	if len(sf.byTitleCalls) != 0 {
		t.Errorf("FindByTitle calls = %v, want 0 (search-criteria short-circuit)", sf.byTitleCalls)
	}
}

// MapFixture.cs:146-154 — should_FindByTitle_when_search_criteria_matching_fails.
// SearchCriteria.Series doesn't match parsedInfo → falls through to step 3
// FindByTitle.
func TestMapSearchCriteriaMissFallsThroughToFindByTitle(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{} // FindByTitle returns ErrNotFound → ser stays nil
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)
	crit := &decisionengine.SearchCriteria{Series: ser}

	parsedInfo := mapTestParsedInfo()
	parsedInfo.SeriesTitle = "Another Name"

	_, err := svc.Map(context.Background(), parsedInfo, 10, 10, "", crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(sf.byTitleCalls) != 1 {
		t.Errorf("FindByTitle calls = %v, want 1", sf.byTitleCalls)
	}
}

// MapFixture.cs:179-187 — should_FindByTvdbId_when_search_criteria_and_FindByTitle_matching_fails.
func TestMapFallsBackToTvdbIDAfterSearchCriteriaMiss(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)
	crit := &decisionengine.SearchCriteria{Series: ser}

	parsedInfo := mapTestParsedInfo()
	parsedInfo.SeriesTitle = "Another Name"

	_, err := svc.Map(context.Background(), parsedInfo, 10, 10, "", crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(sf.byTvdbIDCalls) != 1 || sf.byTvdbIDCalls[0] != 10 {
		t.Errorf("FindByTvdbID calls = %v, want [10]", sf.byTvdbIDCalls)
	}
}

// MapFixture.cs:190-198 — should_FindByTvRageId_when_search_criteria_and_FindByTitle_matching_fails.
// tvdbId argument 0 → step 7 fires.
func TestMapFallsBackToTvRageIDAfterSearchCriteriaMiss(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)
	crit := &decisionengine.SearchCriteria{Series: ser}

	parsedInfo := mapTestParsedInfo()
	parsedInfo.SeriesTitle = "Another Name"

	_, err := svc.Map(context.Background(), parsedInfo, 0, 10, "", crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(sf.byTvRageIDCalls) != 1 || sf.byTvRageIDCalls[0] != 10 {
		t.Errorf("FindByTvRageID calls = %v, want [10]", sf.byTvRageIDCalls)
	}
}

// MapFixture.cs:201-209 — should_not_FindByTvRageId_when_...tvdb_id_is_specified.
// Step 7 is gated on tvdbId<=0 so a non-zero tvdbId argument suppresses it.
func TestMapSkipsTvRageFallbackWhenTvdbIdSpecified(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)
	crit := &decisionengine.SearchCriteria{Series: ser}

	parsedInfo := mapTestParsedInfo()
	parsedInfo.SeriesTitle = "Another Name"

	_, err := svc.Map(context.Background(), parsedInfo, 10, 10, "", crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(sf.byTvRageIDCalls) != 0 {
		t.Errorf("FindByTvRageID calls = %v, want 0 (tvdbId > 0 suppresses TvRage fallback)", sf.byTvRageIDCalls)
	}
}

// MapFixture.cs:212-220 — should_FindByImdbId_when_search_criteria_and_FindByTitle_matching_fails.
func TestMapFallsBackToImdbIDAfterSearchCriteriaMiss(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)
	crit := &decisionengine.SearchCriteria{Series: ser}

	parsedInfo := mapTestParsedInfo()
	parsedInfo.SeriesTitle = "Another Name"

	_, err := svc.Map(context.Background(), parsedInfo, 0, 0, "tt12345", crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(sf.byImdbIDCalls) != 1 || sf.byImdbIDCalls[0] != "tt12345" {
		t.Errorf("FindByImdbID calls = %v, want [tt12345]", sf.byImdbIDCalls)
	}
}

// MapFixture.cs:223-231 — should_not_FindByImdbId_when_...tvdb_id_is_specified.
func TestMapSkipsImdbFallbackWhenTvdbIdSpecified(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)
	crit := &decisionengine.SearchCriteria{Series: ser}

	parsedInfo := mapTestParsedInfo()
	parsedInfo.SeriesTitle = "Another Name"

	_, err := svc.Map(context.Background(), parsedInfo, 10, 10, "tt12345", crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(sf.byImdbIDCalls) != 0 {
		t.Errorf("FindByImdbID calls = %v, want 0 (tvdbId > 0 suppresses IMDb fallback)", sf.byImdbIDCalls)
	}
}

// MapFixture.cs:258-275 — should_use_scene_season_number_from_xem_mapping_if_alias_matches_a_specific_season_number.
func TestMapXemSceneSeasonNumberOverridesMappedSeason(t *testing.T) {
	t.Parallel()
	scenum := 2
	mapping := &scenemappings.Mapping{
		Type:              "XemService",
		SceneSeasonNumber: &scenum,
	}
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{mapping: mapping}, sf)

	pi := mapTestParsedInfo()
	pi.SeasonNumber = 1
	sub, err := svc.Map(context.Background(), pi, 0, 0, "", nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.MappedSeasonNumber != 2 {
		t.Errorf("MappedSeasonNumber = %d, want 2 (XemService override)", sub.MappedSeasonNumber)
	}
}

// MapFixture.cs:277-295 — should_not_use_scene_season_number_from_xem_mapping_if_..._but_did_not_parse_season_1.
// Confusingly named in C# — the docstring says "should not" but the assertion
// expects the override TO take effect with SeasonNumber=2 because the offset
// arm (SeasonNumber!=null && SceneSeasonNumber<=parsedInfo.SeasonNumber) fires
// instead of the XemService override. C# `:191-195` + `:201-207`: a non-Xem
// path adds the diff; the test's mapping doesn't set SeasonNumber so the
// offset arm is a no-op, but the XemService elif also doesn't fire because
// parsedInfo.SeasonNumber != 1. Result: MappedSeasonNumber stays as parsed.
func TestMapXemOverrideOnlyFiresAtSeasonOne(t *testing.T) {
	t.Parallel()
	scenum := 2
	mapping := &scenemappings.Mapping{
		Type:              "XemService",
		SceneSeasonNumber: &scenum,
	}
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{mapping: mapping}, sf)

	pi := mapTestParsedInfo()
	pi.SeasonNumber = 2
	sub, err := svc.Map(context.Background(), pi, 0, 0, "", nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.MappedSeasonNumber != 2 {
		t.Errorf("MappedSeasonNumber = %d, want 2 (no XemService override at S>1)", sub.MappedSeasonNumber)
	}
}

// MapFixture.cs:323-345 — should_not_use_tvdbid_matching_when_alias_without_year_is_found_with_wrong_year.
// AliasMapping resolves but the resolved series's Year disagrees with
// parsedInfo's Year → abandon. The seriesFinder will be queried twice
// (step 5 FindByTitleAndYear → miss, then step 5b alias FindByTvdbID →
// returns a row that fails the year cross-check).
func TestMapAliasWithoutYearWrongYearAbandons(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries() // ser.Year = 2020

	// stubSceneByArg returns the alias-keyed mapping only when called
	// for the TitleWithoutYear ("Series Alias") — Step 1's
	// FindSceneMapping(SeriesTitle="Series Alias 2020") returns nil, so
	// the test exercises Step 5b (alias-by-year scene-mapping fallback)
	// in isolation.
	scene := stubSceneByArg{
		bySeriesTitle: map[string]*scenemappings.Mapping{
			"Series Alias": {TvdbID: ser.TvdbID},
		},
	}

	parsedInfo := mapTestParsedInfo()
	parsedInfo.SeriesTitle = "Series Alias 2020"
	parsedInfo.SeriesTitleInfo = &parser.SeriesTitleInfo{
		Title:            "Series Alias 2020",
		TitleWithoutYear: "Series Alias",
		Year:             2021,
	}

	sf := &stubSeriesFinder{
		byTvdbID: map[int]*series.Series{ser.TvdbID: ser},
	}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, scene, sf)

	sub, err := svc.Map(context.Background(), parsedInfo, 0, 0, "", nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.Series != nil {
		t.Errorf("Series = %v, want nil (wrong year abandons alias)", sub.Series)
	}
	if len(sf.byTvdbIDCalls) != 1 {
		t.Errorf("FindByTvdbID calls = %v, want 1", sf.byTvdbIDCalls)
	}
}

// MapFixture.cs:298-320 — should_use_tvdbid_matching_when_alias_without_year_is_found.
// Same as above but Year matches → alias resolution succeeds with MatchTypeAlias.
func TestMapAliasWithoutYearMatchesResolvesAlias(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries() // ser.Year = 2020
	scene := stubSceneByArg{
		bySeriesTitle: map[string]*scenemappings.Mapping{
			"Series Alias": {TvdbID: ser.TvdbID},
		},
	}

	parsedInfo := mapTestParsedInfo()
	parsedInfo.SeriesTitle = "Series Alias 2020"
	parsedInfo.SeriesTitleInfo = &parser.SeriesTitleInfo{
		Title:            "Series Alias 2020",
		TitleWithoutYear: "Series Alias",
		Year:             2020,
	}

	sf := &stubSeriesFinder{
		byTvdbID: map[int]*series.Series{ser.TvdbID: ser},
	}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, scene, sf)

	sub, err := svc.Map(context.Background(), parsedInfo, ser.TvdbID, ser.TvRageID, ser.ImdbID, nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.Series != ser {
		t.Errorf("Series = %v, want %v", sub.Series, ser)
	}
	if sub.SeriesMatchType != series.MatchTypeAlias {
		t.Errorf("SeriesMatchType = %v, want Alias", sub.SeriesMatchType)
	}
}

// Subject populated when no series resolves — Languages still set,
// Episodes is empty, SeriesMatchType is Unknown.
func TestMapNoSeriesResolvedReturnsBareSubject(t *testing.T) {
	t.Parallel()
	sf := &stubSeriesFinder{}
	svc := mkSvcAll(t, &stubFinder{}, &stubInfos{}, stubScene{}, sf)

	pi := mapTestParsedInfo()
	sub, err := svc.Map(context.Background(), pi, 0, 0, "", nil)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub == nil {
		t.Fatalf("nil subject")
	}
	if sub.Series != nil {
		t.Errorf("Series = %v, want nil", sub.Series)
	}
	if sub.SeriesMatchType != series.MatchTypeUnknown {
		t.Errorf("SeriesMatchType = %v, want Unknown", sub.SeriesMatchType)
	}
	if len(sub.Episodes) != 0 {
		t.Errorf("Episodes = %v, want empty", sub.Episodes)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.English {
		t.Errorf("Languages = %v, want [English]", sub.Languages)
	}
	if sub.ParsedEpisodeInfo != pi {
		t.Errorf("ParsedEpisodeInfo not propagated")
	}
}

// Resolved series with successful episode lookup → Episodes populated;
// when the matched episode is in searchCriteria.Episodes, EpisodeRequested
// is true. Mirrors C# `:238-242`.
func TestMapPopulatesEpisodesAndEpisodeRequested(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	ep := episodes.Episode{ID: 99, SeasonNumber: 1, EpisodeNumber: 1}
	ef := &stubFinder{
		findEpisodeMap: map[[3]int]*episodes.Episode{
			{ser.ID, 1, 1}: &ep,
		},
	}
	sf := &stubSeriesFinder{byTitle: map[string]*series.Series{"30 Stone": ser}}
	svc := mkSvcAll(t, ef, &stubInfos{}, stubScene{}, sf)

	crit := &decisionengine.SearchCriteria{
		Series:   ser,
		Episodes: []episodes.Episode{ep},
	}
	// Use a searchCriteria with matching Series so step 2 short-circuits
	// (cleanTitle match) — we want to test the EpisodeRequested tagging.
	sub, err := svc.Map(context.Background(), mapTestParsedInfo(), ser.TvdbID, ser.TvRageID, ser.ImdbID, crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.Series != ser {
		t.Fatalf("Series = %v, want %v", sub.Series, ser)
	}
	if len(sub.Episodes) != 1 || sub.Episodes[0].ID != ep.ID {
		t.Errorf("Episodes = %v, want [%v]", sub.Episodes, ep)
	}
	if !sub.EpisodeRequested {
		t.Errorf("EpisodeRequested = false, want true (matched episode in searchCriteria.Episodes)")
	}
}

// EpisodeRequested stays false when the matched episodes don't intersect
// searchCriteria.Episodes — guards against spurious tagging.
func TestMapEpisodeRequestedFalseOnDisjointEpisodes(t *testing.T) {
	t.Parallel()
	ser := mapTestSeries()
	matched := episodes.Episode{ID: 99, SeasonNumber: 1, EpisodeNumber: 1}
	requested := episodes.Episode{ID: 100, SeasonNumber: 1, EpisodeNumber: 2}
	ef := &stubFinder{
		findEpisodeMap: map[[3]int]*episodes.Episode{
			{ser.ID, 1, 1}: &matched,
		},
	}
	sf := &stubSeriesFinder{byTitle: map[string]*series.Series{"30 Stone": ser}}
	svc := mkSvcAll(t, ef, &stubInfos{}, stubScene{}, sf)

	crit := &decisionengine.SearchCriteria{Series: ser, Episodes: []episodes.Episode{requested}}

	sub, err := svc.Map(context.Background(), mapTestParsedInfo(), ser.TvdbID, ser.TvRageID, ser.ImdbID, crit)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if sub.EpisodeRequested {
		t.Errorf("EpisodeRequested = true, want false")
	}
}

// stubSceneByArg returns per-args FindSceneMapping responses so tests
// that need different mappings for the original-SeriesTitle vs alias
// lookup paths (Step 1 nil, Step 5b non-nil) can pin behaviour.
type stubSceneByArg struct {
	bySeriesTitle map[string]*scenemappings.Mapping
}

func (s stubSceneByArg) FindSceneMapping(seriesTitle, _ string, _ int) *scenemappings.Mapping {
	return s.bySeriesTitle[seriesTitle]
}
func (s stubSceneByArg) GetSceneSeasonNumber(_, _ string) *int { return nil }
