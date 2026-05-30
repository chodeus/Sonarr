package importlists_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/importlistexclusions"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/series"
)

// stubExclusions implements the exclusionLister narrow interface.
type stubExclusions struct {
	rows []importlistexclusions.Exclusion
	err  error
}

func (s *stubExclusions) All(_ context.Context) ([]importlistexclusions.Exclusion, error) {
	return s.rows, s.err
}

// stubSeriesLookup implements the seriesLookup narrow interface.
type stubSeriesLookup struct {
	ids []int
	err error
}

func (s *stubSeriesLookup) AllTvdbIDs(_ context.Context) ([]int, error) {
	return s.ids, s.err
}

// stubSeriesAdder captures the dispatched batch so tests can pin
// the orchestrator's output. Implements the seriesAdder narrow
// interface.
type stubSeriesAdder struct {
	mu    sync.Mutex
	batch []series.Series
	err   error
}

func (s *stubSeriesAdder) AddSeries(_ context.Context, batch []series.Series) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batch = append(s.batch, batch...)
	return s.err
}

// fakeTranslator implements the IDTranslator interface. Tests
// configure per-method outcomes via the maps.
type fakeTranslator struct {
	imdb    map[string]int
	tmdb    map[int]int
	anilist map[int]int
	mal     map[int]int
	titles  map[int]string
}

func (f *fakeTranslator) ByImdb(_ context.Context, id string) (int, string, bool) {
	if t, ok := f.imdb[id]; ok {
		return t, f.titles[t], true
	}
	return 0, "", false
}

func (f *fakeTranslator) ByTmdb(_ context.Context, id int) (int, string, bool) {
	if t, ok := f.tmdb[id]; ok {
		return t, f.titles[t], true
	}
	return 0, "", false
}

func (f *fakeTranslator) ByAniList(_ context.Context, id int) (int, string, bool) {
	if t, ok := f.anilist[id]; ok {
		return t, f.titles[t], true
	}
	return 0, "", false
}

func (f *fakeTranslator) ByMyAnimeList(_ context.Context, id int) (int, string, bool) {
	if t, ok := f.mal[id]; ok {
		return t, f.titles[t], true
	}
	return 0, "", false
}

// fakeFetchProvider is a Provider that returns a fixed FetchResult.
// Used to control orchestrator inputs without standing up a real
// concrete provider package.
type fakeFetchProvider struct {
	def      *importlists.ImportList
	result   importlists.FetchResult
	fetchErr error
}

func (p *fakeFetchProvider) Name() string                        { return "FakeFetch" }
func (p *fakeFetchProvider) Definition() *importlists.ImportList { return p.def }
func (p *fakeFetchProvider) ListType() importlists.ListType      { return importlists.ListTypeOther }
func (p *fakeFetchProvider) MinRefreshInterval() time.Duration   { return time.Hour }
func (p *fakeFetchProvider) Fetch(context.Context) (importlists.FetchResult, error) {
	return p.result, p.fetchErr
}
func (p *fakeFetchProvider) Test(context.Context) ([]importlists.TestFailure, error) {
	return nil, nil
}
func (p *fakeFetchProvider) RequestAction(context.Context, string, map[string]string) (any, error) {
	return nil, nil
}

// Build a Factory whose registry produces fakeFetchProvider entries
// per row. Each row's Settings JSON encodes the FetchResult for that
// list. Simpler: keep results in a closure-captured map keyed by
// row.ID.
func buildSyncFactory(t *testing.T, rows []importlists.ImportList, fetchByID map[int]importlists.FetchResult, fetchErrByID map[int]error) *importlists.Factory {
	t.Helper()
	registry := importlists.NewRegistry()
	contract := "StubSettings"
	if err := registry.Register("Stub", contract, func(def *importlists.ImportList, _ importlists.Dependencies) (importlists.Provider, error) {
		return &fakeFetchProvider{
			def:      def,
			result:   fetchByID[def.ID],
			fetchErr: fetchErrByID[def.ID],
		}, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Make sure every row carries the matching Implementation +
	// ConfigContract so Construct can look up our factory.
	for i := range rows {
		if rows[i].Implementation == "" {
			rows[i].Implementation = "Stub"
		}
		if rows[i].ConfigContract == nil {
			c := contract
			rows[i].ConfigContract = &c
		}
	}
	repo := &stubFactoryRepo{rows: rows}
	factory, err := importlists.NewFactory(repo, registry, newStubFactoryStatus(), importlists.Dependencies{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	return factory
}

// newSync builds a SyncService wired to controllable stubs.
func newSync(t *testing.T, factory *importlists.Factory, exclusions *stubExclusions, lookup *stubSeriesLookup, adder *stubSeriesAdder, translator importlists.IDTranslator) *importlists.SyncService {
	t.Helper()
	s, err := importlists.NewSyncService(factory, exclusions, lookup, adder, translator, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewSyncService: %v", err)
	}
	return s
}

func TestNewSyncService_RejectsNilDeps(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	factory := buildSyncFactory(t, rows, nil, nil)
	exclusions := &stubExclusions{}
	lookup := &stubSeriesLookup{}
	adder := &stubSeriesAdder{}

	cases := []struct {
		name string
		f    *importlists.Factory
		x    *stubExclusions
		l    *stubSeriesLookup
		a    *stubSeriesAdder
	}{
		{"nil_factory", nil, exclusions, lookup, adder},
		{"nil_exclusions", factory, nil, lookup, adder},
		{"nil_lookup", factory, exclusions, nil, adder},
		{"nil_adder", factory, exclusions, lookup, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var (
				exc importlists.IDTranslator
				ex  *stubExclusions
				lk  *stubSeriesLookup
				ad  *stubSeriesAdder
			)
			_ = exc
			ex = tc.x
			lk = tc.l
			ad = tc.a
			var addrExc importlists.IDTranslator
			_ = addrExc
			// Build the call directly: each stub is nil-when-missing.
			var x interface {
				All(context.Context) ([]importlistexclusions.Exclusion, error)
			}
			if ex != nil {
				x = ex
			}
			var l interface {
				AllTvdbIDs(context.Context) ([]int, error)
			}
			if lk != nil {
				l = lk
			}
			var a interface {
				AddSeries(context.Context, []series.Series) error
			}
			if ad != nil {
				a = ad
			}
			if _, err := importlists.NewSyncService(tc.f, x, l, a, nil, nil); err == nil {
				t.Errorf("%s: expected error", tc.name)
			}
		})
	}
}

func TestSync_HappyPath_FiltersExclusions_ExistingDedup(t *testing.T) {
	t.Parallel()
	parent := makeRow(1, "PeerList", boolPtr(true))
	parent.RootFolderPath = "/mnt/tv"
	parent.QualityProfileID = 5
	parent.Tags = []int{11, 22}
	parent.ShouldMonitor = 1 // != 12 (None) → monitored=true
	parent.SeasonFolder = true
	parent.SeriesType = 0
	parent.MonitorNewItems = 1

	rows := []importlists.ImportList{parent}
	// 4 items: one healthy, one excluded, one already-in-library, one dup.
	year := 2020
	_ = year
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{
			{TvdbID: 100, Title: "Healthy", Year: 2020, ImportListID: 1, ImportList: parent.Name},
			{TvdbID: 200, Title: "Excluded", ImportListID: 1, ImportList: parent.Name},
			{TvdbID: 300, Title: "AlreadyExists", ImportListID: 1, ImportList: parent.Name},
			{TvdbID: 100, Title: "DupHealthy", ImportListID: 1, ImportList: parent.Name},
		}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	exclusions := &stubExclusions{rows: []importlistexclusions.Exclusion{
		{ID: 1, TvdbID: 200, Title: "Excluded"},
	}}
	lookup := &stubSeriesLookup{ids: []int{300}}
	adder := &stubSeriesAdder{}
	svc := newSync(t, factory, exclusions, lookup, adder, nil)

	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.ItemsFetched != 4 {
		t.Errorf("ItemsFetched: %d want 4", res.ItemsFetched)
	}
	if res.SeriesAdded != 1 {
		t.Errorf("SeriesAdded: %d want 1", res.SeriesAdded)
	}
	if res.SkippedExcluded != 1 {
		t.Errorf("SkippedExcluded: %d want 1", res.SkippedExcluded)
	}
	if res.SkippedExisting != 1 {
		t.Errorf("SkippedExisting: %d want 1", res.SkippedExisting)
	}
	if len(adder.batch) != 1 || adder.batch[0].TvdbID != 100 {
		t.Fatalf("batch: %+v", adder.batch)
	}
	got := adder.batch[0]
	if got.Title != "Healthy" {
		t.Errorf("Title: %q", got.Title)
	}
	if got.Year == 0 || got.Year != 2020 {
		t.Errorf("Year: %v", got.Year)
	}
	if !got.Monitored {
		t.Errorf("Monitored: false want true")
	}
	if got.RootFolderPath != "/mnt/tv" {
		t.Errorf("RootFolderPath: %q", got.RootFolderPath)
	}
	if got.QualityProfileID == 0 || got.QualityProfileID != 5 {
		t.Errorf("QualityProfileID: %v", got.QualityProfileID)
	}
	if len(got.Tags) != 2 || got.Tags[0] != 11 || got.Tags[1] != 22 {
		t.Errorf("Tags: %v", got.Tags)
	}
}

func TestSync_MonitorTypeNoneNotMonitored(t *testing.T) {
	t.Parallel()
	parent := makeRow(1, "L", boolPtr(true))
	parent.ShouldMonitor = 12 // MonitorTypes.None
	rows := []importlists.ImportList{parent}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{
			{TvdbID: 100, Title: "X", ImportListID: 1},
		}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	adder := &stubSeriesAdder{}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, nil)
	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.SeriesAdded != 1 || adder.batch[0].Monitored {
		t.Errorf("monitored: got %v want false", adder.batch[0].Monitored)
	}
}

func TestSync_NoEnabledLists_NoOp(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "Off", boolPtr(false))}
	factory := buildSyncFactory(t, rows, nil, nil)
	adder := &stubSeriesAdder{}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, nil)
	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.SeriesAdded != 0 || len(adder.batch) != 0 {
		t.Errorf("expected no-op, got %+v", res)
	}
}

func TestSync_FetchError_OneListFails_OthersContinue(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		makeRow(1, "Broken", boolPtr(true)),
		makeRow(2, "Healthy", boolPtr(true)),
	}
	fetchByID := map[int]importlists.FetchResult{
		2: {Series: []importlists.ItemInfo{
			{TvdbID: 100, Title: "Y", ImportListID: 2},
		}},
	}
	fetchErrByID := map[int]error{1: errors.New("transport")}
	factory := buildSyncFactory(t, rows, fetchByID, fetchErrByID)
	adder := &stubSeriesAdder{}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, nil)
	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !res.AnyFailure {
		t.Errorf("AnyFailure: false want true")
	}
	if res.SeriesAdded != 1 {
		t.Errorf("SeriesAdded: %d want 1", res.SeriesAdded)
	}
}

func TestSync_PartialFailureAnyFailureBubblesUp(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "X", ImportListID: 1}}, AnyFailure: true},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, &stubSeriesAdder{}, nil)
	res, _ := svc.Sync(context.Background())
	if !res.AnyFailure {
		t.Errorf("AnyFailure: false want true (per-list partial failure should bubble up)")
	}
}

func TestSync_UnresolvedID_NilTranslator_Skipped(t *testing.T) {
	t.Parallel()
	parent := makeRow(1, "L", boolPtr(true))
	rows := []importlists.ImportList{parent}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{
			{TvdbID: 0, ImdbID: "tt1", Title: "NoTvdb", ImportListID: 1},
		}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	adder := &stubSeriesAdder{}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, nil)
	res, _ := svc.Sync(context.Background())
	if res.SkippedUnresolvedID != 1 || res.SeriesAdded != 0 {
		t.Errorf("unresolved: %+v", res)
	}
}

func TestSync_IDTranslation_IMDB(t *testing.T) {
	t.Parallel()
	parent := makeRow(1, "L", boolPtr(true))
	rows := []importlists.ImportList{parent}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{
			{TvdbID: 0, ImdbID: "tt1", Title: "Pre", ImportListID: 1},
		}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	adder := &stubSeriesAdder{}
	translator := &fakeTranslator{
		imdb:   map[string]int{"tt1": 555},
		titles: map[int]string{555: "Canonical"},
	}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, translator)
	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.SeriesAdded != 1 {
		t.Fatalf("SeriesAdded: %d", res.SeriesAdded)
	}
	if adder.batch[0].TvdbID != 555 || adder.batch[0].Title != "Canonical" {
		t.Errorf("translated batch[0]: %+v", adder.batch[0])
	}
}

func TestSync_IDTranslation_AniListChain_AnyHits(t *testing.T) {
	t.Parallel()
	parent := makeRow(1, "L", boolPtr(true))
	rows := []importlists.ImportList{parent}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{
			{TvdbID: 0, AniListID: 42, Title: "Anime", ImportListID: 1},
		}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	adder := &stubSeriesAdder{}
	translator := &fakeTranslator{
		anilist: map[int]int{42: 777},
		titles:  map[int]string{777: "Animu"},
	}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, translator)
	res, _ := svc.Sync(context.Background())
	if res.SeriesAdded != 1 {
		t.Fatalf("SeriesAdded: %d", res.SeriesAdded)
	}
	if adder.batch[0].TvdbID != 777 {
		t.Errorf("translated tvdb: %d want 777", adder.batch[0].TvdbID)
	}
}

func TestSyncList_PicksSpecificList(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		makeRow(1, "L1", boolPtr(true)),
		makeRow(2, "L2", boolPtr(true)),
	}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "FromL1", ImportListID: 1}}},
		2: {Series: []importlists.ItemInfo{{TvdbID: 200, Title: "FromL2", ImportListID: 2}}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	adder := &stubSeriesAdder{}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, nil)

	res, err := svc.SyncList(context.Background(), 2)
	if err != nil {
		t.Fatalf("SyncList: %v", err)
	}
	if res.SeriesAdded != 1 || adder.batch[0].TvdbID != 200 {
		t.Errorf("expected only L2's item, got %+v", adder.batch)
	}
}

func TestSyncList_UnknownIDReturnsError(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	factory := buildSyncFactory(t, rows, nil, nil)
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, &stubSeriesAdder{}, nil)
	if _, err := svc.SyncList(context.Background(), 999); err == nil {
		t.Errorf("expected error for unknown list id")
	}
}

func TestSync_ExclusionsLoadError(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "X", ImportListID: 1}}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	exclusions := &stubExclusions{err: errors.New("db down")}
	svc := newSync(t, factory, exclusions, &stubSeriesLookup{}, &stubSeriesAdder{}, nil)
	if _, err := svc.Sync(context.Background()); err == nil {
		t.Errorf("expected error from exclusions lookup")
	}
}

func TestSync_SeriesLookupError(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "X", ImportListID: 1}}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	lookup := &stubSeriesLookup{err: errors.New("db down")}
	svc := newSync(t, factory, &stubExclusions{}, lookup, &stubSeriesAdder{}, nil)
	if _, err := svc.Sync(context.Background()); err == nil {
		t.Errorf("expected error from series lookup")
	}
}

func TestSync_AdderError(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "X", ImportListID: 1}}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	adder := &stubSeriesAdder{err: errors.New("write barrier")}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, nil)
	if _, err := svc.Sync(context.Background()); err == nil {
		t.Errorf("expected error from adder")
	}
}

// Cross-list dedup: same TvdbID surfaces in two different lists →
// only first wins.
func TestSync_CrossListDedup(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		makeRow(1, "L1", boolPtr(true)),
		makeRow(2, "L2", boolPtr(true)),
	}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "FirstSawIt", ImportListID: 1}}},
		2: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "AlsoSawIt", ImportListID: 2}}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	adder := &stubSeriesAdder{}
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, adder, nil)
	res, _ := svc.Sync(context.Background())
	if res.SeriesAdded != 1 || adder.batch[0].Title != "FirstSawIt" {
		t.Errorf("dedup: kept %+v", adder.batch)
	}
}

// --- I4c per-list persistence + status wiring (J500) ---

type persistCall struct {
	listID int
	items  []importlists.ItemInfo
}

type stubItemPersister struct {
	calls   []persistCall
	removed map[int]int // listID → removed count to report
	err     map[int]error
}

func (s *stubItemPersister) SyncSeriesForList(_ context.Context, items []importlists.ItemInfo, listID int) (int, error) {
	s.calls = append(s.calls, persistCall{listID: listID, items: items})
	if s.err != nil {
		if e := s.err[listID]; e != nil {
			return 0, e
		}
	}
	if s.removed != nil {
		return s.removed[listID], nil
	}
	return 0, nil
}

type statusCall struct {
	listID  int
	removed bool
}

type stubStatusUpdater struct {
	calls []statusCall
}

func (s *stubStatusUpdater) UpdateListSyncStatus(_ context.Context, listID int, removed bool) error {
	s.calls = append(s.calls, statusCall{listID: listID, removed: removed})
	return nil
}

func TestSync_PersistsCleanlyFetchedLists(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		makeRow(1, "A", boolPtr(true)),
		makeRow(2, "B", boolPtr(true)),
	}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "X", ImportListID: 1}}},
		2: {Series: []importlists.ItemInfo{{TvdbID: 200, Title: "Y", ImportListID: 2}}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, &stubSeriesAdder{}, nil)
	persister := &stubItemPersister{removed: map[int]int{1: 0, 2: 3}}
	status := &stubStatusUpdater{}
	svc.SetItemPersister(persister)
	svc.SetListStatusUpdater(status)

	if _, err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(persister.calls) != 2 {
		t.Fatalf("persist calls: got %d, want 2", len(persister.calls))
	}
	if len(status.calls) != 2 {
		t.Fatalf("status calls: got %d, want 2", len(status.calls))
	}
	// Status must report removed>0 only for list 2 (removed=3).
	got := map[int]bool{}
	for _, c := range status.calls {
		got[c.listID] = c.removed
	}
	if got[1] != false || got[2] != true {
		t.Errorf("removed flags: %+v want {1:false, 2:true}", got)
	}
}

func TestSync_DoesNotPersistFailedFetch(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{
		makeRow(1, "Broken", boolPtr(true)),
		makeRow(2, "Healthy", boolPtr(true)),
	}
	fetchByID := map[int]importlists.FetchResult{
		2: {Series: []importlists.ItemInfo{{TvdbID: 200, Title: "Y", ImportListID: 2}}},
	}
	fetchErrByID := map[int]error{1: errors.New("transport")}
	factory := buildSyncFactory(t, rows, fetchByID, fetchErrByID)
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, &stubSeriesAdder{}, nil)
	persister := &stubItemPersister{}
	status := &stubStatusUpdater{}
	svc.SetItemPersister(persister)
	svc.SetListStatusUpdater(status)

	if _, err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Only the healthy list (2) is persisted; the broken list (1) is not.
	if len(persister.calls) != 1 || persister.calls[0].listID != 2 {
		t.Errorf("persist calls: %+v want only list 2", persister.calls)
	}
	if len(status.calls) != 1 || status.calls[0].listID != 2 {
		t.Errorf("status calls: %+v want only list 2", status.calls)
	}
}

func TestSync_DoesNotPersistPartialFailure(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "X", ImportListID: 1}}, AnyFailure: true},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, &stubSeriesAdder{}, nil)
	persister := &stubItemPersister{}
	status := &stubStatusUpdater{}
	svc.SetItemPersister(persister)
	svc.SetListStatusUpdater(status)

	if _, err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// A list that reported AnyFailure must NOT be persisted (a transient
	// failure returning fewer items must not wipe persisted rows).
	if len(persister.calls) != 0 {
		t.Errorf("persist calls: %+v want none on partial failure", persister.calls)
	}
	if len(status.calls) != 0 {
		t.Errorf("status calls: %+v want none on partial failure", status.calls)
	}
}

func TestSync_PersistError_SkipsStatusUpdate(t *testing.T) {
	t.Parallel()
	rows := []importlists.ImportList{makeRow(1, "L", boolPtr(true))}
	fetchByID := map[int]importlists.FetchResult{
		1: {Series: []importlists.ItemInfo{{TvdbID: 100, Title: "X", ImportListID: 1}}},
	}
	factory := buildSyncFactory(t, rows, fetchByID, nil)
	svc := newSync(t, factory, &stubExclusions{}, &stubSeriesLookup{}, &stubSeriesAdder{}, nil)
	persister := &stubItemPersister{err: map[int]error{1: errors.New("db down")}}
	status := &stubStatusUpdater{}
	svc.SetItemPersister(persister)
	svc.SetListStatusUpdater(status)

	if _, err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err) // best-effort: persist error is swallowed
	}
	if len(status.calls) != 0 {
		t.Errorf("status must not be updated when persist failed: %+v", status.calls)
	}
}
