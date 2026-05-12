package indexersearch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

type stubSeriesGetter struct {
	byID map[int]*series.Series
	err  error
}

func (s *stubSeriesGetter) GetSeries(_ context.Context, id int) (*series.Series, error) {
	if s.err != nil {
		return nil, s.err
	}
	if v, ok := s.byID[id]; ok {
		return v, nil
	}
	return nil, errors.New("series not found")
}

type stubEpisodeRepo struct {
	mu        sync.Mutex
	episodes  map[int]*episodes.Episode
	bySeason  map[[2]int][]episodes.Episode
	updates   [][]int // each entry: episode ids that were stamped
	getErr    error
	updateErr error
}

func (e *stubEpisodeRepo) GetEpisode(_ context.Context, id int) (*episodes.Episode, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	if v, ok := e.episodes[id]; ok {
		return v, nil
	}
	return nil, errors.New("episode not found")
}

func (e *stubEpisodeRepo) GetEpisodesBySeason(_ context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error) {
	return e.bySeason[[2]int{seriesID, seasonNumber}], nil
}

func (e *stubEpisodeRepo) UpdateLastSearchTime(_ context.Context, list []episodes.Episode) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]int, len(list))
	for i, ep := range list {
		ids[i] = ep.ID
	}
	e.updates = append(e.updates, ids)
	return e.updateErr
}

// fixtureSearchSvc builds a Service with one indexer that returns
// `releases` from FetchSearch and remembers the last criteria seen.
type capturingIndexer struct {
	id        int
	released  []any
	mu        sync.Mutex
	criteria  []Criteria
	failNext  bool
	failError error
}

func (c *capturingIndexer) ID() int                       { return c.id }
func (c *capturingIndexer) Name() string                  { return "cap" }
func (c *capturingIndexer) Priority() int                 { return 25 }
func (c *capturingIndexer) Tags() []int                   { return nil }
func (c *capturingIndexer) EnableRSS() bool               { return false }
func (c *capturingIndexer) EnableAutomaticSearch() bool   { return true }
func (c *capturingIndexer) EnableInteractiveSearch() bool { return true }
func (c *capturingIndexer) FetchRecent(_ context.Context) ([]any, error) {
	return nil, nil
}
func (c *capturingIndexer) FetchSearch(_ context.Context, criteria Criteria) ([]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.criteria = append(c.criteria, criteria)
	if c.failNext {
		return nil, c.failError
	}
	return c.released, nil
}

func (c *capturingIndexer) lastCriteria() Criteria {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.criteria) == 0 {
		return nil
	}
	return c.criteria[len(c.criteria)-1]
}

func newSearchSvc(t *testing.T, ser *series.Series, idx Indexer) (*Service, *stubEpisodeRepo) {
	t.Helper()
	src := &stubSource{indexers: []Indexer{idx}}
	sg := &stubSeriesGetter{byID: map[int]*series.Series{ser.ID: ser}}
	er := &stubEpisodeRepo{episodes: map[int]*episodes.Episode{}, bySeason: map[[2]int][]episodes.Episode{}}
	svc := NewService(src, nil, sg, er, &stubMapper{}, nil, nil)
	return svc, er
}

// --- EpisodeSearch routing ---

func TestEpisodeSearch_StandardEpisodeUsesSearchSingle(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", TvdbID: 1, SeriesType: series.SeriesTypeStandard}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 2, EpisodeNumber: 5}
	idx := &capturingIndexer{id: 1, released: []any{"r1"}}
	svc, er := newSearchSvc(t, ser, idx)
	er.episodes[ep.ID] = ep

	releases, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{})
	if err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases=%d, want 1", len(releases))
	}
	got, ok := idx.lastCriteria().(SingleEpisodeCriteria)
	if !ok {
		t.Fatalf("expected SingleEpisodeCriteria, got %T", idx.lastCriteria())
	}
	if got.SeasonNumber != 2 || got.EpisodeNumber != 5 {
		t.Errorf("criteria mismatch: %+v", got)
	}
	if len(er.updates) == 0 {
		t.Errorf("expected UpdateLastSearchTime to be called")
	}
}

func TestEpisodeSearch_DailyMissingAirDateErrors(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Daily", TvdbID: 1, SeriesType: series.SeriesTypeDaily}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 2, EpisodeNumber: 1}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.episodes[ep.ID] = ep

	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err == nil {
		t.Fatalf("expected daily-missing-airdate error")
	}
}

func TestEpisodeSearch_DailyDispatchesDailyEpisodeCriteria(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Daily", TvdbID: 1, SeriesType: series.SeriesTypeDaily}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 2026, EpisodeNumber: 1, AirDate: "2026-05-11"}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.episodes[ep.ID] = ep

	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	got, ok := idx.lastCriteria().(DailyEpisodeCriteria)
	if !ok {
		t.Fatalf("expected DailyEpisodeCriteria, got %T", idx.lastCriteria())
	}
	if got.AirDate.Format("2006-01-02") != "2026-05-11" {
		t.Errorf("airDate=%v", got.AirDate)
	}
}

func TestEpisodeSearch_AnimeNonSpecialUsesAnimeEpisodeCriteria(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Anime", TvdbID: 1, SeriesType: series.SeriesTypeAnime}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 5, AbsoluteEpisodeNumber: intPtr(50)}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.episodes[ep.ID] = ep

	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	got, ok := idx.lastCriteria().(AnimeEpisodeCriteria)
	if !ok {
		t.Fatalf("expected AnimeEpisodeCriteria, got %T", idx.lastCriteria())
	}
	if got.AbsoluteEpisodeNumber != 50 {
		t.Errorf("absoluteEpisodeNumber=%d, want 50", got.AbsoluteEpisodeNumber)
	}
}

func TestEpisodeSearch_Season0SpecialUsesSpecialEpisodeCriteria(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", TvdbID: 1, SeriesType: series.SeriesTypeStandard}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 0, EpisodeNumber: 1, Title: "Behind The Scenes"}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.episodes[ep.ID] = ep

	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	// SearchSpecial dispatches a SpecialEpisodeCriteria and then falls
	// through to SearchSingle for season/episode-number coverage; the
	// last criteria seen will be the SingleEpisodeCriteria.
	if len(idx.criteria) < 2 {
		t.Fatalf("expected 2+ dispatches (special + single), got %d", len(idx.criteria))
	}
	if _, ok := idx.criteria[0].(SpecialEpisodeCriteria); !ok {
		t.Errorf("first criteria should be SpecialEpisodeCriteria, got %T", idx.criteria[0])
	}
}

func TestEpisodeSearch_NilEpisodeErrors(t *testing.T) {
	src := &stubSource{}
	svc := NewService(src, nil, nil, nil, nil, nil, nil)
	if _, err := svc.EpisodeSearchByEpisode(context.Background(), nil, SearchSettings{}); err == nil {
		t.Fatalf("expected error for nil episode")
	}
}

// --- SeasonSearch routing ---

func TestSeasonSearch_StandardSeriesPerMappingDispatch(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", TvdbID: 1, SeriesType: series.SeriesTypeStandard}
	eps := []episodes.Episode{
		{ID: 1, SeriesID: 10, SeasonNumber: 3, EpisodeNumber: 1},
		{ID: 2, SeriesID: 10, SeasonNumber: 3, EpisodeNumber: 2},
	}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.bySeason[[2]int{10, 3}] = eps

	if _, err := svc.SeasonSearch(context.Background(), 10, 3, false, SearchSettings{}); err != nil {
		t.Fatalf("SeasonSearch: %v", err)
	}
	if len(idx.criteria) < 1 {
		t.Fatalf("expected at least 1 dispatch")
	}
	// Multi-episode mapping → SeasonCriteria.
	if _, ok := idx.criteria[0].(SeasonCriteria); !ok {
		t.Errorf("expected SeasonCriteria, got %T", idx.criteria[0])
	}
}

func TestSeasonSearch_SingleEpisodeUsesSingleCriteria(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", TvdbID: 1, SeriesType: series.SeriesTypeStandard}
	eps := []episodes.Episode{
		{ID: 1, SeriesID: 10, SeasonNumber: 3, EpisodeNumber: 1},
	}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.bySeason[[2]int{10, 3}] = eps

	if _, err := svc.SeasonSearch(context.Background(), 10, 3, false, SearchSettings{}); err != nil {
		t.Fatalf("SeasonSearch: %v", err)
	}
	if len(idx.criteria) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(idx.criteria))
	}
	if _, ok := idx.criteria[0].(SingleEpisodeCriteria); !ok {
		t.Errorf("expected SingleEpisodeCriteria, got %T", idx.criteria[0])
	}
}

func TestSeasonSearch_MissingOnlyFiltersHasFile(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", TvdbID: 1, SeriesType: series.SeriesTypeStandard}
	have := intPtr(123)
	eps := []episodes.Episode{
		{ID: 1, SeriesID: 10, SeasonNumber: 3, EpisodeNumber: 1, EpisodeFileID: have},
		{ID: 2, SeriesID: 10, SeasonNumber: 3, EpisodeNumber: 2}, // missing
	}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.bySeason[[2]int{10, 3}] = eps

	if _, err := svc.SeasonSearch(context.Background(), 10, 3, true, SearchSettings{}); err != nil {
		t.Fatalf("SeasonSearch: %v", err)
	}
	// Only the missing episode should have driven a dispatch — single
	// criteria.
	if len(idx.criteria) != 1 {
		t.Fatalf("expected 1 dispatch (only missing), got %d", len(idx.criteria))
	}
	got, ok := idx.criteria[0].(SingleEpisodeCriteria)
	if !ok {
		t.Fatalf("expected SingleEpisodeCriteria, got %T", idx.criteria[0])
	}
	if got.EpisodeNumber != 2 {
		t.Errorf("expected episode 2, got %d", got.EpisodeNumber)
	}
}

func TestSeasonSearch_NoEpisodesReturnsNil(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", SeriesType: series.SeriesTypeStandard}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.bySeason[[2]int{10, 3}] = nil

	got, err := svc.SeasonSearch(context.Background(), 10, 3, false, SearchSettings{})
	if err != nil {
		t.Fatalf("SeasonSearch: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestSeasonSearch_DailySeriesPathUsesDailySeasonCriteria(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Daily", SeriesType: series.SeriesTypeDaily}
	eps := []episodes.Episode{
		{ID: 1, SeriesID: 10, AirDate: "2026-01-01"},
		{ID: 2, SeriesID: 10, AirDate: "2026-02-01"},
	}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.bySeason[[2]int{10, 0}] = eps

	if _, err := svc.SeasonSearch(context.Background(), 10, 0, false, SearchSettings{}); err != nil {
		t.Fatalf("SeasonSearch: %v", err)
	}
	if _, ok := idx.criteria[0].(DailySeasonCriteria); !ok {
		t.Errorf("expected DailySeasonCriteria, got %T", idx.criteria[0])
	}
}

// --- dispatchSearch / UpdateLastSearchTime ---

func TestDispatchSearch_StampsLastSearchTime(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", SeriesType: series.SeriesTypeStandard}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1}
	idx := &capturingIndexer{id: 1, released: []any{"a"}}
	svc, er := newSearchSvc(t, ser, idx)
	er.episodes[ep.ID] = ep

	before := time.Now().UTC()
	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	if len(er.updates) == 0 {
		t.Fatalf("expected UpdateLastSearchTime invocation")
	}
	// Timing sanity: the per-episode stamp lives inside the
	// UpdateLastSearchTime call payload, not in stubEpisodeRepo's
	// captured ids — but the call happened, which is what we
	// assert. The ts predates `now` so the test is stable.
	_ = before
}

func TestDispatchSearch_UpdateLastSearchTimeErrorIsLogged(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", SeriesType: series.SeriesTypeStandard}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.episodes[ep.ID] = ep
	er.updateErr = errors.New("write conflict")

	// Error must not propagate — it's best-effort per the
	// dispatchSearch doc-comment.
	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err != nil {
		t.Fatalf("EpisodeSearch should swallow the update error, got %v", err)
	}
}

// --- error propagation from underlying lookups ---

func TestEpisodeSearch_LoadEpisodeErrorPropagates(t *testing.T) {
	src := &stubSource{}
	er := &stubEpisodeRepo{getErr: errors.New("db gone")}
	svc := NewService(src, nil, nil, er, &stubMapper{}, nil, nil)
	if _, err := svc.EpisodeSearch(context.Background(), 1, SearchSettings{}); err == nil {
		t.Fatalf("expected error to propagate")
	}
}

func TestEpisodeSearch_LoadSeriesErrorPropagates(t *testing.T) {
	er := &stubEpisodeRepo{episodes: map[int]*episodes.Episode{
		1: {ID: 1, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1},
	}}
	sg := &stubSeriesGetter{err: errors.New("series db down")}
	svc := NewService(&stubSource{}, nil, sg, er, &stubMapper{}, nil, nil)
	if _, err := svc.EpisodeSearch(context.Background(), 1, SearchSettings{}); err == nil {
		t.Fatalf("expected series error to propagate")
	}
}

func TestEpisodeSearch_DailyParseErrorPropagates(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Daily", SeriesType: series.SeriesTypeDaily}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, AirDate: "not-a-date"}
	idx := &capturingIndexer{id: 1}
	svc, er := newSearchSvc(t, ser, idx)
	er.episodes[ep.ID] = ep
	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err == nil {
		t.Fatalf("expected airdate parse error")
	}
}

// --- criteriaForEpisodes (GetSceneNames integration) ---

func TestCriteriaForEpisodes_AppendsSeriesTitleWhenAbsent(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "MyShow", TvdbID: 100, SeriesType: series.SeriesTypeStandard}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2026-01-01"}
	mapper := &stubMapper{names: []string{"AltA", "AltB"}}
	idx := &capturingIndexer{id: 1}
	src := &stubSource{indexers: []Indexer{idx}}
	sg := &stubSeriesGetter{byID: map[int]*series.Series{ser.ID: ser}}
	er := &stubEpisodeRepo{episodes: map[int]*episodes.Episode{ep.ID: ep}}
	svc := NewService(src, nil, sg, er, mapper, nil, nil)

	// Daily series → criteriaForEpisodes path.
	prevType := ser.SeriesType
	ser.SeriesType = series.SeriesTypeDaily
	defer func() { ser.SeriesType = prevType }()

	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	c, ok := idx.lastCriteria().(DailyEpisodeCriteria)
	if !ok {
		t.Fatalf("expected DailyEpisodeCriteria, got %T", idx.lastCriteria())
	}
	titles := c.SceneTitles
	if len(titles) != 3 || titles[0] != "AltA" || titles[1] != "AltB" || titles[2] != "MyShow" {
		t.Errorf("expected [AltA AltB MyShow], got %v", titles)
	}
}

func TestCriteriaForEpisodes_DoesNotDuplicateSeriesTitle(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "MyShow", TvdbID: 100, SeriesType: series.SeriesTypeDaily}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2026-01-01"}
	mapper := &stubMapper{names: []string{"myshow"}} // case-insensitive collision
	idx := &capturingIndexer{id: 1}
	src := &stubSource{indexers: []Indexer{idx}}
	sg := &stubSeriesGetter{byID: map[int]*series.Series{ser.ID: ser}}
	er := &stubEpisodeRepo{episodes: map[int]*episodes.Episode{ep.ID: ep}}
	svc := NewService(src, nil, sg, er, mapper, nil, nil)

	if _, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{}); err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	c, _ := idx.lastCriteria().(DailyEpisodeCriteria)
	titles := c.SceneTitles
	if len(titles) != 1 {
		t.Errorf("expected 1 (case-insensitive dedup), got %v", titles)
	}
}

// --- Slice 4.BV (C-cleanup [C13]) DecisionMaker wiring -----------

// recordingDecisionMaker satisfies DecisionMaker. Captures the
// (reports, criteria) pair passed in + returns one Decision per
// report so tests can verify the wiring without exercising the full
// spec pipeline.
type recordingDecisionMaker struct {
	calls    int
	reports  []any
	criteria []*decisionengine.SearchCriteria
}

func (r *recordingDecisionMaker) GetSearchDecision(_ context.Context, reports []any, criteria *decisionengine.SearchCriteria) []decisionengine.Decision {
	r.calls++
	r.reports = append(r.reports, reports...)
	r.criteria = append(r.criteria, criteria)
	out := make([]decisionengine.Decision, 0, len(reports))
	for _, rep := range reports {
		out = append(out, decisionengine.NewDecision(rep))
	}
	return out
}

func TestDispatchSearch_InvokesDecisionMakerWithCriteria(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", TvdbID: 1, SeriesType: series.SeriesTypeStandard}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 2, EpisodeNumber: 5}
	idx := &capturingIndexer{id: 1, released: []any{"r1", "r2"}}
	src := &stubSource{indexers: []Indexer{idx}}
	sg := &stubSeriesGetter{byID: map[int]*series.Series{ser.ID: ser}}
	er := &stubEpisodeRepo{episodes: map[int]*episodes.Episode{ep.ID: ep}}
	rec := &recordingDecisionMaker{}

	svc := NewService(src, nil, sg, er, &stubMapper{}, rec, nil)
	got, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{UserInvokedSearch: true})
	if err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("decisionMaker.GetSearchDecision calls = %d, want 1", rec.calls)
	}
	if len(rec.reports) != 2 {
		t.Errorf("reports passed = %d, want 2", len(rec.reports))
	}
	if len(rec.criteria) != 1 || rec.criteria[0] == nil {
		t.Fatalf("criteria = %+v, want non-nil pointer", rec.criteria)
	}
	if rec.criteria[0].Series == nil || rec.criteria[0].Series.ID != 10 {
		t.Errorf("criteria.Series = %+v, want ID=10", rec.criteria[0].Series)
	}
	if !rec.criteria[0].UserInvokedSearch {
		t.Errorf("criteria.UserInvokedSearch lost in projection")
	}
	if len(got) != 2 {
		t.Errorf("decisions returned = %d, want 2", len(got))
	}
}

func TestDispatchSearch_NilDecisionMakerFallsBackToPassthrough(t *testing.T) {
	ser := &series.Series{ID: 10, Title: "Std", TvdbID: 1, SeriesType: series.SeriesTypeStandard}
	ep := &episodes.Episode{ID: 100, SeriesID: 10, SeasonNumber: 2, EpisodeNumber: 5}
	idx := &capturingIndexer{id: 1, released: []any{"r1", "r2", "r3"}}
	src := &stubSource{indexers: []Indexer{idx}}
	sg := &stubSeriesGetter{byID: map[int]*series.Series{ser.ID: ser}}
	er := &stubEpisodeRepo{episodes: map[int]*episodes.Episode{ep.ID: ep}}

	svc := NewService(src, nil, sg, er, &stubMapper{}, nil, nil)
	got, err := svc.EpisodeSearch(context.Background(), ep.ID, SearchSettings{})
	if err != nil {
		t.Fatalf("EpisodeSearch: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("passthrough wraps each report; got %d decisions, want 3", len(got))
	}
	// Each Decision's Subject is the raw report from the indexer.
	for i, d := range got {
		if d.Subject != idx.released[i] {
			t.Errorf("decision[%d].Subject = %v, want %v", i, d.Subject, idx.released[i])
		}
	}
}
