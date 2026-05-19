package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// downloadClientRowLister is the narrow surface the adapter consumes
// for "fetch every persisted download-client row". *downloadclients.Repo
// satisfies it via its embedded Repository[DownloadClient].All; the
// indirection lets unit tests inject a fixed slice without spinning up
// a real database.
type downloadClientRowLister interface {
	All(ctx context.Context) ([]downloadclients.DownloadClient, error)
}

// downloadClientLister is the production binding for
// [download.ClientLister]. It walks the DownloadClients table, filters
// to enabled rows, sorts by Priority (lower numeric value = higher
// priority — mirrors the Sonarr UI's "1 is top" convention and the
// per-row OrderBy already encoded in the downloadclients mapper),
// then hydrates each row into a concrete [downloadclients.Provider]
// via the registered Constructor. The final list is filtered to
// providers whose Protocol() matches the orchestrator's requested
// protocol so [download.ProcessDownloadDecisions.dispatch] sees the
// correct family.
//
// Mirrors the C# `IDownloadClientFactory.GetAvailableProviders` filter
// chain at `Download/DownloadClientFactory.cs` — the
// `DownloadService.GetDownloadClient(protocol)` call site at
// `Download/DownloadService.cs:69-78` does the same Enable +
// Protocol filter on a Priority-sorted list.
//
// Per-row Construct errors are logged and skipped (one broken row
// must not stop the grab loop). Mirrors the C# behaviour where a
// Settings-JSON drift between the row and the registered
// ConfigContract throws at Map time and is caught + logged by the
// factory — the broken client is omitted from the returned set.
type downloadClientLister struct {
	rows     downloadClientRowLister
	registry *downloadclients.Registry
	deps     downloadclients.Dependencies
	log      *slog.Logger
}

// newDownloadClientLister builds the production lister. The caller
// supplies the shared [downloadclients.Dependencies] bundle so the
// HTTP transport, ConfigProvider, DiskProvider, Logger, and (once
// they port) RemotePathMappingService + StatusService are reused
// across every constructed Provider.
//
// rows MUST be non-nil — the orchestrator calls ListByProtocol once
// per grab batch and a nil lister would panic deep in the call. The
// registry MAY be nil; a nil registry collapses to "no clients
// available" so the orchestrator buckets every grab to Pending
// (matches the C# "no clients configured" behaviour). log defaults
// to slog.Default() when nil.
func newDownloadClientLister(rows downloadClientRowLister, registry *downloadclients.Registry, deps downloadclients.Dependencies, log *slog.Logger) *downloadClientLister {
	if log == nil {
		log = slog.Default()
	}
	return &downloadClientLister{rows: rows, registry: registry, deps: deps, log: log}
}

// ListByProtocol implements [download.ClientLister]. Returns the
// enabled providers for the supplied protocol in priority order
// (lower Priority numeric → earlier in the slice → tried first by
// dispatch). Returns (nil, nil) when no enabled+constructable+
// protocol-matching client is configured — matches the
// download.ClientLister doc contract.
//
// A repo error propagates verbatim; a per-row Construct error is
// logged + skipped without aborting the loop. A nil registry skips
// the construction entirely (returns empty slice + nil).
func (l *downloadClientLister) ListByProtocol(ctx context.Context, protocol indexers.DownloadProtocol) ([]downloadclients.Provider, error) {
	if l.rows == nil {
		return nil, fmt.Errorf("download-client lister: nil rows source")
	}
	if l.registry == nil {
		return nil, nil
	}
	rows, err := l.rows.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("download-client lister: list rows: %w", err)
	}
	// Defensive resort by Priority — the repo's OrderBy already
	// honours this, but a future swap to a different rows source
	// (or a caller that pre-filters) shouldn't break the priority
	// contract.
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Priority < rows[j].Priority
	})

	out := make([]downloadclients.Provider, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !row.Enable {
			continue
		}
		provider, err := l.registry.Construct(row, l.deps)
		if err != nil {
			l.log.Warn("download-client lister: construct failed; skipping row",
				slog.Int("id", row.ID),
				slog.String("name", row.Name),
				slog.String("implementation", row.Implementation),
				slog.String("configContract", row.ConfigContract),
				slog.Any("err", err),
			)
			continue
		}
		if provider.Protocol() != protocol {
			continue
		}
		out = append(out, provider)
	}
	return out, nil
}

// Compile-time guard: downloadClientLister must satisfy
// download.ClientLister.
var _ download.ClientLister = (*downloadClientLister)(nil)
