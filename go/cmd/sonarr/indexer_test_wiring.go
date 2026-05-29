package main

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/indexers/broadcasthenet"
	"sonarr.tv/sonarr/internal/indexers/fanzub"
	"sonarr.tv/sonarr/internal/indexers/filelist"
	"sonarr.tv/sonarr/internal/indexers/hdbits"
	"sonarr.tv/sonarr/internal/indexers/iptorrents"
	"sonarr.tv/sonarr/internal/indexers/newznab"
	"sonarr.tv/sonarr/internal/indexers/nyaa"
	"sonarr.tv/sonarr/internal/indexers/torrentleech"
	"sonarr.tv/sonarr/internal/indexers/torrentrss"
	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/provider"
)

// G10 (J441): wire the indexer Test/TestAll/Action endpoints to real
// providers. The concrete indexer packages register their Settings
// constructors into `provider.Default()` (the FamilyIndexer catalog)
// but NOT into `indexers.Default()` (the `indexers.Registry` the
// `registryIndexerTester` uses), so every test returned 400 "Unknown
// implementation". This file registers Registry constructors that build
// the concrete indexer (Newznab/Torznab — the Newznab-protocol family,
// the dominant real-world case) via the same construction the search
// path uses (`provider.NewFactory(provider.Default()).LoadDefinition` +
// the concrete `NewIndexer`), wrapping it in an adapter that satisfies
// the `indexers.Provider` Test/RequestAction surface.
//
// Remaining providers (broadcasthenet/hdbits/filelist/fanzub/iptorrents/
// nyaa/torrentleech/torrentrss) follow in a later slice — they share the
// uniform concreteIndexer surface so each is one more `reg(...)` line.

// concreteIndexer is the uniform method set every ported indexer
// `Indexer` type exposes (verified across all 10 provider packages).
type concreteIndexer interface {
	Test(ctx context.Context) provider.ValidationResult
	RequestAction(stage string, query map[string]string) (any, error)
	SetDefinition(*provider.Definition)
}

// concreteIndexerProviderAdapter bridges a concreteIndexer to the
// `indexers.Provider` interface the Test/Action endpoints consume:
// it translates `provider.ValidationResult` → `[]indexers.TestFailure`
// (error-severity only — warnings don't block the test, matching C#
// `ValidationResult.IsValid`) and adapts the ctx-less RequestAction.
type concreteIndexerProviderAdapter struct {
	idx concreteIndexer
}

func (a concreteIndexerProviderAdapter) Test(ctx context.Context) ([]indexers.TestFailure, error) {
	res := a.idx.Test(ctx)
	var out []indexers.TestFailure
	for _, f := range res.Errors {
		if f.Severity != provider.MessageError {
			continue
		}
		out = append(out, indexers.TestFailure{
			PropertyName:        f.PropertyName,
			ErrorMessage:        f.Message,
			DetailedDescription: f.DetailedDescription,
		})
	}
	return out, nil
}

func (a concreteIndexerProviderAdapter) RequestAction(_ context.Context, action string, query map[string]string) (any, error) {
	return a.idx.RequestAction(action, query)
}

// registerIndexerTestProviders registers the Registry constructors so
// `indexers.Default()` (and thus the Test/TestAll/Action endpoints)
// builds real providers. Idempotent: tolerates ErrAlreadyRegistered so
// repeated serve()/buildHostServer boots (tests) don't panic. The
// captured httpClient is the host-configured client (rate-limited +
// host-status-tracked), shared with the search path.
func registerIndexerTestProviders(httpClient httpclient.Client) {
	if httpClient == nil {
		return
	}
	caps := newznab.NewCapabilitiesProvider(httpClient)
	factory := provider.NewFactory(provider.Default())

	// mk builds a Registry constructor: load the Definition from the row
	// (via the global catalog), construct the concrete indexer, bind the
	// definition, and wrap it in the Provider adapter.
	mk := func(build func() concreteIndexer) indexers.Constructor {
		return func(row *indexers.Indexer, _ indexers.Dependencies) (indexers.Provider, error) {
			def, err := factory.LoadDefinition(provider.FamilyIndexer, indexerRowAdapter{row: row})
			if err != nil {
				return nil, err
			}
			idx := build()
			idx.SetDefinition(def)
			return concreteIndexerProviderAdapter{idx: idx}, nil
		}
	}
	reg := func(impl, contract string, c indexers.Constructor) {
		if err := indexers.Default().Register(impl, contract, c); err != nil && !errors.Is(err, indexers.ErrAlreadyRegistered) {
			panic(fmt.Errorf("registerIndexerTestProviders %s/%s: %w", impl, contract, err))
		}
	}

	// Newznab-protocol family (take the shared caps provider).
	reg("Newznab", "NewznabSettings", mk(func() concreteIndexer { return newznab.NewIndexer(httpClient, caps) }))
	reg("Torznab", "TorznabSettings", mk(func() concreteIndexer { return torznab.NewIndexer(httpClient, caps) }))

	// Bespoke trackers + RSS (take only the http client — no caps
	// provider). Keys mirror the FamilyIndexer catalog (provider/ids.go)
	// so they match the persisted Implementation/ConfigContract columns.
	reg("Fanzub", "FanzubSettings", mk(func() concreteIndexer { return fanzub.NewIndexer(httpClient) }))
	reg("BroadcastheNet", "BroadcastheNetSettings", mk(func() concreteIndexer { return broadcasthenet.NewIndexer(httpClient) }))
	reg("HDBits", "HDBitsSettings", mk(func() concreteIndexer { return hdbits.NewIndexer(httpClient) }))
	reg("IPTorrents", "IPTorrentsSettings", mk(func() concreteIndexer { return iptorrents.NewIndexer(httpClient) }))
	reg("Nyaa", "NyaaSettings", mk(func() concreteIndexer { return nyaa.NewIndexer(httpClient) }))
	reg("Torrentleech", "TorrentleechSettings", mk(func() concreteIndexer { return torrentleech.NewIndexer(httpClient) }))
	reg("FileList", "FileListSettings", mk(func() concreteIndexer { return filelist.NewIndexer(httpClient) }))
	reg("TorrentRssIndexer", "TorrentRssIndexerSettings", mk(func() concreteIndexer { return torrentrss.NewIndexer(httpClient) }))
}
