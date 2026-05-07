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
	s, err := parsingservice.NewService(ef, ib, slog.Default())
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
	_, err := parsingservice.NewService(nil, &stubInfos{}, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "nil EpisodeFinder") {
		t.Errorf("got %v, want error mentioning nil EpisodeFinder", err)
	}
}

func TestNewServiceNilInfosUsesDefault(t *testing.T) {
	t.Parallel()
	s, err := parsingservice.NewService(&stubFinder{}, nil, nil)
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
