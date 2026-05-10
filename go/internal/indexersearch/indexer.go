package indexersearch

import (
	"context"
	"fmt"
)

// Indexer is the narrow runtime surface the orchestrator consumes. C#
// cite: src/NzbDrone.Core/Indexers/IIndexer.cs:9-23 +
// HttpIndexerBase<TSettings>.cs:30-66, but pared to the fields
// ReleaseSearchService actually reads.
//
// Why a narrow interface, not the full provider.Provider:
//
//   - The orchestrator only needs (ID, Name, Priority, Tags) for routing
//     and dedup — Test/RequestAction/Definition mutators are irrelevant
//     to fan-out.
//   - The three concrete indexer packages (newznab/torznab/torrentrss)
//     return different concrete types from FetchRecent/FetchSearch
//     (`[]*parser.ReleaseInfo` vs `[]*parser.TorrentInfo`); this
//     interface flattens to `[]any` so callers downstream type-assert
//     via `parser.AsTorrentInfo` (the documented C#-`as` replacement
//     pattern from parser/release_info.go:117-135).
//   - No import dependency on internal/indexers/* — the orchestrator
//     stays in the leaf import position so the wiring layer (queue
//     [7d]) can introduce concrete indexers without an import cycle.
//
// Construction: the wiring layer (cmd/sonarr at queue [7d]) builds
// concrete Indexer values via Adapter (below) bound to the typed
// FetchRecent/FetchSearch methods on each newznab/torznab/torrentrss
// Indexer. Tests in this package use stub Indexers built ad-hoc.
type Indexer interface {
	// ID is the persisted Indexers row ID. Used to key
	// indexerstatus.Recorder calls and to group dedup ties by indexer
	// in queue [7c]. C# cite: ProviderDefinition.Id at
	// src/NzbDrone.Core/ThingiProvider/ProviderDefinition.cs:9.
	ID() int

	// Name is the user-visible indexer label (e.g. "Newznab — DOGnzb").
	// Used in log lines and rejection messages. C# cite:
	// ProviderDefinition.Name at :11.
	Name() string

	// Priority is the persisted IndexerPriority used as a tiebreaker
	// for duplicate releases. Lower wins (C# IndexerDefinition.cs:14
	// — DefaultPriority=25, range 1..50; 1 is highest).
	Priority() int

	// Tags is the persisted Tags slice. The orchestrator restricts
	// dispatch to indexers that are either untagged or share at least
	// one tag with the criteria's Series. C# cite:
	// ReleaseSearchService.cs:527.
	Tags() []int

	// EnableRSS reports whether this indexer participates in RssSync.
	// C# cite: IndexerDefinition.EnableRss at :19.
	EnableRSS() bool

	// EnableAutomaticSearch reports whether automatic searches dispatch
	// to this indexer. C# cite: IndexerDefinition.EnableAutomaticSearch
	// at :20.
	EnableAutomaticSearch() bool

	// EnableInteractiveSearch reports whether user-invoked interactive
	// searches dispatch to this indexer. C# cite:
	// IndexerDefinition.EnableInteractiveSearch at :21.
	EnableInteractiveSearch() bool

	// FetchRecent issues the RSS-feed pull. Returns the upcasted slice
	// of *parser.ReleaseInfo or *parser.TorrentInfo (the C# `IList<ReleaseInfo>`
	// upcast equivalent — torrent-typed entries survive via
	// parser.AsTorrentInfo at the consumer). Mirrors
	// HttpIndexerBase.FetchRecent at :84-91.
	FetchRecent(ctx context.Context) ([]any, error)

	// FetchSearch dispatches a typed criteria value. Concrete indexers
	// route the criteria value through their package's request
	// generator. Mirrors HttpIndexerBase.Fetch(SearchCriteriaBase) at
	// :93-128.
	FetchSearch(ctx context.Context, criteria Criteria) ([]any, error)
}

// Criteria is the marker interface every typed search criteria from
// criteria.go satisfies via its String() method. Used as the
// FetchSearch parameter type so callers can pass any of
// SingleEpisodeCriteria / SeasonCriteria / DailyEpisodeCriteria /
// DailySeasonCriteria / AnimeEpisodeCriteria / AnimeSeasonCriteria /
// SpecialEpisodeCriteria without an explicit cast at the call site.
//
// The concrete indexer's FetchSearch then type-switches on the
// underlying value to pick the right request-generator entry point.
// Mirrors the C# `IndexerBase<T>.Fetch(SearchCriteriaBase)` virtual
// dispatch — each concrete Fetch overload checks `is
// SingleEpisodeSearchCriteria` etc.
type Criteria interface {
	fmt.Stringer
}

// Adapter is the function-backed Indexer implementation the wiring
// layer uses to bind a concrete indexer (newznab/torznab/torrentrss)
// without forcing this leaf package to import any of them.
//
// Field names end in `Value` / `Func` to dodge the
// "field-and-method-cannot-share-a-name" Go restriction — the value
// fields are exported so the wiring layer fills them positionally via
// struct literal, and the methods on *Adapter implement Indexer.
//
// Zero-value Adapter is unusable (FetchRecentFunc / FetchSearchFunc
// nil). Construct via struct literal, e.g.:
//
//	idx := &indexersearch.Adapter{
//	    IDValue:         row.ID,
//	    NameValue:       row.Name,
//	    PriorityValue:   row.Priority,
//	    TagsValue:       row.Tags,
//	    EnableRSSValue:  row.EnableRss != nil && *row.EnableRss,
//	    EnableAutomaticSearchValue: ...,
//	    FetchRecentFunc: func(ctx context.Context) ([]any, error) {
//	        rs, err := concrete.FetchRecent(ctx)
//	        return upcastReleaseInfo(rs), err
//	    },
//	    FetchSearchFunc: func(ctx context.Context, c indexersearch.Criteria) ([]any, error) {
//	        rs, err := concrete.FetchSearch(ctx, c)
//	        return upcastReleaseInfo(rs), err
//	    },
//	}
type Adapter struct {
	IDValue                      int
	NameValue                    string
	PriorityValue                int
	TagsValue                    []int
	EnableRSSValue               bool
	EnableAutomaticSearchValue   bool
	EnableInteractiveSearchValue bool
	FetchRecentFunc              func(ctx context.Context) ([]any, error)
	FetchSearchFunc              func(ctx context.Context, criteria Criteria) ([]any, error)
}

// ID implements Indexer.
func (a *Adapter) ID() int { return a.IDValue }

// Name implements Indexer.
func (a *Adapter) Name() string { return a.NameValue }

// Priority implements Indexer.
func (a *Adapter) Priority() int { return a.PriorityValue }

// Tags implements Indexer. Returns nil rather than the zero-len slice
// when TagsValue is empty so callers using `len(idx.Tags()) == 0` see
// the same shape regardless of construction.
func (a *Adapter) Tags() []int { return a.TagsValue }

// EnableRSS implements Indexer.
func (a *Adapter) EnableRSS() bool { return a.EnableRSSValue }

// EnableAutomaticSearch implements Indexer.
func (a *Adapter) EnableAutomaticSearch() bool { return a.EnableAutomaticSearchValue }

// EnableInteractiveSearch implements Indexer.
func (a *Adapter) EnableInteractiveSearch() bool { return a.EnableInteractiveSearchValue }

// FetchRecent implements Indexer. Panics if FetchRecentFunc is nil —
// a programming error in the wiring layer (a runtime indexer must
// always have a FetchRecent path even if EnableRSS is false; the
// Service consults EnableRSS before invoking).
func (a *Adapter) FetchRecent(ctx context.Context) ([]any, error) {
	if a.FetchRecentFunc == nil {
		return nil, fmt.Errorf("indexersearch: Adapter %q has nil FetchRecentFunc", a.NameValue)
	}
	return a.FetchRecentFunc(ctx)
}

// FetchSearch implements Indexer. Returns an error rather than panic
// when FetchSearchFunc is nil — search-incapable indexers (e.g. the
// generic torrentrss Provider that hardcodes SupportsSearch=false in
// C# at :15) can leave the func nil and the orchestrator routes
// around them.
func (a *Adapter) FetchSearch(ctx context.Context, criteria Criteria) ([]any, error) {
	if a.FetchSearchFunc == nil {
		return nil, fmt.Errorf("indexersearch: Adapter %q does not support search", a.NameValue)
	}
	return a.FetchSearchFunc(ctx, criteria)
}

// Compile-time interface check: Adapter must satisfy Indexer.
var _ Indexer = (*Adapter)(nil)
