package healthcheck

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/indexers"
)

// fakeIndexerLister stubs the IndexerJackettAllLister surface.
type fakeIndexerLister struct {
	rows []indexers.Indexer
	err  error
}

func (f fakeIndexerLister) All(_ context.Context) ([]indexers.Indexer, error) {
	return f.rows, f.err
}

func newEnabledIndexer(name, contract, settings string) indexers.Indexer {
	rss := true
	cc := contract
	s := settings
	return indexers.Indexer{
		Name:           name,
		ConfigContract: &cc,
		Settings:       &s,
		EnableRss:      &rss,
	}
}

func TestNewIndexerJackettAllCheck_RejectsNilLister(t *testing.T) {
	t.Parallel()
	if _, err := NewIndexerJackettAllCheck(nil); err == nil {
		t.Errorf("expected error for nil lister")
	}
}

func TestIndexerJackettAllCheck_OKEmptyList(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{})
	got := chk.Run(t.Context())
	if got.Type != ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestIndexerJackettAllCheck_OKNoMatchingPaths(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		newEnabledIndexer("Clean Torznab", "TorznabSettings",
			`{"baseUrl":"https://example.com","apiPath":"/api"}`),
	}
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{rows: rows})
	got := chk.Run(t.Context())
	if got.Type != ResultOK {
		t.Errorf("type = %v, want OK; msg=%q", got.Type, got.Message)
	}
}

func TestIndexerJackettAllCheck_WarnsOnOldJackettAllInBaseUrl(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		newEnabledIndexer("BadJackett", "TorznabSettings",
			`{"baseUrl":"https://jackett.local/torznab/all","apiPath":"/api"}`),
	}
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{rows: rows})
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Fatalf("type = %v, want Warning", got.Type)
	}
	if got.Reason != ReasonIndexerJackettAll {
		t.Errorf("reason = %v", got.Reason)
	}
	if !strings.Contains(got.Message, "BadJackett") {
		t.Errorf("message missing indexer name: %q", got.Message)
	}
}

func TestIndexerJackettAllCheck_WarnsOnV2PathInApiPath(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		newEnabledIndexer("V2Jackett", "TorznabSettings",
			`{"baseUrl":"https://j.local","apiPath":"/api/v2.0/indexers/all/results/torznab"}`),
	}
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{rows: rows})
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Errorf("type = %v, want Warning", got.Type)
	}
}

func TestIndexerJackettAllCheck_CaseInsensitive(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		newEnabledIndexer("UpperPath", "TorznabSettings",
			`{"baseUrl":"https://j.local","apiPath":"/Api/V2.0/INDEXERS/all/results/TORZNAB"}`),
	}
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{rows: rows})
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Errorf("type = %v, want Warning (case-insensitive match)", got.Type)
	}
}

func TestIndexerJackettAllCheck_IgnoresDisabledIndexers(t *testing.T) {
	t.Parallel()
	cc := "TorznabSettings"
	s := `{"baseUrl":"https://j.local/torznab/all","apiPath":""}`
	rss := false
	auto := false
	rows := []indexers.Indexer{{
		Name:                    "DisabledJackett",
		ConfigContract:          &cc,
		Settings:                &s,
		EnableRss:               &rss,
		EnableAutomaticSearch:   &auto,
		EnableInteractiveSearch: false,
	}}
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{rows: rows})
	got := chk.Run(t.Context())
	if got.Type != ResultOK {
		t.Errorf("type = %v, want OK for disabled indexer", got.Type)
	}
}

func TestIndexerJackettAllCheck_IgnoresNewznab(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		newEnabledIndexer("Newznab", "NewznabSettings",
			`{"baseUrl":"https://j.local/torznab/all","apiPath":""}`),
	}
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{rows: rows})
	got := chk.Run(t.Context())
	if got.Type != ResultOK {
		t.Errorf("type = %v, want OK for non-Torznab contract", got.Type)
	}
}

func TestIndexerJackettAllCheck_MessageListsAllOffenders(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		newEnabledIndexer("Zeta", "TorznabSettings",
			`{"baseUrl":"https://j/torznab/all","apiPath":""}`),
		newEnabledIndexer("Alpha", "TorznabSettings",
			`{"baseUrl":"https://j2/torznab/all","apiPath":""}`),
		newEnabledIndexer("Mid", "TorznabSettings",
			`{"baseUrl":"https://j3/torznab/all","apiPath":""}`),
	}
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{rows: rows})
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Fatalf("type = %v", got.Type)
	}
	// Names sorted alphabetically for stable output across runs.
	if !strings.Contains(got.Message, "Alpha, Mid, Zeta") {
		t.Errorf("expected sorted names; got %q", got.Message)
	}
}

func TestIndexerJackettAllCheck_HandlesListerError(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{err: errors.New("db boom")})
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Errorf("type = %v, want Warning on lister error", got.Type)
	}
	if !strings.Contains(got.Message, "db boom") {
		t.Errorf("message should surface lister error: %q", got.Message)
	}
}

func TestIndexerJackettAllCheck_IgnoresMalformedSettings(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		newEnabledIndexer("Broken", "TorznabSettings", `{not json}`),
	}
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{rows: rows})
	got := chk.Run(t.Context())
	if got.Type != ResultOK {
		t.Errorf("type = %v, want OK (malformed JSON → no match)", got.Type)
	}
}

func TestIndexerJackettAllCheck_RespectsCheckOnFlags(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerJackettAllCheck(fakeIndexerLister{})
	if !chk.CheckOnStartup() {
		t.Errorf("CheckOnStartup should default true")
	}
	if !chk.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule should default true")
	}
	if len(chk.EventTriggers()) == 0 {
		t.Errorf("expected at least one event trigger")
	}
}
