// indexersearch_wiring.go binds the persisted Indexers rows to
// concrete runtime indexer instances and exposes them to
// indexersearch.Service via the IndexerSource interface. This is
// queue [7] sub-arc 7c (final) — the wiring layer that closes the
// orchestrator's plumbing by feeding it real indexers built from
// each row's Implementation/Settings via the provider Registry.
//
// C# parallel: src/NzbDrone.Core/Indexers/IndexerFactory.cs:23-150 —
// the IIndexerFactory implementation that hydrates IndexerDefinition
// rows into IIndexer instances on demand. The Go port here mirrors
// the structural shape (per-call snapshot, runtime-constructor
// dispatch keyed on the persisted Implementation string) but stays
// small because the orchestrator already owns the eligibility
// filtering — this file only builds the (row → runtime → Adapter)
// chain.
//
// Why this lives in cmd/sonarr (not internal/indexersearch):
//
//   - internal/indexersearch is the leaf-import position; importing
//     internal/indexers/{newznab,torznab,torrentrss} from there would
//     induce a cycle once the concrete packages reference back into
//     indexersearch for criteria types (see criteria.go).
//   - cmd/sonarr is the composition root; importing every concrete
//     indexer here is fine and matches the wiring pattern used for
//     decisionengine + downloadedimport.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/indexers/fanzub"
	"sonarr.tv/sonarr/internal/indexers/filelist"
	"sonarr.tv/sonarr/internal/indexers/iptorrents"
	"sonarr.tv/sonarr/internal/indexers/newznab"
	"sonarr.tv/sonarr/internal/indexers/nyaa"
	"sonarr.tv/sonarr/internal/indexers/torrentleech"
	"sonarr.tv/sonarr/internal/indexers/torrentrss"
	"sonarr.tv/sonarr/internal/indexers/torznab"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/provider"
)

// indexerRowAdapter wraps an indexers.Indexer row so it satisfies the
// provider.Row interface without polluting the persistence package
// with provider concerns. The wrapper is constructed per-call inside
// dbIndexerSource.Enabled.
type indexerRowAdapter struct {
	row *indexers.Indexer
}

func (a indexerRowAdapter) GetID() int { return a.row.ID }

func (a indexerRowAdapter) GetName() string { return a.row.Name }

func (a indexerRowAdapter) GetImplementation() string { return a.row.Implementation }

func (a indexerRowAdapter) GetConfigContract() string {
	if a.row.ConfigContract == nil {
		return ""
	}
	return *a.row.ConfigContract
}

// GetEnable reports whether the row is enabled for ANY of the three
// indexer-side flags (RSS / automatic search / interactive search).
// The orchestrator filters per-flag eligibility internally — this
// row-level "enabled at all" check just decides whether to hydrate +
// instantiate the runtime indexer.
func (a indexerRowAdapter) GetEnable() bool {
	rss := a.row.EnableRss != nil && *a.row.EnableRss
	auto := a.row.EnableAutomaticSearch != nil && *a.row.EnableAutomaticSearch
	return rss || auto || a.row.EnableInteractiveSearch
}

func (a indexerRowAdapter) GetSettingsJSON() string {
	if a.row.Settings == nil {
		return ""
	}
	return *a.row.Settings
}

func (a indexerRowAdapter) GetTags() []int { return a.row.Tags }

func (a indexerRowAdapter) GetMessage() *provider.Message { return nil }

// dbIndexerSource implements indexersearch.IndexerSource by querying
// indexers.Repo.All on every Enabled call, hydrating rows via
// provider.Factory, and wrapping each instantiated indexer in an
// indexersearch.Adapter that closes over the typed FetchRecent /
// FetchSearch surface.
type dbIndexerSource struct {
	repo       *indexers.Repo
	factory    provider.Factory
	httpClient httpclient.Client
	capsProv   *newznab.CapabilitiesProvider
	log        *slog.Logger
}

// newIndexerSource builds the production IndexerSource. Constructed
// once at boot; the per-call repo.All hits are cheap (Indexers is a
// small table — typically a handful of rows).
//
// capsProv is shared by the newznab and torznab wrappers since the
// `?t=caps` probe is identical for both protocols.
func newIndexerSource(repo *indexers.Repo, httpClient httpclient.Client, log *slog.Logger) *dbIndexerSource {
	return &dbIndexerSource{
		repo:       repo,
		factory:    provider.NewFactory(provider.Default()),
		httpClient: httpClient,
		capsProv:   newznab.NewCapabilitiesProvider(httpClient),
		log:        log,
	}
}

// Enabled implements indexersearch.IndexerSource. Returns one
// Adapter per enabled row whose Implementation matches a registered
// runtime constructor; rows whose Implementation is unknown
// (uninstalled provider, renamed C# class) are skipped with a
// warning rather than failing the whole list — mirrors the C#
// IndexerFactory.GetAvailableProviders behaviour where unresolvable
// rows are logged + skipped.
func (s *dbIndexerSource) Enabled(ctx context.Context) ([]indexersearch.Indexer, error) {
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexersearch wiring: list rows: %w", err)
	}
	out := make([]indexersearch.Indexer, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !rowEnabled(row) {
			continue
		}
		def, err := s.factory.LoadDefinition(provider.FamilyIndexer, indexerRowAdapter{row: row})
		if err != nil {
			s.log.Warn("indexersearch wiring: hydrate definition failed",
				slog.String("name", row.Name),
				slog.String("implementation", row.Implementation),
				slog.Any("err", err),
			)
			continue
		}
		idx, err := s.buildAdapter(row, def)
		if err != nil {
			s.log.Warn("indexersearch wiring: build adapter failed",
				slog.String("name", row.Name),
				slog.String("implementation", row.Implementation),
				slog.Any("err", err),
			)
			continue
		}
		out = append(out, idx)
	}
	return out, nil
}

// buildAdapter routes the row to the right concrete-indexer
// constructor and wraps the typed FetchRecent/FetchSearch methods in
// the closure shape indexersearch.Adapter expects.
func (s *dbIndexerSource) buildAdapter(row *indexers.Indexer, def *provider.Definition) (indexersearch.Indexer, error) {
	common := commonRowFields(row)
	switch row.Implementation {
	case "Newznab":
		idx := newznab.NewIndexer(s.httpClient, s.capsProv)
		idx.SetDefinition(def)
		common.FetchRecentFunc = func(ctx context.Context) ([]any, error) {
			rs, err := idx.FetchRecent(ctx)
			return upcastReleaseInfos(rs), err
		}
		common.FetchSearchFunc = func(ctx context.Context, c indexersearch.Criteria) ([]any, error) {
			rs, err := idx.FetchSearch(ctx, c)
			return upcastReleaseInfos(rs), err
		}
		return common, nil

	case "Torznab":
		idx := torznab.NewIndexer(s.httpClient, s.capsProv)
		idx.SetDefinition(def)
		common.FetchRecentFunc = func(ctx context.Context) ([]any, error) {
			rs, err := idx.FetchRecent(ctx)
			return upcastTorrentInfos(rs), err
		}
		common.FetchSearchFunc = func(ctx context.Context, c indexersearch.Criteria) ([]any, error) {
			rs, err := idx.FetchSearch(ctx, c)
			return upcastTorrentInfos(rs), err
		}
		return common, nil

	case "TorrentRss":
		idx := torrentrss.NewIndexer(s.httpClient)
		idx.SetDefinition(def)
		common.FetchRecentFunc = func(ctx context.Context) ([]any, error) {
			rs, err := idx.FetchRecent(ctx)
			return upcastTorrentInfos(rs), err
		}
		// torrentrss hardcodes SupportsSearch=false — leave
		// FetchSearchFunc nil so Adapter.FetchSearch returns the
		// "does not support search" error and the orchestrator
		// routes around it gracefully.
		return common, nil

	case "Torrentleech":
		idx := torrentleech.NewIndexer(s.httpClient)
		idx.SetDefinition(def)
		common.FetchRecentFunc = func(ctx context.Context) ([]any, error) {
			rs, err := idx.FetchRecent(ctx)
			return upcastTorrentInfos(rs), err
		}
		// Slice 5.J146: Torrentleech is RSS-only (SupportsSearch=false
		// on the C# side at Torrentleech.cs:14). Same posture as
		// torrentrss — leave FetchSearchFunc nil.
		return common, nil

	case "IPTorrents":
		idx := iptorrents.NewIndexer(s.httpClient)
		idx.SetDefinition(def)
		common.FetchRecentFunc = func(ctx context.Context) ([]any, error) {
			rs, err := idx.FetchRecent(ctx)
			return upcastTorrentInfos(rs), err
		}
		// Slice 5.J147: IPTorrents is RSS-only (SupportsSearch=false
		// on the C# side at IPTorrents.cs:14). Same posture as
		// torrentleech — leave FetchSearchFunc nil.
		return common, nil

	case "FileList":
		idx := filelist.NewIndexer(s.httpClient)
		idx.SetDefinition(def)
		common.FetchRecentFunc = func(ctx context.Context) ([]any, error) {
			rs, err := idx.FetchRecent(ctx)
			return upcastTorrentInfos(rs), err
		}
		// Slice 5.J152 (first slice of the FileList arc): only the
		// JSON `latest-torrents` FetchRecent path lands. The
		// per-criteria search routing (FileListRequestGenerator.cs:24-120
		// — IMDB-search + Name-search dual-flow with anime-episode
		// fan-out) ships as a follow-up slice. Leave FetchSearchFunc
		// nil so the adapter surfaces the "does not support search"
		// error instead of silently returning empty.
		return common, nil

	case "Fanzub":
		idx := fanzub.NewIndexer(s.httpClient)
		idx.SetDefinition(def)
		common.FetchRecentFunc = func(ctx context.Context) ([]any, error) {
			rs, err := idx.FetchRecent(ctx)
			return upcastReleaseInfos(rs), err
		}
		// Slice 5.J151 closes the J150 deferral — FetchSearch routes
		// Season + AnimeEpisode + AnimeSeason criteria through the
		// per-overload search-string builders + pipe-joins them into
		// one q=<...> value.
		common.FetchSearchFunc = func(ctx context.Context, c indexersearch.Criteria) ([]any, error) {
			rs, err := idx.FetchSearch(ctx, c)
			return upcastReleaseInfos(rs), err
		}
		return common, nil

	case "Nyaa":
		idx := nyaa.NewIndexer(s.httpClient)
		idx.SetDefinition(def)
		common.FetchRecentFunc = func(ctx context.Context) ([]any, error) {
			rs, err := idx.FetchRecent(ctx)
			return upcastTorrentInfos(rs), err
		}
		// Slice 5.J149 closes the J148 FetchSearch deferral —
		// FetchSearch now routes the typed criteria through the
		// per-overload dispatch (SingleEpisode + Anime + Special
		// emit per-title term URLs; Daily / Season / non-anime
		// short-circuit empty) and dedupes by Guid.
		common.FetchSearchFunc = func(ctx context.Context, c indexersearch.Criteria) ([]any, error) {
			rs, err := idx.FetchSearch(ctx, c)
			return upcastTorrentInfos(rs), err
		}
		return common, nil

	default:
		return nil, fmt.Errorf("unknown Implementation %q", row.Implementation)
	}
}

// commonRowFields builds an Adapter pre-populated with the
// non-Func metadata (ID/Name/Priority/Tags/Enable*). The caller
// fills in FetchRecentFunc/FetchSearchFunc per-implementation.
func commonRowFields(row *indexers.Indexer) *indexersearch.Adapter {
	return &indexersearch.Adapter{
		IDValue:                      row.ID,
		NameValue:                    row.Name,
		PriorityValue:                row.Priority,
		TagsValue:                    row.Tags,
		EnableRSSValue:               row.EnableRss != nil && *row.EnableRss,
		EnableAutomaticSearchValue:   row.EnableAutomaticSearch != nil && *row.EnableAutomaticSearch,
		EnableInteractiveSearchValue: row.EnableInteractiveSearch,
	}
}

// rowEnabled mirrors indexerRowAdapter.GetEnable — duplicated here
// so the per-row filter doesn't need to construct a temporary
// adapter just to read the flags.
func rowEnabled(row *indexers.Indexer) bool {
	rss := row.EnableRss != nil && *row.EnableRss
	auto := row.EnableAutomaticSearch != nil && *row.EnableAutomaticSearch
	return rss || auto || row.EnableInteractiveSearch
}

func upcastReleaseInfos(rs []*parser.ReleaseInfo) []any {
	if len(rs) == 0 {
		return nil
	}
	out := make([]any, len(rs))
	for i, r := range rs {
		out[i] = r
	}
	return out
}

func upcastTorrentInfos(rs []*parser.TorrentInfo) []any {
	if len(rs) == 0 {
		return nil
	}
	out := make([]any, len(rs))
	for i, r := range rs {
		out[i] = r
	}
	return out
}

// Compile-time guard: dbIndexerSource must satisfy IndexerSource.
var _ indexersearch.IndexerSource = (*dbIndexerSource)(nil)

// Unused-import suppressor: datastore is referenced indirectly via
// indexers.NewRepo signature only. Keep the import here so a future
// reorganization (e.g. moving the repo construction into this file)
// finds it pre-imported.
var _ = (*datastore.DB)(nil)

// minimumSeedersAdapter implements decisionengine.MinimumSeedersReader
// — the per-indexer MinimumSeeders lookup the TorrentSeedingSpecification
// at slice 4.P (queue [8]) consumes.
//
// Mirrors the C# `IIndexerFactory.Get(id).Settings as ITorrentIndexerSettings`
// pattern at TorrentSeedingSpecification.cs:34-46, flattened to a
// single MinimumSeeders read since the spec doesn't need the rest of
// the IIndexer surface. The (id → row → Definition →
// type-switch-on-Settings) chain is identical to dbIndexerSource's
// row-hydration path in this file; consolidated into one helper to
// keep the spec's contract narrow.
//
// On indexers.ErrNotFound the adapter returns
// decisionengine.ErrTorrentIndexerNotFound — the C# spec catches
// ModelNotFoundException and accepts; the spec's IsSatisfiedBy at
// internal/decisionengine/spec_torrent_seeding.go translates this
// sentinel back to accept.
type minimumSeedersAdapter struct {
	repo    *indexers.Repo
	factory provider.Factory
}

// newMinimumSeedersAdapter binds the dependencies. Used by main.go
// when building decisionEngineDeps.
func newMinimumSeedersAdapter(repo *indexers.Repo) minimumSeedersAdapter {
	return minimumSeedersAdapter{
		repo:    repo,
		factory: provider.NewFactory(provider.Default()),
	}
}

// MinimumSeeders implements decisionengine.MinimumSeedersReader.
func (a minimumSeedersAdapter) MinimumSeeders(ctx context.Context, id int) (int, bool, error) {
	row, err := a.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, indexers.ErrNotFound) {
			return 0, false, decisionengine.ErrTorrentIndexerNotFound
		}
		return 0, false, fmt.Errorf("indexers.Get(%d): %w", id, err)
	}
	def, err := a.factory.LoadDefinition(provider.FamilyIndexer, indexerRowAdapter{row: row})
	if err != nil {
		return 0, false, fmt.Errorf("provider.LoadDefinition(%d): %w", id, err)
	}
	if def == nil || def.Settings == nil {
		// Implementation unknown / unregistered — treat as
		// non-torrent (mirrors the C# settings-cast-fails path).
		return 0, false, nil
	}
	switch s := def.Settings.(type) {
	case *torznab.Settings:
		return s.MinimumSeeders, true, nil
	case *torrentrss.Settings:
		return s.MinimumSeeders, true, nil
	case *torrentleech.Settings:
		return s.MinimumSeeders, true, nil
	case *iptorrents.Settings:
		return s.MinimumSeeders, true, nil
	case *nyaa.Settings:
		return s.MinimumSeeders, true, nil
	case *filelist.Settings:
		return s.MinimumSeeders, true, nil
	default:
		// Newznab + any non-torrent provider class.
		return 0, false, nil
	}
}

// Compile-time guard.
var _ decisionengine.MinimumSeedersReader = minimumSeedersAdapter{}

// enabledIndexerListerAdapter implements
// healthcheck.EnabledIndexerLister — the "list every enabled indexer
// row" projection the slice 4.R IndexerStatusCheck consumes.
// Wraps indexers.Repo.All and applies the Enable predicate
// (EnableRss || EnableAutomaticSearch || EnableInteractiveSearch)
// already encoded in rowEnabled. Reuses the same filter the
// dbIndexerSource uses for runtime indexer membership, so the
// healthcheck and the orchestrator agree on "what counts as
// enabled."
//
// C# parallel: IndexerStatusCheck.cs:28 (`enabledProviders =
// _providerFactory.GetAvailableProviders()`). The Go port flattens
// to (ID, Name) since the healthcheck doesn't need anything else.
type enabledIndexerListerAdapter struct {
	repo *indexers.Repo
}

// newEnabledIndexerListerAdapter binds the dependency.
func newEnabledIndexerListerAdapter(repo *indexers.Repo) enabledIndexerListerAdapter {
	return enabledIndexerListerAdapter{repo: repo}
}

// EnabledIndexers implements healthcheck.EnabledIndexerLister.
func (a enabledIndexerListerAdapter) EnabledIndexers(ctx context.Context) ([]healthcheck.EnabledIndexer, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexers.All: %w", err)
	}
	out := make([]healthcheck.EnabledIndexer, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !rowEnabled(row) {
			continue
		}
		out = append(out, healthcheck.EnabledIndexer{ID: row.ID, Name: row.Name})
	}
	return out, nil
}

// Compile-time guard.
var _ healthcheck.EnabledIndexerLister = enabledIndexerListerAdapter{}
