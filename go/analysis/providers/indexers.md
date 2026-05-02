# Indexers provider matrix

Family-level orchestration is documented in `subsystems/indexers.md`; the canonical ThingiProvider plumbing (factory, definition, settings round-trip, escalating cool-down) lives in `subsystems/thingiprovider.md`. **10 concrete indexer providers** ship in the box. Newznab additionally registers 9 named presets via its overridden `DefaultDefinitions` (`src/NzbDrone.Core/Indexers/Newznab/Newznab.cs:47-61`). Each row below corresponds to a single Phase 4 PR.

Two protocols only: `DownloadProtocol.Usenet` and `DownloadProtocol.Torrent` (`src/NzbDrone.Core/Indexers/DownloadProtocol.cs`). All ten classes inherit `HttpIndexerBase<TSettings>` (`src/NzbDrone.Core/Indexers/HttpIndexerBase.cs:21`), which adds `RateLimit` (default 2s), `PageSize`, `MaxNumResultsPerQuery = 1000`, the pageable request chain, and the `RecordSuccess`/`RecordFailure` plumbing into `IIndexerStatusService`.

## Family-level contract

| Aspect | Value |
|--------|-------|
| Provider interface | `IIndexer : IProvider` (`src/NzbDrone.Core/Indexers/IIndexer.cs:10`) |
| Settings interface | `IIndexerSettings : IProviderConfig` (`src/NzbDrone.Core/Indexers/IIndexerSettings.cs`); torrents add `ITorrentIndexerSettings` (adds `MinimumSeeders`, `SeedCriteriaSettings`, `RejectBlocklistedTorrentHashesWhileGrabbing`) |
| Abstract base | `IndexerBase<TSettings>` (`src/NzbDrone.Core/Indexers/IndexerBase.cs:19`) → `HttpIndexerBase<TSettings>` (`src/NzbDrone.Core/Indexers/HttpIndexerBase.cs:21`) |
| Definition type | `IndexerDefinition : ProviderDefinition` (`src/NzbDrone.Core/Indexers/IndexerDefinition.cs`) — adds `Protocol, SupportsRss/Search, EnableRss/AutomaticSearch/InteractiveSearch, DownloadClientId, Priority(default 25), SeasonSearchMaximumSingleEpisodeAge, Status` |
| Factory | `IndexerFactory : ProviderFactory<IIndexer, IndexerDefinition>` (`src/NzbDrone.Core/Indexers/IndexerFactory.cs`) — overrides `Active`, `SetProviderCharacteristics`, `Test` (records into `IIndexerStatusService`); adds `RssEnabled`, `AutomaticSearchEnabled`, `InteractiveSearchEnabled`, `DiscoverNewSearchEnabled`, `FilterBlockedIndexers` |
| Repository | `IndexerRepository : ProviderRepository<IndexerDefinition>` (`src/NzbDrone.Core/Indexers/IndexerRepository.cs`) — adds `FindByName(string)` |
| DB table | `Indexers` (mig 001 / 022 / 159; `Name` is unique). Status tracking in `IndexerStatus` (mig 091, FK column is `IndexerId` not `ProviderId` — TP-Q7 carry-over) |
| Status service | `IndexerStatusService : ProviderStatusServiceBase<IIndexer, IndexerStatus>` (`src/NzbDrone.Core/Indexers/IndexerStatusService.cs`) — adds `LastRssSyncReleaseInfo` getter + `RecordRss`. Defaults: `MaxLevel=9`, `MinimumTimeSinceInitialFailure=Zero`, `MinimumTimeSinceStartup=15min` (inherited) |
| Test action contract | `IIndexer.Test()` (sync-over-async — `IndexerBase.cs:115`) returns `ValidationResult`. Each subclass implements `Test(List<ValidationFailure>)` async; failures localised via `ILocalizationService`. `IndexerFactory.Test(definition)` (`IndexerFactory.cs:140-159`) records success/failure into `IndexerStatusService` only when `definition.Id > 0` |
| RequestAction surface | `Newznab` / `Torznab` expose `newznabCategories` (per `subsystems/thingiprovider.md` action table). Default base returns `null` (`IndexerBase.cs:69-72`) |
| Cache | `CachedIndexerSettingsProvider` (`src/NzbDrone.Core/Indexers/CachedIndexerSettingsProvider.cs`) — 1h TTL of `{FailDownloads, SeedCriteriaSettings}`, invalidated by `ProviderUpdatedEvent<IIndexer>` / `ProviderDeletedEvent<IIndexer>` |

## Concrete providers

### Newznab

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `Newznab` |
| `ConfigContract` (DB string) | `NewznabSettings` |
| Source file | `src/NzbDrone.Core/Indexers/Newznab/Newznab.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/Newznab/NewznabSettings.cs` |
| Status type | `IndexerStatus` (shared) |
| Protocol | Usenet |
| External service | User-configured Newznab host (Apache-licensed nntmux variants etc). Per `subsystems/indexers.md`, 9 well-known hosts ship as presets: DOGnzb, DrunkenSlug, Nzb.life, NZBCat, NZBFinder.ws, NZBgeek, nzbplanet.net, SimplyNZBs, Tabula Rasa |
| Auth model | API key (validated when host is in `ApiKeyAllowList = { nzbs.org, nzb.life, dognzb.cr, nzbplanet.net, nzbid.org, nzbndx.com, nzbindex.in }` — `NewznabSettings.cs:18`) |
| Rate-limit hint | inherited 2s default. PageSize from caps `min(100, max(default,max))` (`NewznabRequestGenerator`) |
| Capabilities | Fetched lazily from `t=caps`, cached 7d (`NewznabCapabilitiesProvider.cs`). Default `MaxPages=30`, hard cap 3000 results/tier (INDEXERS-Q4) |
| Notable quirks | Hardcoded `apikey` allow-list; `NewznabifySeasonNumber` season=0→`'00'` workaround (`NewznabRequestGenerator.cs:638`); 9-element preset list in `Newznab.DefaultDefinitions` |
| Test fixtures | `src/NzbDrone.Core.Test/IndexerTests/NewznabTests/{NewznabFixture,NewznabRequestGeneratorFixture,NewznabSettingFixture,NewznabCapabilitiesProviderFixture}.cs` |

**Settings fields** (verified against `NewznabSettings.cs:65-91`):

| C# property | C# type | `[FieldDefinition]` | Default | Required? | Privacy |
|-------------|---------|---------------------|---------|-----------|---------|
| `BaseUrl` | string | order=0, Label="URL" | (none) | Yes (`ValidRootUrl`) | Normal |
| `ApiPath` | string | order=1, Label="IndexerSettingsApiPath", Advanced=true, FieldToken→"/api" | `/api` | `ValidUrlBase("/api")` | Normal |
| `ApiKey` | string | order=2, Label="ApiKey", Privacy=ApiKey | (none) | When host in allow-list | ApiKey |
| `Categories` | IEnumerable<int> | order=3, Type=Select, SelectOptionsProviderAction="newznabCategories" | `[5030,5040]` | Either Categories or AnimeCategories non-empty | Normal |
| `AnimeCategories` | IEnumerable<int> | order=4, Type=Select, SelectOptionsProviderAction="newznabCategories" | `[]` | Either-or | Normal |
| `AnimeStandardFormatSearch` | bool | order=5, Type=Checkbox | false | No | Normal |
| `AdditionalParameters` | string | order=6, Advanced=true | null | Regex `(&.+?=.+?)+` | Normal |
| `MultiLanguages` | IEnumerable<int> | order=7, Type=Select, SelectOptions=`RealLanguageFieldConverter`, Advanced=true | `[]` | No | Normal |
| `FailDownloads` | IEnumerable<int> | order=8, Type=Select, SelectOptions=`FailDownloads`, Advanced=true | `[]` | No | Normal |

**Open questions:** PROV-Q1 — preserve all 9 named presets verbatim in the Go port? Each preset row in user DBs has `Implementation="Newznab"` and a fixed `Name` (e.g. "DOGnzb"). The presets are NOT first-class providers; they only exist in the schema endpoint output. Decide whether the Go port hardcodes the same list or reads it from a YAML config so operators can curate.

### Torznab

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `Torznab` |
| `ConfigContract` (DB string) | `TorznabSettings` |
| Source file | `src/NzbDrone.Core/Indexers/Torznab/Torznab.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/Torznab/TorznabSettings.cs` (extends `NewznabSettings` and `ITorrentIndexerSettings`) |
| Status type | `IndexerStatus` |
| Protocol | Torrent |
| External service | User-configured Torznab host (Jackett, Prowlarr, private trackers) |
| Auth model | API key (same allow-list as Newznab via inherited validator) |
| Rate-limit hint | 2s default; `JackettAll` URL pattern (`/torznab/all` or `/api/v2.0/indexers/all/results/torznab`) raises a *warning* validation |
| Notable quirks | Reuses `NewznabRequestGenerator` + `NewznabCapabilitiesProvider`; `TorznabRssParser` for the response shape. `Torznab.cs:57` private `GetDefinition` hard-codes `Protocol=Usenet` — appears to be dead code (INDEXERS-Q1 / PROV-Q2) |
| Test fixtures | `src/NzbDrone.Core.Test/IndexerTests/TorznabTests/TorznabFixture.cs` |

**Settings fields** — inherits everything from `NewznabSettings`, plus:

| C# property | C# type | `[FieldDefinition]` | Default | Required? | Privacy |
|-------------|---------|---------------------|---------|-----------|---------|
| `MinimumSeeders` | int | order=8 (overrides Newznab's `FailDownloads` slot — see field-ordinal comment on `NewznabSettings.cs:93`) | `IndexerDefaults.MINIMUM_SEEDERS = 1` | No | Normal |
| `SeedCriteria` | `SeedCriteriaSettings` (nested) | recursed via SchemaBuilder | new instance | per-field validators | Normal |
| `RejectBlocklistedTorrentHashesWhileGrabbing` | bool | Type=Checkbox, Advanced=true | false | No | Normal |

**Open questions:** PROV-Q2 — drop or fix `Torznab.cs:57` `GetDefinition` dead code in Go port; decide canonical `MinimumSeeders` default (currently shared with `IndexerDefaults`).

### Fanzub

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `Fanzub` |
| `ConfigContract` (DB string) | `FanzubSettings` |
| Source file | `src/NzbDrone.Core/Indexers/Fanzub/Fanzub.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/Fanzub/FanzubSettings.cs` |
| Status type | `IndexerStatus` |
| Protocol | Usenet |
| External service | fanzub.com (anime-focused public RSS) |
| Auth model | None (anonymous RSS) |
| Rate-limit hint | 2s default |
| Notable quirks | Plain RSS via `RssParser` with `UseEnclosureUrl=true, UseEnclosureLength=true` |
| Test fixtures | `IndexerTests/FanzubTests/{FanzubFixture,FanzubRequestGeneratorFixture}.cs` |

**Settings fields:** `BaseUrl` (order=0, default `http://fanzub.com/rss/`), `MultiLanguages`, `FailDownloads` (Advanced).

### BroadcastheNet

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `BroadcastheNet` |
| `ConfigContract` (DB string) | `BroadcastheNetSettings` |
| Source file | `src/NzbDrone.Core/Indexers/BroadcastheNet/BroadcastheNet.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/BroadcastheNet/BroadcastheNetSettings.cs` |
| Status type | `IndexerStatus` |
| Protocol | Torrent |
| External service | broadcasthe.net JSON-RPC API |
| Auth model | API key |
| Rate-limit hint | 5s (override of HttpIndexerBase 2s) |
| Notable quirks | JSON-RPC, not RSS. PageSize=100. RSS-cursor seed parses `BTN-<id>` from previous `LastRssSyncReleaseInfo` (`BroadcastheNet.cs:31`) |
| Test fixtures | `IndexerTests/BroadcastheNetTests/BroadcastheNetFixture.cs` |

**Settings fields:** `BaseUrl` (default `http://api.broadcasthe.net`), `ApiKey` (Privacy=ApiKey), `MultiLanguages`, `FailDownloads`.

### HDBits

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `HDBits` |
| `ConfigContract` (DB string) | `HDBitsSettings` |
| Source file | `src/NzbDrone.Core/Indexers/HDBits/HDBits.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/HDBits/HDBitsSettings.cs` |
| Status type | `IndexerStatus` |
| Protocol | Torrent |
| External service | hdbits.org JSON API |
| Auth model | Username + passkey (`Passkey` is `Privacy.ApiKey`) |
| Rate-limit hint | 2s default. PageSize=100 |
| Notable quirks | `HDBitsParser` ctor takes settings (passkey/codec/medium filters). `Codecs` and `Mediums` are `IEnumerable<int>` Select fields |
| Test fixtures | `IndexerTests/HDBitsTests/HDBitsFixture.cs` |

**Settings fields:** `BaseUrl`, `Username`, `ApiKey` (passkey), `Categories` (`IEnumerable<int>`), `Codecs` (Select), `Mediums` (Select), `Origins` (Select), `MinimumSeeders`, `SeedCriteria` (nested), `RejectBlocklistedTorrentHashesWhileGrabbing`.

### IPTorrents

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `IPTorrents` |
| `ConfigContract` (DB string) | `IPTorrentsSettings` |
| Source file | `src/NzbDrone.Core/Indexers/IPTorrents/IPTorrents.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/IPTorrents/IPTorrentsSettings.cs` |
| Status type | `IndexerStatus` |
| Protocol | Torrent |
| External service | iptorrents.com user-specific RSS feed URL |
| Auth model | Token embedded in feed URL (no separate field) |
| Rate-limit hint | 2s; `SupportsSearch=false`, `PageSize=0` (RSS-only) |
| Notable quirks | Uses `TorrentRssParser` with `ParseSizeInDescription=true` |
| Test fixtures | `IndexerTests/IPTorrentsTests/IPTorrentsFixture.cs` |

**Settings fields:** `BaseUrl` (the user-specific feed URL — Privacy is implicit since the URL is the credential; FieldDefinition Label="FeedUrl"), `MinimumSeeders`, `SeedCriteria`, `RejectBlocklistedTorrentHashesWhileGrabbing`.

### Nyaa

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `Nyaa` |
| `ConfigContract` (DB string) | `NyaaSettings` |
| Source file | `src/NzbDrone.Core/Indexers/Nyaa/Nyaa.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/Nyaa/NyaaSettings.cs` |
| Status type | `IndexerStatus` |
| Protocol | Torrent |
| External service | nyaa.si and forks (anime-focused) |
| Auth model | None (anonymous) |
| Rate-limit hint | 2s |
| Notable quirks | `TorrentRssParser` with `UseGuidInfoUrl=false`, custom XML node names: `size`, `infoHash`, `seeders`, `leechers`, `CalculatePeersAsSum=true` |
| Test fixtures | `IndexerTests/NyaaTests/{NyaaFixture,NyaaRequestGeneratorFixture}.cs` |

**Settings fields:** `BaseUrl`, `AdditionalParameters`, `MinimumSeeders`, `SeedCriteria`, `RejectBlocklistedTorrentHashesWhileGrabbing`.

### Torrentleech

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `Torrentleech` |
| `ConfigContract` (DB string) | `TorrentleechSettings` |
| Source file | `src/NzbDrone.Core/Indexers/Torrentleech/Torrentleech.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/Torrentleech/TorrentleechSettings.cs` |
| Status type | `IndexerStatus` |
| Protocol | Torrent |
| External service | torrentleech.org RSS |
| Auth model | API key (rss_key in URL) |
| Rate-limit hint | 2s; RSS-only |
| Notable quirks | `TorrentRssParser{UseGuidInfoUrl=true, ParseSeedersInDescription=true}` |
| Test fixtures | `IndexerTests/TorrentleechTests/TorrentleechFixture.cs` |

**Settings fields:** `BaseUrl`, `ApiKey` (`Privacy=ApiKey`), `MinimumSeeders`, `SeedCriteria`, `RejectBlocklistedTorrentHashesWhileGrabbing`.

### FileList

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `FileList` |
| `ConfigContract` (DB string) | `FileListSettings` |
| Source file | `src/NzbDrone.Core/Indexers/FileList/FileList.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/FileList/FileListSettings.cs` |
| Status type | `IndexerStatus` |
| Protocol | Torrent |
| External service | filelist.io JSON API (Romanian tracker) |
| Auth model | Username + passkey |
| Rate-limit hint | 2s |
| Notable quirks | JSON not RSS; `FileListParser` ctor takes settings |
| Test fixtures | `IndexerTests/FileListTests/FileListFixture.cs` |

**Settings fields:** `BaseUrl`, `Username`, `Passkey` (`Privacy=Password` or `ApiKey` — verify), `Categories` (Select), `MinimumSeeders`, `SeedCriteria`, `RejectBlocklistedTorrentHashesWhileGrabbing`.

### TorrentRssIndexer

| Field | Value |
|-------|-------|
| `Implementation` (DB string) | `TorrentRssIndexer` |
| `ConfigContract` (DB string) | `TorrentRssIndexerSettings` |
| Source file | `src/NzbDrone.Core/Indexers/TorrentRss/TorrentRssIndexer.cs` |
| Settings class | `src/NzbDrone.Core/Indexers/TorrentRss/TorrentRssIndexerSettings.cs` |
| Status type | `IndexerStatus` |
| Protocol | Torrent |
| External service | Generic torrent RSS |
| Auth model | Optional cookie or none |
| Rate-limit hint | 2s; `SupportsSearch=false`, `PageSize=0` |
| Notable quirks | Parser is built dynamically by `TorrentRssParserFactory` — runs `TorrentRssSettingsDetector` once, caches parser settings 7 days, picks `EzrssTorrentRssParser` if EZTV format detected |
| Test fixtures | `IndexerTests/TorrentRssIndexerTests/{TorrentRssIndexerFixture,TorrentRssParserFactoryFixture,TorrentRssSettingsDetectorFixture}.cs` |

**Settings fields:** `BaseUrl`, `Cookie` (Advanced), `AllowZeroSize` (Checkbox, Advanced), `MinimumSeeders`, `SeedCriteria`, `RejectBlocklistedTorrentHashesWhileGrabbing`.

## Open questions

- **PROV-Q1** — How should the Go port handle Newznab's 9 named presets? Options: (a) hardcode list mirroring `Newznab.cs:47-61`, (b) externalise to YAML/embed.fs config, (c) drop and require operators to add manually. Affects upgrade UX (existing user DBs already have rows with these names; presets only feed `GET /schema`'s `presets[]`, not the persisted rows). Cross-cite TP-Q1 for the registry decision.
- **PROV-Q2** — `Torznab.cs:57`'s private `GetDefinition` (sets `Protocol=Usenet`) appears to be dead code — confirm by grepping for callers across the C# tree; if dead, omit from Go port.
- **PROV-Q3** — `IndexerStatus.IndexerId` column-name divergence (vs `*Status.ProviderId` for the other three families) is preserved by Dapper's column-ordinal mapping. Decide whether the Go schema homogenises to `ProviderId` (one migration) or preserves to keep dual-running with C# possible. Cross-cite TP-Q7.
- **PROV-Q4** — Per `subsystems/indexers.md` INDEXERS-Q8: `RateLimitKey = Definition.Id` ties rate-limit buckets to the indexer-row PK, not to host. Two definitions pointing at the same Newznab host get separate buckets. Preserve in Go port or switch to host-based? Worth fixing because each Newznab preset row would otherwise share a bucket only with itself even though they all hit the same upstream.
- **PROV-Q5** — `BroadcastheNet`'s 5s rate-limit override is the only non-default. Capture as a per-provider const in Go, or push into `IndexerSettings.RateLimit`?
- **PROV-Q6** — `TorrentRssIndexerSettings` parser-detection caches results 7d outside the settings JSON. The Go port needs an equivalent disk-or-in-memory cache; decide where it lives (separate `IndexerCacheRepository`? in-process map keyed by definition Id?).
