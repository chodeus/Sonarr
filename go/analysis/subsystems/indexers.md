# Indexers

**Source:** `src/NzbDrone.Core/Indexers/**`
**Target Go package:** `go/internal/indexers/`
**Status:** drafted

## Purpose
Indexers are Sonarr's release-discovery providers. Each indexer is a configurable plug-in that knows how to (a) fetch a recent-releases feed for RSS Sync and (b) execute targeted searches for specific episodes/seasons. Indexers normalize provider-specific responses into a common `ReleaseInfo`/`TorrentInfo` shape, then hand them off to the DecisionEngine. Two protocols are supported, `Usenet` and `Torrent` (`Indexers/DownloadProtocol.cs`). The subsystem is the upstream input boundary for the entire grab pipeline; everything downstream (DecisionEngine, DownloadClients, history) consumes its output. Indexers are configured through the `ThingiProvider` framework (see `datastore.md`/embedded-docs), persisted as `IndexerDefinition` rows with `*Settings` payloads serialized as JSON.

## Architecture: interface and base hierarchy

Top-level interface `IIndexer : IProvider` (`Indexers/IIndexer.cs:10`) requires:
- `bool SupportsRss`, `bool SupportsSearch`, `DownloadProtocol Protocol`
- `Task<IList<ReleaseInfo>> FetchRecent()` plus seven `Fetch(<X>SearchCriteria)` overloads (Single/Season/Daily/DailySeason/Anime/AnimeSeason/Special)
- `HttpRequest GetDownloadRequest(string link)`

Class hierarchy:

1. `IndexerBase<TSettings> : IIndexer` (`Indexers/IndexerBase.cs:19`) — abstract; ties the indexer to its `TSettings : IIndexerSettings, new()`. Owns:
   - `ConfigContract` returning `typeof(TSettings)`
   - `DefaultDefinitions` (default seed for `IndexerDefinition`)
   - `Definition`, `Settings` accessors
   - `CleanupReleases()` — dedupes by `Guid`, applies `MultiLanguages` defaulting, stamps `IndexerId`/`Indexer`/`DownloadProtocol`/`IndexerPriority`/`SeasonSearchMaximumSingleEpisodeAge` onto each `ReleaseInfo`
   - `Test()` / abstract `Test(List<ValidationFailure>)`
2. `HttpIndexerBase<TSettings> : IndexerBase<TSettings>` (`Indexers/HttpIndexerBase.cs:21`) — concrete-but-abstract HTTP fetch loop. Defaults `SupportsRss = SupportsSearch = true`, exposes `PageSize` (override → enables paging), `RateLimit = 2s` default. Subclasses MUST implement:
   - `IIndexerRequestGenerator GetRequestGenerator()`
   - `IParseIndexerResponse GetParser()`
3. Concrete provider (e.g. `Newznab`, `BroadcastheNet`, etc.).

Auxiliary interfaces:
- `IIndexerSettings : IProviderConfig` (`Indexers/IIndexerSettings.cs`) — `BaseUrl`, `MultiLanguages`, `FailDownloads`.
- `ITorrentIndexerSettings : IIndexerSettings` (`Indexers/ITorrentIndexerSettings.cs`) — adds `MinimumSeeders`, nested `SeedCriteriaSettings`, `RejectBlocklistedTorrentHashesWhileGrabbing`.
- `IIndexerRequestGenerator` (`Indexers/IIndexerRequestGenerator.cs`) — `GetRecentRequests()` plus seven `GetSearchRequests(...SearchCriteria)` overloads, all returning `IndexerPageableRequestChain`.
- `IParseIndexerResponse` (`Indexers/IProcessIndexerResponse.cs`) — single `ParseResponse(IndexerResponse) → IList<ReleaseInfo>`.
- `IndexerDefinition : ProviderDefinition` (`Indexers/IndexerDefinition.cs`) — adds `Protocol`, `SupportsRss/Search`, `EnableRss/AutomaticSearch/InteractiveSearch`, `DownloadClientId`, `Priority` (default 25), `SeasonSearchMaximumSingleEpisodeAge`, `Status`.

### Pageable request chain
`IndexerPageableRequestChain` (`Indexers/IndexerPageableRequestChain.cs`) is a list-of-list-of-pageable-iterables. Each "tier" is tried in turn; the first tier that yields non-empty results wins (`HttpIndexerBase.cs:201`). Within a tier, multiple `IndexerPageableRequest`s run sequentially; each pageable iterates pages until empty, until `PageSize` not reached, or until `MaxNumResultsPerQuery = 1000`.

## Pipeline

### RSS Sync
1. `RssSyncCommand` (`Indexers/RssSyncCommand.cs`) is dispatched on schedule.
2. `RssSyncService.Execute` (`Indexers/RssSyncService.cs:60`) calls `Sync()` → `IFetchAndParseRss.Fetch()`.
3. `FetchAndParseRssService.Fetch()` (`Indexers/FetchAndParseRssService.cs:25`) → `IndexerFactory.RssEnabled()` (filters definitions with `EnableRss == true` AND blocked-status check), then `Task.WhenAll(indexer.FetchRecent())`.
4. `HttpIndexerBase.FetchRecent()` → `FetchReleases(g => g.GetRecentRequests(), isRecent:true)` which:
   - For `isRecent`, fetches `lastReleaseInfo` via `_indexerStatusService.GetLastRssSyncReleaseInfo(Definition.Id)`.
   - Iterates tiers/pageables; for each request calls `FetchPage` → `FetchIndexerResponse` → `parser.ParseResponse`.
   - Stops paging when overlap with the last-seen release is found OR a "full" age window is covered.
   - Records success via `_indexerStatusService.RecordSuccess` and updates `UpdateRssSyncStatus`.
5. Releases are concatenated with pending releases, fed to `IMakeDownloadDecision.GetRssDecision`, then `IProcessDownloadDecisions.ProcessDecisions`.
6. Final `RssSyncCompleteEvent` published (`Indexers/RssSyncCompleteEvent.cs`).

### Search
Search drivers live under `IndexerSearch/` (separate subsystem) and call `IIndexer.Fetch(<X>SearchCriteria)`. Same `FetchReleases` loop, but `isRecent = false`, so paging stops only at empty page / page-size threshold / `MaxNumResultsPerQuery`.

### Per-page steps
`FetchPage()` (`HttpIndexerBase.cs:333`) does:
- `FetchIndexerResponse` — sets `RateLimit` and `RateLimitKey = Definition.Id` on the `HttpRequest`, executes via `IHttpClient`, wraps result as `IndexerResponse`.
- `parser.ParseResponse(response)` — provider-specific (typically extends `RssParser`).
- On parse error, the response is attached to the exception with `ex.WithData(response.HttpResponse, 128 * 1024)` and rethrown.

`CleanupReleases` (`IndexerBase.cs:86`) is the final pass before returning to caller — dedupes by `Guid`, fills indexer metadata.

## Provider inventory

| Provider | Protocol | Base | File path | Notable quirks |
|----------|----------|------|-----------|----------------|
| `Newznab` | Usenet | `HttpIndexerBase<NewznabSettings>` | `Indexers/Newznab/Newznab.cs` | Capabilities-driven; ships 9 default presets (DOGnzb, DrunkenSlug, Nzb.life, NZBCat, NZBFinder.ws, NZBgeek, nzbplanet.net, SimplyNZBs, Tabula Rasa). Caps fetched via `t=caps` and cached 7d (`NewznabCapabilitiesProvider.cs`). API-key allow-list of well-known hosts forces `apikey` validation. PageSize from `min(100, max(default,max))`. |
| `Torznab` | Torrent | `HttpIndexerBase<TorznabSettings>` (settings extend `NewznabSettings`) | `Indexers/Torznab/Torznab.cs` | Reuses `NewznabRequestGenerator` and `NewznabCapabilitiesProvider`; uses `TorznabRssParser`. Has `JackettAll` warning when `/torznab/all` or `/api/v2.0/indexers/all/results/torznab` is detected. `TorznabSettings` adds `MinimumSeeders`, `SeedCriteria`, `RejectBlocklistedTorrentHashesWhileGrabbing`. |
| `Fanzub` | Usenet | `HttpIndexerBase<FanzubSettings>` | `Indexers/Fanzub/Fanzub.cs` | Plain RSS using `RssParser` with `UseEnclosureUrl/Length`. Anime-focused. |
| `BroadcastheNet` | Torrent | `HttpIndexerBase<BroadcastheNetSettings>` | `Indexers/BroadcastheNet/BroadcastheNet.cs` | JSON-RPC API (not RSS). PageSize 100, `RateLimit = 5s`. Uses BTN torrent ID parsed from last RSS-sync `ReleaseInfo.Guid` (`BTN-<id>` prefix) to bound recent fetch. Custom `BroadcastheNetParser` consumes `BroadcastheNetTorrents`/`BroadcastheNetTorrentQuery` types. |
| `HDBits` | Torrent | `HttpIndexerBase<HDBitsSettings>` | `Indexers/HDBits/HDBits.cs` | JSON API (`HDBitsApi.cs`). PageSize 100. `HDBitsParser` ctor takes settings (uses passkey/codec/medium filters). |
| `IPTorrents` | Torrent | `HttpIndexerBase<IPTorrentsSettings>` | `Indexers/IPTorrents/IPTorrents.cs` | RSS-only (`SupportsSearch = false`, `PageSize = 0`). Uses `TorrentRssParser{ParseSizeInDescription=true}`. |
| `Nyaa` | Torrent | `HttpIndexerBase<NyaaSettings>` | `Indexers/Nyaa/Nyaa.cs` | Anime-focused. `TorrentRssParser` with custom XML element names: `size`, `infoHash`, `seeders`, `leechers`, `CalculatePeersAsSum`. |
| `Torrentleech` | Torrent | `HttpIndexerBase<TorrentleechSettings>` | `Indexers/Torrentleech/Torrentleech.cs` | RSS-only. Uses `TorrentRssParser{UseGuidInfoUrl, ParseSeedersInDescription}`. |
| `FileList` | Torrent | `HttpIndexerBase<FileListSettings>` | `Indexers/FileList/FileList.cs` | JSON API; `FileListParser` ctor takes settings. Romanian tracker. |
| `TorrentRssIndexer` | Torrent | `HttpIndexerBase<TorrentRssIndexerSettings>` | `Indexers/TorrentRss/TorrentRssIndexer.cs` | Generic torrent RSS. Parser is built dynamically by `TorrentRssParserFactory` — runs `TorrentRssSettingsDetector` once, caches parser settings 7 days, picks `EzrssTorrentRssParser` if EZTV format detected. `SupportsSearch = false`, `PageSize = 0`. |

Total concrete indexer providers: **10** (Newznab, Torznab, Fanzub, BroadcastheNet, HDBits, IPTorrents, Nyaa, Torrentleech, FileList, TorrentRssIndexer). Newznab additionally registers 9 named presets via `DefaultDefinitions`.

## Settings persistence

- `IndexerDefinition` is a `ProviderDefinition` row (table `Indexers`). The `Settings` column stores the concrete `*Settings` JSON (embedded document) — see `embedded-docs/catalogue.md`.
- `IndexerRepository` (`Indexers/IndexerRepository.cs`) is a thin `ProviderRepository<IndexerDefinition>` with `FindByName(name)`.
- Per-indexer status is a separate row in `IndexerStatus` (table managed by `IndexerStatusRepository.cs`). `IndexerStatus : ProviderStatusBase` (`Indexers/IndexerStatus.cs`) adds `LastRssSyncReleaseInfo`.
- `CachedIndexerSettingsProvider` (`Indexers/CachedIndexerSettingsProvider.cs`) keeps a 1-hour rolling cache of `{FailDownloads, SeedCriteriaSettings}` keyed by indexerId; invalidated on `ProviderUpdatedEvent<IIndexer>` / `ProviderDeletedEvent<IIndexer>`.

`*Settings` types (each has a FluentValidation validator embedded):
- `NewznabSettings` (`Indexers/Newznab/NewznabSettings.cs`) — BaseUrl, ApiPath (default `/api`), ApiKey, Categories (default `5030,5040`), AnimeCategories, AnimeStandardFormatSearch, AdditionalParameters, MultiLanguages, FailDownloads.
- `TorznabSettings : NewznabSettings, ITorrentIndexerSettings` — adds MinimumSeeders, SeedCriteria, RejectBlocklistedTorrentHashesWhileGrabbing.
- `FanzubSettings`, `BroadcastheNetSettings`, `HDBitsSettings`, `IPTorrentsSettings`, `NyaaSettings`, `TorrentleechSettings`, `FileListSettings`, `TorrentRssIndexerSettings`.
- Shared `SeedCriteriaSettings` (`Indexers/SeedCriteriaSettings.cs`) — SeedRatio, SeedTime, SeasonPackSeedGoal, SeasonPackSeedRatio, SeasonPackSeedTime, with `SeedCriteriaSettingsValidator` taking minimum thresholds.

## Capabilities and feature flags

Indexer definition flags (`IndexerDefinition`):
- `EnableRss`, `EnableAutomaticSearch`, `EnableInteractiveSearch` (any-of → `Enable`).
- `SupportsRss`, `SupportsSearch` (filled in by `IndexerFactory.SetProviderCharacteristics`).
- `Priority` (default 25), `SeasonSearchMaximumSingleEpisodeAge`.

Indexer instance characteristics (`HttpIndexerBase`):
- `PageSize` (0 disables paging), `RateLimit`, `MaxNumResultsPerQuery = 1000`.

Newznab capabilities (`Indexers/Newznab/NewznabCapabilities.cs`) advertised via `t=caps` (parsed in `NewznabCapabilitiesProvider.cs:94`):
- `DefaultPageSize`, `MaxPageSize`.
- `SupportedSearchParameters` (e.g. `q`).
- `SupportedTvSearchParameters` (e.g. `q`, `title`, `tvdbid`, `imdbid`, `rid`, `tvmazeid`, `tmdbid`, `season`, `ep`).
- `SupportsAggregateIdSearch` (combine multiple IDs in one request — set true when `supportedParams` attribute exists on `tv-search`).
- `TextSearchEngine`, `TvTextSearchEngine` (default `sphinx`; `raw` flips title-cleaning behavior in `NewznabRequestGenerator`).
- `Categories` (list of `NewznabCategory` with subcategories).

`IndexerFactory.RssEnabled / AutomaticSearchEnabled / InteractiveSearchEnabled` filter on the corresponding `Enable*` flag and (by default) drop providers currently in `IndexerStatusService.GetBlockedProviders()`.

## Rate limiting / backoff / health tracking

- Rate limiting per request: each `IndexerRequest` has its `RateLimit` raised to `RateLimit` and `RateLimitKey = Definition.Id` (`HttpIndexerBase.cs:353`); `IHttpClient` honors keyed token-bucket gating. Defaults: 2s (HttpIndexerBase), 5s (BroadcastheNet override).
- `IndexerStatusService` (`Indexers/IndexerStatusService.cs`, base `ProviderStatusServiceBase`) records:
  - `RecordSuccess(definitionId)` on every successful fetch.
  - `RecordFailure(definitionId)` — escalating disable window via base class.
  - `RecordFailure(definitionId, retryAfter)` for 429/`TooManyRequestsException` and `RequestLimitReachedException` (uses `ex.RetryAfter` else 1h minimum).
  - `RecordConnectionFailure` for DNS/connect failures.
- `IndexerFactory.FilterBlockedIndexers` excludes any definition whose `IndexerStatus.DisabledTill` is in the future (logs at Debug).
- Test paths (`HttpIndexerBase.TestConnection`) catch all major failure modes (`ApiKeyException`, `RequestLimitReachedException`, `CloudFlareCaptchaException`, `UnsupportedFeedException`, `IndexerException`, `HttpException`, `HttpRequestException`, `TaskCanceledException`, `WebException`, generic `Exception`) and surface localized `ValidationFailure`s.
- `RssSyncStatus` records the last release seen (`UpdateRssSyncStatus`) — used to bound future RSS pagination.

## Quirks / TODOs / HACKs

- `NewznabRequestGenerator.NewznabifySeasonNumber` (`Indexers/Newznab/NewznabRequestGenerator.cs:638`): comment says `Temporary workaround for NNTMux considering season=0 -> null. '00' should work on existing newznab indexers.`
- `NewznabCapabilities` (`Indexers/Newznab/NewznabCapabilities.cs:21,23`): comments insist `rid` and `sphinx` defaults must remain for older Newznab installs.
- `NewznabSettings.cs:93`: comment "Field 8 is used by TorznabSettings MinimumSeeders" — field-ordinal coordination across the Torznab inheritance.
- `ITorrentIndexerSettings.cs:7`: `// TODO: System.Text.Json requires setter be public for sub-object deserialization in 3.0. https://github.com/dotnet/corefx/issues/42515`
- `Torznab.cs:57`: hard-codes `Protocol = DownloadProtocol.Usenet` in `GetDefinition` even though Torznab is torrent — the helper is private/unused (`GetSettings`/`GetDefinition` defined but no `DefaultDefinitions` override). Possible dead code, see open question.
- `XmlCleaner` is run on every RSS payload before parse (`RssParser.LoadXmlDocument`); needed because some indexers emit invalid entities/unicode.
- `FetchAndParseRssService.FetchIndexer` swallows ALL exceptions with `_logger.Error(ex, "Error during RSS Sync")`; per-indexer failure does not abort the batch.
- `HttpIndexerBase.FetchReleases` uses the first non-empty tier (`releases.Any() break`); a tier that throws still increments failure counter but doesn't fall through to the next tier — successive tiers are only considered when a tier returned an empty list.
- `RssParser` aggressively rewrites HTML-content-typed responses as `IndexerException("html content. Site is likely blocked or unavailable.")`.
- BroadcastheNet uses RSS-sync state to seed its JSON cursor by parsing the `BTN-<id>` GUID prefix (`BroadcastheNet.cs:31`).
- `Test()` runs `Test(failures).GetAwaiter().GetResult()` — sync-over-async (`IndexerBase.cs:115`).
- `CleanupReleases` mutates each `ReleaseInfo` in-place to inject indexer identity; re-running on already-cleaned releases is idempotent but not formally guarded.

## Test files

- `src/NzbDrone.Core.Test/IndexerTests/BasicRssParserFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/IndexerServiceFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/IndexerStatusServiceFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/SeasonSearchFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/SeedConfigProviderFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/TestIndexer.cs` (test stub)
- `src/NzbDrone.Core.Test/IndexerTests/TestIndexerSettings.cs`
- `src/NzbDrone.Core.Test/IndexerTests/XElementExtensionsFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/BroadcastheNetTests/BroadcastheNetFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/FanzubTests/FanzubFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/FanzubTests/FanzubRequestGeneratorFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/FileListTests/FileListFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/HDBitsTests/HDBitsFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/IPTorrentsTests/IPTorrentsFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/IntegrationTests/IndexerIntegrationTests.cs`
- `src/NzbDrone.Core.Test/IndexerTests/NewznabTests/NewznabCapabilitiesProviderFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/NewznabTests/NewznabFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/NewznabTests/NewznabRequestGeneratorFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/NewznabTests/NewznabSettingFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/NyaaTests/NyaaFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/NyaaTests/NyaaRequestGeneratorFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/TorrentRssIndexerTests/TestTorrentRssIndexer.cs`
- `src/NzbDrone.Core.Test/IndexerTests/TorrentRssIndexerTests/TorrentRssIndexerFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/TorrentRssIndexerTests/TorrentRssParserFactoryFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/TorrentRssIndexerTests/TorrentRssSettingsDetectorFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/TorrentleechTests/TorrentleechFixture.cs`
- `src/NzbDrone.Core.Test/IndexerTests/TorznabTests/TorznabFixture.cs`

## DB tables touched

- `Indexers` — read/write (CRUD via `IndexerRepository`).
- `IndexerStatus` — read/write (failure tracking, last-RSS pointer).
- Indirectly: `PendingReleases` (consumed in `RssSyncService.Sync`).

## Events raised

| Event | Payload | Producers | Consumers |
|-------|---------|-----------|-----------|
| `RssSyncCompleteEvent` | `ProcessedDecisions` | `RssSyncService.Execute` | downstream notifications/log |
| `ProviderAddedEvent<IIndexer>` / `ProviderUpdatedEvent<IIndexer>` / `ProviderDeletedEvent<IIndexer>` | provider entity | `ProviderFactory` lifecycle | `CachedIndexerSettingsProvider` (cache invalidate) |

## Events consumed

| Event | Handler | Behavior |
|-------|---------|----------|
| `ProviderUpdatedEvent<IIndexer>` | `CachedIndexerSettingsProvider.Handle` | clear settings cache |
| `ProviderDeletedEvent<IIndexer>` | `CachedIndexerSettingsProvider.Handle` | clear settings cache |

## Jobs / commands

| Command | Trigger | Frequency | Idempotent? |
|---------|---------|-----------|-------------|
| `RssSyncCommand` | scheduled + on-demand | configurable interval (default 25 min) | Yes — last-release pointer dedupes within RSS, DecisionEngine dedupes against history. |

## External services

- Per-indexer remote: 9 default Newznab hosts (DOGnzb, DrunkenSlug, Nzb.life, NZBCat, NZBFinder.ws, NZBgeek, nzbplanet.net, SimplyNZBs, Tabula Rasa) plus user-configured Newznab/Torznab/private trackers.
- BroadcastheNet API (JSON-RPC), HDBits API, FileList API.
- Hard-coded API-key allow-list domains: `nzbs.org, nzb.life, dognzb.cr, nzbplanet.net, nzbid.org, nzbndx.com, nzbindex.in` (`NewznabSettings.cs:18`).

## Open questions

- **INDEXERS-Q1**: `Torznab.GetDefinition` (line 57) hard-codes `Protocol = DownloadProtocol.Usenet` even though Torznab is torrent. Both `GetDefinition` and `GetSettings` are private and no `DefaultDefinitions` override exists in `Torznab.cs` — confirm this is dead code that can be removed in the Go port, or whether it's reachable through reflection.
- **INDEXERS-Q2**: Tier-fallthrough semantics in `HttpIndexerBase.FetchReleases` — when a tier throws (e.g. `IndexerException`), control jumps to the outer catch and *all* later tiers are skipped, even though tiered chains exist precisely to provide fallbacks. Should the Go port retry the next tier on per-tier exceptions, or preserve current behavior?
- **INDEXERS-Q3**: `CachedIndexerSettingsProvider` cache TTL is 1 hour and only invalidates on add/update/delete events. If a settings JSON change bypasses the factory (e.g. direct DB update during migration), seed config will be stale for up to 1 hour. Decide whether the Go port should subscribe to a broader change feed.
- **INDEXERS-Q4**: `MaxPages = 30` is hard-coded in `NewznabRequestGenerator` ctor. Combined with `PageSize` capped at 100 this gives a hard ceiling of 3000 results per search-tier per category-set. Confirm this matches operator expectations or whether it should become a setting.
- **INDEXERS-Q5**: `Test()` is sync-over-async (`IndexerBase.cs:115`). The Go port can be natively async — confirm that test endpoints don't depend on synchronous semantics in callers.
- **INDEXERS-Q6**: How are `BroadcastheNet`/`HDBits`/`FileList` JSON parsers structured (they don't go through `RssParser`)? They're outside this card's scope but the Go port needs separate per-provider parser packages — flag for follow-up cards.
- **INDEXERS-Q7**: `XmlCleaner` is applied unconditionally to every RSS payload. The Go port using a stricter XML decoder may reject inputs the C# permissive cleaner accepts; build a regression corpus from real-world Newznab/Torznab feeds before swapping.
- **INDEXERS-Q8**: `RateLimitKey = Definition.Id.ToString()` ties rate-limit buckets to *indexer ID*, not domain. Two definitions pointing at the same Newznab host will get separate buckets — intentional or bug?
- **INDEXERS-Q9**: `IndexerDefaults.MINIMUM_SEEDERS = 1` is a single global default; per-indexer minimums in `TorznabSettings/ITorrentIndexerSettings` override it. Ensure embedded-docs catalogue lists every indexer that exposes `MinimumSeeders`.
