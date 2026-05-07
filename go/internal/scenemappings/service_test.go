package scenemappings_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/scenemappings"
)

// stubReader is a deterministic [scenemappings.MappingsReader] for
// service-level tests that don't need a real DB. The repo-backed
// integration suite at the bottom of the file covers the SQL path.
type stubReader struct {
	out   []scenemappings.Mapping
	err   error
	calls int
}

func (s *stubReader) All(_ context.Context) ([]scenemappings.Mapping, error) {
	s.calls++
	return s.out, s.err
}

func ptrInt2(i int) *int { return &i }

// --- constructor ---------------------------------------------------

func TestNewServiceNilRepoReturnsNil(t *testing.T) {
	t.Parallel()
	if s := scenemappings.NewService(nil, nil); s != nil {
		t.Errorf("got %+v, want nil", s)
	}
}

// --- RefreshCache + lazy populate ----------------------------------

func TestRefreshCachePopulatesOnFirstReadOnly(t *testing.T) {
	t.Parallel()

	reader := &stubReader{
		out: []scenemappings.Mapping{
			{ID: 1, TvdbID: 100, Title: "Test", ParseTerm: "test", SearchTerm: "Test"},
			{ID: 2, TvdbID: 100, Title: "Test", ParseTerm: "test", SearchTerm: "Test"},
		},
	}
	s := scenemappings.NewService(reader, nil)

	// First call lazy-populates; reader.calls = 1.
	_, err := s.FindByTvdbID(context.Background(), 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reader.calls != 1 {
		t.Errorf("first call: reader.calls = %d, want 1", reader.calls)
	}

	// Second call should reuse the cache; reader.calls stays at 1.
	_, err = s.FindByTvdbID(context.Background(), 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reader.calls != 1 {
		t.Errorf("after second call: reader.calls = %d, want 1 (cache)", reader.calls)
	}

	// Explicit RefreshCache hits the reader again.
	if err := s.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache: %v", err)
	}
	if reader.calls != 2 {
		t.Errorf("after RefreshCache: reader.calls = %d, want 2", reader.calls)
	}
}

func TestRefreshCacheRepoErrPropagates(t *testing.T) {
	t.Parallel()

	reader := &stubReader{err: errors.New("EIO")}
	s := scenemappings.NewService(reader, nil)

	if err := s.RefreshCache(context.Background()); err == nil {
		t.Errorf("expected error")
	}
}

func TestLazyPopulateRepoErrPropagates(t *testing.T) {
	t.Parallel()

	reader := &stubReader{err: errors.New("EIO")}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindSceneMapping(context.Background(), "any", "rt", -1)
	if err == nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, error)", got, err)
	}
}

// --- FindByTvdbID --------------------------------------------------

func TestFindByTvdbIDReturnsCopy(t *testing.T) {
	t.Parallel()

	original := []scenemappings.Mapping{
		{ID: 1, TvdbID: 100, Title: "Test", ParseTerm: "test"},
	}
	reader := &stubReader{out: original}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindByTvdbID(context.Background(), 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("got %+v", got)
	}

	// Mutate the returned slice; subsequent reads should see the cache untouched.
	got[0].Title = "MUTATED"
	got2, _ := s.FindByTvdbID(context.Background(), 100)
	if got2[0].Title == "MUTATED" {
		t.Errorf("cache leaked: external mutation visible on subsequent read")
	}
}

func TestFindByTvdbIDUnknownReturnsEmpty(t *testing.T) {
	t.Parallel()

	reader := &stubReader{out: []scenemappings.Mapping{
		{TvdbID: 100, Title: "Test", ParseTerm: "test"},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindByTvdbID(context.Background(), 999)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

// --- FindSceneMapping ----------------------------------------------

func TestFindSceneMappingEmptySeriesTitleReturnsNil(t *testing.T) {
	t.Parallel()

	reader := &stubReader{out: []scenemappings.Mapping{
		{TvdbID: 1, Title: "Foo", ParseTerm: "foo"},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindSceneMapping(context.Background(), "  ", "rt", -1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
	// Empty seriesTitle short-circuits BEFORE the populate; reader
	// should not have been called.
	if reader.calls != 0 {
		t.Errorf("reader called %d times; expected 0 (short-circuit)", reader.calls)
	}
}

func TestFindSceneMappingNoMatchReturnsNil(t *testing.T) {
	t.Parallel()

	reader := &stubReader{out: []scenemappings.Mapping{
		{TvdbID: 1, Title: "Foo", ParseTerm: "foo"},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindSceneMapping(context.Background(), "Bar", "rt", -1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestFindSceneMappingSingleHitReturnsMapping(t *testing.T) {
	t.Parallel()

	want := scenemappings.Mapping{
		ID: 5, TvdbID: 100, Title: "Foo", ParseTerm: "foo", SearchTerm: "Foo",
	}
	reader := &stubReader{out: []scenemappings.Mapping{want}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindSceneMapping(context.Background(), "Foo", "rt", -1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 5 {
		t.Errorf("got %+v, want ID=5", got)
	}
}

func TestFindSceneMappingMultipleSameTvdbIDReturnsFirst(t *testing.T) {
	t.Parallel()

	// Two mappings → same TvdbID → DistinctBy → 1 → resolves cleanly.
	reader := &stubReader{out: []scenemappings.Mapping{
		{ID: 1, TvdbID: 100, Title: "Foo", ParseTerm: "foo"},
		{ID: 2, TvdbID: 100, Title: "Foo", ParseTerm: "foo"},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindSceneMapping(context.Background(), "Foo", "rt", -1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.TvdbID != 100 {
		t.Errorf("got %+v, want TvdbID=100", got)
	}
}

func TestFindSceneMappingAmbiguousReturnsNil(t *testing.T) {
	t.Parallel()

	// Two distinct TvdbIDs both with Title=="Foo" — the exact-title
	// arm catches both and DistinctBy(TvdbId) leaves 2 → ambiguous.
	reader := &stubReader{out: []scenemappings.Mapping{
		{ID: 1, TvdbID: 100, Title: "Foo", ParseTerm: "foo"},
		{ID: 2, TvdbID: 200, Title: "Foo", ParseTerm: "foo"},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindSceneMapping(context.Background(), "Foo", "rt", -1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil (ambiguous → no Levenshtein closest match in Go port)", got)
	}
}

func TestFindSceneMappingExactTitlePreferredOverInexact(t *testing.T) {
	t.Parallel()

	// Two mappings: one with exact Title match, one with the same
	// ParseTerm but different Title. Exact match wins.
	reader := &stubReader{out: []scenemappings.Mapping{
		{ID: 1, TvdbID: 100, Title: "Foo Inexact", ParseTerm: "foo"},
		{ID: 2, TvdbID: 200, Title: "Foo", ParseTerm: "foo"},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindSceneMapping(context.Background(), "Foo", "rt", -1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 2 {
		t.Errorf("got %+v, want ID=2 (exact-title match)", got)
	}
}

func TestFindSceneMappingSceneSeasonFilterRespected(t *testing.T) {
	t.Parallel()

	// Mapping has both SceneSeasonNumber + SeasonNumber set →
	// goes into the "filtered" bucket. With sceneSeasonNumber=2,
	// the filter narrows to SceneSeasonNumber<=2.
	reader := &stubReader{out: []scenemappings.Mapping{
		{
			ID:                1,
			TvdbID:            100,
			Title:             "Foo",
			ParseTerm:         "foo",
			SeasonNumber:      ptrInt2(2),
			SceneSeasonNumber: ptrInt2(2),
		},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindSceneMapping(context.Background(), "Foo", "rt", 2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.ID != 1 {
		t.Errorf("got %+v, want filtered mapping ID=1", got)
	}
}

// --- GetSceneSeasonNumber + FindTvdbID -----------------------------

func TestGetSceneSeasonNumberDelegates(t *testing.T) {
	t.Parallel()

	reader := &stubReader{out: []scenemappings.Mapping{
		{TvdbID: 100, Title: "Foo", ParseTerm: "foo", SceneSeasonNumber: ptrInt2(7)},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.GetSceneSeasonNumber(context.Background(), "Foo", "rt")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || *got != 7 {
		t.Errorf("got %+v, want 7", got)
	}
}

func TestGetSceneSeasonNumberNoMappingReturnsNil(t *testing.T) {
	t.Parallel()

	reader := &stubReader{}
	s := scenemappings.NewService(reader, nil)

	got, err := s.GetSceneSeasonNumber(context.Background(), "Foo", "rt")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestFindTvdbIDDelegates(t *testing.T) {
	t.Parallel()

	reader := &stubReader{out: []scenemappings.Mapping{
		{TvdbID: 555, Title: "Foo", ParseTerm: "foo"},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.FindTvdbID(context.Background(), "Foo", "rt", -1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || *got != 555 {
		t.Errorf("got %+v, want 555", got)
	}
}

// --- GetSceneNames -------------------------------------------------

func TestGetSceneNamesFiltersBySeasonAndDedupes(t *testing.T) {
	t.Parallel()

	reader := &stubReader{out: []scenemappings.Mapping{
		// Match by SeasonNumber=1.
		{TvdbID: 100, SearchTerm: "alpha", SeasonNumber: ptrInt2(1), ParseTerm: "x"},
		// Match by SceneSeasonNumber=2.
		{TvdbID: 100, SearchTerm: "Beta", SceneSeasonNumber: ptrInt2(2), ParseTerm: "x"},
		// Catch-all (no season info, non-tvdb origin) — included.
		{TvdbID: 100, SearchTerm: "gamma", SceneOrigin: "scene", ParseTerm: "x"},
		// Catch-all but tvdb origin — excluded.
		{TvdbID: 100, SearchTerm: "delta", SceneOrigin: "tvdb", ParseTerm: "x"},
		// Duplicate SearchTerm (case-insensitive) — deduped.
		{TvdbID: 100, SearchTerm: "ALPHA", SeasonNumber: ptrInt2(1), ParseTerm: "x"},
		// Mismatched season — excluded.
		{TvdbID: 100, SearchTerm: "epsilon", SeasonNumber: ptrInt2(99), ParseTerm: "x"},
	}}
	s := scenemappings.NewService(reader, nil)

	got, err := s.GetSceneNames(context.Background(), 100, []int{1}, []int{2})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"alpha", "Beta", "gamma"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetSceneNamesUnknownTvdbReturnsEmpty(t *testing.T) {
	t.Parallel()
	reader := &stubReader{}
	s := scenemappings.NewService(reader, nil)

	got, err := s.GetSceneNames(context.Background(), 999, []int{1}, []int{2})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// --- repo-backed integration suite ---------------------------------

func runServiceSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := scenemappings.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc := scenemappings.NewService(repo, nil)

	// Seed a single mapping; FindByTvdbID lazy-populates.
	want := &scenemappings.Mapping{
		TvdbID:      999,
		Title:       "Integration Show",
		SearchTerm:  "Integration Show",
		ParseTerm:   "integrationshow",
		Type:        "TestProvider",
		SceneOrigin: "scene",
	}
	if _, err := repo.Insert(t.Context(), want); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// FindByTvdbID hit — confirms lazy populate + scan path.
	all, err := svc.FindByTvdbID(t.Context(), 999)
	if err != nil {
		t.Fatalf("FindByTvdbID: %v", err)
	}
	if len(all) != 1 || all[0].Title != "Integration Show" {
		t.Errorf("FindByTvdbID got %+v", all)
	}

	// FindSceneMapping by Title via parser.CleanSeriesTitle round-trip.
	got, err := svc.FindSceneMapping(t.Context(), "Integration Show", "rt", -1)
	if err != nil {
		t.Fatalf("FindSceneMapping: %v", err)
	}
	if got == nil || got.TvdbID != 999 {
		t.Errorf("FindSceneMapping got %+v", got)
	}

	// RefreshCache picks up a NEW row inserted after lazy populate.
	if _, err := repo.Insert(t.Context(), &scenemappings.Mapping{
		TvdbID: 1000, Title: "Other", SearchTerm: "Other",
		ParseTerm: "other", Type: "TestProvider",
	}); err != nil {
		t.Fatalf("Insert other: %v", err)
	}
	// Pre-RefreshCache: still cached, finds nothing.
	if pre, _ := svc.FindByTvdbID(t.Context(), 1000); len(pre) != 0 {
		t.Errorf("pre-refresh: expected 0 hits for new row, got %d", len(pre))
	}
	if err := svc.RefreshCache(t.Context()); err != nil {
		t.Fatalf("RefreshCache: %v", err)
	}
	if post, _ := svc.FindByTvdbID(t.Context(), 1000); len(post) != 1 {
		t.Errorf("post-refresh: expected 1 hit for new row, got %d", len(post))
	}
}

func TestService_SQLite(t *testing.T) {
	t.Parallel()
	runServiceSuite(t, datastoretest.OpenSQLite(t))
}

func TestService_Postgres(t *testing.T) {
	runServiceSuite(t, datastoretest.OpenPostgres(t))
}
