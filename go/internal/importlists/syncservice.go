package importlists

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/importlistexclusions"
	"sonarr.tv/sonarr/internal/series"
)

// SyncService ports the C# `ImportListSyncService` at
// `src/NzbDrone.Core/ImportLists/ImportListSyncService.cs:19-342`.
//
// Slice 4.AP (sub-arc I4b of queue [I4]). The orchestrator drives
// the scheduled-poll fan-out:
//
//  1. Factory.AutomaticAddEnabled(filterBlocked=true) → providers.
//  2. For each provider: Fetch(ctx) → []ItemInfo (per-list).
//  3. Aggregate items across all lists.
//  4. Per-item ID translation via the optional IDTranslator
//     (IMDb / TMDb / AniList / MAL → TVDB) — when the translator
//     is nil and TvdbID is zero, the item is skipped.
//  5. Skip items whose TvdbID is in importlistexclusions.
//  6. Skip items whose TvdbID is already in the series library.
//  7. Dedup against the local accumulator.
//  8. Build [series.Series] rows + dispatch to SeriesAdder.
//
// Concurrency: per-list Fetch calls run sequentially, matching the
// C# `FetchAndParseImportListService` which iterates providers in
// a single loop. The Go port can lift this to a per-list goroutine
// pool in a follow-up if profiling shows the sync run is HTTP-bound;
// the current shape is the safe minimum that keeps test scaffolding
// tractable.
//
// Deferred to sub-arc I4c (and explicitly out-of-scope here):
//
//   - TryCleanLibrary + CleanLibrary at `ImportListSyncService.cs:
//     278-336`. Requires internal/importlistitems (not yet in
//     scope), config.ListSyncLevel + config.ListSyncTag getters
//     (not yet ported), seriesService.UpdateSeries.
//   - ImportListSyncCommand handler + ProviderDeletedEvent handler
//     at `ImportListSyncService.cs:266-276 + 338-341` — wires to
//     the commands/handlers + thingiprovider eventing stack;
//     deferred to I4c with the main.go wiring.
//
// Methods Sync / SyncList delegate to the same private
// processListItems helper. The ID translation, exclusion check,
// and existing-TvdbID gate happen per-item in the helper.
type SyncService struct {
	factory       *Factory
	exclusions    exclusionLister
	seriesLookup  seriesLookup
	seriesAdder   seriesAdder
	idTranslator  IDTranslator
	itemPersister itemPersister
	statusUpdater listStatusUpdater
	cleanConfig   cleanLibraryConfig
	itemLister    libraryItemLister
	seriesCleaner librarySeriesCleaner
	cleanGate     cleanStatusGate
	logger        *slog.Logger
}

// itemPersister persists one list's freshly-fetched candidate set into
// the ImportListItems table and reports how many stale rows were
// removed. Mirrors C# `IImportListItemService.SyncSeriesForList` at
// `FetchAndParseImportListService.cs:80`. Optional — when nil, no items
// are persisted (and therefore the clean-library trigger, which reads
// that table, can never fire). The concrete adapter maps ItemInfo →
// internal/importlistitems.Item, keeping this package free of that
// dependency.
type itemPersister interface {
	SyncSeriesForList(ctx context.Context, items []ItemInfo, listID int) (removed int, err error)
}

// listStatusUpdater records a per-list sync completion (LastInfoSync =
// now) plus whether that sync removed any items. Mirrors C#
// `IImportListStatusService.UpdateListSyncStatus` at
// `FetchAndParseImportListService.cs:81`. Optional — when nil, the
// per-list sync bookkeeping the clean-library gate reads is skipped.
// *importliststatus.Service satisfies this directly.
type listStatusUpdater interface {
	UpdateListSyncStatus(ctx context.Context, importListID int, removedItems bool) error
}

// exclusionLister is the narrow interface SyncService needs from
// *importlistexclusions.Service. The real Service satisfies this
// via its All method.
type exclusionLister interface {
	All(ctx context.Context) ([]importlistexclusions.Exclusion, error)
}

// seriesLookup is the narrow interface SyncService needs to know
// whether a TvdbID is already in the library. The C# code reads
// `_seriesService.AllSeriesTvdbIds()` once at sync start; the Go
// port matches by reading once + caching in a local map for the
// duration of a single sync pass.
type seriesLookup interface {
	AllTvdbIDs(ctx context.Context) ([]int, error)
}

// seriesAdder is the narrow interface SyncService needs to dispatch
// the constructed series batch to the series-add pipeline. Mirrors
// C# `IAddSeriesService.AddSeries(seriesToAdd, true)` at
// `ImportListSyncService.cs:261`.
//
// The boolean parameter (C# `ignoreErrors=true`) is hard-coded at
// the call-site: every series that succeeds gets added; per-row
// failures are logged but do not abort the batch. The Go port
// mirrors by accepting only the slice — concrete implementations
// MUST be tolerant of per-row failures.
type seriesAdder interface {
	AddSeries(ctx context.Context, batch []series.Series) error
}

// IDTranslator is the optional narrow interface SyncService calls
// to translate non-TVDB external identifiers (IMDb / TMDb / AniList
// / MyAnimeList) into a TVDB id. Mirrors the C#
// `ISearchForNewSeries.SearchForNewSeriesByImdbId / ByTmdbId /
// ByAniListId / ByMyAnimeListId` call chain at
// `ImportListSyncService.cs:151-208`.
//
// When the translator is nil, items whose TvdbID is zero are
// skipped with a debug log. Production wiring (queue [I4c] +
// metadata-source family port) supplies a real translator.
//
// Each method returns (tvdbID, title, ok) — when ok=false the
// caller treats the item as unresolvable for that ID kind and
// falls through to the next translation step (IMDb → TMDb →
// AniList → MAL). The Title return is used to update the
// per-item Title when the upstream metadata source has a
// canonical title.
type IDTranslator interface {
	ByImdb(ctx context.Context, imdbID string) (tvdbID int, title string, ok bool)
	ByTmdb(ctx context.Context, tmdbID int) (tvdbID int, title string, ok bool)
	ByAniList(ctx context.Context, aniListID int) (tvdbID int, title string, ok bool)
	ByMyAnimeList(ctx context.Context, malID int) (tvdbID int, title string, ok bool)
}

// SyncResult is returned by Sync / SyncList. Carries the per-pass
// stats so tests can pin behaviour + the v3 API can surface them
// in a future progress endpoint.
//
// AnyFailure mirrors the C# behaviour where partial-failure (one
// list's Fetch failed) does NOT abort the sync — the orchestrator
// processes whatever items the healthy lists produced. The Go port
// surfaces the flag so callers can choose to skip the cleanup pass
// (matching C# `TryCleanLibrary`'s gate on
// AllListsSuccessfulWithAPendingClean).
type SyncResult struct {
	// ItemsFetched is the total number of items returned across
	// all lists (pre-deduplication, pre-exclusion).
	ItemsFetched int

	// SeriesAdded is the count of rows dispatched to SeriesAdder.
	SeriesAdded int

	// SkippedExcluded is the count of items rejected by the
	// importlistexclusions filter.
	SkippedExcluded int

	// SkippedExisting is the count of items already in the series
	// library.
	SkippedExisting int

	// SkippedUnresolvedID is the count of items rejected because
	// their TvdbID was zero AND no IDTranslator translation
	// succeeded.
	SkippedUnresolvedID int

	// AnyFailure reports a partial-failure inside the sync — at
	// least one provider's Fetch returned an error. Mirrors C#
	// `ImportListFetchResult.AnyFailure` (aggregated across lists).
	AnyFailure bool
}

// NewSyncService wires the orchestrator. factory + exclusions +
// seriesLookup + seriesAdder are required; idTranslator + logger
// are optional. nil logger substitutes slog.Default.
func NewSyncService(
	factory *Factory,
	exclusions exclusionLister,
	seriesLookup seriesLookup,
	seriesAdder seriesAdder,
	idTranslator IDTranslator,
	logger *slog.Logger,
) (*SyncService, error) {
	if factory == nil {
		return nil, errors.New("importlists: NewSyncService: nil factory")
	}
	if exclusions == nil {
		return nil, errors.New("importlists: NewSyncService: nil exclusions")
	}
	if seriesLookup == nil {
		return nil, errors.New("importlists: NewSyncService: nil seriesLookup")
	}
	if seriesAdder == nil {
		return nil, errors.New("importlists: NewSyncService: nil seriesAdder")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SyncService{
		factory:      factory,
		exclusions:   exclusions,
		seriesLookup: seriesLookup,
		seriesAdder:  seriesAdder,
		idTranslator: idTranslator,
		logger:       logger,
	}, nil
}

// SetItemPersister wires the per-list ImportListItems persistence
// (queue [I4c]). Optional — assigned post-construction in main.go so the
// NewSyncService signature stays stable and the typed-nil-into-interface
// trap is avoided (only ever assign a non-nil concrete adapter).
func (s *SyncService) SetItemPersister(p itemPersister) { s.itemPersister = p }

// SetListStatusUpdater wires the per-list sync-status bookkeeping
// (queue [I4c]). Optional — same post-construction rationale as
// SetItemPersister. *importliststatus.Service satisfies the interface.
func (s *SyncService) SetListStatusUpdater(u listStatusUpdater) { s.statusUpdater = u }

// SetCleanLibrary wires the clean-library dependencies (queue [I4c] —
// C# TryCleanLibrary/CleanLibrary). All four are required together;
// passing any nil leaves the whole clean-library feature disabled (a
// no-op), which is also the default pre-wiring posture. Assigned
// post-construction in main.go — see SetItemPersister for the rationale.
func (s *SyncService) SetCleanLibrary(cfg cleanLibraryConfig, items libraryItemLister, series librarySeriesCleaner, gate cleanStatusGate) {
	s.cleanConfig = cfg
	s.itemLister = items
	s.seriesCleaner = series
	s.cleanGate = gate
}

// Sync ports C# `SyncAll` at `ImportListSyncService.cs:85-103`.
// Fans out AutomaticAddEnabled lists, processes items, dispatches
// to SeriesAdder. Returns a SyncResult with per-pass stats.
//
// Cleanup (C# TryCleanLibrary at `:278-289`) is deferred to
// sub-arc I4c — see the package-level doc-comment.
func (s *SyncService) Sync(ctx context.Context) (SyncResult, error) {
	providers, err := s.factory.AutomaticAddEnabled(ctx, true)
	if err != nil {
		return SyncResult{}, fmt.Errorf("importlists: sync: factory: %w", err)
	}
	if len(providers) == 0 {
		s.logger.Debug("importlists: sync: no automatic-add lists enabled")
		return SyncResult{}, nil
	}
	s.logger.Info("importlists: sync: starting", slog.Int("lists", len(providers)))
	result, err := s.fanOutAndProcess(ctx, providers)
	if err != nil {
		return result, err
	}
	// C# SyncAll calls TryCleanLibrary() last (ImportListSyncService.cs:102).
	s.tryCleanLibrary(ctx)
	return result, nil
}

// SyncList ports C# `SyncList(definition)` at
// `ImportListSyncService.cs:105-116`. Refreshes a single configured
// list by Id. Used by the v3 API "refresh this list now" endpoint.
//
// Returns an error when the list id does not match any persisted
// AutomaticAddEnabled row — mirrors the C# `_importListFactory.Get`
// throwing on missing id.
func (s *SyncService) SyncList(ctx context.Context, listID int) (SyncResult, error) {
	// Read every list (filterBlocked=false: even a currently-blocked
	// list may be the target of a manual refresh — the user is
	// explicitly asking to retry).
	providers, err := s.factory.All(ctx)
	if err != nil {
		return SyncResult{}, fmt.Errorf("importlists: syncList: factory: %w", err)
	}
	for _, p := range providers {
		if p.Definition() != nil && p.Definition().ID == listID {
			s.logger.Info("importlists: refresh", slog.Int("list_id", listID), slog.String("list_name", p.Definition().Name))
			result, err := s.fanOutAndProcess(ctx, []Provider{p})
			if err != nil {
				return result, err
			}
			// C# SyncList calls TryCleanLibrary() last (ImportListSyncService.cs:115).
			s.tryCleanLibrary(ctx)
			return result, nil
		}
	}
	return SyncResult{}, fmt.Errorf("importlists: syncList: list id %d not found", listID)
}

// fanOutAndProcess iterates the providers + collects per-list
// items into a single aggregate, then routes through
// processListItems. Per-list Fetch errors are swallowed (recorded
// in AnyFailure) so a single broken list doesn't abort the
// whole sync — mirrors C# `IFetchAndParseImportListService` which
// aggregates AnyFailure across the per-list calls.
func (s *SyncService) fanOutAndProcess(ctx context.Context, providers []Provider) (SyncResult, error) {
	var (
		aggregate  []ItemInfo
		anyFailure bool
	)
	for _, p := range providers {
		res, err := p.Fetch(ctx)
		if err != nil {
			anyFailure = true
			name := "<unknown>"
			if p.Definition() != nil {
				name = p.Definition().Name
			}
			s.logger.Warn("importlists: list fetch failed",
				slog.String("list", name),
				slog.Any("err", err))
			continue
		}
		if res.AnyFailure {
			anyFailure = true
		} else {
			// Clean fetch — persist this list's candidate set + record
			// the sync. Mirrors the C# gate at
			// `FetchAndParseImportListService.cs:76-82`: SyncSeriesForList
			// + UpdateListSyncStatus run ONLY for a list that fetched
			// without failure, so a transient error returning an empty
			// set can't wipe the list's persisted items and spuriously
			// trigger a clean.
			if def := p.Definition(); def != nil {
				s.persistList(ctx, def.ID, res.Series)
			}
		}
		aggregate = append(aggregate, res.Series...)
	}

	result, err := s.processListItems(ctx, aggregate, providers)
	if err != nil {
		return SyncResult{}, err
	}
	result.AnyFailure = anyFailure
	return result, nil
}

// persistList reconciles one cleanly-fetched list's candidate set into
// the ImportListItems table and records the sync outcome. Ports the C#
// per-list body at `FetchAndParseImportListService.cs:78-81`:
// SyncSeriesForList returns the removed count, then UpdateListSyncStatus
// stamps LastInfoSync (always, on a clean fetch) and OR-accumulates the
// removed>0 flag the clean-library gate consults.
//
// Best-effort: a persistence or status error is logged and swallowed so
// one list's DB hiccup doesn't abort the whole sync (matches the C#
// per-list try/catch). When itemPersister is nil the whole step is a
// no-op (pre-I4c-wiring posture). Status is updated only after a
// successful persist — mirroring the C# ordering where an exception in
// SyncSeriesForList skips UpdateListSyncStatus.
func (s *SyncService) persistList(ctx context.Context, listID int, items []ItemInfo) {
	if s.itemPersister == nil {
		return
	}
	removed, err := s.itemPersister.SyncSeriesForList(ctx, items, listID)
	if err != nil {
		s.logger.Warn("importlists: persist list items failed",
			slog.Int("list_id", listID), slog.Any("err", err))
		return
	}
	if s.statusUpdater != nil {
		if err := s.statusUpdater.UpdateListSyncStatus(ctx, listID, removed > 0); err != nil {
			s.logger.Warn("importlists: update list sync status failed",
				slog.Int("list_id", listID), slog.Any("err", err))
		}
	}
}

// processListItems ports C# `ProcessListItems` at
// `ImportListSyncService.cs:118-264`. Walks each item, applies the
// ID translation chain + exclusion filter + existing-series gate +
// cross-item dedup, builds the series batch, dispatches.
//
// providers is the full list of configured imports (needed to look
// up the per-item parent ImportListID's EnableAutomaticAdd +
// MonitorTypes + RootFolderPath + QualityProfileId + Tags etc).
func (s *SyncService) processListItems(ctx context.Context, items []ItemInfo, providers []Provider) (SyncResult, error) {
	var result SyncResult
	result.ItemsFetched = len(items)

	if len(items) == 0 {
		s.logger.Debug("importlists: sync: no list items to process")
		return result, nil
	}

	exclusions, err := s.exclusions.All(ctx)
	if err != nil {
		return result, fmt.Errorf("importlists: sync: load exclusions: %w", err)
	}
	excludedTvdb := make(map[int]struct{}, len(exclusions))
	for _, e := range exclusions {
		excludedTvdb[e.TvdbID] = struct{}{}
	}

	existingIDs, err := s.seriesLookup.AllTvdbIDs(ctx)
	if err != nil {
		return result, fmt.Errorf("importlists: sync: load existing: %w", err)
	}
	existingTvdb := make(map[int]struct{}, len(existingIDs))
	for _, id := range existingIDs {
		existingTvdb[id] = struct{}{}
	}

	// Per-listID lookup for fast access to the parent ImportList
	// definition during series-construction.
	listByID := make(map[int]*ImportList, len(providers))
	for _, p := range providers {
		if def := p.Definition(); def != nil {
			listByID[def.ID] = def
		}
	}

	batch := make([]series.Series, 0, len(items))
	batchTvdb := make(map[int]struct{}, len(items))

	for i := range items {
		item := items[i] // value copy so mutations stay local
		parent, ok := listByID[item.ImportListID]
		if !ok {
			// The owning list disappeared between Fetch and process —
			// mirrors C# `Single(...)` which would throw; the Go port
			// logs and skips to keep the rest of the batch alive.
			s.logger.Debug("importlists: orphan item — owning list not found",
				slog.Int("import_list_id", item.ImportListID),
				slog.String("title", item.Title))
			continue
		}

		// C# `if (!importList.EnableAutomaticAdd) continue;` at :145.
		// Should never fire here since Sync uses AutomaticAddEnabled,
		// but SyncList(listID) bypasses that filter so the guard
		// stays.
		if parent.EnableAutomaticAdd == nil || !*parent.EnableAutomaticAdd {
			continue
		}

		// ID translation chain — IMDB → TMDb → AniList → MAL.
		if item.TvdbID <= 0 {
			s.translateID(ctx, &item)
		}
		if item.TvdbID <= 0 {
			result.SkippedUnresolvedID++
			s.logger.Debug("importlists: rejected unresolvable id",
				slog.String("title", item.Title),
				slog.String("imdb", item.ImdbID),
				slog.Int("tmdb", item.TmdbID),
				slog.Int("anilist", item.AniListID),
				slog.Int("mal", item.MalID))
			continue
		}

		// Exclusion check.
		if _, excluded := excludedTvdb[item.TvdbID]; excluded {
			result.SkippedExcluded++
			s.logger.Debug("importlists: rejected by exclusion",
				slog.Int("tvdb", item.TvdbID),
				slog.String("title", item.Title))
			continue
		}

		// Existing-series check.
		if _, exists := existingTvdb[item.TvdbID]; exists {
			result.SkippedExisting++
			s.logger.Debug("importlists: rejected existing series",
				slog.Int("tvdb", item.TvdbID),
				slog.String("title", item.Title))
			continue
		}

		// Cross-item dedup.
		if _, dup := batchTvdb[item.TvdbID]; dup {
			continue
		}
		batchTvdb[item.TvdbID] = struct{}{}

		batch = append(batch, buildSeries(item, parent))
	}

	if len(batch) == 0 {
		s.logger.Info("importlists: sync complete — nothing to add",
			slog.Int("items_fetched", result.ItemsFetched))
		return result, nil
	}

	if err := s.seriesAdder.AddSeries(ctx, batch); err != nil {
		return result, fmt.Errorf("importlists: sync: add series: %w", err)
	}
	result.SeriesAdded = len(batch)
	s.logger.Info("importlists: sync complete",
		slog.Int("items_fetched", result.ItemsFetched),
		slog.Int("series_added", result.SeriesAdded),
		slog.Int("skipped_excluded", result.SkippedExcluded),
		slog.Int("skipped_existing", result.SkippedExisting),
		slog.Int("skipped_unresolved", result.SkippedUnresolvedID))
	return result, nil
}

// translateID walks the C# `:150-208` ID translation chain. The
// chain is short-circuiting — once a non-zero TvdbID is found, the
// remaining steps are skipped. Mutates the passed-in item.
//
// When idTranslator is nil, every step is a no-op (the chain
// returns without touching the item). Caller's responsibility to
// fall through to the SkippedUnresolvedID path.
func (s *SyncService) translateID(ctx context.Context, item *ItemInfo) {
	if s.idTranslator == nil {
		return
	}
	if item.ImdbID != "" {
		if tvdb, title, ok := s.idTranslator.ByImdb(ctx, item.ImdbID); ok {
			item.TvdbID = tvdb
			item.Title = title
		}
	}
	if item.TvdbID <= 0 && item.TmdbID > 0 {
		if tvdb, title, ok := s.idTranslator.ByTmdb(ctx, item.TmdbID); ok {
			item.TvdbID = tvdb
			item.Title = title
		}
	}
	if item.TvdbID <= 0 && item.AniListID > 0 {
		if tvdb, title, ok := s.idTranslator.ByAniList(ctx, item.AniListID); ok {
			item.TvdbID = tvdb
			item.Title = title
		}
	}
	if item.TvdbID <= 0 && item.MalID > 0 {
		if tvdb, title, ok := s.idTranslator.ByMyAnimeList(ctx, item.MalID); ok {
			item.TvdbID = tvdb
			item.Title = title
		}
	}
}

// buildSeries ports the C# `seriesToAdd.Add(new Series { ... })`
// block at `ImportListSyncService.cs:237-258`. The C# code creates
// a transient Series + AddSeriesOptions; the Go port builds the
// matching internal/series.Series with the runtime-only
// RootFolderPath field set so the add pipeline knows where to
// install the show.
//
// Monitored flag mirrors the C# `monitored = importList.ShouldMonitor
// != MonitorTypes.None` at :235. ShouldMonitor is persisted as int
// on the Go ImportList row; MonitorTypes.None ordinal is 12 per the
// C# enum at Tv/MonitoringOptions.cs:30. We treat any non-12 value
// as monitored=true.
//
// Year is *int on internal/series.Series; the Go port copies the
// item's Year only when non-zero, matching the C# default-zero
// behaviour where Year=0 means "unknown".
//
// Tags is propagated from the import list (the C# adds the list's
// tags to the new series for cross-reference / cleanup). AddOptions
// + Seasons + ListSyncLevel-tagged cleanup paths are NOT modelled
// here — series.Series does not yet have an AddOptions field; the
// downstream add pipeline (queue [I4c] — series-add wiring) will
// consume the SearchForMissingEpisodes hint from the parent list.
func buildSeries(item ItemInfo, list *ImportList) series.Series {
	monitored := list.ShouldMonitor != monitorTypeNoneOrdinal
	out := series.Series{
		TvdbID:           item.TvdbID,
		Title:            item.Title,
		Monitored:        monitored,
		SeasonFolder:     list.SeasonFolder,
		SeriesType:       list.SeriesType,
		MonitorNewItems:  list.MonitorNewItems,
		RootFolderPath:   list.RootFolderPath,
		Tags:             append([]int(nil), list.Tags...),
		QualityProfileID: list.QualityProfileID,
	}
	if item.Year > 0 {
		out.Year = item.Year
	}
	return out
}

// monitorTypeNoneOrdinal is the C# `MonitorTypes.None` enum value
// at `src/NzbDrone.Core/Tv/MonitoringOptions.cs:30` — used by
// [buildSeries] to derive the per-row monitored flag. Pinned as a
// constant rather than an imported enum because the MonitorType
// enum has not been promoted to its own Go type yet (per the
// foundation-types slice 4.AK note).
const monitorTypeNoneOrdinal = 12
