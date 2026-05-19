package healthcheck

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

type fakeDCLister struct {
	rows []downloadclients.DownloadClient
	err  error
}

func (f fakeDCLister) All(_ context.Context) ([]downloadclients.DownloadClient, error) {
	return f.rows, f.err
}

type fakeIxDCLister struct {
	rows []indexers.Indexer
	err  error
}

func (f fakeIxDCLister) All(_ context.Context) ([]indexers.Indexer, error) {
	return f.rows, f.err
}

func enabledIndexerWithDC(name string, dcID int) indexers.Indexer {
	rss := true
	return indexers.Indexer{
		Name:             name,
		EnableRss:        &rss,
		DownloadClientID: dcID,
	}
}

func TestNewIndexerDownloadClientCheck_RejectsNils(t *testing.T) {
	t.Parallel()
	if _, err := NewIndexerDownloadClientCheck(nil, fakeDCLister{}); err == nil {
		t.Errorf("nil indexer lister should error")
	}
	if _, err := NewIndexerDownloadClientCheck(fakeIxDCLister{}, nil); err == nil {
		t.Errorf("nil DC lister should error")
	}
}

func TestIndexerDownloadClientCheck_OKEmptyLists(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerDownloadClientCheck(fakeIxDCLister{}, fakeDCLister{})
	got := chk.Run(t.Context())
	if got.Type != ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestIndexerDownloadClientCheck_OKIndexerWithoutDCBinding(t *testing.T) {
	t.Parallel()
	// DownloadClientID == 0 means "use any available client" — not a misconfiguration.
	chk, _ := NewIndexerDownloadClientCheck(
		fakeIxDCLister{rows: []indexers.Indexer{enabledIndexerWithDC("Free", 0)}},
		fakeDCLister{},
	)
	if got := chk.Run(t.Context()); got.Type != ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestIndexerDownloadClientCheck_OKWhenDCEnabled(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerDownloadClientCheck(
		fakeIxDCLister{rows: []indexers.Indexer{enabledIndexerWithDC("Indy", 7)}},
		fakeDCLister{rows: []downloadclients.DownloadClient{{ID: 7, Enable: true}}},
	)
	if got := chk.Run(t.Context()); got.Type != ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestIndexerDownloadClientCheck_WarnsWhenDCMissing(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerDownloadClientCheck(
		fakeIxDCLister{rows: []indexers.Indexer{enabledIndexerWithDC("Indy", 99)}},
		fakeDCLister{rows: []downloadclients.DownloadClient{{ID: 1, Enable: true}}},
	)
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Fatalf("type = %v, want Warning", got.Type)
	}
	if got.Reason != ReasonIndexerDownloadClient {
		t.Errorf("reason = %v", got.Reason)
	}
	if !strings.Contains(got.Message, "Indy") {
		t.Errorf("message missing indexer name: %q", got.Message)
	}
}

func TestIndexerDownloadClientCheck_WarnsWhenDCDisabled(t *testing.T) {
	t.Parallel()
	// DC row exists for ID=7 but Enable=false → still counts as missing.
	chk, _ := NewIndexerDownloadClientCheck(
		fakeIxDCLister{rows: []indexers.Indexer{enabledIndexerWithDC("Indy", 7)}},
		fakeDCLister{rows: []downloadclients.DownloadClient{{ID: 7, Enable: false}}},
	)
	if got := chk.Run(t.Context()); got.Type != ResultWarning {
		t.Errorf("type = %v, want Warning (disabled DC)", got.Type)
	}
}

func TestIndexerDownloadClientCheck_IgnoresDisabledIndexers(t *testing.T) {
	t.Parallel()
	rss := false
	auto := false
	rows := []indexers.Indexer{{
		Name:                    "Off",
		EnableRss:               &rss,
		EnableAutomaticSearch:   &auto,
		EnableInteractiveSearch: false,
		DownloadClientID:        99,
	}}
	chk, _ := NewIndexerDownloadClientCheck(
		fakeIxDCLister{rows: rows},
		fakeDCLister{},
	)
	if got := chk.Run(t.Context()); got.Type != ResultOK {
		t.Errorf("type = %v, want OK (disabled indexer should not warn)", got.Type)
	}
}

func TestIndexerDownloadClientCheck_MessageSortsAndLists(t *testing.T) {
	t.Parallel()
	rows := []indexers.Indexer{
		enabledIndexerWithDC("Zeta", 88),
		enabledIndexerWithDC("Alpha", 89),
		enabledIndexerWithDC("Mid", 90),
	}
	chk, _ := NewIndexerDownloadClientCheck(
		fakeIxDCLister{rows: rows},
		fakeDCLister{}, // no enabled DCs
	)
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Fatalf("type = %v", got.Type)
	}
	if !strings.Contains(got.Message, "Alpha, Mid, Zeta") {
		t.Errorf("expected alphabetically-sorted list; got %q", got.Message)
	}
}

func TestIndexerDownloadClientCheck_DCListerErrorPropagates(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerDownloadClientCheck(
		fakeIxDCLister{},
		fakeDCLister{err: errors.New("dc db boom")},
	)
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Errorf("type = %v, want Warning", got.Type)
	}
	if !strings.Contains(got.Message, "dc db boom") {
		t.Errorf("message should surface DC error: %q", got.Message)
	}
}

func TestIndexerDownloadClientCheck_IxListerErrorPropagates(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerDownloadClientCheck(
		fakeIxDCLister{err: errors.New("ix db boom")},
		fakeDCLister{},
	)
	got := chk.Run(t.Context())
	if got.Type != ResultWarning {
		t.Errorf("type = %v, want Warning", got.Type)
	}
	if !strings.Contains(got.Message, "ix db boom") {
		t.Errorf("message should surface indexer error: %q", got.Message)
	}
}

func TestIndexerDownloadClientCheck_RespectsCheckOnFlags(t *testing.T) {
	t.Parallel()
	chk, _ := NewIndexerDownloadClientCheck(fakeIxDCLister{}, fakeDCLister{})
	if !chk.CheckOnStartup() || !chk.CheckOnSchedule() {
		t.Errorf("startup/schedule flags should default true")
	}
	if len(chk.EventTriggers()) == 0 {
		t.Errorf("expected at least one event trigger")
	}
}
