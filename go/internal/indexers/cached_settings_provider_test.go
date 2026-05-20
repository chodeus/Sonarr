package indexers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/indexers"
)

// stubFinder is a hand-rolled [indexers.IndexerFinder] returning a
// canned per-ID Indexer row, with an optional error and a call
// counter for cache-hit assertions.
type stubFinder struct {
	byID  map[int]*indexers.Indexer
	err   error
	calls int
}

func (s *stubFinder) Find(_ context.Context, indexerID int) (*indexers.Indexer, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.byID[indexerID], nil
}

// strPtr returns a *string for the rows' nullable Settings column.
// (Named distinctly from indexers_test.go's ptrStr.)
func strPtr(s string) *string { return &s }

func TestCachedSettingsProvider_ZeroIDReturnsNil(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	got, err := p.GetSettings(context.Background(), 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got=%v, want nil for indexerID=0", got)
	}
	if finder.calls != 0 {
		t.Errorf("finder called %d times; want 0", finder.calls)
	}
}

func TestCachedSettingsProvider_UnknownIndexerReturnsNil(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{byID: map[int]*indexers.Indexer{}}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	got, err := p.GetSettings(context.Background(), 7)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got=%v, want nil for unknown indexer", got)
	}
}

func TestCachedSettingsProvider_DecodesTorrentSeedCriteria(t *testing.T) {
	t.Parallel()
	settingsJSON := `{
		"baseUrl": "https://example.com",
		"failDownloads": [0, 2],
		"seedCriteria": {
			"seedRatio": 1.5,
			"seedTime": 60,
			"seasonPackSeedGoal": 1,
			"seasonPackSeedRatio": 3.0,
			"seasonPackSeedTime": 180
		}
	}`
	finder := &stubFinder{byID: map[int]*indexers.Indexer{
		1: {ID: 1, Implementation: "Torznab", Settings: strPtr(settingsJSON)},
	}}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	got, err := p.GetSettings(context.Background(), 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatal("got=nil")
	}
	if len(got.FailDownloads) != 2 ||
		got.FailDownloads[0] != indexers.FailDownloadsExecutables ||
		got.FailDownloads[1] != indexers.FailDownloadsUserDefinedExtensions {
		t.Errorf("FailDownloads=%v, want [Executables, UserDefinedExtensions]", got.FailDownloads)
	}
	if got.SeedCriteriaSettings == nil {
		t.Fatal("SeedCriteriaSettings=nil")
	}
	sc := got.SeedCriteriaSettings
	if sc.SeedRatio == nil || *sc.SeedRatio != 1.5 {
		t.Errorf("SeedRatio=%v, want 1.5", sc.SeedRatio)
	}
	if sc.SeedTime == nil || *sc.SeedTime != 60 {
		t.Errorf("SeedTime=%v, want 60", sc.SeedTime)
	}
	if sc.SeasonPackSeedGoal != indexers.SeasonPackSeedGoalUseSeasonPack {
		t.Errorf("SeasonPackSeedGoal=%v, want UseSeasonPack", sc.SeasonPackSeedGoal)
	}
	if sc.SeasonPackSeedRatio == nil || *sc.SeasonPackSeedRatio != 3.0 {
		t.Errorf("SeasonPackSeedRatio=%v, want 3.0", sc.SeasonPackSeedRatio)
	}
	if sc.SeasonPackSeedTime == nil || *sc.SeasonPackSeedTime != 180 {
		t.Errorf("SeasonPackSeedTime=%v, want 180", sc.SeasonPackSeedTime)
	}
}

func TestCachedSettingsProvider_UsenetIndexerHasNilSeedCriteria(t *testing.T) {
	t.Parallel()
	// Newznab (usenet) ships failDownloads but no seedCriteria key.
	settingsJSON := `{"baseUrl":"https://news.example.com","failDownloads":[1]}`
	finder := &stubFinder{byID: map[int]*indexers.Indexer{
		2: {ID: 2, Implementation: "Newznab", Settings: strPtr(settingsJSON)},
	}}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	got, err := p.GetSettings(context.Background(), 2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatal("got=nil")
	}
	if got.SeedCriteriaSettings != nil {
		t.Errorf("SeedCriteriaSettings=%v, want nil for usenet indexer", got.SeedCriteriaSettings)
	}
	if len(got.FailDownloads) != 1 || got.FailDownloads[0] != indexers.FailDownloadsPotentiallyDangerous {
		t.Errorf("FailDownloads=%v, want [PotentiallyDangerous]", got.FailDownloads)
	}
}

func TestCachedSettingsProvider_EmptySettingsJSONReturnsNil(t *testing.T) {
	t.Parallel()
	// Indexer row with NULL Settings column (Settings *string is nil).
	finder := &stubFinder{byID: map[int]*indexers.Indexer{
		3: {ID: 3, Implementation: "Torznab", Settings: nil},
	}}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	got, err := p.GetSettings(context.Background(), 3)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got=%v, want nil for empty settings JSON", got)
	}
}

func TestCachedSettingsProvider_MalformedJSONPropagatesError(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{byID: map[int]*indexers.Indexer{
		4: {ID: 4, Implementation: "Torznab", Settings: strPtr("{not valid")},
	}}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	got, err := p.GetSettings(context.Background(), 4)
	if got != nil {
		t.Errorf("got=%v, want nil on decode failure", got)
	}
	if err == nil {
		t.Errorf("err=nil, want decode error")
	}
}

func TestCachedSettingsProvider_CacheHitAvoidsFinder(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{byID: map[int]*indexers.Indexer{
		5: {ID: 5, Implementation: "Torznab", Settings: strPtr(`{"failDownloads":[]}`)},
	}}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	for i := 0; i < 5; i++ {
		if _, err := p.GetSettings(context.Background(), 5); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if finder.calls != 1 {
		t.Errorf("finder calls=%d, want 1 (subsequent calls cache-hit)", finder.calls)
	}
}

func TestCachedSettingsProvider_CacheExpiresAfterTTL(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{byID: map[int]*indexers.Indexer{
		6: {ID: 6, Implementation: "Torznab", Settings: strPtr(`{"failDownloads":[]}`)},
	}}
	base := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	clock := base
	p := indexers.NewCachedSettingsProvider(finder, nil,
		indexers.WithCachedSettingsTTL(15*time.Minute),
		indexers.WithCachedSettingsClock(func() time.Time { return clock }),
	)

	if _, err := p.GetSettings(context.Background(), 6); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetSettings(context.Background(), 6); err != nil {
		t.Fatal(err)
	}
	if finder.calls != 1 {
		t.Fatalf("within TTL: finder calls=%d, want 1", finder.calls)
	}

	clock = base.Add(20 * time.Minute)
	if _, err := p.GetSettings(context.Background(), 6); err != nil {
		t.Fatal(err)
	}
	if finder.calls != 2 {
		t.Errorf("after TTL: finder calls=%d, want 2", finder.calls)
	}
}

func TestCachedSettingsProvider_ClearPurgesAllEntries(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{byID: map[int]*indexers.Indexer{
		7: {ID: 7, Implementation: "Torznab", Settings: strPtr(`{"failDownloads":[]}`)},
		8: {ID: 8, Implementation: "Newznab", Settings: strPtr(`{"failDownloads":[]}`)},
	}}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	if _, err := p.GetSettings(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetSettings(context.Background(), 8); err != nil {
		t.Fatal(err)
	}
	if finder.calls != 2 {
		t.Fatalf("initial calls=%d, want 2", finder.calls)
	}

	p.Clear()

	if _, err := p.GetSettings(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetSettings(context.Background(), 8); err != nil {
		t.Fatal(err)
	}
	if finder.calls != 4 {
		t.Errorf("after Clear: finder calls=%d, want 4 (both rows re-fetched)", finder.calls)
	}
}

func TestCachedSettingsProvider_TransientErrorNotCached(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{err: errors.New("transient")}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	for i := 0; i < 3; i++ {
		if _, err := p.GetSettings(context.Background(), 9); err == nil {
			t.Fatalf("call %d: want err", i)
		}
	}
	if finder.calls != 3 {
		t.Errorf("finder calls=%d, want 3 (errors NOT cached)", finder.calls)
	}
}

func TestCachedSettingsProvider_NegativeResultIsCached(t *testing.T) {
	t.Parallel()
	// nil-row "no such indexer" → cached as nil-entry so we don't
	// re-walk the finder on every poll.
	finder := &stubFinder{byID: map[int]*indexers.Indexer{}}
	p := indexers.NewCachedSettingsProvider(finder, nil)

	for i := 0; i < 4; i++ {
		if _, err := p.GetSettings(context.Background(), 99); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if finder.calls != 1 {
		t.Errorf("finder calls=%d, want 1 (negative cache)", finder.calls)
	}
}

func TestCachedSettingsProvider_CustomDecoderHook(t *testing.T) {
	t.Parallel()
	// A custom decoder forces a specific FailDownloads + SeedCriteria
	// result regardless of the JSON — proves the hook is consulted
	// rather than the default decoder.
	wantSettings := &indexers.CachedIndexerSettings{
		FailDownloads: []indexers.FailDownloads{indexers.FailDownloadsExecutables},
	}
	called := 0
	decoder := func(_ string, _ string) (*indexers.CachedIndexerSettings, error) {
		called++
		return wantSettings, nil
	}
	finder := &stubFinder{byID: map[int]*indexers.Indexer{
		10: {ID: 10, Implementation: "CustomImpl", Settings: strPtr(`{}`)},
	}}
	p := indexers.NewCachedSettingsProvider(finder, nil, indexers.WithSettingsDecoder(decoder))

	got, err := p.GetSettings(context.Background(), 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != wantSettings {
		t.Errorf("got=%v, want %v from custom decoder", got, wantSettings)
	}
	if called != 1 {
		t.Errorf("custom decoder called %d times, want 1", called)
	}
}

func TestCachedSettingsProvider_NilFinderPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil finder")
		}
	}()
	indexers.NewCachedSettingsProvider(nil, nil)
}

// repoStub is a tiny stand-in for *Repo that returns ErrNotFound or
// a canned row, used to exercise RepoIndexerFinder's ErrNotFound→nil
// mapping without needing a live DB.
type repoStub struct{ row *indexers.Indexer }

func (r repoStub) Get(_ context.Context, id int) (*indexers.Indexer, error) {
	if r.row == nil || r.row.ID != id {
		return nil, indexers.ErrNotFound
	}
	return r.row, nil
}

func TestRepoIndexerFinder_MapsErrNotFoundToNil(t *testing.T) {
	t.Parallel()
	f := indexers.RepoIndexerFinder{Repo: repoStub{}}

	row, err := f.Find(context.Background(), 1)
	if err != nil {
		t.Errorf("err=%v, want nil for ErrNotFound", err)
	}
	if row != nil {
		t.Errorf("row=%v, want nil", row)
	}
}

func TestRepoIndexerFinder_ForwardsRow(t *testing.T) {
	t.Parallel()
	want := &indexers.Indexer{ID: 5, Implementation: "X"}
	f := indexers.RepoIndexerFinder{Repo: repoStub{row: want}}

	row, err := f.Find(context.Background(), 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if row != want {
		t.Errorf("row=%v, want %v", row, want)
	}
}

func TestRepoIndexerFinder_NilRepoErrors(t *testing.T) {
	t.Parallel()
	f := indexers.RepoIndexerFinder{Repo: nil}
	if _, err := f.Find(context.Background(), 1); err == nil {
		t.Errorf("err=nil, want non-nil for nil repo")
	}
}

// Verify the provider satisfies the J92 IndexerSettingsLookup so the
// wiring slice can pass *CachedSettingsProvider directly to
// download.NewIndexerSeedConfigProvider.
func TestCachedSettingsProvider_SatisfiesIndexerSettingsLookup(t *testing.T) {
	t.Parallel()
	finder := &stubFinder{byID: map[int]*indexers.Indexer{}}
	var _ indexers.IndexerSettingsLookup = indexers.NewCachedSettingsProvider(finder, nil)
}
