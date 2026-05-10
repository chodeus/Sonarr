package indexersearch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/series"
)

// stubIndexer is a hand-rolled Indexer for the dispatcher tests. We
// don't use Adapter here because Adapter is itself under test by the
// same suite — using a separate hand-rolled stub keeps the contracts
// independent.
type stubIndexer struct {
	id       int
	name     string
	priority int
	tags     []int
	rss      bool
	auto     bool
	inter    bool
	recent   func(ctx context.Context) ([]any, error)
	search   func(ctx context.Context, c Criteria) ([]any, error)
}

func (s *stubIndexer) ID() int                       { return s.id }
func (s *stubIndexer) Name() string                  { return s.name }
func (s *stubIndexer) Priority() int                 { return s.priority }
func (s *stubIndexer) Tags() []int                   { return s.tags }
func (s *stubIndexer) EnableRSS() bool               { return s.rss }
func (s *stubIndexer) EnableAutomaticSearch() bool   { return s.auto }
func (s *stubIndexer) EnableInteractiveSearch() bool { return s.inter }
func (s *stubIndexer) FetchRecent(ctx context.Context) ([]any, error) {
	if s.recent == nil {
		return nil, errors.New("stub: no FetchRecent")
	}
	return s.recent(ctx)
}
func (s *stubIndexer) FetchSearch(ctx context.Context, c Criteria) ([]any, error) {
	if s.search == nil {
		return nil, errors.New("stub: no FetchSearch")
	}
	return s.search(ctx, c)
}

// stubSource returns the configured Indexers for every call.
type stubSource struct {
	indexers []Indexer
	err      error
}

func (s *stubSource) Enabled(_ context.Context) ([]Indexer, error) {
	return s.indexers, s.err
}

// stubRecorder captures success/failure calls for assertion.
type stubRecorder struct {
	mu        sync.Mutex
	successes []int
	failures  []int
	failErr   error
}

func (r *stubRecorder) RecordSuccess(_ context.Context, id int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.successes = append(r.successes, id)
	return nil
}

func (r *stubRecorder) RecordFailure(_ context.Context, id int, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, id)
	return r.failErr
}

func (r *stubRecorder) RecordConnectionFailure(_ context.Context, _ int) error {
	return nil
}

func (r *stubRecorder) OnProviderDeleted(_ context.Context, _ int) error {
	return nil
}

func makeRelease(guid string) any {
	return map[string]any{"Guid": guid}
}

// --- Adapter ---

func TestAdapter_ImplementsIndexer(t *testing.T) {
	a := &Adapter{
		IDValue:                      7,
		NameValue:                    "AdapterTest",
		PriorityValue:                15,
		TagsValue:                    []int{1, 2},
		EnableRSSValue:               true,
		EnableAutomaticSearchValue:   true,
		EnableInteractiveSearchValue: false,
		FetchRecentFunc: func(ctx context.Context) ([]any, error) {
			return []any{makeRelease("g1")}, nil
		},
		FetchSearchFunc: func(ctx context.Context, c Criteria) ([]any, error) {
			return []any{makeRelease("g2")}, nil
		},
	}
	if a.ID() != 7 || a.Name() != "AdapterTest" || a.Priority() != 15 {
		t.Fatalf("metadata mismatch: %+v", a)
	}
	if !a.EnableRSS() || !a.EnableAutomaticSearch() || a.EnableInteractiveSearch() {
		t.Fatalf("flags mismatch")
	}
	if got := a.Tags(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("tags mismatch: %v", got)
	}
	rs, err := a.FetchRecent(context.Background())
	if err != nil || len(rs) != 1 {
		t.Fatalf("FetchRecent: got %v / %v", rs, err)
	}
	rs, err = a.FetchSearch(context.Background(), SingleEpisodeCriteria{})
	if err != nil || len(rs) != 1 {
		t.Fatalf("FetchSearch: got %v / %v", rs, err)
	}
}

func TestAdapter_NilFetchRecentFunc_Errors(t *testing.T) {
	a := &Adapter{NameValue: "noRecent"}
	if _, err := a.FetchRecent(context.Background()); err == nil {
		t.Fatalf("expected error for nil FetchRecentFunc")
	}
}

func TestAdapter_NilFetchSearchFunc_Errors(t *testing.T) {
	a := &Adapter{NameValue: "noSearch"}
	if _, err := a.FetchSearch(context.Background(), SingleEpisodeCriteria{}); err == nil {
		t.Fatalf("expected error for nil FetchSearchFunc")
	}
}

// --- Eligible ---

func newCriteria(tags []int, interactive bool) CriteriaBase {
	s := &series.Series{Title: "test", Tags: tags}
	return CriteriaBase{Series: s, InteractiveSearch: interactive}
}

func TestEligible_AutomaticSelectsAutoFlag(t *testing.T) {
	auto := &stubIndexer{id: 1, name: "auto", auto: true}
	inter := &stubIndexer{id: 2, name: "inter", inter: true}
	src := &stubSource{indexers: []Indexer{auto, inter}}
	svc := NewService(src, nil, nil)

	got, err := svc.Eligible(context.Background(), newCriteria(nil, false))
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if len(got) != 1 || got[0].ID() != 1 {
		t.Fatalf("expected only auto indexer, got %d entries", len(got))
	}
}

func TestEligible_InteractiveSelectsInteractiveFlag(t *testing.T) {
	auto := &stubIndexer{id: 1, name: "auto", auto: true}
	inter := &stubIndexer{id: 2, name: "inter", inter: true}
	src := &stubSource{indexers: []Indexer{auto, inter}}
	svc := NewService(src, nil, nil)

	got, err := svc.Eligible(context.Background(), newCriteria(nil, true))
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if len(got) != 1 || got[0].ID() != 2 {
		t.Fatalf("expected only interactive indexer, got %d entries", len(got))
	}
}

func TestEligible_TagsUntaggedIndexerMatches(t *testing.T) {
	idx := &stubIndexer{id: 1, name: "untagged", auto: true}
	src := &stubSource{indexers: []Indexer{idx}}
	svc := NewService(src, nil, nil)

	// Untagged indexer matches a series with arbitrary tags.
	got, err := svc.Eligible(context.Background(), newCriteria([]int{99}, false))
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
}

func TestEligible_TagsIntersectionRequired(t *testing.T) {
	matching := &stubIndexer{id: 1, name: "match", auto: true, tags: []int{42}}
	disjoint := &stubIndexer{id: 2, name: "disjoint", auto: true, tags: []int{99}}
	src := &stubSource{indexers: []Indexer{matching, disjoint}}
	svc := NewService(src, nil, nil)

	got, err := svc.Eligible(context.Background(), newCriteria([]int{42}, false))
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if len(got) != 1 || got[0].ID() != 1 {
		t.Fatalf("expected only matching indexer, got %d entries", len(got))
	}
}

func TestEligible_TaggedIndexerVsUntaggedSeriesIsExcluded(t *testing.T) {
	tagged := &stubIndexer{id: 1, name: "tagged", auto: true, tags: []int{42}}
	src := &stubSource{indexers: []Indexer{tagged}}
	svc := NewService(src, nil, nil)

	got, err := svc.Eligible(context.Background(), newCriteria(nil, false))
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 (tagged indexer + untagged series), got %d", len(got))
	}
}

func TestEligible_NilEntriesSkipped(t *testing.T) {
	idx := &stubIndexer{id: 1, name: "ok", auto: true}
	src := &stubSource{indexers: []Indexer{nil, idx, nil}}
	svc := NewService(src, nil, nil)

	got, err := svc.Eligible(context.Background(), newCriteria(nil, false))
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 non-nil, got %d", len(got))
	}
}

func TestEligible_SourceErrorPropagates(t *testing.T) {
	src := &stubSource{err: errors.New("boom")}
	svc := NewService(src, nil, nil)
	if _, err := svc.Eligible(context.Background(), newCriteria(nil, false)); err == nil {
		t.Fatalf("expected source error to propagate")
	}
}

// --- Dispatch ---

func TestDispatch_AggregatesAcrossIndexers(t *testing.T) {
	a := &stubIndexer{id: 1, name: "a", auto: true,
		search: func(ctx context.Context, c Criteria) ([]any, error) {
			return []any{makeRelease("a1"), makeRelease("a2")}, nil
		},
	}
	b := &stubIndexer{id: 2, name: "b", auto: true,
		search: func(ctx context.Context, c Criteria) ([]any, error) {
			return []any{makeRelease("b1")}, nil
		},
	}
	src := &stubSource{indexers: []Indexer{a, b}}
	rec := &stubRecorder{}
	svc := NewService(src, rec, nil)

	criteria := SingleEpisodeCriteria{
		CriteriaBase:  newCriteria(nil, false),
		EpisodeNumber: 1, SeasonNumber: 1,
	}
	releases, err := svc.Dispatch(context.Background(), func(ctx context.Context, idx Indexer) ([]any, error) {
		return idx.FetchSearch(ctx, criteria)
	}, criteria.CriteriaBase)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(releases) != 3 {
		t.Fatalf("expected 3 aggregated releases, got %d", len(releases))
	}
	if got := len(rec.successes); got != 2 {
		t.Fatalf("expected 2 RecordSuccess calls, got %d", got)
	}
	if got := len(rec.failures); got != 0 {
		t.Fatalf("expected 0 RecordFailure calls, got %d", got)
	}
}

func TestDispatch_PerIndexerErrorIsolated(t *testing.T) {
	good := &stubIndexer{id: 1, name: "good", auto: true,
		search: func(ctx context.Context, c Criteria) ([]any, error) {
			return []any{makeRelease("g1")}, nil
		},
	}
	bad := &stubIndexer{id: 2, name: "bad", auto: true,
		search: func(ctx context.Context, c Criteria) ([]any, error) {
			return nil, errors.New("indexer down")
		},
	}
	src := &stubSource{indexers: []Indexer{good, bad}}
	rec := &stubRecorder{}
	svc := NewService(src, rec, nil)

	releases, err := svc.Dispatch(context.Background(), func(ctx context.Context, idx Indexer) ([]any, error) {
		return idx.FetchSearch(ctx, SingleEpisodeCriteria{})
	}, newCriteria(nil, false))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release (only the good indexer), got %d", len(releases))
	}
	if len(rec.successes) != 1 || rec.successes[0] != 1 {
		t.Fatalf("expected RecordSuccess(1), got %v", rec.successes)
	}
	if len(rec.failures) != 1 || rec.failures[0] != 2 {
		t.Fatalf("expected RecordFailure(2), got %v", rec.failures)
	}
}

func TestDispatch_NoEligibleReturnsEmpty(t *testing.T) {
	idx := &stubIndexer{id: 1, name: "i", inter: true} // auto=false
	src := &stubSource{indexers: []Indexer{idx}}
	svc := NewService(src, nil, nil)

	releases, err := svc.Dispatch(context.Background(), func(ctx context.Context, idx Indexer) ([]any, error) {
		t.Fatalf("action should not be invoked when no indexers eligible")
		return nil, nil
	}, newCriteria(nil, false))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("expected 0 releases, got %d", len(releases))
	}
}

func TestDispatch_NilActionErrors(t *testing.T) {
	src := &stubSource{}
	svc := NewService(src, nil, nil)
	if _, err := svc.Dispatch(context.Background(), nil, newCriteria(nil, false)); err == nil {
		t.Fatalf("expected nil-action error")
	}
}

func TestDispatch_RecorderNilSafe(t *testing.T) {
	idx := &stubIndexer{id: 1, name: "ok", auto: true,
		search: func(ctx context.Context, c Criteria) ([]any, error) { return nil, nil },
	}
	src := &stubSource{indexers: []Indexer{idx}}
	svc := NewService(src, nil, nil) // recorder=nil

	if _, err := svc.Dispatch(context.Background(), func(ctx context.Context, idx Indexer) ([]any, error) {
		return idx.FetchSearch(ctx, SingleEpisodeCriteria{})
	}, newCriteria(nil, false)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
}

// --- FetchRSS ---

func TestFetchRSS_FiltersToEnableRSSOnly(t *testing.T) {
	rssOn := &stubIndexer{id: 1, name: "rssOn", rss: true,
		recent: func(ctx context.Context) ([]any, error) {
			return []any{makeRelease("r1")}, nil
		},
	}
	rssOff := &stubIndexer{id: 2, name: "rssOff", rss: false,
		recent: func(ctx context.Context) ([]any, error) {
			t.Fatalf("rssOff indexer should not be invoked")
			return nil, nil
		},
	}
	src := &stubSource{indexers: []Indexer{rssOn, rssOff}}
	rec := &stubRecorder{}
	svc := NewService(src, rec, nil)

	releases, err := svc.FetchRSS(context.Background())
	if err != nil {
		t.Fatalf("FetchRSS: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	if len(rec.successes) != 1 || rec.successes[0] != 1 {
		t.Fatalf("expected RecordSuccess(1), got %v", rec.successes)
	}
}

func TestFetchRSS_NoRSSEnabledReturnsEmpty(t *testing.T) {
	idx := &stubIndexer{id: 1, name: "noRss", rss: false}
	src := &stubSource{indexers: []Indexer{idx}}
	svc := NewService(src, nil, nil)

	releases, err := svc.FetchRSS(context.Background())
	if err != nil {
		t.Fatalf("FetchRSS: %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("expected 0 releases, got %d", len(releases))
	}
}

func TestFetchRSS_PerIndexerErrorIsolated(t *testing.T) {
	good := &stubIndexer{id: 1, name: "good", rss: true,
		recent: func(ctx context.Context) ([]any, error) {
			return []any{makeRelease("g1")}, nil
		},
	}
	bad := &stubIndexer{id: 2, name: "bad", rss: true,
		recent: func(ctx context.Context) ([]any, error) {
			return nil, errors.New("rss timeout")
		},
	}
	src := &stubSource{indexers: []Indexer{good, bad}}
	rec := &stubRecorder{}
	svc := NewService(src, rec, nil)

	releases, err := svc.FetchRSS(context.Background())
	if err != nil {
		t.Fatalf("FetchRSS: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	if len(rec.failures) != 1 || rec.failures[0] != 2 {
		t.Fatalf("expected RecordFailure(2), got %v", rec.failures)
	}
}

func TestFetchRSS_SourceErrorPropagates(t *testing.T) {
	src := &stubSource{err: errors.New("db gone")}
	svc := NewService(src, nil, nil)
	if _, err := svc.FetchRSS(context.Background()); err == nil {
		t.Fatalf("expected source error to propagate")
	}
}

// --- helper coverage ---

func TestTagsMatch(t *testing.T) {
	cases := []struct {
		name        string
		indexerTags []int
		seriesTags  []int
		want        bool
	}{
		{"untagged-indexer matches always", nil, []int{1, 2}, true},
		{"untagged-indexer matches untagged-series", nil, nil, true},
		{"tagged-indexer untagged-series excluded", []int{1}, nil, false},
		{"intersect matches", []int{1, 3}, []int{2, 3}, true},
		{"disjoint excluded", []int{1}, []int{2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := map[int]struct{}{}
			for _, t := range tc.seriesTags {
				set[t] = struct{}{}
			}
			if len(set) == 0 {
				set = nil
			}
			if got := tagsMatch(tc.indexerTags, set); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
