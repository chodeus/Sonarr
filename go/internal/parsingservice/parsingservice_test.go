package parsingservice_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

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
	s, err := parsingservice.NewService(ef, ib, nil, slog.Default())
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
	s, err := parsingservice.NewService(ef, ib, sm, slog.Default())
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
	_, err := parsingservice.NewService(nil, &stubInfos{}, nil, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "nil EpisodeFinder") {
		t.Errorf("got %v, want error mentioning nil EpisodeFinder", err)
	}
}

func TestNewServiceNilOptionalsUseDefaults(t *testing.T) {
	t.Parallel()
	s, err := parsingservice.NewService(&stubFinder{}, nil, nil, nil)
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
