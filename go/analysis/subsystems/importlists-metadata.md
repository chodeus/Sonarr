# ImportLists, MetadataSource, DataAugmentation

**Source:**
- `src/NzbDrone.Core/ImportLists/**`
- `src/NzbDrone.Core/MetadataSource/**`
- `src/NzbDrone.Core/DataAugmentation/**`

**Target Go package:** `go/internal/importlists/`, `go/internal/metadatasource/`, `go/internal/dataaugmentation/`
**Status:** drafted

## Purpose

These three subsystems form the "external metadata" boundary of Sonarr. They share an HTTP-client / `ThingiProvider`-style architecture but answer different questions:

- **ImportLists** discover **which series the user wants tracked**. Each list provider periodically fetches a remote watchlist / collection / popular feed (Plex Watchlist, Trakt, Simkl, AniList, MyAnimeList, another Sonarr instance, RSS, an arbitrary HTTP endpoint), normalizes results to `ImportListItemInfo` (TVDB/IMDB/TMDB/MAL/AniList ID + title/year), maps non-TVDB IDs to TVDB via `ISearchForNewSeries`, filters against the exclusion list and existing library, then hands new entries to `IAddSeriesService`. Results are persisted in `ImportListItems` so library cleanup (unmonitor / tag) can detect series the user has removed from their list.
- **MetadataSource** answers "what is this TVDB ID?" and "what TVDB IDs match this query?". A single concrete provider, **SkyHook** (`https://skyhook.sonarr.tv/v1/tvdb/...`), implements both `IProvideSeriesInfo` (returns `Series` + episode list for an integer TVDB id) and `ISearchForNewSeries` (free-text search plus targeted `imdb:`, `tmdb:`, `mal:`, `anilist:`, `tvdb:` prefixed lookups). All series-add / refresh paths funnel through `IProvideSeriesInfo`; SkyHook is the upstream of upstreams.
- **DataAugmentation** layers **per-series corrections on top of SkyHook output**: alternate scene/title mappings (services.sonarr.tv `/scenemapping`), TVDB↔scene episode-number mappings from theXEM, and the daily-series flag (services.sonarr.tv `/dailyseries`). It does *not* fetch metadata itself — it tweaks already-loaded series/episodes so parser+search work for renames and the scene numbering scheme.

The three are tightly coupled at runtime: `SkyHookProxy` is constructed with `IDailySeriesService` and queries it during `MapSeries`; `XemService` implements `ISceneMappingProvider` and is one of the providers `SceneMappingService` aggregates; `ImportListSyncService` injects `ISearchForNewSeries` (i.e. SkyHook) to map non-TVDB list items to TVDB ids before adding.

## ImportList architecture

### Interface and base hierarchy

`IImportList : IProvider` (`ImportLists/IImportList.cs:6`) requires three things only:
- `ImportListType ListType { get; }`
- `TimeSpan MinRefreshInterval { get; }`
- `ImportListFetchResult Fetch()`

`ImportListFetchResult` (`ImportLists/ImportListBase.cs:14`) is `{ List<ImportListItemInfo> Series, bool AnyFailure }`.

Class hierarchy:

1. `ImportListBase<TSettings> : IImportList` (`ImportLists/ImportListBase.cs:31`) — abstract, ties the provider to its `TSettings : IImportListSettings, new()`. Owns `ConfigContract`, `Definition`, `Settings` (cast of `Definition.Settings`), `DefaultDefinitions` (single seed with `EnableAutomaticAdd = settings.Validate().IsValid`), `RequestAction` (default returns `null`; OAuth flows override), `CleanupListItems` (DistinctBy `{Title, TvdbId, ImdbId}` and stamps `ImportListId`/`ImportList`), and `Test()` boilerplate.
2. `HttpImportListBase<TSettings> : ImportListBase<TSettings>` (`ImportLists/HttpImportListBase.cs:20`) — abstract HTTP fetch loop. Defaults: `PageSize = 0` (paging off), `RateLimit = 2s`, `MaxNumResultsPerQuery = 1000`. Subclasses MUST implement:
   - `IImportListRequestGenerator GetRequestGenerator()`
   - `IParseImportListResponse GetParser()`
3. `RssImportBase<TSettings>` (`ImportLists/Rss/RssImportBase.cs:10`) — generic RSS feed with `RssImportBaseParser`, used by `PlexRssImport` and as a public extension hook (`ListType = Advanced`).
4. `TraktImportBase<TSettings>` / `SimklImportBase<TSettings>` / `AniListImportBase<TSettings>` — abstract `HttpImportListBase` subclasses adding shared OAuth token-refresh logic (see "Provider inventory").
5. `ImportListBase<TSettings>` direct subclasses that bypass the HTTP loop: `SonarrImport` and `CustomImport` use a hand-rolled proxy and own try/catch.

Auxiliary interfaces:
- `IImportListSettings : IProviderConfig` (`ImportLists/IImportListSettings.cs:5`) — only required field is `string BaseUrl { get; set; }`.
- `ImportListSettingsBase<TSettings>` (`ImportLists/ImportListSettingsBase.cs:7`) — `Equ.MemberwiseEqualityComparer` boilerplate so settings are value-comparable for `ProviderUpdatedEvent` deduplication.
- `IImportListRequestGenerator` (`ImportLists/IImportListRequestGenerator.cs:3`) — single `ImportListPageableRequestChain GetListItems()`.
- `IParseImportListResponse` (`ImportLists/IProcessImportListResponse.cs:6`) — single `IList<ImportListItemInfo> ParseResponse(ImportListResponse)`.
- `ImportListDefinition : ProviderDefinition` (`ImportLists/ImportListDefinition.cs:8`) — adds `EnableAutomaticAdd`, `SearchForMissingEpisodes`, `ShouldMonitor` (`MonitorTypes`), `MonitorNewItems` (`NewItemMonitorTypes`), `QualityProfileId`, `SeriesType`, `SeasonFolder`, `RootFolderPath`, `Tags` (inherited from `ProviderDefinition`), plus runtime-only `[MemberwiseEqualityIgnore]` `Status`/`ListType`/`MinRefreshInterval`. `Enable => EnableAutomaticAdd` overrides the base.

### Pageable request chain
`ImportListPageableRequestChain` (`ImportLists/ImportListPageableRequestChain.cs`) is a list-of-list-of-iterables (same shape as `IndexerPageableRequestChain`). Each tier is tried in order; the first tier producing any result wins (`HttpImportListBase.cs:89`). Within a tier, multiple `ImportListPageableRequest`s run sequentially; each iterates pages until `IsFullPage(page)` is false (i.e. fewer items than `PageSize`) or `MaxNumResultsPerQuery=1000` is reached. In practice every concrete provider only adds a single tier with a single pageable request — only Plex's `PlexListRequestGenerator` (`ImportLists/Plex/PlexListRequestGenerator.cs:32`) and AniList's custom `FetchItems` actually drive >1 page.

### Sync pipeline
1. **Trigger.** `ImportListSyncCommand` (`ImportLists/ImportListSyncCommand.cs`) is dispatched on schedule (jobs subsystem) or explicitly with a `DefinitionId` for a single list. `ImportListUpdatedHandler` (`ImportLists/ImportListUpdatedHandler.cs`) also pushes a single-list `ImportListSyncCommand` whenever a definition is added/updated.
2. **Dispatch.** `ImportListSyncService.Execute` (`ImportLists/ImportListSyncService.cs:266`) calls `SyncList(def)` or `SyncAll()`.
3. **Fetch.** `IFetchAndParseImportList` (`ImportLists/FetchAndParseImportListService.cs:32`) iterates `_importListFactory.AutomaticAddEnabled()`, skips lists whose `LastInfoSync + MinRefreshInterval > UtcNow`, then `Task.WhenAll`s `importList.Fetch()` for each. Per-list result rows are persisted via `_importListItemService.SyncSeriesForList` (which inserts/updates/deletes against existing rows for that list id and returns the count of removed rows). `_importListStatusService.UpdateListSyncStatus(id, removed > 0)` flips `HasRemovedItemSinceLastClean` if anything dropped off the list. After all tasks join, results are deduped by `{TvdbId, ImdbId, Title}`.
4. **Process.** `ImportListSyncService.ProcessListItems` (`ImportLists/ImportListSyncService.cs:118`) iterates the merged list. For each item with no TVDB id but with IMDB / TMDB / AniList / MAL ids, it calls `ISearchForNewSeries.SearchForNewSeriesByXxxId(...)` (i.e. SkyHook) and back-fills `TvdbId` + `Title`. Items still without a TVDB id are dropped. Then it filters out: rows in `ImportListExclusion`, rows already in `_seriesService.AllSeriesTvdbIds()`, and duplicates within the batch. Survivors are turned into `Series` shells and submitted to `IAddSeriesService.AddSeries(seriesToAdd, true)`.
5. **Library clean-up.** `TryCleanLibrary` (`ImportListSyncService.cs:278`) runs only when `ListSyncLevel != Disabled` AND every list has had at least one successful sync since startup AND at least one list has reported a removal since the last clean. It then walks all series and, for any series not present in any list (matched by TVDB / IMDB / TMDB / MAL / AniList id), applies one of: `LogOnly`, `KeepAndUnmonitor` (sets `Monitored = false`), `KeepAndTag` (adds `_configService.ListSyncTag` to `series.Tags`). After the pass, `MarkListsAsCleaned()` zeroes `HasRemovedItemSinceLastClean` on every list status. (`ListSyncLevelType` is in `ImportLists/ListSyncLevelType.cs`: `Disabled, LogOnly, KeepAndUnmonitor, KeepAndTag`.) Also handled on `ProviderDeletedEvent<IImportList>` via `ImportListSyncService.HandleAsync`.

### Persistence layer
- `IImportListRepository` / `ImportListRepository` (`ImportLists/ImportListRepository.cs`) — extends `ProviderRepository<ImportListDefinition>`, adds `UpdateSettings(definition)` so OAuth refresh can persist new tokens without rewriting the whole row.
- `IImportListStatusRepository` / `ImportListStatusService` (`ImportLists/ImportListStatusService.cs`) — extends `ProviderStatusServiceBase<IImportList, ImportListStatus>`. `ImportListStatus` adds `LastInfoSync` and `HasRemovedItemSinceLastClean` over the base failure-tracking fields. `RecordFailure` and `RecordConnectionFailure` are inherited from the base class and produce a `DisabledTill` window that `ImportListFactory.FilterBlockedImportLists` then honors (`ImportLists/ImportListFactory.cs:58`).
- `IImportListItemRepository` / `ImportListItemService` (`ImportLists/ImportListItems/`) — owns the `ImportListItems` table. `SyncSeriesForList` does an upsert keyed by any-of-{TvdbId, ImdbId, TmdbId, MalId, AniListId} (`ImportListItemService.cs:80`). `HandleAsync(ProviderDeletedEvent<IImportList>)` purges the rows when a list is deleted.
- `IImportListExclusionRepository` / `ImportListExclusionService` (`ImportLists/Exclusions/`) — table is `ImportListExclusions`, model is `{ TvdbId, Title }` (`ImportListExclusion.cs`). `HandleAsync(SeriesDeletedEvent)` auto-creates an exclusion when the user deletes a series with the "add list exclusion" checkbox set (`ImportListExclusionService.cs:70`).
- `ImportListItemInfo` (the in-memory transport model, *not* the saved row — they share the same class) lives at `Parser/Model/ImportListItemInfo.cs`. Fields: `ImportListId, ImportList, Title, Year, TvdbId, TmdbId, ImdbId, MalId, AniListId, ReleaseDate, List<Season> Seasons`.

## ImportList provider inventory

| Provider | Class | Base | List type enum | Transport / Endpoint | Auth | Refresh interval | Notes |
|----------|-------|------|----------------|----------------------|------|------------------|-------|
| Plex Watchlist | `PlexImport` | `HttpImportListBase<PlexListSettings>` | `Plex` | Discovers via `IPlexTvService.GetWatchlist(token,size,offset)` (paged 100, max 10 pages) | Plex.tv OAuth (Sonarr-side `RequestAction` for `startOAuth`/`continueOAuth`/`pollOAuth`/`getOAuthToken`) | 6h | `ImportLists/Plex/PlexImport.cs`. RateLimit 5s. Settings hold only `AccessToken`+OAuth token. |
| Plex Watchlist (RSS) | `PlexRssImport` | `RssImportBase<PlexRssImportSettings>` | `Plex` | User-supplied RSS URL from `https://app.plex.tv/desktop/#!/settings/watchlist` | Anonymous (signed URL) | 6h | `ImportLists/Rss/Plex/PlexRssImport.cs`. Custom `PlexRssImportParser` filters category=show and parses `imdb://`/`tvdb://`/`tmdb://` guids. |
| Trakt User List | `TraktUserImport` | `TraktImportBase<TraktUserSettings>` | `Trakt` | `https://api.trakt.tv/users/{user}/{watchlist|watched|collection}/shows[/sorting]` | Trakt OAuth via servarr.com proxy (client_id `d44ba57c…`, redirect `https://auth.servarr.com/v1/trakt_sonarr/auth`, renew `…/renew`) | 12h | Pick of `UserWatchList | UserWatchedList | UserCollectionList`; sort options Rank/Added/Title/Released. Limit default 100 (1–100). |
| Trakt Popular | `TraktPopularImport` | `TraktImportBase<TraktPopularSettings>` | `Trakt` | `/shows/{trending|popular|anticipated|watched/{weekly|monthly|all}|recommended/{weekly|monthly|all}}` | Same Trakt OAuth | 12h | 11 list types in `TraktPopularListType`; `TopWatchedByYear` and `RecommendedByYear` are `[Obsolete]` and rejected by validator. Filters: years, genres, ratings, free-form `TraktAdditionalParameters`. |
| Trakt List | `TraktListImport` | `TraktImportBase<TraktListSettings>` | `Trakt` | `/users/{user}/lists/{list-slug}/items/show,season,episode?limit=N` | Same Trakt OAuth | 12h | Captures show/season/episode-typed list items. |
| Simkl User | `SimklUserImport` | `SimklImportBase<SimklUserSettings>` | `Simkl` | `https://api.simkl.com/sync/all-items/{shows|anime}/{watching|plantowatch|hold|completed|dropped}` | Simkl OAuth via servarr.com proxy (client_id `3281c139…`) | 6h | Pre-checks `/sync/activities` `LastActivity`; if no activity since `LastInfoSync − 2h`, returns empty result with `AnyFailure=true` so library-clean stays inhibited (`SimklImportBase.cs:50`). |
| AniList User List | `AniListImport` | `AniListImportBase<AniListSettings>` | `Other` | GraphQL POST to `https://graphql.anilist.co` with paged `Page(page:)/mediaList(userName:type:ANIME,status_in:)` query (`ImportLists/AniList/AniListAPI.cs:18`) | AniList OAuth via servarr.com proxy (client_id `13780`) | 12h | `AniListImport` overrides `FetchItems` because AniList caps each page at 50 items and requires server-driven pagination via `pageInfo.hasNextPage`. Status filters: ImportCurrent/Planning/Completed/Dropped/Paused/Repeating + media-status filters Releasing/Finished/Unreleased/Cancelled/Hiatus. |
| MyAnimeList | `MyAnimeListImport` | `HttpImportListBase<MyAnimeListSettings>` | `Other` | `https://api.myanimelist.net/v2/users/@me/animelist?fields=list_status&limit=1000[&status=]` | MAL OAuth via servarr.com proxy (`oauth/myanimelist/{authorize|auth|renew}`) | 6h | Status enum: All/Watching/Completed/OnHold/Dropped/PlanToWatch (`MyAnimeListStatus.cs`). Optional `MinimumScore` 0–10. |
| Sonarr (another instance) | `SonarrImport` | `ImportListBase<SonarrSettings>` (no HTTP base) | `Program` | `GET /api/v3/series` + ancillary `qualityprofile` / `languageprofile` / `tag` / `rootfolder` via `SonarrV3Proxy` | API key in `X-Api-Key` header | 5m | Filters by ProfileIds, LanguageProfileIds, TagIds, RootFolderPaths sets. `SyncSeasonMonitoring` flag copies per-season `Monitored`. `RequestAction` exposes `getProfiles`/`getLanguageProfiles`/`getTags`/`getRootFolders` to populate the UI selects. |
| Custom List | `CustomImport` | `ImportListBase<CustomSettings>` | `Advanced` | `GET <BaseUrl>` returning `[{ Title, TvdbId, TmdbId, ImdbId }, …]` JSON | None | 6h | Catch-all bring-your-own-endpoint. `CustomSeries` resource is `Custom/CustomAPIResource.cs`. |

Total concrete import-list providers: **10** (Plex Watchlist, Plex RSS, Trakt User, Trakt Popular, Trakt List, Simkl User, AniList, MyAnimeList, Sonarr, Custom). Note `RssImportBase` is published as a base type but only `PlexRssImport` ships as a concrete RSS provider — the generic `RssImportBase` is not directly registered.

## MetadataSource architecture

### Interfaces
- `IProvideSeriesInfo` (`MetadataSource/IProvideSeriesInfo.cs:7`) — `Tuple<Series, List<Episode>> GetSeriesInfo(int tvdbSeriesId)`. Used by `AddSeriesService`, `RefreshSeriesService`, `RefreshEpisodeService`.
- `ISearchForNewSeries` (`MetadataSource/ISearchForNewSeries.cs:6`) — `SearchForNewSeries(string title)` plus four typed lookups: `SearchForNewSeriesByImdbId(string)`, `SearchForNewSeriesByAniListId(int)`, `SearchForNewSeriesByTmdbId(int)`, `SearchForNewSeriesByMyAnimeListId(int malId)`. Used by the UI add-series page and by `ImportListSyncService.ProcessListItems` for ID translation.
- `SearchSeriesComparer` (`MetadataSource/SearchSeriesComparer.cs`) — `IComparer<Series>` that ranks search results by exact-match → article-stripped match → Levenshtein ≤1 → year-aware Levenshtein → prefix `Title:` match → fallback weighted Levenshtein with year proximity bonus. Consumed at the API layer; not by SkyHook itself.

### SkyHook (the only implementation)
`SkyHookProxy` (`MetadataSource/SkyHook/SkyHookProxy.cs`) implements both interfaces.

- **Endpoint base.** `SkyHookTvdb = new HttpRequestBuilder("https://skyhook.sonarr.tv/v1/tvdb/{route}/{language}/").SetSegment("language", "en").CreateFactory()` (`NzbDrone.Common/Cloud/SonarrCloudRequestBuilder.cs:18`). Language is hardcoded `en`.
- **`GetSeriesInfo(tvdbId)`.** `GET /v1/tvdb/shows/en/{id}/`, deserialized to `ShowResource` (`MetadataSource/SkyHook/Resource/ShowResource.cs`). On `404 → SeriesNotFoundException`; other HTTP errors rethrown as `HttpException`. Mapping (`SkyHookProxy.MapSeries`, line 182):
  - Copies `TvdbId`, `TvRageId`, `TvMazeId`, `TmdbId`, `ImdbId`, `MalIds` (HashSet), `AniListIds` (HashSet).
  - Computes `CleanTitle = Parser.Parser.CleanSeriesTitle(Title)`, `SortTitle = SeriesTitleNormalizer.Normalize(...)`.
  - `OriginalLanguage = IsoLanguages.Find(show.OriginalLanguage)?.Language ?? Language.English`.
  - Parses `FirstAired`/`LastAired` with `yyyy-MM-dd`, AssumeUniversal+AdjustToUniversal.
  - `AirTime` is `HH:mm` from the upstream `TimeOfDayResource`.
  - `Status` mapped: `ended → Ended`, `upcoming → Upcoming`, anything else → `Continuing`.
  - **DataAugmentation hook:** `if (_dailySeriesService.IsDailySeries(series.TvdbId)) series.SeriesType = SeriesTypes.Daily;` (line 249).
  - `Seasons` are mapped with `Monitored = SeasonNumber > 0` (i.e. specials default-unmonitored).
  - `Monitored = true` set unconditionally on the returned Series.
- **`SearchForNewSeries(title)`.** Pre-validates the term against `IsPathValid(AnyOs)` to reject filesystem-injection attempts (`InvalidSearchTermException`, `MetadataSource/SkyHook/InvalidSearchTermException.cs`). Recognises a `tvdb:` / `tvdbid:` prefix and short-circuits to either `_seriesService.FindByTvdbId(...)` (already in library — return as-is) or `GetSeriesInfo(...)` (return single mapped result). Otherwise: `GET /v1/tvdb/search/en/?term={lower-trimmed-title}` returning `List<ShowResource>`. Each result is mapped through `MapSearchResult`, which prefers an existing local `Series` when the TVDB id is already in the library. Errors wrapped as `SkyHookException` (HTTP 503).
- **Typed-id helpers.** All four (`SearchForNewSeriesByImdbId`, `…ByTmdbId`, `…ByMyAnimeListId`, `…ByAniListId`) just delegate to `SearchForNewSeries($"{prefix}:{id}")`, with prefixes `imdb`, `tmdb`, `mal`, `anilist`. (Yes — these prefixes and the `tvdb`/`tvdbid` short-circuit are case-sensitive; the title is `ToLowerInvariant`'d before checking, so the prefix paths require lowercase.)
- **No client-side caching** of SkyHook responses. There is no `ICached*` field on `SkyHookProxy`. No application-side rate-limit either; relies on the shared `IHttpClient` honoring server-supplied `Retry-After` and on caller-side throttling (e.g. `RefreshSeriesService` runs on a serial command). Caching that *does* exist downstream is the local DB row plus media-cover blob cache — see `tv.md` and `mediafiles.md`.
- **Resources.** `ShowResource`, `EpisodeResource`, `SeasonResource`, `ActorResource`, `ImageResource`, `RatingResource`, `TimeOfDayResource` (`MetadataSource/SkyHook/Resource/`).

### Exceptions
- `SkyHookException : NzbDroneClientException` (HTTP 503). Wraps any HTTP/network error from the proxy (`MetadataSource/SkyHook/SkyHookException.cs`).
- `InvalidSearchTermException : NzbDroneClientException` (HTTP 400). Thrown when the search term contains path-like characters.

## DataAugmentation

Three independent fixers, all driven by Servarr-hosted feeds.

### Scene mappings
- **Model.** `SceneMapping` (`DataAugmentation/Scene/SceneMapping.cs`): `MappingId, Title, ParseTerm, SearchTerm ([JsonProperty("searchTitle")]), TvdbId, SeasonNumber ([JsonProperty("season")]), SceneSeasonNumber, SceneOrigin, SearchMode?, Comment, FilterRegex, Type`. Persisted in `SceneMappings` table by `SceneMappingRepository`.
- **Providers.** Aggregator `SceneMappingService` walks every `ISceneMappingProvider` (`DataAugmentation/Scene/ISceneMappingProvider.cs`) and unions their results. There are **two** registered providers:
  - `ServicesProvider` (`DataAugmentation/Scene/ServicesProvider.cs`) — calls `ISceneMappingProxy.Fetch()` (`SceneMappingProxy`), which is `GET https://services.sonarr.tv/v1/scenemapping`. This is the canonical alternate-title list.
  - `XemService` (`DataAugmentation/Xem/XemService.cs`, also acts as a `SceneMappingProvider` via `GetSceneMappings`) — pulls TVDB↔scene title aliases from theXEM `/allNames?seasonNumbers=true`.
- **Update flow.** `SceneMappingService.UpdateMappings()` (`SceneMappingService.cs:132`) iterates providers, validates each mapping (drops rows missing `Title`/`SearchTerm`/`MappingId`, warns), assigns `ParseTerm = Title.CleanSeriesTitle()` and `Type = providerType`. Then it diffs against existing rows of the same `Type` keyed by `MappingId`: insert new, update touched, delete missing. Triggered by `UpdateSceneMappingCommand` on a schedule, by the `SeriesRefreshStartingEvent` (manual trigger only, with 1-min cache TTL), and once each on first `SeriesAddedEvent`/`SeriesImportedEvent` after start-up. Publishes `SceneMappingsUpdatedEvent` after a successful run.
- **Read API.** `ISceneMappingService` (`SceneMappingService.cs:15`) exposes:
  - `List<string> GetSceneNames(tvdbId, seasonNumbers, sceneSeasonNumbers)` — alt search terms for indexer searches (used by IndexerSearch).
  - `int? FindTvdbId(sceneTitle, releaseTitle, sceneSeasonNumber)` — parser-side reverse lookup.
  - `List<SceneMapping> FindByTvdbId(int)`.
  - `SceneMapping FindSceneMapping(...)` / `int? GetSceneSeasonNumber(...)`.
- **Caches.** Two `ICachedDictionary<List<SceneMapping>>` instances keyed by parse-term and tvdb-id, refreshed from the repo on first miss or on update. `FilterSceneMappings(...)` applies `FilterRegex` (released title regex must match) and prefers `SceneSeasonNumber`-bound mappings when available. Title disambiguation between multiple TVDB-id matches uses Levenshtein on the original title (line 249).
- **Throws.** `InvalidSceneMappingException` if multiple distinct TVDB-ids match a single scene title with no way to disambiguate.

### theXEM (scene episode-number mappings)
- **Endpoint.** `https://thexem.info/map/` with `?origin=tvdb` suffix (`DataAugmentation/Xem/XemProxy.cs:22`). Three calls: `/havemap` (returns the list of TVDB ids that have a mapping), `/all?id={tvdb}` (returns `XemSceneTvdbMapping[]`), `/allNames?seasonNumbers=true` (returns alt-title rows feeding the SceneMapping pipeline above).
- **Service.** `XemService` (`DataAugmentation/Xem/XemService.cs`) handles `SeriesUpdatedEvent` and `SeriesRefreshStartingEvent`; for series whose TVDB id is in the cached `havemap` set OR which already has `UseSceneNumbering = true`, it calls `PerformUpdate(series)` which:
  - Resets all episodes' `SceneAbsoluteEpisodeNumber/SceneSeasonNumber/SceneEpisodeNumber/UnverifiedSceneNumbering`.
  - For every `XemSceneTvdbMapping`, finds the matching episode by `(SeasonNumber, EpisodeNumber)` and copies scene numbers across.
  - Skips bogus mappings where `Scene.{Absolute,Season,Episode}` are all 0.
  - `ExtrapolateMappings(...)` (line 100) flags additional episodes as `UnverifiedSceneNumbering` when only part of a season is mapped, and projects scene numbers forward when the latest TVDB season is past the last fully mapped scene season.
  - Writes back via `_episodeService.UpdateEpisodes(episodes)` and updates `series.UseSceneNumbering = mappings.Any()`.
- **Cache.** `ICachedDictionary<bool> _cache` of "this TVDB id has an XEM mapping". Populated by `UpdateXemSeriesIds()`, refreshed when older than 3h on `SeriesUpdatedEvent` or 1m on manual `SeriesRefreshStartingEvent`. On any failure, the proxy increments the cache TTL via `ExtendTTL()` so missing-scene-numbering doesn't oscillate.
- **Quirks.** Hard-coded special-case for `series.Key == 79151` (Fate/Zero) skipping seasons > 1 in the alt-name parser (`XemProxy.cs:97`). `IgnoredErrors = ["no single connection", "no show with the tvdb_id"]` are silently swallowed (`XemProxy.cs:27`).

### Daily series flag
- **Endpoint.** `GET https://services.sonarr.tv/v1/dailyseries` returning `[{ "tvdbId": int }, …]` (`DataAugmentation/DailySeries/DailySeriesDataProxy.cs:30`).
- **Service.** `DailySeriesService.IsDailySeries(tvdbid)` (`DataAugmentation/DailySeries/DailySeriesService.cs`) — caches the entire id list under key `"all"` for **1 hour**. Failure-tolerant: empty array on exception (logged as warning).
- **Consumer.** Only `SkyHookProxy.MapSeries` calls it, to set `SeriesTypes.Daily`. (`MetadataSource/SkyHook/SkyHookProxy.cs:249`.)

### Other Servarr-hosted lookups (out of subsystem but adjacent)
- `https://services.sonarr.tv/v1/scenemapping` — see above.
- `https://services.sonarr.tv/v1/dailyseries` — see above.
- The trakt/anilist/simkl/MAL OAuth proxies (`https://auth.servarr.com/v1/<provider>_sonarr/{auth,renew}`) are *not* part of DataAugmentation — they are part of each ImportList provider's OAuth flow.

## Settings persistence

All ImportList settings are persisted under `ImportListDefinition.Settings` (JSON column on the `ImportLists` table — see `datastore.md` / embedded-docs catalogue). The provider framework (`ProviderRepository<TDef>`) handles serialization; the `[FieldDefinition]`-decorated properties on each `ImportListSettingsBase<T>` describe the UI form layout. Hidden fields (`HiddenType.Hidden`) are used for OAuth tokens (`AccessToken`/`RefreshToken`/`Expires`/`AuthUser`). On token refresh, providers call `IImportListRepository.UpdateSettings((ImportListDefinition)Definition)` (`ImportListRepository.cs:19`) to persist only the `Settings` column, avoiding a full row rewrite.

`ImportListStatus` rows live in their own `ImportListStatus` table managed by `ImportListStatusRepository`. `ImportListItems` rows live in their own table managed by `ImportListItemRepository`. `ImportListExclusions` rows live in `ImportListExclusions`.

`SceneMapping` rows live in `SceneMappings`, keyed by `(MappingId, Type)` for diff-update semantics. `DailySeries` and the XEM havemap are *not* persisted — they live entirely in in-memory caches.

SkyHook fetches are not persisted; the consumer (`AddSeriesService` / `RefreshSeriesService` in `tv.md`) writes the resulting `Series` and `Episode` rows.

## Quirks / TODOs / HACKs

- **Sonarr `LanguageProfileIds` legacy.** `SonarrSettings.LanguageProfileIds` (`ImportLists/Sonarr/SonarrSettings.cs:51`) carries a `// TODO: Remove this eventually, no translation added as deprecated` comment. v3 source supported language profiles; v4 collapsed them into quality. Still validated/filtered in `SonarrImport.Fetch` (`SonarrImport.cs:50`).
- **Trakt yearly lists obsolete.** `TraktPopularListType.TopWatchedByYear` and `RecommendedByYear` are `[Obsolete]` and the validator rejects them with "Yearly lists are no longer supported" (`TraktPopularSettings.cs:16`). The request generator still has dead `case` arms inside `#pragma warning disable CS0612` (`TraktPopularRequestGenerator.cs:43`).
- **Simkl no-activity short-circuit.** Returns an empty `ImportListFetchResult { AnyFailure = true }` when `/sync/activities` reports nothing changed since the last sync minus 2h (`SimklImportBase.cs:50`). The `AnyFailure=true` is intentional — it tricks `FetchAndParseImportListService` into skipping `_importListItemService.SyncSeriesForList`, which otherwise would interpret the empty list as "user removed everything" and trigger library cleanup. Documented in the inline comment.
- **AniList paging override.** `AniListImport` reimplements the entire `FetchItems` try/catch ladder (`AniList/List/AniListImport.cs:49`) because AniList drives pagination via response-side `pageInfo.HasNextPage` rather than `PageSize` overflow. The duplicated catch-block is a copy of `HttpImportListBase.FetchItems` minus the tier loop. Risk: if `HttpImportListBase` adds new exception handling, AniList won't pick it up.
- **Plex MaxPages cap.** `PlexListRequestGenerator` (`Plex/PlexListRequestGenerator.cs:8`) caps the watchlist at `MaxPages = 10 * PageSize = 1000` items. Longer watchlists are silently truncated.
- **Custom list fall-through title.** `CustomImport.Fetch` substitutes `Title = "TvdbId: {tvdbId}"` when the upstream payload omits a title (`Custom/CustomImport.cs:46`). Passing such a row through `IsValidItem` will succeed (`HttpImportListBase.cs:171` — only fails when ALL of Title, ImdbId, TmdbId are empty). It will then survive `CleanupListItems` because a synthetic title is non-empty.
- **`FetchAndParseImportListService.FetchSingleList` `result.AnyFailure |= ...` is duplicated** (lines 141 and 144) — first call inside the `lock`, second call outside it. Harmless but visibly wrong.
- **No SkyHook caching.** `SkyHookProxy` issues a fresh HTTP request on every `GetSeriesInfo`/`SearchForNewSeries` call. The consumer-side rate-limit and per-series command serialization are the only safeguards. Listed as IL/MD-Q below for the Go port.
- **Hardcoded TVDB id 79151 in XemProxy.** `// hack to deal with Fate/Zero` (`XemProxy.cs:97`).
- **`SkyHookProxy` ctor double-assigns `_requestBuilder`** (`SkyHookProxy.cs:36-40`). The first assignment is overwritten by the last line. Pure dead code.
- **`TolerantEnumConverter`** (`ImportLists/TolerantEnumConverter.cs`) — exists but I haven't traced its consumers; likely used by the Trakt JSON parsers (Trakt media-type enum strings change occasionally).
- **`Plex.Scope`** (`Plex/PlexListSettings.cs:26`) returns `""` and is never overridden — a vestigial OAuth scope hook.
- **MyAnimeList `Limit=1000`.** Hardcoded in the request generator (`MyAnimeList/MyAnimeListRequestGenerator.cs:35`); larger lists are truncated server-side.
- **Library-clean gating uses any-removed.** `AllListsSuccessfulWithAPendingClean` (`ImportListSyncService.cs:58`) requires every list to have synced AND ≥1 list to have removed an item. If a single list returns "no change" forever, clean-up never fires even if other lists do drop items — until the disabled-till lock fires on a list and it goes through `LastInfoSync.HasValue == false`, blocking forever. Edge case worth a Q.

## Test files

ImportLists:
- `src/NzbDrone.Core.Test/ImportListTests/FetchAndParseImportListFixture.cs`
- `src/NzbDrone.Core.Test/ImportListTests/ImportListStatusServiceFixture.cs`
- `src/NzbDrone.Core.Test/ImportListTests/ImportListSyncServiceFixture.cs`
- `src/NzbDrone.Core.Test/ImportListTests/ImportListItemServiceFixture.cs`
- `src/NzbDrone.Core.Test/ImportListTests/Plex/PlexParserFixture.cs`
- `src/NzbDrone.Core.Test/Housekeeping/Housekeepers/CleanupOrphanedImportListStatusFixture.cs`
- `src/NzbDrone.Core.Test/HealthCheck/Checks/ImportListStatusCheckFixture.cs`

MetadataSource:
- `src/NzbDrone.Core.Test/MetadataSource/SearchSeriesComparerFixture.cs`
- `src/NzbDrone.Core.Test/MetadataSource/SkyHook/SkyHookProxyFixture.cs`
- `src/NzbDrone.Core.Test/MetadataSource/SkyHook/SkyHookProxySearchFixture.cs`

DataAugmentation:
- `src/NzbDrone.Core.Test/DataAugmentation/Scene/SceneMappingProxyFixture.cs`
- `src/NzbDrone.Core.Test/DataAugmentation/Scene/SceneMappingServiceFixture.cs`
- `src/NzbDrone.Core.Test/DataAugmentation/SceneNumbering/XemServiceFixture.cs`
- `src/NzbDrone.Core.Test/DataAugmentation/DailySeries/DailySeriesDataProxyFixture.cs`
- `src/NzbDrone.Core.Test/Providers/XemProxyFixture.cs` (legacy location)
- `src/NzbDrone.Core.Test/Files/Xem/` (test fixture data)

## Open questions

### ImportLists (IL-Q*)
- **IL-Q1.** Should the Go port keep `ListType` as a UI grouping enum, or make it a runtime characteristic only (currently it's the latter — `ImportListDefinition.ListType` is `[MemberwiseEqualityIgnore]` and rebuilt by the factory)?
- **IL-Q2.** `FetchAndParseImportListService` runs every list as a separate `LongRunning` `Task` and `Task.WaitAll`s. Do we want to preserve concurrent-list-per-sync semantics, or serialize for predictable rate-limit behaviour? AniList in particular hammers a single GraphQL endpoint with pages.
- **IL-Q3.** The exclusion list is keyed only by TVDB id. Should it also key on IMDB / TMDB / MAL / AniList ids so a list item that maps via SkyHook *to* an excluded TVDB id still correctly skips? (Today it does — exclusion is checked after the ID translation step in `ProcessListItems` line 217. Confirm this stays the post-translation gate in Go.)
- **IL-Q4.** `ListSyncLevelType.KeepAndTag` adds `_configService.ListSyncTag` once and never removes it — what's the user-facing reset semantics? Manual untag, or does `MarkListsAsCleaned` reset tags? (It does *not* — only zeroes `HasRemovedItemSinceLastClean`.)
- **IL-Q5.** Should the Go port keep all 10 providers? Plex Watchlist API + RSS overlap heavily; Sonarr-to-Sonarr is rarely used in practice. Cut MAL/AniList here and rely on the Custom endpoint?
- **IL-Q6.** OAuth proxying through `auth.servarr.com` requires the live Servarr proxy to keep working. Is that infra in scope for the rewrite, or do we shift to per-instance OAuth? (Affects Trakt, Simkl, AniList, MAL.)
- **IL-Q7.** Plex `MaxPages = 10` is a silent watchlist truncation. Surface a warning when `pagedReleases.Count == MaxPages * PageSize`?
- **IL-Q8.** Library-clean gating: see "Quirks". Is the "every list must have ever synced" gate a deliberate first-run safeguard or an oversight that prevents cleanup when a single list is permanently empty?
- **IL-Q9.** `ImportListItemInfo` is reused as both transport DTO and persisted row. Should the Go port split these (proper DTO vs. DB model)?
- **IL-Q10.** The `Custom` provider validates only `BaseUrl` — no API key, no schema check. Should we ship a JSON-schema validator on the response shape?
- **IL-Q11.** All providers depend on `IParsingService` (injected through `ImportListBase`) but most concrete classes never use it. Is that an unused legacy dep, or used somewhere in `CleanupListItems`/test paths?
- **IL-Q12.** `RssImportBase` is publicly registerable as `Advanced` but appears not to be wired up anywhere — only `PlexRssImport` (which subclasses it) is exposed. Confirm the user can or can't add a generic RSS list.

### MetadataSource (MD-Q*)
- **MD-Q1.** SkyHook URL is hardcoded in `SonarrCloudRequestBuilder`. The Go port should make this configurable for self-hosted skyhook mirrors and for testing — but how should the `tvdb:` short-circuit behave when the configured server isn't actually skyhook?
- **MD-Q2.** No client-side cache on SkyHook means hot-add scenarios (importing N series in a row) generate N HTTP requests. Add an LRU on `(GetSeriesInfo, tvdbId)` keyed on a TTL plus an `If-Modified-Since` flow? Skyhook exposes a `Last-Modified` header — verify.
- **MD-Q3.** SkyHook only knows English (`SetSegment("language", "en")`). Is multi-language metadata in scope for v5? The skyhook URL template already takes a `{language}` segment.
- **MD-Q4.** `SearchForNewSeries` mixes free-text and prefixed (`tvdb:`, `imdb:`, `tmdb:`, `mal:`, `anilist:`) lookups in a single endpoint. Worth splitting in the Go API for type safety / better caching?
- **MD-Q5.** `MapSearchResult` returns the existing `Series` row from local DB if the TVDB id matches. The caller cannot distinguish "library hit" from "fresh search hit" — which matters for the add-series UI. Add an `IsExisting` flag?
- **MD-Q6.** `Series.Monitored = true` is set unconditionally in `MapSeries`. Add-series flow overrides this from the user's choice, but is anywhere relying on the default? (Search and import-list both override before save.)
- **MD-Q7.** `OriginalLanguage` falls back to `Language.English` when the upstream omits or sends an unrecognised ISO code. Should the Go port use a distinct "Unknown" or surface a warning?
- **MD-Q8.** What happens if SkyHook is *down*? `SearchForNewSeries` throws `SkyHookException` (HTTP 503) to the caller. `GetSeriesInfo` rethrows the underlying `HttpException`. The refresh service swallows `SeriesNotFoundException` only — what about `503`? (Confirmed in `tv.md` — it should bubble.)

### DataAugmentation (DA-Q*)
- **DA-Q1.** XEM hardcoded `series.Key == 79151` (Fate/Zero) workaround — should we keep it or drop it as no-longer-relevant? Verify with current XEM data.
- **DA-Q2.** `DailySeriesService` has a 1-hour cache. Should we be more aggressive (12h or 24h)? It's effectively a static list.
- **DA-Q3.** `SceneMappingService.UpdateMappings` deletes mappings missing from a provider's response — but if the provider returns an empty list, it warns and skips (`SceneMappingService.cs:204`). Good. But what if a provider returns a partial list during an outage? Today: deletes everything missing. Possible regression if upstream truncates. Add a "must contain ≥X mappings" sanity floor?
- **DA-Q4.** `XemService.PerformUpdate` writes to `_episodeService.UpdateEpisodes(episodes)` and `_seriesService.UpdateSeries(series)` *inside* a `SeriesUpdatedEvent` handler — there's a re-entrancy risk if `UpdateSeries` re-fires `SeriesUpdatedEvent`. Verify this hasn't deadlocked or looped (and decide for Go).
- **DA-Q5.** `SceneMapping` is a single table for two distinct provider-types (Services and XEM). Cross-provider Levenshtein disambiguation in `FindMappings` mixes both. Is that intentional, or should we partition by `Type`?
- **DA-Q6.** XEM proxy ignores errors `"no single connection"` and `"no show with the tvdb_id"` (`XemProxy.cs:27`). Are those still the actual upstream messages, or have they drifted?
- **DA-Q7.** `ServicesProvider` (`/scenemapping`) and the `IDailySeriesDataProxy` (`/dailyseries`) both hit `services.sonarr.tv`. Should they share a single client with one rate-limit budget?
- **DA-Q8.** No tests cover the `ExtrapolateMappings` path explicitly (only `XemServiceFixture` with a couple of fixtures). Risky logic to port without expanding coverage.
