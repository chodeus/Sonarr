package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// stubRowLister returns a fixed slice of rows + an optional error.
type stubRowLister struct {
	rows []downloadclients.DownloadClient
	err  error
}

func (s stubRowLister) All(_ context.Context) ([]downloadclients.DownloadClient, error) {
	return s.rows, s.err
}

// stubProvider satisfies downloadclients.Provider with just enough
// surface for the lister tests — Name + Protocol + Definition. The
// other methods return zero values; the lister never invokes them.
type stubProvider struct {
	name     string
	protocol indexers.DownloadProtocol
	def      *downloadclients.DownloadClient
}

func (p *stubProvider) Name() string                                { return p.name }
func (p *stubProvider) Protocol() indexers.DownloadProtocol         { return p.protocol }
func (p *stubProvider) Definition() *downloadclients.DownloadClient { return p.def }
func (p *stubProvider) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", nil
}

func (p *stubProvider) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, nil
}

func (p *stubProvider) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

func (p *stubProvider) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return nil
}

func (p *stubProvider) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{}, nil
}

func (p *stubProvider) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

func (p *stubProvider) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	return nil, nil
}

func (p *stubProvider) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

func buildRegistry(t *testing.T, entries ...struct {
	impl, contract string
	protocol       indexers.DownloadProtocol
}) *downloadclients.Registry {
	t.Helper()
	r := downloadclients.NewRegistry()
	for _, e := range entries {
		proto := e.protocol
		if err := r.Register(e.impl, e.contract, func(def *downloadclients.DownloadClient, _ downloadclients.Dependencies) (downloadclients.Provider, error) {
			return &stubProvider{name: def.Name, protocol: proto, def: def}, nil
		}); err != nil {
			t.Fatalf("Register(%s/%s): %v", e.impl, e.contract, err)
		}
	}
	return r
}

func TestDownloadClientLister_FiltersEnableAndProtocolAndSortsByPriority(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{
		// Out-of-order to prove the sort runs.
		{ID: 3, Name: "qbit-low", Implementation: "qBittorrent", ConfigContract: "QBittorrentSettings", Enable: true, Priority: 30},
		{ID: 1, Name: "sab-top", Implementation: "Sabnzbd", ConfigContract: "SabnzbdSettings", Enable: true, Priority: 10},
		{ID: 4, Name: "disabled-sab", Implementation: "Sabnzbd", ConfigContract: "SabnzbdSettings", Enable: false, Priority: 5},
		{ID: 2, Name: "qbit-top", Implementation: "qBittorrent", ConfigContract: "QBittorrentSettings", Enable: true, Priority: 20},
	}
	reg := buildRegistry(t,
		struct {
			impl, contract string
			protocol       indexers.DownloadProtocol
		}{"Sabnzbd", "SabnzbdSettings", indexers.DownloadProtocolUsenet},
		struct {
			impl, contract string
			protocol       indexers.DownloadProtocol
		}{"qBittorrent", "QBittorrentSettings", indexers.DownloadProtocolTorrent},
	)
	lister := newDownloadClientLister(stubRowLister{rows: rows}, reg, downloadclients.Dependencies{}, nil)

	usenets, err := lister.ListByProtocol(context.Background(), indexers.DownloadProtocolUsenet)
	if err != nil {
		t.Fatalf("ListByProtocol(Usenet): %v", err)
	}
	if len(usenets) != 1 || usenets[0].Name() != "sab-top" {
		got := make([]string, len(usenets))
		for i, p := range usenets {
			got[i] = p.Name()
		}
		t.Fatalf("Usenet: want [sab-top], got %v (disabled-sab must be filtered)", got)
	}

	torrents, err := lister.ListByProtocol(context.Background(), indexers.DownloadProtocolTorrent)
	if err != nil {
		t.Fatalf("ListByProtocol(Torrent): %v", err)
	}
	if len(torrents) != 2 {
		t.Fatalf("Torrent: want 2 providers, got %d", len(torrents))
	}
	if torrents[0].Name() != "qbit-top" || torrents[1].Name() != "qbit-low" {
		got := make([]string, len(torrents))
		for i, p := range torrents {
			got[i] = p.Name()
		}
		t.Errorf("Torrent priority order: want [qbit-top qbit-low], got %v", got)
	}
}

func TestDownloadClientLister_NilRegistryReturnsEmpty(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{
		{ID: 1, Name: "sab", Implementation: "Sabnzbd", Enable: true},
	}
	lister := newDownloadClientLister(stubRowLister{rows: rows}, nil, downloadclients.Dependencies{}, nil)
	got, err := lister.ListByProtocol(context.Background(), indexers.DownloadProtocolUsenet)
	if err != nil {
		t.Fatalf("ListByProtocol: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty slice with nil registry, got %d providers", len(got))
	}
}

func TestDownloadClientLister_RepoErrorPropagates(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	// Pass non-zero Dependencies so the unparam linter sees variance
	// across call sites — the deps bundle is real production data
	// once 6.A6c wires main.go.
	// Pass non-zero Dependencies + a non-nil logger so the unparam
	// linter sees variance across call sites — both deps carry real
	// production data once 6.A6c wires main.go.
	quietLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	lister := newDownloadClientLister(stubRowLister{err: sentinel}, downloadclients.NewRegistry(), downloadclients.Dependencies{Logger: "sentinel"}, quietLog)
	_, err := lister.ListByProtocol(context.Background(), indexers.DownloadProtocolUsenet)
	if !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel, got %v", err)
	}
}

func TestDownloadClientLister_ConstructErrorSkipsRow(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{
		{ID: 1, Name: "good", Implementation: "Sabnzbd", ConfigContract: "SabnzbdSettings", Enable: true, Priority: 10},
		{ID: 2, Name: "broken", Implementation: "Broken", ConfigContract: "BrokenSettings", Enable: true, Priority: 5},
	}
	reg := downloadclients.NewRegistry()
	if err := reg.Register("Sabnzbd", "SabnzbdSettings", func(def *downloadclients.DownloadClient, _ downloadclients.Dependencies) (downloadclients.Provider, error) {
		return &stubProvider{name: def.Name, protocol: indexers.DownloadProtocolUsenet, def: def}, nil
	}); err != nil {
		t.Fatalf("register Sabnzbd: %v", err)
	}
	if err := reg.Register("Broken", "BrokenSettings", func(_ *downloadclients.DownloadClient, _ downloadclients.Dependencies) (downloadclients.Provider, error) {
		return nil, errors.New("settings JSON drift")
	}); err != nil {
		t.Fatalf("register Broken: %v", err)
	}
	lister := newDownloadClientLister(stubRowLister{rows: rows}, reg, downloadclients.Dependencies{}, nil)
	got, err := lister.ListByProtocol(context.Background(), indexers.DownloadProtocolUsenet)
	if err != nil {
		t.Fatalf("ListByProtocol: %v", err)
	}
	if len(got) != 1 || got[0].Name() != "good" {
		names := make([]string, len(got))
		for i, p := range got {
			names[i] = p.Name()
		}
		t.Errorf("want [good] (broken row skipped), got %v", names)
	}
}

func TestDownloadClientLister_UnregisteredImplementationSkipped(t *testing.T) {
	t.Parallel()
	rows := []downloadclients.DownloadClient{
		{ID: 1, Name: "ghost", Implementation: "Ghost", ConfigContract: "GhostSettings", Enable: true},
	}
	lister := newDownloadClientLister(stubRowLister{rows: rows}, downloadclients.NewRegistry(), downloadclients.Dependencies{}, nil)
	got, err := lister.ListByProtocol(context.Background(), indexers.DownloadProtocolUsenet)
	if err != nil {
		t.Fatalf("ListByProtocol: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty (unregistered row skipped), got %d providers", len(got))
	}
}
