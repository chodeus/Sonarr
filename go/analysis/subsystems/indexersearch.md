# IndexerSearch

**Source:** `src/NzbDrone.Core/IndexerSearch/**`
**Target Go package:** `go/internal/indexersearch/`
**Status:** drafted

> Split-out card. The `http-instrumentation-misc.md` § 7 "IndexerSearch" section was the seed for this file. Numbering of `IS-Q*` continues from where that section stopped (`IS-Q4`); the porter should treat *this* card as canonical and ignore the section in `http-instrumentation-misc.md`.

## Purpose

`IndexerSearch` is the **search-orchestration entry point** that sits between user/scheduler-issued search commands and the lower-level `indexers` / `decisionengine` / `download` subsystems. There are five `IExecute<TCommand>` handlers, but they all funnel into the single `ISearchForReleases` interface (`ReleaseSearchService`), which:

1. Loads the relevant `Series` and `Episodes` from `tv`.
2. Translates TVDB-numbered episodes into release-numbered queries via `DataAugmentation.Scene` mappings (xem) and produces one or more `SearchCriteriaBase`-derived DTOs.
3. Picks the eligible indexer set (automatic vs interactive, tag-filtered) from `IIndexerFactory`.
4. Fans `indexer.Fetch(criteria)` out across all eligible indexers in parallel via `Task.WhenAll`.
5. Stamps `Episode.LastSearchTime = UtcNow` for every episode in the criteria batch (regardless of result).
6. Hands the merged `IList<ReleaseInfo>` off to `IMakeDownloadDecision.GetSearchDecision` (covered in `decisionengine.md`).
7. Deduplicates by release `Guid` (lowest rejection count, then highest indexer priority) and returns `List<DownloadDecision>`.

Caller services then pass the decisions to `IProcessDownloadDecisions.ProcessDecisions` (covered in `download.md`) which is what actually grabs to a download client and emits `History` rows. Nothing in `IndexerSearch` writes history itself; the search subsystem is "pure" except for the `LastSearchTime` write.

This card describes only the orchestration. The per-indexer `Fetch(criteria)` contract is in `indexers.md`; the per-decision spec pipeline is in `decisionengine.md`; the actual grab is in `download.md`.

## Public types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `EpisodeSearchCommand` | `IndexerSearch/EpisodeSearchCommand.cs:6` | command | `List<int> EpisodeIds`, `SendUpdatesToClient => true`. |
| `MissingEpisodeSearchCommand` | `IndexerSearch/MissingEpisodeSearchCommand.cs:5` | command | `int? SeriesId`, `bool Monitored = true`. |
| `CutoffUnmetEpisodeSearchCommand` | `IndexerSearch/CutoffUnmetEpisodeSearchCommand.cs:5` | command | Same shape as Missing. |
| `SeasonSearchCommand` | `IndexerSearch/SeasonSearchCommand.cs:5` | command | `int SeriesId, int SeasonNumber`. |
| `SeriesSearchCommand` | `IndexerSearch/SeriesSearchCommand.cs:5` | command | `int SeriesId`. |
| `EpisodeSearchService` | `IndexerSearch/EpisodeSearchService.cs:17-19` | service | `IExecute` of all three episode-list commands. |
| `SeasonSearchService` | `IndexerSearch/SeasonSearchService.cs:8` | service | `IExecute<SeasonSearchCommand>`. |
| `SeriesSearchService` | `IndexerSearch/SeriesSearchService.cs:12` | service | `IExecute<SeriesSearchCommand>`. |
| `ISearchForReleases` | `IndexerSearch/ReleaseSearchService.cs:20-26` | interface | Four overloads: `EpisodeSearch(int)`, `EpisodeSearch(Episode)`, two `SeasonSearch` shapes. |
| `ReleaseSearchService` | `IndexerSearch/ReleaseSearchService.cs:28-573` | service | The orchestrator. Owns scene mapping, dispatch, dedupe. |
| `EpisodeSearchGroup` | `IndexerSearch/EpisodeSearchGroup.cs:6-12` | DTO | Internal-only: `{ SeriesId, SeasonNumber, List<Episode> Episodes }`. |
| `SearchCriteriaBase` (and 7 concrete subclasses) | `IndexerSearch/Definitions/*.cs` | DTO hierarchy | Passed to `IIndexer.Fetch`. See § "Search criteria hierarchy". |
| `SceneEpisodeMapping` / `SceneSeasonMapping` | `IndexerSearch/Definitions/{Scene*Mapping}.cs` | internal | Intermediate scene-mapping records used during query expansion. |

## Public services

| Type | Methods | Side effects | Notes |
|------|---------|--------------|-------|
| `ISearchForReleases` | `EpisodeSearch(int episodeId, bool userInvoked, bool interactive)`<br>`EpisodeSearch(Episode, bool, bool)`<br>`SeasonSearch(int seriesId, int seasonNumber, bool missingOnly, bool monitoredOnly, bool userInvoked, bool interactive)`<br>`SeasonSearch(int seriesId, int seasonNumber, List<Episode> episodes, bool monitoredOnly, bool userInvoked, bool interactive)` | Writes `Episode.LastSearchTime` for every episode in `criteriaBase.Episodes` after a successful indexer fan-out (`ReleaseSearchService.cs:540-547`). No other writes. | The third overload re-loads `_episodeService.GetEpisodesBySeason` then optionally filters `!HasFile` (`ReleaseSearchService.cs:96-112`). |
| `EpisodeSearchService` | `Execute(EpisodeSearchCommand)`<br>`Execute(MissingEpisodeSearchCommand)`<br>`Execute(CutoffUnmetEpisodeSearchCommand)` | Calls `_releaseSearchService.*` then `_processDownloadDecisions.ProcessDecisions` (the latter is what writes `History` and pushes to the download client; see `download.md`). | All three handlers do `.GetAwaiter().GetResult()` on tasks because `IExecute<T>` is sync (see `system.md`). |
| `SeasonSearchService` | `Execute(SeasonSearchCommand)` | Same pattern. | `missingOnly: false, monitoredOnly: true` is hard-coded (`SeasonSearchService.cs:25`). |
| `SeriesSearchService` | `Execute(SeriesSearchCommand)` | Branches: see § Call graph. | Reads `series.QualityProfile.Value.UpgradeAllowed` and uses it to drive `missingOnly`. |

## Search criteria hierarchy (`Definitions/`)

`SearchCriteriaBase` (`Definitions/SearchCriteriaBase.cs:11-43`) defines:

- `Series Series`
- `List<string> SceneTitles` — raw scene titles to query indexers with.
- `List<Episode> Episodes` — episodes the criteria apply to (used post-search to stamp `LastSearchTime`).
- `SearchMode SearchMode` — comes from `DataAugmentation.Scene.SearchMode` (see DA cards): `Default`, `SearchTitle`, etc.
- `bool MonitoredEpisodesOnly`
- `bool UserInvokedSearch` — user clicked a button vs. scheduled.
- `bool InteractiveSearch` — the V3/V5 `/api/v3/release` controller (see `rest/inventory.md`) sets this true for the manual "find releases" UI.
- Computed: `AllSceneTitles` = SceneTitles ∪ CleanSceneTitles distinct; `CleanSceneTitles` = each title run through `GetCleanSceneTitle`.
- Static: `GetCleanSceneTitle(string)` (`:28-42`) — strips leading "the ", `'`/`'`/`'`/`` ` ``/`´`, replaces `&`→`and`, replaces non-word with `+`, collapses `++`, removes diacritics, trims `+`/space. This is the canonical search-token form used in URL query strings by Newznab/Torznab indexers.

Concrete subclasses (each overrides `ToString` to produce a logger-friendly tag):

| Type | File | Distinguishing fields | Used by |
|------|------|----------------------|---------|
| `SingleEpisodeSearchCriteria` | `Definitions/SingleEpisodeSearchCriteria.cs:3-12` | `int EpisodeNumber, int SeasonNumber` | non-anime / non-daily single-episode searches; also each entry of `SeasonSearch` when a scene mapping resolves to one episode. |
| `SeasonSearchCriteria` | `Definitions/SeasonSearchCriteria.cs:3-11` | `int SeasonNumber` | non-anime full-season pack searches. |
| `AnimeEpisodeSearchCriteria` | `Definitions/AnimeEpisodeSearchCriteria.cs:3-14` | `int AbsoluteEpisodeNumber, int EpisodeNumber, int SeasonNumber, bool IsSeasonSearch` | anime single-ep. `IsSeasonSearch` is `true` only when invoked from `SearchAnimeSeason` per-episode loop (`ReleaseSearchService.cs:426`). |
| `AnimeSeasonSearchCriteria` | `Definitions/AnimeSeasonSearchCriteria.cs:3-11` | `int SeasonNumber` | anime full-season. |
| `DailyEpisodeSearchCriteria` | `Definitions/DailyEpisodeSearchCriteria.cs:5-13` | `DateTime AirDate` | daily single-ep. `series.SeriesType == Daily` and `episode.AirDate` parsed via `Episode.AIR_DATE_FORMAT`. |
| `DailySeasonSearchCriteria` | `Definitions/DailySeasonSearchCriteria.cs:3-11` | `int Year` | daily season-as-year. Bulk year-grouped daily episodes. |
| `SpecialEpisodeSearchCriteria` | `Definitions/SpecialEpisodeSearchCriteria.cs:5-20` | `string[] EpisodeQueryTitles` | season 0 / specials. |
| `SceneEpisodeMapping` (internal) | `Definitions/SceneEpisodeMapping.cs:7-32` | `Episode, SearchMode, SceneTitles, SeasonNumber, EpisodeNumber, AbsoluteEpisodeNumber?` — overrides `Equals`/`GetHashCode` to key only `(SeasonNumber, EpisodeNumber, SearchMode)`. | output of `GetSceneEpisodeMappings`. |
| `SceneSeasonMapping` (internal) | `Definitions/SceneSeasonMapping.cs:7-32` | `Episodes, EpisodeMapping, SearchMode, SceneTitles, SeasonNumber` — overrides `Equals`/`GetHashCode` keyed on `(SeasonNumber, SearchMode)`. | output of `GetSceneSeasonMappings`. |

Notes on field semantics (cross-reference `parser.md` for how the year/scene/aliases get parsed back out of release titles):

- `SceneTitles` carries the **un-cleaned** titles. The cleaning happens lazily in `CleanSceneTitles` and the `+`-encoded form is what most indexers see in their URL `q=` parameter — see `indexers.md`.
- "Year" is **only** present on `DailySeasonSearchCriteria` (it is the broadcast year, used to bulk-search daily series like talk-shows). Standard series do not carry a year in the criteria; the year disambiguates a series at indexer level via the title (e.g. `Doctor Who 2005`).
- "Scene aliases" travel as additional entries in `SceneTitles` plus their `Clean` derivations. `ReleaseSearchService.Get<T>(...)` ensures `Series.Title` is always present in `SceneTitles` (`ReleaseSearchService.cs:478-481`).
- "Custom episode numbers" — a misnomer. Sonarr does not have user-entered custom numbers; what travels through is `episode.SceneSeasonNumber`, `episode.SceneEpisodeNumber`, `episode.SceneAbsoluteEpisodeNumber` populated from the xem mapping in `DataAugmentation`. These fall back to TVDB numbers when the xem mapping is null.

## Call graph

### Commands → services

1. **`EpisodeSearchCommand`** → `EpisodeSearchService.Execute(EpisodeSearchCommand)` (`EpisodeSearchService.cs:110-119`):
   - For each `episodeId` in payload: `await _releaseSearchService.EpisodeSearch(episodeId, manual?, false)` then `_processDownloadDecisions.ProcessDecisions(decisions)`.
   - **No batching** across episodes — one search per episode, sequentially. (Different from `SearchForBulkEpisodes`.)
   - `interactiveSearch = false` always (this is a non-interactive automatic re-search).

2. **`MissingEpisodeSearchCommand`** → `EpisodeSearchService.Execute(MissingEpisodeSearchCommand)` (`:121-161`):
   - If `SeriesId.HasValue`: `_episodeService.GetEpisodeBySeries(seriesId).Where(e => e.Monitored == monitored && !e.HasFile && e.AirDateUtc.HasValue && e.AirDateUtc < UtcNow)`.
   - Else: paged scan via `_episodeService.EpisodesWithoutFiles(pagingSpec, includeSpecials: true)` with page-size `1000000` and a filter expression that selects either `(Monitored && Series.Monitored)` or `(!Monitored || !Series.Monitored)` based on `message.Monitored` — note the second branch is `OR` not `AND`, so unmonitored search returns episodes whose series is monitored but episode is not, and vice versa.
   - Subtract anything currently queued: `GetQueuedEpisodeIds` (`:197-203`) flattens `_queueService.GetQueue()` and excludes those episode IDs (no consideration of `Status`/`FailedPending` here — different from `decisionengine.QueueSpecification`).
   - Hand off to `SearchForBulkEpisodes` (`:43-103`).

3. **`CutoffUnmetEpisodeSearchCommand`** → `EpisodeSearchService.Execute(CutoffUnmetEpisodeSearchCommand)` (`:163-195`):
   - Same shape as Missing but uses `_episodeCutoffService.EpisodesWhereCutoffUnmet(pagingSpec)` (page-size `100000`, ten times smaller than Missing — see `IS-Q5`).
   - Same queue subtraction.
   - Same `SearchForBulkEpisodes` handoff.

4. **`SeasonSearchCommand`** → `SeasonSearchService.Execute` (`SeasonSearchService.cs:23-29`):
   - Single call: `await _releaseSearchService.SeasonSearch(seriesId, seasonNumber, missingOnly: false, monitoredOnly: true, userInvokedSearch: manual?, interactiveSearch: false)`.
   - `missingOnly: false` ⇒ all episodes in season are searched, including those already on disk. This is so quality upgrades can be found.

5. **`SeriesSearchCommand`** → `SeriesSearchService.Execute` (`SeriesSearchService.cs:33-75`):
   - Reads `series.QualityProfile.Value` and `series.Seasons`.
   - Branch A — **No seasons monitored** (`series.Seasons.None(s => s.Monitored)`, line `:40`): pivot to per-episode search. `_episodeService.GetEpisodeBySeries(id).Where(e => e.Monitored && !e.HasFile && e.AirDateUtc.HasValue && e.AirDateUtc < UtcNow)`. For each, `_releaseSearchService.EpisodeSearch(episode, ...)`.
   - Branch B — **Any season monitored** (`:60`): for every monitored season, `_releaseSearchService.SeasonSearch(seriesId, seasonNumber, missingOnly: !profile.UpgradeAllowed, monitoredOnly: true, userInvokedSearch: manual?, interactiveSearch: false)`. Iteration is `OrderBy(s => s.SeasonNumber)` ascending. (See `IS-Q1` re semantics of `missingOnly = !UpgradeAllowed`.)
   - Logs `Series search completed. {0} reports downloaded.`

### `EpisodeSearchService.SearchForBulkEpisodes` (`:43-103`)

The bulk-handling code reused by both `Missing` and `CutoffUnmet`:

1. Group input `List<Episode>` by `(SeriesId, SeasonNumber)` into `EpisodeSearchGroup` entries (`:49-60`). The group is the unit of dispatch; if a season has multiple wanted episodes a single season search runs, otherwise a single-episode search runs.
2. **Stale-first ordering** (`:62`): `groups.OrderBy(g => g.Episodes.Min(e => e.LastSearchTime ?? DateTime.MinValue))`. Episodes never searched (`LastSearchTime == null`) sort first via `DateTime.MinValue`. Prevents starvation when the bulk queue is large and individual indexers throttle.
3. For each group:
   - `Count > 1` ⇒ `_releaseSearchService.SeasonSearch(seriesId, seasonNumber, episodes, monitoredOnly, userInvoked, false)` (the `episodes`-overload, which skips the `missingOnly`/`!HasFile` filter inside `ReleaseSearchService.SeasonSearch`).
   - `Count == 1` ⇒ `_releaseSearchService.EpisodeSearch(episode, userInvoked, false)`.
   - Either path is wrapped in `try/catch (Exception)` — exceptions are logged and the loop **continues** with the next group. A single bad season does not abort the bulk.
4. Each group's `decisions` list is fed into `_processDownloadDecisions.ProcessDecisions` immediately (one call per group, not one global call). This means `History`/grab events are emitted incrementally as the bulk progresses, which is what the UI progress indicator depends on.

### `ReleaseSearchService.EpisodeSearch(Episode)` (`:59-94`)

Branches by `series.SeriesType`:

- `Daily` (`:63-72`): require `episode.AirDate.IsNotNullOrWhiteSpace()`. Throws `SearchFailedException("Air date is missing")` otherwise. Calls `SearchDaily` which builds `DailyEpisodeSearchCriteria { AirDate }`.
- `Anime` (`:74-85`):
  - If `SeasonNumber == 0 && SceneAbsoluteEpisodeNumber == null && AbsoluteEpisodeNumber == null` → `SearchSpecial(series, [episode], false, ...)`. (Anime specials lacking absolute numbers fall back to title-query.)
  - Else → `SearchAnime(series, episode, false, ...)`.
- Standard (default branch, `:87-93`):
  - `SeasonNumber == 0` → `SearchSpecial`.
  - Else → `SearchSingle`.

### `ReleaseSearchService.SeasonSearch` (two overloads, `:96-161`)

The first overload (`:96-112`) loads episodes from the season then optionally filters `!HasFile` if `missingOnly`. If empty after filter, returns empty list. Otherwise delegates to the second overload (`:114-161`).

Second overload branches by `series.SeriesType`:
- `Anime` → `SearchAnimeSeason(series, episodes, ...)` (`:120`).
- `Daily` → `SearchDailySeason(series, episodes, ...)` (`:125`).
- Standard → `GetSceneSeasonMappings(series, episodes)` then per-mapping:
  - `mapping.SeasonNumber == 0` ⇒ `SearchSpecial(series, mapping.Episodes, ...)`.
  - `mapping.Episodes.Count == 1` ⇒ `SingleEpisodeSearchCriteria` with the mapped season/episode numbers.
  - else ⇒ `SeasonSearchCriteria` with the mapped season number.
- Final: `DeDupeDecisions`.

### Per-type search builders (all in `ReleaseSearchService.cs`)

| Method | Lines | Notes |
|--------|-------|-------|
| `SearchSingle` | `:323-340` | Iterates `GetSceneEpisodeMappings`; one `SingleEpisodeSearchCriteria` per mapping; dedupes. |
| `SearchDaily` | `:342-351` | Parses `episode.AirDate` with `CultureInfo.InvariantCulture` and `Episode.AIR_DATE_FORMAT`. |
| `SearchAnime` | `:353-366` | Sets `IsSeasonSearch` from caller. Falls back from scene→TVDB numbers via `??`. `AbsoluteEpisodeNumber` defaults to `0` if both scene-abs and abs are null (`:361`). |
| `SearchSpecial` | `:368-396` | Builds `EpisodeQueryTitles` as cartesian product `CleanSceneTitles × episode.Title` over episodes that pass the monitored-or-interactive gate; calls `Dispatch` once with the special criteria; **then** also calls `SearchSingle` for each non-skipped episode (so specials get both title-queries and `S00Exx` queries). |
| `SearchAnimeSeason` | `:398-430` | Filters episodes to `(interactive || !monitoredOnly || ep.Monitored)` and `(AirDateUtc.HasValue && AirDateUtc < UtcNow)`. Bulk-dispatches one `AnimeSeasonSearchCriteria` per scene-mapped `SeasonNumber`, then per-episode dispatches a `SearchAnime(...)` with `IsSeasonSearch: true`. |
| `SearchDailySeason` | `:432-461` | Filters episodes the same way (without the `< UtcNow` gate). Groups by `Year` parsed from `AirDate`. Year groups with `> 1` episode get one `DailySeasonSearchCriteria { Year }`; year groups with `==1` episode fall back to `SearchDaily`. |

### Scene mapping (the heart of the criteria builder)

Two collectors: `GetSceneEpisodeMappings(series, episode)` (`:208-234`) for single episodes and `GetSceneSeasonMappings(series, episodes)` (`:163-206`) for batches. Both call into the lower-level enumerator `GetSceneEpisodeMappings(series, episode, sceneMappings)` (`:236-321`).

The enumerator is the central translation between TVDB-numbered episodes and scene-numbered queries. It loops over scene mappings fetched from `_sceneMapping.FindByTvdbId(series.TvdbId)` (cross-reference: `DataAugmentation.Scene.ISceneMappingService` covered in `http-instrumentation-misc.md` § DA). For each mapping it decides:

- **Mapping kind** (`:242-252`): "release-season-mapped" (`sceneMapping.SceneSeasonNumber` set) translates **release** season numbers → TVDB; "episode-season-mapped" translates **TVDB** season numbers → release. The `ignoreSceneNumbering` flag is set for `SceneOrigin == "tvdb"` or `"unknown:tvdb"`, which causes the emitted mapping to use the TVDB episode number even when scene-numbered would have differed (`:283-293`).
- **Scope check** (`:254-272`): the mapping's `SeasonNumber` (or its `SceneSeasonNumber` for release-mapped) must match the current episode's season; mismatches `continue`.
- **Implied-global suppression** (`:274-278`): if any mapping has `SearchTerm == series.Title && FilterRegex.IsNullOrWhiteSpace()`, set `includeGlobal = false`. This avoids emitting a duplicate query for the series's own title when an explicit mapping already does.
- **SearchMode default** (`:281`): if the mapping has `SearchMode` set, use it; else `SearchTitle` if the mapping is release-season-mapped and the series's `CleanTitle` differs from `sceneMapping.SearchTerm.CleanSeriesTitle()`; else `SearchMode.Default`.

After the loop, an "implied global" mapping is appended (`:309-320`) using `series.Title` as the sole scene title — unless it was suppressed. This is **always** the last fallback mapping the indexers see.

Both wrapper methods then dedup-merge the results: for episode-mappings the dictionary key is `(SeasonNumber, EpisodeNumber, SearchMode)`; for season-mappings it is `(SeasonNumber, SearchMode)`. Where multiple xem entries collapse to the same key, their `SceneTitles` and `Episodes` are unioned. After the merge, `SceneTitles` is `Distinct(StringComparer.InvariantCultureIgnoreCase)` (`:202` and `:230`).

Quirk: the lookup grouping at `:170` uses `((SceneSeasonNumber ?? SeasonNumber) * 100000) + SeasonNumber` as a composite hash — assumes season numbers stay below 100000 (real-world fine, theoretically gross).

### Indexer fan-out — `Dispatch` (`:520-550`)

```
Dispatch(searchAction, criteriaBase):
  indexers = criteriaBase.InteractiveSearch
                ? IndexerFactory.InteractiveSearchEnabled()
                : IndexerFactory.AutomaticSearchEnabled()
  indexers = indexers.Where(i =>
                i.Definition.Tags.Empty()
             || i.Definition.Tags.Intersect(criteriaBase.Series.Tags).Any())
  tasks = indexers.Select(i => DispatchIndexer(searchAction, i, criteriaBase))
  batch = await Task.WhenAll(tasks)
  reports = batch.SelectMany(x => x).ToList()
  if (indexers.Any()):
    criteriaBase.Episodes.ForEach(e => e.LastSearchTime = UtcNow)
    _episodeService.UpdateLastSearchTime(criteriaBase.Episodes)
  return _makeDownloadDecision.GetSearchDecision(reports, criteriaBase).ToList()
```

- **Eligibility**: the boolean is on `IndexerDefinition` (`Indexers/IndexerFactory.cs:66-88`):
  - `EnableAutomaticSearch == true` for the default `AutomaticSearchEnabled` path.
  - `EnableInteractiveSearch == true` when `criteriaBase.InteractiveSearch == true` (manual search via REST controllers `Sonarr.Api.V3/Indexers/ReleaseController.cs:192,212`).
  - Both filter `FilterBlockedIndexers` by default — which strips out indexers in the 15-second-cached blocklist (cross-reference `decisionengine.md` BlockedIndexerSpecification).
- **Tag filter**: untagged indexers always match. Tagged indexers must share at least one tag with the series.
- **Concurrency**: unbounded `Task.WhenAll`. No semaphore. Per-indexer rate limits live inside `HttpIndexerBase` (cross-reference `indexers.md`). See `IS-Q2`.
- **`LastSearchTime` write** is unconditional on `indexers.Any()` — even if every indexer returned zero or threw, the timestamp is still updated. The Go port should preserve this (it's how the UI knows "we tried"). The write is a single batched UPDATE via `_episodeService.UpdateLastSearchTime`.
- **Per-indexer error handling — `DispatchIndexer` (`:552-563`)**: catches `Exception`, logs, returns `Array.Empty<ReleaseInfo>()`. Failures of one indexer never abort the whole search. There is no surface here for "this indexer is unhealthy" — that signal lives elsewhere via `IIndexerStatusService` (cross-reference `indexers.md`). See `IS-Q3`.

### Result deduplication — `DeDupeDecisions` (`:566-572`)

```
decisions
  .GroupBy(d => d.RemoteEpisode.Release.Guid)
  .Select(g => g.OrderBy(v => v.Rejections.Count())
                .ThenBy(v => v.RemoteEpisode?.Release?.IndexerPriority ?? IndexerDefinition.DefaultPriority)
                .First())
  .ToList()
```

- Key: `Release.Guid` (set by the indexer; for Newznab this is the GUID element of the RSS, for Torznab the InfoHash, etc. — see `indexers.md` and `parser.md` for how the parser pulls this through).
- Tie-breaking: minimum rejection count first, then minimum `IndexerPriority` (lower numeric priority is "higher").
- Note: dedupe is applied **per-search-call result**, not globally across an entire bulk `SearchForBulkEpisodes` run. Two season-search groups for *different seasons* can return the same `Guid` and both will reach `ProcessDecisions`. (`download.md`'s `QueueSpecification` re-checks against the active queue, so a true duplicate doesn't get grabbed twice — but the dedupe is not the safeguard.)

### Handoff to `download.md`

After `Execute` collects `decisions`, it calls `_processDownloadDecisions.ProcessDecisions(decisions)` (e.g. `EpisodeSearchService.cs:97,115` and `SeriesSearchService.cs:54,69`). That service (`Download/ProcessDownloadDecisions.cs:14-220`) is what:

- Re-runs the protocol-disabled / failed-pending bookkeeping per decision (`:48-110`).
- Calls `_downloadService.DownloadReport` which talks to the actual download client and then writes the `History` row of type `Grabbed` (or `DownloadFailed` if the client rejects the release) — see `download.md` and `system.md` for the events emitted (`EpisodeGrabbedEvent` is what triggers the History row, not anything in `IndexerSearch`).

So the IndexerSearch subsystem itself emits **no** `History` rows. The only DB write inside this subsystem is `Episode.LastSearchTime`. See `IS-Q6`.

## Triggers — who pushes these commands?

| Command | Trigger sources (file:line) | Idempotent? |
|---------|----------------------------|-------------|
| `EpisodeSearchCommand` | UI (manual "search this episode") via the command-bus REST endpoint; `EpisodeRefreshedService.Search` (`Tv/EpisodeRefreshedService.cs:47`) auto-fires for episodes that aired in the last 14 days when their `AbsoluteEpisodeNumber` was just added or they were freshly inserted; `RedownloadFailedDownloadService` (`Download/RedownloadFailedDownloadService.cs:56,79`) re-fires after a download client failure if the `RedownloadFailed` config is on. | No — every call dispatches another search and updates `LastSearchTime`. The natural rate limit is the indexer's own rate limit; the queue subtraction in `EpisodeSearchService.Execute(MissingEpisodeSearchCommand)` does NOT apply to plain `EpisodeSearchCommand`. |
| `MissingEpisodeSearchCommand` | UI ("search for missing"); `SeriesScannedHandler` (`Tv/SeriesScannedHandler.cs:63`) on series-add when `addOptions.SearchForMissingEpisodes && !addOptions.SearchForCutoffUnmetEpisodes`. | Idempotent in the sense that re-running picks up the same set; not idempotent against `LastSearchTime`. |
| `CutoffUnmetEpisodeSearchCommand` | UI ("search for cutoff unmet"); `SeriesScannedHandler` (`Tv/SeriesScannedHandler.cs:68`) on series-add when only the cutoff-unmet add-option is set. | Same. |
| `SeasonSearchCommand` | UI (per-season "search" button); `RedownloadFailedDownloadService` (`Download/RedownloadFailedDownloadService.cs:68`) when re-download failures span a whole season. | Same. |
| `SeriesSearchCommand` | UI (per-series "search all"); `SeriesScannedHandler` (`Tv/SeriesScannedHandler.cs:57`) on series-add when **both** `SearchForMissingEpisodes` and `SearchForCutoffUnmetEpisodes` are set (scanned-handler comment at `:51-53` explains: "this way multiple searches for the same season are skipped"). | Same. |

All five commands: `SendUpdatesToClient => true`, so the SignalR hub publishes progress events for the UI.

The REST `Sonarr.Api.V3/Indexers/ReleaseController.cs:192,212` calls `_releaseSearchService.EpisodeSearch / SeasonSearch` **directly**, bypassing the command-bus. This path is for the "interactive search" / "find releases" UI which shows decisions to the user and lets them grab one. It uses `userInvokedSearch: true, interactiveSearch: true`.

There is also `Sonarr.Api.V5/Release/ReleaseController.cs` — the V5 equivalent — which does the same (cross-reference `rest/inventory.md`).

## Anime / absolute-numbering handling

- The criterion type `AnimeEpisodeSearchCriteria` carries both `AbsoluteEpisodeNumber` and `(SeasonNumber, EpisodeNumber)` because anime indexers index by absolute number while Sonarr's library is TVDB-numbered.
- `SearchAnime` (`:353-366`) prefers scene-derived numbers via `??` fallback chain: `episode.SceneSeasonNumber ?? episode.SeasonNumber`, `episode.SceneEpisodeNumber ?? episode.EpisodeNumber`, `episode.SceneAbsoluteEpisodeNumber ?? episode.AbsoluteEpisodeNumber ?? 0`. The `0` fallback for absolute is a bug-shaped corner case — see `IS-Q7`.
- Special handling for season-zero anime without absolute numbers: pivot to `SearchSpecial` so the title-query path runs (`:80-82`).
- `SearchAnimeSeason` runs both a per-season-number season search **and** a per-episode anime search with `IsSeasonSearch: true` — the indexer can choose to return season packs for the season query and per-episode releases for the per-ep query.

## Missing vs CutoffUnmet — the real difference

Both commands look almost identical in shape. The substantive difference is which DB query they run to find candidate episodes:

- **Missing** (`EpisodeSearchService.cs:121-161`): `_episodeService.EpisodesWithoutFiles(pagingSpec, true)` — episodes with `EpisodeFileId == 0` / `HasFile == false`. The `true` is `includeSpecials`. Page size `1000000`.
- **CutoffUnmet** (`:163-195`): `_episodeCutoffService.EpisodesWhereCutoffUnmet(pagingSpec)` — episodes where `HasFile == true` but the existing file's `Quality` is below the series's profile cutoff and/or the `CustomFormatScore` is below `MinUpgradeFormatScore`. The actual SQL is built inside `EpisodeCutoffService` (cross-reference `tv.md` / `quality-profiles-formats-organizer.md` for the cutoff semantics). Page size `100000`.

Both then subtract queued episode IDs and call `SearchForBulkEpisodes` with the same `monitoredOnly` flag.

## Monitor/queue/has-file gating summary

The subsystem applies these filters at different layers:

| Filter | Where | Notes |
|--------|-------|-------|
| `Monitored` (series + episode) | `EpisodeSearchService.Execute(MissingEpisodeSearchCommand):129-152` and the same expression in `Execute(CutoffUnmet...)`:181-188 | XOR-style: when `monitored=true`, both episode and series must be monitored; when `monitored=false`, either may be unmonitored. |
| `HasFile == false` | `Missing` only (`:130`); `SeriesSearchService` Branch A (`:46`); `SeasonSearch` first-overload `missingOnly: true` (`:101-102`). | `CutoffUnmet` does the opposite (file exists but below cutoff). |
| `AirDateUtc.HasValue && AirDateUtc < UtcNow` | `Missing` series-scoped path (`:131`); `SeriesSearchService` Branch A (`:46`); `SearchAnimeSeason` (`:407-408`). | NOT applied in the paged `Missing` cross-series path — the `EpisodesWithoutFiles` query handles its own filtering. |
| Queue subtraction | `Missing` and `CutoffUnmet` only (`:157-158`, `:191-192`). | NOT applied to `EpisodeSearchCommand`, `SeasonSearchCommand`, or `SeriesSearchCommand`. See `IS-Q8`. |
| `MonitoredEpisodesOnly` (passed to indexer through criteria) | Set from the same `monitored` boolean. Used inside `SearchSpecial`, `SearchAnimeSeason`, `SearchDailySeason` for per-episode skip during query expansion. | Indexers themselves do not honor it directly; it's a hint for orchestration-side filtering. |

## DB tables touched

- **Read**: `Series` (via `SeriesService`), `Seasons` (embedded on `Series`), `Episodes` (`EpisodeService`), `EpisodeFiles` (via `EpisodeCutoffService`/`EpisodesWithoutFiles`), `Indexers` (via `IndexerFactory`/`IndexerStatusRepository`), `IndexerStatus` (blocklist via `FilterBlockedIndexers`).
- **Write**: `Episodes.LastSearchTime` after every dispatched search (`ReleaseSearchService.cs:545-546`, single batched UPDATE).

No other writes. (`History` rows are written by `download.md`'s `IDownloadService` after `ProcessDecisions` succeeds — not by this subsystem.)

## Events raised (IEventAggregator)

None. IndexerSearch is command-driven and uses synchronous service calls.

## Events consumed

None directly. The triggers listed above are *publishers* of commands, not events consumed by the search services themselves. (`EpisodeRefreshedService` and `SeriesScannedHandler` consume events from `tv.md` and push commands into the search subsystem; they live in `Tv/`.)

## Config keys

- None at this layer. The behavior toggles come from `IndexerDefinition.EnableAutomaticSearch`, `EnableInteractiveSearch`, `Tags`, `Priority` — read indirectly via `IIndexerFactory`. Cross-reference `indexers.md`.
- `series.QualityProfile.Value.UpgradeAllowed` is read at `SeriesSearchService.cs:38,68`. Cross-reference `quality-profiles-formats-organizer.md`.

## Jobs / commands (`IExecute<TCommand>`)

| Command | Trigger | Frequency | Idempotent? |
|---------|---------|-----------|-------------|
| `EpisodeSearchCommand` | UI / `EpisodeRefreshedService` / `RedownloadFailedDownloadService` | On-demand | No (always rewrites `LastSearchTime`, always dispatches). |
| `MissingEpisodeSearchCommand` | UI / `SeriesScannedHandler` | On-demand or scheduled by user | No. |
| `CutoffUnmetEpisodeSearchCommand` | UI / `SeriesScannedHandler` | On-demand or scheduled by user | No. |
| `SeasonSearchCommand` | UI / `RedownloadFailedDownloadService` | On-demand | No. |
| `SeriesSearchCommand` | UI / `SeriesScannedHandler` | On-demand | No. |

None of these are scheduled by the built-in scheduler; the user creates schedules via UI if desired (see `system.md` for command scheduling). All publish progress to SignalR (`SendUpdatesToClient => true`).

## External services

- None directly. Indexers are abstracted behind `IIndexer.Fetch(criteria)` (`indexers.md`).

## Notable quirks / TODOs / HACKs found in source

- **Sync-over-async**: `.GetAwaiter().GetResult()` everywhere in the `IExecute` paths (`EpisodeSearchService.cs:114,115,160,194`, `SeasonSearchService.cs:25,26`, `SeriesSearchService.cs:53,54,68,69`). Forced by the sync `IExecute<T>` contract — the Go port can keep these natively async (commands likely become `func(ctx) error`).
- **30-day stale-first ordering**: `SearchForBulkEpisodes` orders groups by `Episodes.Min(e.LastSearchTime ?? DateTime.MinValue)` (`EpisodeSearchService.cs:62`) so newly-failed episodes don't starve.
- **Composite-key hack**: `((SceneSeasonNumber ?? SeasonNumber) * 100000) + SeasonNumber` at `ReleaseSearchService.cs:170` assumes season numbers don't exceed 100000.
- **Cartesian-product specials**: `SearchSpecial.EpisodeQueryTitles` is `CleanSceneTitles × episode.Title` (`ReleaseSearchService.cs:374-379`). For shows with many specials this grows multiplicatively (`O(scene-titles × specials)`).
- **Dead code**: `EpisodeSearchService.IsMonitored(bool, bool)` at `:105-108` is private and never called.
- **Anime absolute-zero**: `SearchAnime` defaults `AbsoluteEpisodeNumber` to `0` if both scene-abs and abs are null (`ReleaseSearchService.cs:361`). `0` is then passed to indexers as if it were a real query.
- **`MonitoredEpisodesOnly` semantics drift**: When monitoring is `false` the paged-missing query uses `OR` (`v.Monitored == false || v.Series.Monitored == false`, `EpisodeSearchService.cs:151`) but the series-scoped path uses `e.Monitored == monitored` strict equality (`:129`) — different semantics for the same flag. See `IS-Q9`.
- **`EpisodesWithoutFiles` page size 1M vs `EpisodesWhereCutoffUnmet` page size 100k**: the missing query allows ten times more rows in a single call than cutoff-unmet (`EpisodeSearchService.cs:140` vs `:170`). No comment explains the difference. See `IS-Q5`.
- **Implicit "the" stripping** in `GetCleanSceneTitle` (`SearchCriteriaBase.cs:32`) — *only* a leading "the " (with trailing space) is stripped; titles like "The Office" become "Office" but "Theatrical" is unchanged.

## Test files (the port must keep passing equivalents)

- `src/NzbDrone.Core.Test/IndexerSearchTests/EpisodeSearchServiceFixture.cs`
- `src/NzbDrone.Core.Test/IndexerSearchTests/ReleaseSearchServiceFixture.cs`
- `src/NzbDrone.Core.Test/IndexerSearchTests/SeriesSearchServiceFixture.cs`
- `src/NzbDrone.Core.Test/IndexerSearchTests/Definitions/SearchCriteriaBaseFixture.cs`

## Cross-references

- `indexers.md` — `IIndexer.Fetch(SearchCriteriaBase)` contract; `IIndexerFactory.AutomaticSearchEnabled / InteractiveSearchEnabled`; per-indexer rate limit, blocklist, `IndexerStatusService`.
- `decisionengine.md` — `IMakeDownloadDecision.GetSearchDecision(reports, criteriaBase)` consumed at `ReleaseSearchService.cs:549`.
- `download.md` — `IProcessDownloadDecisions.ProcessDecisions` is the next step after every search; this is what writes `History`.
- `system.md` — `IExecute<TCommand>`, `Command`, `CommandTrigger`, `IManageCommandQueue`, `SendUpdatesToClient` SignalR plumbing.
- `parser.md` — how `ReleaseInfo.Title` is parsed back to `ParsedEpisodeInfo` in `DownloadDecisionMaker` (the consumer side of `Dispatch`).
- `quality-profiles-formats-organizer.md` — the `UpgradeAllowed` flag and the meaning of "cutoff unmet".
- `http-instrumentation-misc.md` § DA — `ISceneMappingService.FindByTvdbId` and `GetSceneNames` (xem mapping source data).

## Open questions

- **IS-Q1**: `SeriesSearchService.Execute` decides "no seasons monitored → per-episode search" vs "any season monitored → per-season search". `_releaseSearchService.SeasonSearch(missingOnly: !profile.UpgradeAllowed, monitoredOnly: true)` is called for the latter. Verify behaviour: when `UpgradeAllowed` is true, `missingOnly=false` means season-search returns *all* releases for that season — desired? (User's intent on series-search is usually "fill missing", not "upgrade everything".) — unresolved, blocks Phase 1.
- **IS-Q2**: `Task.WhenAll` over indexers without bounded concurrency — N indexers fire simultaneously. With rate limiting at the indexer level this is OK, but for N > 20 it can spike connection counts. Worth a bounded semaphore in the Go port? Cross-reference with `indexers.md` rate-limit policy. — unresolved, perf decision.
- **IS-Q3**: `DispatchIndexer` swallows all exceptions including transient HTTP failures. Should the Go port surface a per-indexer health-check from this path, or keep the silent log-and-continue? — unresolved, blocks indexer health design.
- **IS-Q4**: Dead code `EpisodeSearchService.IsMonitored(bool, bool)` (`EpisodeSearchService.cs:105-108`) — remove during port. — port-time cleanup.
- **IS-Q5**: `EpisodesWithoutFiles` page size 1,000,000 vs `EpisodesWhereCutoffUnmet` page size 100,000 (`EpisodeSearchService.cs:140` vs `:170`). Is the 10× difference deliberate, or accidental? Either both should be paged genuinely (fetch in chunks) or both should be a single-shot query — pick one in the Go port. — unresolved, port-time cleanup.
- **IS-Q6**: No `History` rows are emitted by IndexerSearch itself — only by `download.md` after a successful grab. There is no `History` type for "search ran and returned 0 useful results". For Sonarr v5 / Go, do we want a `SearchPerformed` history row so users can see *why* a wanted episode hasn't been grabbed? — design question, scoped to Phase 1+.
- **IS-Q7**: `SearchAnime` defaults `AbsoluteEpisodeNumber` to `0` when both scene-abs and abs are null (`ReleaseSearchService.cs:361`). Should the Go port skip the search instead, or carry an explicit `optional`? Is `abs=0` interpreted as "search for anything" or "search for absolute episode 0" by indexers? — unresolved.
- **IS-Q8**: Queue-subtraction (`!queue.Contains(e.Id)`) is applied only to `Missing`/`CutoffUnmet`, not to `EpisodeSearchCommand` / `SeasonSearchCommand` / `SeriesSearchCommand`. So manual searches will re-fetch indexers even for episodes already queued. Intended? — unresolved.
- **IS-Q9**: `MonitoredEpisodesOnly` semantics drift: paged-missing path uses `OR` between episode-monitored and series-monitored (`EpisodeSearchService.cs:151`), series-scoped path uses strict equality on episode-monitored only (`:129`). For `monitored=false` these can return different episode sets. Is this intended? — unresolved, behavioural correctness.
- **IS-Q10**: `DeDupeDecisions` runs **per `SearchSomething` call**, not over the full bulk. `SearchForBulkEpisodes` iterates groups and processes each group's decisions before the next. Multiple groups can return identical `Guid`s (e.g. a season pack covers two seasons) and feed duplicates to `ProcessDecisions`. The downstream `QueueSpecification` saves us, but it would be cheaper to dedupe globally. Is the current behaviour load-bearing? — unresolved, perf optimisation.
- **IS-Q11**: `LastSearchTime` is written even when *every* indexer threw. From the user's perspective this looks like "we tried" but in fact zero indexers actually returned data. Should the Go port distinguish "attempted" from "completed-with-no-results"? Likely a new `LastSearchSuccessful` bool or two timestamps. — unresolved, UX/observability.
- **IS-Q12**: `ReleaseSearchService.Get<TSpec>(Series, List<Episode>, ...)` (`:463-484`) appends `series.Title` to `SceneTitles` if not already present. The other two `Get<TSpec>` overloads (`:486-518`) do not — they trust the mapping's `SceneTitles` verbatim. Result: a single-episode standard search has whatever the scene-mapping gave it (which already includes the implied `series.Title` mapping at `:309-320`), but a daily / anime search re-appends `series.Title`. Is this asymmetry intentional? — unresolved.
