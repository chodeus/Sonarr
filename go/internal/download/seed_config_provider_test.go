package download_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/parser"
)

// stubLatestGrab is a hand-rolled [download.LatestGrabReader]. The
// keyed map lets each test seed a different infohash → entry
// mapping; tests that expect a single lookup also assert the
// counter increments.
type stubLatestGrab struct {
	entries map[string]*downloadhistory.Entry
	err     error
	calls   atomic.Int32
}

func (s *stubLatestGrab) GetLatestGrab(_ context.Context, downloadID string) (*downloadhistory.Entry, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return s.entries[downloadID], nil
}

// stubIndexerSeedConfig records the (indexerID, fullSeason) args so
// tests can assert dispatch. Returns the canned config.
type stubIndexerSeedConfig struct {
	cfg              *downloadclients.TorrentSeedConfiguration
	err              error
	calls            atomic.Int32
	lastIndexerID    int
	lastFullSeason   bool
	gotIndexerArgsCh chan [2]any
}

func (s *stubIndexerSeedConfig) GetSeedConfiguration(_ context.Context, indexerID int, fullSeason bool) (*downloadclients.TorrentSeedConfiguration, error) {
	s.calls.Add(1)
	s.lastIndexerID = indexerID
	s.lastFullSeason = fullSeason
	if s.gotIndexerArgsCh != nil {
		s.gotIndexerArgsCh <- [2]any{indexerID, fullSeason}
	}
	return s.cfg, s.err
}

// mustReleaseJSON marshals a parser.ReleaseInfo with the given title
// into the JSON shape the downloadhistory.Entry.Release column
// stores. Used to seed test history entries.
func mustReleaseJSON(t *testing.T, title string) string {
	t.Helper()
	rel := parser.ReleaseInfo{Title: title}
	b, err := json.Marshal(rel)
	if err != nil {
		t.Fatalf("marshal ReleaseInfo: %v", err)
	}
	return string(b)
}

func TestSeedConfigProvider_EmptyInfoHashReturnsNil(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{}
	idx := &stubIndexerSeedConfig{}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	for _, hash := range []string{"", "   ", "\t\n"} {
		cfg, err := p.GetSeedConfiguration(context.Background(), hash)
		if err != nil {
			t.Errorf("GetSeedConfiguration(%q): err=%v", hash, err)
		}
		if cfg != nil {
			t.Errorf("GetSeedConfiguration(%q): cfg=%v, want nil", hash, cfg)
		}
	}
	if hist.calls.Load() != 0 {
		t.Errorf("history was called %d times for empty infohashes; want 0", hist.calls.Load())
	}
	if idx.calls.Load() != 0 {
		t.Errorf("indexer was called %d times for empty infohashes; want 0", idx.calls.Load())
	}
}

func TestSeedConfigProvider_NoHistoryReturnsNil(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{entries: map[string]*downloadhistory.Entry{}}
	idx := &stubIndexerSeedConfig{cfg: &downloadclients.TorrentSeedConfiguration{}}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	cfg, err := p.GetSeedConfiguration(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg=%v, want nil (no history)", cfg)
	}
	if idx.calls.Load() != 0 {
		t.Errorf("indexer called %d times; want 0", idx.calls.Load())
	}
}

func TestSeedConfigProvider_UppercasesAndCachesInfoHash(t *testing.T) {
	t.Parallel()
	const lower = "abc123def456"
	const upper = "ABC123DEF456"

	hist := &stubLatestGrab{
		entries: map[string]*downloadhistory.Entry{
			upper: {IndexerID: 7, Release: mustReleaseJSON(t, "Test.Show.S01E02.720p.WEB-DL.x264-GROUP")},
		},
	}
	wantRatio := 1.5
	idx := &stubIndexerSeedConfig{cfg: &downloadclients.TorrentSeedConfiguration{Ratio: &wantRatio}}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	cfg, err := p.GetSeedConfiguration(context.Background(), lower)
	if err != nil {
		t.Fatalf("first call err: %v", err)
	}
	if cfg == nil || cfg.Ratio == nil || *cfg.Ratio != wantRatio {
		t.Fatalf("first call cfg=%+v, want Ratio=%v", cfg, wantRatio)
	}
	if idx.lastIndexerID != 7 {
		t.Errorf("indexer got indexerID=%d, want 7", idx.lastIndexerID)
	}
	if idx.lastFullSeason {
		t.Errorf("indexer got fullSeason=true, want false for single-episode title")
	}

	cfg2, err := p.GetSeedConfiguration(context.Background(), upper)
	if err != nil {
		t.Fatalf("second call err: %v", err)
	}
	if cfg2 == nil || cfg2.Ratio == nil || *cfg2.Ratio != wantRatio {
		t.Fatalf("second call cfg=%+v", cfg2)
	}
	if hist.calls.Load() != 1 {
		t.Errorf("history called %d times across lower+upper; want 1 (uppercase cache key)", hist.calls.Load())
	}
}

func TestSeedConfigProvider_IndexerIDPropagatesFromHistory(t *testing.T) {
	t.Parallel()
	// The Go [parser.ParseTitle] currently produces FullSeason=false
	// for every title that parses (the season-pack regex cluster
	// from C# `Parser.cs` ReportTitleRegex hasn't ported — see
	// `parser/scene_checker_test.go:20-27`). Once that cluster
	// lands, an additional row exercising FullSeason=true should
	// be added here. For now the assertion is that the IndexerID
	// from the history row is plumbed verbatim and FullSeason
	// reflects whatever the parser produced.
	hist := &stubLatestGrab{
		entries: map[string]*downloadhistory.Entry{
			"DEADBEEF": {IndexerID: 42, Release: mustReleaseJSON(t, "Test.Show.S01E02.720p.WEB-DL")},
		},
	}
	idx := &stubIndexerSeedConfig{cfg: &downloadclients.TorrentSeedConfiguration{}}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	if _, err := p.GetSeedConfiguration(context.Background(), "deadbeef"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if idx.lastIndexerID != 42 {
		t.Errorf("indexerID=%d, want 42", idx.lastIndexerID)
	}
	if idx.lastFullSeason {
		t.Errorf("fullSeason=%v, want false for single-episode title", idx.lastFullSeason)
	}
}

func TestSeedConfigProvider_NilReleaseColumnReturnsNil(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{
		entries: map[string]*downloadhistory.Entry{
			"DEAD": {IndexerID: 3, Release: ""},
		},
	}
	idx := &stubIndexerSeedConfig{cfg: &downloadclients.TorrentSeedConfiguration{}}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	cfg, err := p.GetSeedConfiguration(context.Background(), "dead")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg=%v, want nil for empty Release column", cfg)
	}
	if idx.calls.Load() != 0 {
		t.Errorf("indexer called %d times; want 0 (no parsed title)", idx.calls.Load())
	}
}

func TestSeedConfigProvider_UnparseableTitleReturnsNil(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{
		entries: map[string]*downloadhistory.Entry{
			"DEAD": {IndexerID: 9, Release: mustReleaseJSON(t, "")},
		},
	}
	idx := &stubIndexerSeedConfig{cfg: &downloadclients.TorrentSeedConfiguration{}}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	cfg, err := p.GetSeedConfiguration(context.Background(), "dead")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg=%v, want nil for unparseable empty title", cfg)
	}
	if idx.calls.Load() != 0 {
		t.Errorf("indexer called %d times; want 0", idx.calls.Load())
	}
}

func TestSeedConfigProvider_MalformedReleaseJSONReturnsNil(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{
		entries: map[string]*downloadhistory.Entry{
			"DEAD": {IndexerID: 11, Release: "{not valid json"},
		},
	}
	idx := &stubIndexerSeedConfig{cfg: &downloadclients.TorrentSeedConfiguration{}}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	cfg, err := p.GetSeedConfiguration(context.Background(), "dead")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg=%v, want nil for malformed Release JSON", cfg)
	}
}

func TestSeedConfigProvider_HistoryErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("db down")
	hist := &stubLatestGrab{err: wantErr}
	idx := &stubIndexerSeedConfig{}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	cfg, err := p.GetSeedConfiguration(context.Background(), "abc")
	if cfg != nil {
		t.Errorf("cfg=%v, want nil on history error", cfg)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err=%v, want wrap of %v", err, wantErr)
	}
}

func TestSeedConfigProvider_TransientErrorIsNotCached(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{err: errors.New("transient")}
	idx := &stubIndexerSeedConfig{}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	for i := 0; i < 3; i++ {
		if _, err := p.GetSeedConfiguration(context.Background(), "abc"); err == nil {
			t.Fatalf("call %d: want err, got nil", i)
		}
	}
	if hist.calls.Load() != 3 {
		t.Errorf("history called %d times; want 3 (errors must not poison cache)", hist.calls.Load())
	}
}

func TestSeedConfigProvider_CacheExpiresAfterTTL(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{
		entries: map[string]*downloadhistory.Entry{
			"AB": {IndexerID: 1, Release: mustReleaseJSON(t, "Show.S01E01.720p")},
		},
	}
	idx := &stubIndexerSeedConfig{cfg: &downloadclients.TorrentSeedConfiguration{}}

	base := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	clock := base
	p := download.NewSeedConfigProvider(hist, idx, nil,
		download.WithSeedConfigCacheTTL(10*time.Minute),
		download.WithSeedConfigClock(func() time.Time { return clock }),
	)

	if _, err := p.GetSeedConfiguration(context.Background(), "ab"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetSeedConfiguration(context.Background(), "ab"); err != nil {
		t.Fatal(err)
	}
	if hist.calls.Load() != 1 {
		t.Errorf("after two calls within TTL: history calls=%d, want 1", hist.calls.Load())
	}

	clock = base.Add(11 * time.Minute)
	if _, err := p.GetSeedConfiguration(context.Background(), "ab"); err != nil {
		t.Fatal(err)
	}
	if hist.calls.Load() != 2 {
		t.Errorf("after expiry: history calls=%d, want 2", hist.calls.Load())
	}
}

func TestSeedConfigProvider_NegativeCacheStored(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{entries: map[string]*downloadhistory.Entry{}}
	idx := &stubIndexerSeedConfig{cfg: &downloadclients.TorrentSeedConfiguration{}}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	for i := 0; i < 5; i++ {
		if _, err := p.GetSeedConfiguration(context.Background(), "nohit"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if hist.calls.Load() != 1 {
		t.Errorf("nil-result negative cache: history calls=%d, want 1", hist.calls.Load())
	}
}

func TestSeedConfigProvider_IndexerNilResultPropagates(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{
		entries: map[string]*downloadhistory.Entry{
			"AB": {IndexerID: 5, Release: mustReleaseJSON(t, "Show.S01E01.720p")},
		},
	}
	idx := &stubIndexerSeedConfig{cfg: nil}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	cfg, err := p.GetSeedConfiguration(context.Background(), "ab")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg=%v, want nil when indexer returns nil", cfg)
	}
	if idx.calls.Load() != 1 {
		t.Errorf("indexer calls=%d, want 1", idx.calls.Load())
	}
}

func TestSeedConfigProvider_IndexerErrorPropagatesWithoutCaching(t *testing.T) {
	t.Parallel()
	hist := &stubLatestGrab{
		entries: map[string]*downloadhistory.Entry{
			"AB": {IndexerID: 5, Release: mustReleaseJSON(t, "Show.S01E01.720p")},
		},
	}
	wantErr := errors.New("indexer down")
	idx := &stubIndexerSeedConfig{err: wantErr}
	p := download.NewSeedConfigProvider(hist, idx, nil)

	// indexer-side errors are surfaced — but the negative-cache
	// store happens in fetchIndexer (history+parse step), which
	// SUCCEEDED, so the cached entry is the parsed
	// CachedSeedConfiguration. A retry will re-invoke the indexer
	// (which is what we want — transient indexer failures
	// shouldn't be hidden behind the cache), so history calls
	// should stay at 1 across retries.
	for i := 0; i < 2; i++ {
		_, err := p.GetSeedConfiguration(context.Background(), "ab")
		if !errors.Is(err, wantErr) {
			t.Fatalf("call %d: err=%v, want wrap of %v", i, err, wantErr)
		}
	}
	if hist.calls.Load() != 1 {
		t.Errorf("history calls=%d, want 1 (history-fetch cached, indexer-call retried)", hist.calls.Load())
	}
	if idx.calls.Load() != 2 {
		t.Errorf("indexer calls=%d, want 2 (retried on transient indexer error)", idx.calls.Load())
	}
}

func TestSeedConfigProvider_NilHistoryPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil LatestGrabReader")
		}
	}()
	download.NewSeedConfigProvider(nil, &stubIndexerSeedConfig{}, nil)
}

func TestSeedConfigProvider_NilIndexerPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil IndexerSeedConfig")
		}
	}()
	download.NewSeedConfigProvider(&stubLatestGrab{}, nil, nil)
}
