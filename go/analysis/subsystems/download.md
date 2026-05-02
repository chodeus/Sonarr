# Download

**Source:** `src/NzbDrone.Core/Download/**`
**Target Go package:** `go/internal/download/`
**Status:** drafted

## Purpose
The Download subsystem owns everything between a `DownloadDecision` (from `DecisionEngine`) and a finished file landing on disk ready for import. It (1) abstracts a zoo of third-party download clients (qBittorrent, SABnzbd, Deluge, …) behind a common provider interface, (2) sends approved releases to those clients, (3) tracks each client's queue every minute, (4) detects completion, encryption, and failure, (5) hands completed downloads to the import pipeline, and (6) parks releases that cannot yet be grabbed (delay-profile, blocked indexer, blocked client) in a "pending" queue. It is the second-largest provider family in the application after indexers.

## Architecture: interfaces and base class hierarchy

```
IProvider (NzbDrone.Core.ThingiProvider)
  └─ IDownloadClient                              IDownloadClient.cs
        └─ DownloadClientBase<TSettings>          DownloadClientBase.cs
              ├─ TorrentClientBase<TSettings>     TorrentClientBase.cs   → Protocol = Torrent
              │     └─ <19 concrete torrent clients>
              └─ UsenetClientBase<TSettings>      UsenetClientBase.cs    → Protocol = Usenet
                    └─ <5 concrete Usenet clients>
              (Pneumatic and one DownloadStation variant inherit DownloadClientBase directly)
```

### What `IDownloadClient` requires (`IDownloadClient.cs`)
| Member | Notes |
|--------|-------|
| `DownloadProtocol Protocol { get; }` | `Usenet` / `Torrent`, set by the protocol base class. |
| `Task<string> Download(RemoteEpisode, IIndexer)` | Push a release into the client. Return the download client ID (info-hash for torrents, nzo_id etc. for Usenet). |
| `IEnumerable<DownloadClientItem> GetItems()` | Snapshot the client's queue and history. Polled every refresh cycle. |
| `DownloadClientItem GetImportItem(item, previousAttempt)` | Optional rewrite of the item before import (e.g. Sabnzbd refreshes path on retry). Default returns input. |
| `void RemoveItem(item, deleteData)` | Used by `DownloadEventHub` after import / failure when `RemoveCompletedDownloads` / `RemoveFailedDownloads` is on. |
| `DownloadClientInfo GetStatus()` | Used to surface the client's output / category folder. |
| `void MarkItemAsImported(item)` | Optional post-import label/category move (qBittorrent `TvImportedCategory`, Transmission ≥4 labels). Default throws `NotSupportedException`. |

### What `DownloadClientBase<TSettings>` adds (`DownloadClientBase.cs`)
- Holds `IConfigService`, `IDiskProvider`, `IRemotePathMappingService`, `ILocalizationService`, `Logger`.
- Polly retry pipeline (`RetryStrategy`): 2 retries, 3 s base + jitter + exponential, on 5xx, 408, transport errors (lines 30–60).
- `Test()` wrapper that calls each provider's `Test(failures)` and catches exceptions into a `ValidationFailure`.
- `TestFolder(path, propName, mustBeWritable)` helper for blackholes / watch folders.
- `DeleteItemData(item)` — best-effort delete of an item's `OutputPath` (folder or file), used by torrent clients that delete-on-fail.
- Default `MarkItemAsImported` throws `NotSupportedException` (clients opt-in by overriding).

### What `TorrentClientBase` adds (`TorrentClientBase.cs`)
- Owns `IHttpClient`, `IBlocklistService`, `ITorrentFileInfoReader`.
- Implements `Download()` via two abstract hooks: `AddFromMagnetLink` and `AddFromTorrentFile`.
- Decides magnet-vs-torrent based on `PreferTorrentFile` (default `false`); if magnet fails with `NotSupportedException`, falls back to .torrent and vice-versa.
- `DownloadFromWebUrl` follows redirects manually, hand-converts a `magnet:` `Location:` header into a magnet add, throws `ReleaseUnavailableException` on 404/410, `ReleaseDownloadException` on other HTTP errors.
- Computes info-hash via `MonoTorrent.MagnetLink.Parse` (V1 or V2) and consults `IBlocklistService.BlocklistedTorrentHash` before adding (`EnsureReleaseIsNotBlocklisted`, lines 249–272). Throws `ReleaseBlockedException` on hit.
- Logs a warning when `actualHash != expectedHash` ("Sonarr could potentially lose track of the download in progress").

### What `UsenetClientBase` adds (`UsenetClientBase.cs`)
- Single abstract hook: `AddFromNzbFile(remoteEpisode, filename, fileContent)`.
- `Download()` GETs the NZB URL through the indexer's signed request, validates the bytes via `IValidateNzbs.Validate` (NzbValidationService), then calls `AddFromNzbFile`.

### Provider plumbing
| Type | File | Role |
|------|------|------|
| `DownloadClientDefinition` | `DownloadClientDefinition.cs` | DB-persisted ProviderDefinition. Adds `Priority`, `RemoveCompletedDownloads`, `RemoveFailedDownloads`. |
| `DownloadClientFactory` | `DownloadClientFactory.cs` | `ProviderFactory` subclass; filters disabled clients via `Active()`, exposes `DownloadHandlingEnabled`, drops blocked clients via `IDownloadClientStatusService`. |
| `DownloadClientProvider` | `DownloadClientProvider.cs` | Round-robin selector. Groups by `Priority`, orders by `Id`, remembers `lastUsedDownloadClient` per protocol in a cache, honours `Series.Tags` and `Indexer.DownloadClientId`. |
| `DownloadClientRepository` | `DownloadClientRepository.cs` | `ProviderRepository<DownloadClientDefinition>` (Marr ORM). |
| `DownloadClientStatusService` / Repository | `DownloadClientStatus*.cs` | Tracks consecutive failures with `MinimumTimeSinceInitialFailure = 5min`, `MaximumEscalationLevel = 5`. Exact same status pattern as indexers. |
| `DownloadClientItem` | `DownloadClientItem.cs` | Queue snapshot DTO (DownloadId, Title, TotalSize/RemainingSize, Status, OutputPath, IsEncrypted, CanBeRemoved, CanMoveFiles). |
| `DownloadClientInfo` | `DownloadClientInfo.cs` | Health / output path container returned by `GetStatus()`. |
| `DownloadClientType` | `DownloadClientType.cs` | **Legacy enum**, only the original four (Sabnzbd, Blackhole, Pneumatic, Nzbget) — comment-worthy: not the source of truth for new clients. |

## Lifecycle: grab → queue → import

| Stage | File / class | What happens |
|-------|--------------|--------------|
| 1. Decision arrives | `ProcessDownloadDecisions.ProcessDecisions` | Iterates prioritized decisions, decides between `Grabbed` / `Pending` / `Rejected` / `Failed` / `Skipped`. Once Usenet (or Torrent) fails for a client, *all subsequent* releases of that protocol go to `Pending(DownloadClientUnavailable)` so the run isn't blocked. |
| 2. Grab | `DownloadService.DownloadReport` | Gets candidate clients via `IProvideDownloadClient.GetDownloadClients`, rate-limits the indexer host to ≤2 grabs/sec, fetches `IIndexer`, calls `client.Download(remoteEpisode, indexer)`. Re-raises `ReleaseUnavailableException`, `ReleaseBlockedException`, `DownloadClientRejectedReleaseException`, `ReleaseDownloadException`. On success records indexer + client status, publishes `EpisodeGrabbedEvent`. |
| 3. History row | `DownloadHistoryService` (`History/`) handles `EpisodeGrabbedEvent`, `EpisodeImportedEvent`, `DownloadCompletedEvent`, `DownloadFailedEvent`, `DownloadIgnoredEvent` and writes a `DownloadHistory` row per event. Drives `DownloadAlreadyImported`, `GetLatestGrab`. |
| 4. Refresh queue | `DownloadMonitoringService` | Handles `RefreshMonitoredDownloadsCommand` and `Handle(EpisodeGrabbedEvent / EpisodeImportedEvent / ManualInteractionRequiredEvent)` via a 5-second `Debouncer`. For each enabled client calls `GetItems()` then `TrackedDownloadService.TrackDownload(...)`. After success records client status, after failure records failure. |
| 5. Track | `TrackedDownloadService` | In-memory `ICached<TrackedDownload>` keyed by `downloadId`. On first sight: parses title → `RemoteEpisode`, falls back to source title from history, fills `Indexer`, `Added`, `IndexerFlags`, calls `_aggregationService.Augment` and `_formatCalculator.ParseCustomFormat`. Maintains state machine: `Downloading → ImportPending → Importing → Imported`, plus `ImportBlocked`, `FailedPending → Failed`, `Ignored`. Cache reactive to `EpisodeInfoRefreshedEvent`, `SeriesAddedEvent`, `SeriesEditedEvent`, `SeriesBulkEditedEvent`, `SeriesDeletedEvent` — re-parses cached items so that adding a series surfaces matching downloads. |
| 6. Per-item check | `DownloadMonitoringService.ProcessClientItem` | Calls `FailedDownloadService.Check` then `CompletedDownloadService.Check`. The state transitions there move items into `FailedPending` / `ImportPending`. |
| 7. Process | `DownloadProcessingService` (handles `ProcessMonitoredDownloadsCommand`) | Iterates trackable items: `ImportPending` → `CompletedDownloadService.Import`; `FailedPending` → `FailedDownloadService.ProcessFailed`. Then calls `RemoveCompletedDownloads()` which raises `DownloadCanBeRemovedEvent` for each `Imported` item that `CanBeRemoved`. Finally publishes `DownloadsProcessedEvent`. |
| 8. Import | `CompletedDownloadService.Import` | Re-asks `IProvideImportItemService.ProvideImportItem` (which delegates to `IDownloadClient.GetImportItem` so e.g. Sabnzbd can fetch a fresh path). Calls `IDownloadedEpisodesImportService.ProcessPath`. Calls `VerifyImport`: counts imported episodes vs `RemoteEpisode.Episodes`, falls back to "all imported in history" check via `ITrackedDownloadAlreadyImported`. On success publishes `DownloadCompletedEvent`. On import-rejection delegates to `IRejectedImportService`; on `MultiSeason` rejection or partial import sets `ImportBlocked` and emits `ManualInteractionRequiredEvent` (one-shot — `HasNotifiedManualInteractionRequired`). |
| 9. Cleanup | `DownloadEventHub` | Handles `DownloadFailedEvent` / `DownloadCompletedEvent` / `DownloadCanBeRemovedEvent`. Calls `IDownloadClient.MarkItemAsImported` then, gated on `Definition.RemoveCompletedDownloads` / `RemoveFailedDownloads` and `DownloadItem.CanBeRemoved`, calls `IDownloadClient.RemoveItem(item, deleteData=true)` and `TrackedDownloadService.StopTracking`. Logs (does not crash) on `NotSupportedException`. |
| 10. Re-grab | `RedownloadFailedDownloadService` (handles `DownloadFailedEvent` with `EventHandleOrder.Last`) | If `AutoRedownloadFailed` is on (and `AutoRedownloadFailedFromInteractiveSearch` for IS releases), pushes `EpisodeSearchCommand` (single ep) or `SeasonSearchCommand` (multi). |

## Provider inventory

24 concrete download client classes. (`Vuze` and `Transmission` share `TransmissionBase`; `DownloadStation` ships as `TorrentDownloadStation` + `UsenetDownloadStation`; `Blackhole` ships as `TorrentBlackhole` + `UsenetBlackhole`.)

| # | Name | Protocol | Base class | File | Wire protocol / API | Notable quirks |
|---|------|----------|------------|------|---------------------|----------------|
| 1 | Sabnzbd | Usenet | `UsenetClientBase` | `Clients/Sabnzbd/Sabnzbd.cs` | HTTP+JSON, `mode=…&apikey=…` | Version regex `(\d+)\.(\d+)\.(\d+|x)` ; checks free space + history age; refreshes import path via `GetImportItem`. |
| 2 | Nzbget | Usenet | `UsenetClientBase` | `Clients/Nzbget/Nzbget.cs` | JSON-RPC | `_deleteFailedStatus = HEALTH/DUPE/SCAN/COPY/BAD`; throws `DownloadClientRejectedReleaseException` on duplicates. |
| 3 | NzbVortex | Usenet | `UsenetClientBase` | `Clients/NzbVortex/NzbVortex.cs` | HTTPS+JSON, session API key | Custom `JsonConverters/` (group state, file state); `NzbVortexAuthenticationException`, `NzbVortexNotLoggedInException`. |
| 4 | UsenetDownloadStation | Usenet | `UsenetClientBase` | `Clients/DownloadStation/UsenetDownloadStation.cs` | Synology DSM API (`DiskStationApi`) | Uses `SharedFolderResolver` + `SerialNumberProvider` to translate Synology shared folders → host paths. Two backend versions (`DownloadStation2Task` for newer DSM). |
| 5 | UsenetBlackhole | Usenet | `UsenetClientBase` | `Clients/Blackhole/UsenetBlackhole.cs` | filesystem (write `.nzb` to NzbFolder, scan WatchFolder) | Returns `null` download id; relies on `ScanWatchFolder` to surface items as `DownloadClientItem`. `ScanGracePeriod` to avoid importing in-flight files. |
| 6 | Pneumatic | Usenet | `DownloadClientBase` directly | `Clients/Pneumatic/Pneumatic.cs` | filesystem only — writes `.strm` for XBMC/Kodi | No queue; `MarkItemAsImported` not supported. |
| 7 | QBittorrent | Torrent | `TorrentClientBase` | `Clients/QBittorrent/QBittorrent.cs` | HTTP WebUI v1 *or* v2 selected by `QBittorrentProxySelector` | `MarkItemAsImported` switches torrent to `TvImportedCategory`; `_seedingTimeCache`; consults `ITagRepository` for label-tag mapping; `QBittorrentContentLayout` enum (Original / Subfolder / NoSubfolder). |
| 8 | Transmission | Torrent | `TransmissionBase` (extends `TorrentClientBase`) | `Clients/Transmission/Transmission.cs` | RPC over JSON | `SupportsLabels = HasClientVersion(4, 0)`; uses labels for `MarkItemAsImported` only on ≥4.0. |
| 9 | Vuze | Torrent | `TransmissionBase` | `Clients/Vuze/Vuze.cs` | Vuze's Transmission-compat plugin | `MINIMUM_SUPPORTED_PROTOCOL_VERSION = 14`; no labels. |
| 10 | Deluge | Torrent | `TorrentClientBase` | `Clients/Deluge/Deluge.cs` | JSON-RPC over HTTP, cookie session | `_hasAttemptedReconnecting` flag for session expiry retry; `DelugeLabel`, `DelugeError`, `DelugeUpdateUIResult`, `DelugeTorrentStatus` DTOs. |
| 11 | uTorrent | Torrent | `TorrentClientBase` | `Clients/uTorrent/UTorrent.cs` | WebUI HTTP + token | Per-torrent state cache (`UTorrentTorrentCache`); CSRF token bookkeeping; bitfield-encoded `UtorrentState`. |
| 12 | rTorrent | Torrent | `TorrentClientBase` | `Clients/rTorrent/RTorrent.cs` | XML-RPC (or SCGI via HTTP wrapper) | `RTorrentDirectoryValidator` checks subfolder vs flat layout; `PreferTorrentFile` likely true (rTorrent magnet limited). |
| 13 | Aria2 | Torrent | `TorrentClientBase` | `Clients/Aria2/Aria2.cs` | XML-RPC | `Aria2Containers` wraps the funky aria2 status response; uses `secret` auth token. |
| 14 | RQBit | Torrent | `TorrentClientBase` | `Clients/RQBit/RQBit.cs` | HTTP API | Uses `IDownloadSeedConfigProvider`; lightweight client, separate `ResponseModels/`. |
| 15 | Hadouken | Torrent | `TorrentClientBase` | `Clients/Hadouken/Hadouken.cs` | JSON-RPC HTTP | `Models/` for HadoukenTorrent etc. |
| 16 | Flood | Torrent | `TorrentClientBase` | `Clients/Flood/Flood.cs` | REST HTTP | Uses `IDownloadSeedConfigProvider` for seed limits; rich DTO tree under `Models/` and `Types/`. |
| 17 | TorrentDownloadStation | Torrent | `TorrentClientBase` | `Clients/DownloadStation/TorrentDownloadStation.cs` | DSM API | Same shared-folder mapping as Usenet variant. |
| 18 | TorrentFreeboxDownload | Torrent | `TorrentClientBase` | `Clients/FreeboxDownload/TorrentFreeboxDownload.cs` | Freebox HTTP API | Custom HMAC auth; `FreeboxDownloadEncoding`, `FreeboxDownloadPriority`. |
| 19 | TorrentBlackhole | Torrent | `TorrentClientBase` | `Clients/Blackhole/TorrentBlackhole.cs` | filesystem (`.torrent` or `.magnet` to TorrentFolder) | `SaveMagnetTo` setting; same `ScanWatchFolder` machinery as UsenetBlackhole. |
| 20 | TriblerDownloadClient | Torrent | `TorrentClientBase` | `Clients/Tribler/TriblerDownloadClient.cs` | Tribler REST API | Pairs with `Indexers.Tribler`. |

(Helpers / shared types: `DownloadClientSettingsBase.cs`, `TorrentSeedConfiguration.cs`, `Clients/DownloadStation/DiskStationApi.cs`, `Clients/Blackhole/ScanWatchFolder.cs`, `Clients/Blackhole/WatchFolderItem.cs`, `Extensions/XmlExtensions.cs`.)

## TrackedDownloadService and friends (`TrackedDownloads/`)

| Type | File | Role |
|------|------|------|
| `TrackedDownload` | `TrackedDownload.cs` | Stateful row keyed by `DownloadItem.DownloadId`. Holds the live `DownloadClientItem`, the most recent `ImportItem`, parsed `RemoteEpisode`, `Indexer`, `Added`, `Protocol`, `IsTrackable`, `HasNotifiedManualInteractionRequired`, `StatusMessages`, and the state-machine fields `State` (`TrackedDownloadState`) + `Status` (`Ok/Warning/Error`). `Warn(...)` is the canonical way to set a warning; `Fail()` flips state to `FailedPending` and forces `CanBeRemoved = true`. |
| `TrackedDownloadState` enum | same | `Downloading, ImportBlocked, ImportPending, Importing, Imported, FailedPending, Failed, Ignored`. |
| `TrackedDownloadService` | `TrackedDownloadService.cs` | Cache-backed in-memory store via `CacheManager.GetCache<TrackedDownload>(GetType())`. Reseeds `RemoteEpisode` from grab-history `SourceTitle` when title parsing fails. Maintains `IsTrackable` from the per-refresh visible set. |
| `DownloadMonitoringService` | `DownloadMonitoringService.cs` | The "tick". Reacts to `RefreshMonitoredDownloadsCommand` (scheduled job + manual) and the deprecated `CheckForFinishedDownloadCommand`. Logs `"deprecated CheckForFinishedDownload"` warning. |
| `TrackedDownloadAlreadyImported` | `TrackedDownloadAlreadyImported.cs` | Compares episode IDs against history — used by `CompletedDownloadService.VerifyImport` to handle "imported but DecisionEngine rejected re-import" case. |
| Events | `TrackedDownloadRefreshedEvent`, `TrackedDownloadsRemovedEvent` | Pushed via SignalR to the UI queue. |

`TrackedDownloadStatusMessage` (`TrackedDownloadStatusMessage.cs`) is the small `(Title, Messages[])` DTO for `StatusMessages`.

## Failed / Completed download handlers

| Service | File | Trigger | Effect |
|---------|------|---------|--------|
| `CompletedDownloadService` | `CompletedDownloadService.cs` | `Check()` from `DownloadMonitoringService.ProcessClientItem`; `Import()` from `DownloadProcessingService.Execute` | See lifecycle row 6/8. Marks `ImportBlocked` with `ManualInteractionRequiredEvent` if title doesn't parse, multi-season, or "matched by ID + non-interactive". |
| `FailedDownloadService` | `FailedDownloadService.cs` | `Check()` per refresh; `ProcessFailed()` per process; `MarkAsFailed(historyId/trackedDownload)` from REST API + UI | Reads grabbed history rows, builds `DownloadFailedEvent` (with episode IDs, Quality, `ReleaseSource`). Skips automatic handling if no grab history exists. |
| `RedownloadFailedDownloadService` | `RedownloadFailedDownloadService.cs` | `Handle(DownloadFailedEvent)` `EventHandleOrder.Last` | Pushes `EpisodeSearchCommand` (1 ep) or `SeasonSearchCommand` (multi). |
| `DownloadEventHub` | `DownloadEventHub.cs` | Handles `DownloadFailedEvent`, `DownloadCompletedEvent`, `DownloadCanBeRemovedEvent` | Calls `MarkItemAsImported` then `RemoveItem` per the per-client flags. |
| `IgnoredDownloadService` | `IgnoredDownloadService.cs` | UI action | Publishes `DownloadIgnoredEvent`; tracked download moves to `Ignored`. |
| `RejectedImportService` | `RejectedImportService.cs` | Called from `CompletedDownloadService.Import` for single-result rejections | Promotes specific rejection reasons (e.g. quality not wanted) to a `DownloadFailedEvent`. |

## Pending releases (`Pending/`)

| Type | File | Role |
|------|------|------|
| `PendingRelease` | `PendingRelease.cs` | DB row (`PendingReleases` table). Fields: `SeriesId, Title, Added, ParsedEpisodeInfo, Release, Reason, AdditionalInfo`. `RemoteEpisode` is rebuilt at load time and not persisted. |
| `PendingReleaseAdditionalInfo` | same | `SeriesMatchType`, `ReleaseSource`. |
| `PendingReleaseReason` | `PendingReleaseReason.cs` | `Delay = 0, DownloadClientUnavailable = 1, Fallback = 2`. |
| `PendingReleaseRepository` | `PendingReleaseRepository.cs` | Marr ORM repo. |
| `PendingReleaseService` | `PendingReleaseService.cs` | Authoritative service. Maintains a static `_pendingReleases` cache rebuilt from DB on every mutation. Handles `EpisodeGrabbedEvent` (drops same-or-lower quality pending), `RssSyncCompleteEvent` (drops rejected), `SeriesEditedEvent / SeriesUpdatedEvent / SeriesDeletedEvent / QualityProfileUpdatedEvent / ConfigSavedEvent / ApplicationStartedEvent` (re-augment cache). Builds `Queue.Queue` rows (with `EstimatedCompletionTime` based on `Release.PublishDate + DelayProfile.GetProtocolDelay + RssSyncInterval`), filters out blocked indexers. `MinimumAge` config also factors into delay. `GetQueueId` hashes `pending-{id}` (or `pending-{id}-ep{epId}` for the obsolete v3 path) into an int31. |
| `PendingReleasesUpdatedEvent` | `PendingReleasesUpdatedEvent.cs` | Fired on insert/delete; UI listens via SignalR. |

`DelayProfile` (per-tag preferred-protocol delay) lives in `Profiles/Delay/` — `PendingReleaseService` consumes `IDelayProfileService.AllForTags(...)` and `BestForTags(...)`.

`ProcessDownloadDecisions.PreparePending` is where decisions become pending entries; `Fallback` is used for duplicates after a higher-priority release was already grabbed.

## Settings persistence (overlap with embedded-docs)

`DownloadClientDefinition` is persisted to `DownloadClients` table. The `Settings` column is `TSettings` JSON serialised through `IEmbeddedDocument`. Each provider has `XxxSettings.cs` with FluentValidation rules:
- Connection: `Host, Port, UseSsl, UrlBase, Username, Password / ApiKey`.
- Categories / labels: `TvCategory`, `TvImportedCategory` (post-import), `RecentTvPriority`, `OlderTvPriority`.
- Torrent-specific: `InitialState`, `SequentialOrder`, `FirstAndLast`, `ContentLayout`.
- Watch / Nzb folders for blackholes and Pneumatic.
- See `embedded-docs/catalogue.md` (TBD) for field-by-field JSON shape.

Common base: `DownloadClientSettingsBase.cs`. Torrent seed configuration: `TorrentSeedConfiguration.cs` (ratio, seedTime, seasonPackSeedTime, drives `IDownloadSeedConfigProvider`).

## Aggregation (`Aggregation/`)
`IRemoteEpisodeAggregationService.Augment` runs every `IAggregateRemoteEpisode` augmenter on a `RemoteEpisode` with try/catch around each. Currently:
- `AggregateLanguages.cs` — fills `RemoteEpisode.Languages` from the parsed title + indexer hints.

Used both by `TrackedDownloadService` and `PendingReleaseService` so cached items stay in sync.

## Events raised
| Event | File | Producers | Consumers |
|-------|------|-----------|-----------|
| `EpisodeGrabbedEvent` | `EpisodeGrabbedEvent.cs` | `DownloadService.DownloadReport` | `DownloadHistoryService`, `DownloadMonitoringService`, `PendingReleaseService`, `History`, notifications |
| `DownloadCompletedEvent` | `DownloadCompletedEvent.cs` | `CompletedDownloadService.VerifyImport` | `DownloadEventHub`, `DownloadHistoryService`, notifications |
| `DownloadFailedEvent` | `DownloadFailedEvent.cs` | `FailedDownloadService.PublishDownloadFailedEvent` | `DownloadEventHub`, `RedownloadFailedDownloadService`, `DownloadHistoryService`, history, notifications |
| `DownloadIgnoredEvent` | `DownloadIgnoredEvent.cs` | `IgnoredDownloadService` | `DownloadHistoryService`, history |
| `DownloadCanBeRemovedEvent` | `DownloadCanBeRemovedEvent.cs` | `DownloadProcessingService.RemoveCompletedDownloads` | `DownloadEventHub` |
| `DownloadsProcessedEvent` | `DownloadsProcessedEvent.cs` | `DownloadProcessingService.Execute` end | `DownloadMonitoringService` |
| `ManualInteractionRequiredEvent` | `ManualInteractionRequiredEvent.cs` | `CompletedDownloadService.SetStateToImportBlocked` | notifications, `DownloadMonitoringService` (re-refresh) |
| `UntrackedDownloadCompletedEvent` | `UntrackedDownloadCompletedEvent.cs` | manual import | history |
| `TrackedDownloadRefreshedEvent` | `TrackedDownloads/TrackedDownloadRefreshedEvent.cs` | `DownloadMonitoringService` | SignalR queue updates |
| `TrackedDownloadsRemovedEvent` | `TrackedDownloads/TrackedDownloadsRemovedEvent.cs` | `TrackedDownloadService.StopTracking` | `DownloadMonitoringService` |
| `PendingReleasesUpdatedEvent` | `Pending/PendingReleasesUpdatedEvent.cs` | `PendingReleaseService.Insert/Delete` | SignalR queue updates |

## Jobs / commands
| Command | File | Trigger | Idempotent? |
|---------|------|---------|-------------|
| `RefreshMonitoredDownloadsCommand` | `RefreshMonitoredDownloadsCommand.cs` | scheduled (every minute) + bus debounce | yes |
| `CheckForFinishedDownloadCommand` | `CheckForFinishedDownloadCommand.cs` | **deprecated** alias retained for third-party callers (logs a warning) | yes |
| `ProcessMonitoredDownloadsCommand` | `ProcessMonitoredDownloadsCommand.cs` | `DownloadMonitoringService.Refresh` end | yes |

## DB tables touched
- `DownloadClients` — read/write (definitions).
- `DownloadClientStatus` — read/write (failure backoff).
- `PendingReleases` — read/write.
- `DownloadHistory` — write (events) / read (`DownloadAlreadyImported`, `GetLatestDownloadHistoryItem`, `GetLatestGrab`).
- `History` (EpisodeHistory) — read (grab lookup), write (history events through `IHistoryService`).
- `Blocklist` — read (`IBlocklistService.BlocklistedTorrentHash`).

## Notable quirks / TODOs / HACKs found in source
- `DownloadClientType.cs:1-10` — legacy enum with only the original four clients; *not* the source of truth for new clients. Treat it as historical metadata only.
- `TrackedDownloads/DownloadMonitoringService.cs:100` — `// TODO: Stop tracking items for the offline client` — when `GetItems()` throws, items linger in the cache.
- `Pending/PendingReleaseService.cs:67` — `private static List<PendingRelease> _pendingReleases = new();` — *static* mutable in-memory cache; mutated under no explicit lock. The cache is rebuilt from DB after every mutation but that pattern fights multi-instance scenarios. **Port concern.**
- `Pending/PendingReleaseService.cs:111` — `// TODO: Do we need IncludeRemoteEpisodes?` comment.
- `Pending/PendingReleaseService.cs:231` and the `*Obsolete` methods (`GetPendingQueueObsolete`, `FindPendingQueueItemObsolete`, `RemovePendingQueueItemsObsolete`) wrap the legacy v3 per-episode queue ID layout; suppress `CS0612`.
- `CompletedDownloadService.cs:235-242` — `EDGE CASE` comment: `VerifyImport` relies on episode IDs being stable across runs; if a series rebuild changes IDs, an already-imported download is treated as incomplete.
- `TorrentClientBase.cs:206` — when `actualHash != hash` "Sonarr could potentially lose track of the download in progress" — logged at Debug but never escalated. **Port concern**: choose whether to surface in queue.
- `DownloadMonitoringService.cs:166` — `"A third party app used the deprecated CheckForFinishedDownload command, it should be updated RefreshMonitoredDownloads instead"` — keep alias for back-compat.
- `Sabnzbd.cs:38` — version regex allows literal `x` patch (`(\d+)\.(\d+)\.(\d+|x)`) for git builds.
- `Deluge.cs:23` — `_hasAttemptedReconnecting` private field is single-threaded reconnect retry — not thread safe.
- `UTorrent.cs` — full per-torrent state cache (`UTorrentTorrentCache`) needed because uTorrent only ships state deltas via `CACHEID`.
- `QBittorrent.cs:56-60` — `MarkItemAsImported` switches torrent category to `TvImportedCategory` (post-import). Comment: "set post-import category".
- `Vuze.cs:16` — hardcoded minimum protocol version 14.
- `TrackedDownloadService.cs:147-150` — when title parse fails, falls back to parsing `firstHistoryItem.SourceTitle` and finally to `ParseSpecialEpisodeTitle`. `// TODO: Pass the TVDB ID and TVRage IDs in as well…` left in.
- `TorrentClientBase.cs:130-167` — manual redirect handling because the Polly pipeline is set to `AllowAutoRedirect = false` to capture magnet `Location:` headers.
- `DownloadClientFactory.cs:47-57` — `DownloadHandlingEnabled` filters blocked clients separately from `GetAvailableProviders` — duplicates the filter logic also in `DownloadClientProvider.GetFilteredDownloadClients` (lines 176-242). Two near-identical filter paths.
- `ProcessDownloadDecisions.cs:67-72, 97-110` — once a Usenet client fails for a single decision, **all subsequent** Usenet decisions in that batch go to `Pending` without trying *any* other Usenet client. This is intentional ("storing until later") but can mask multi-client setups. **Port concern**: confirm desired behaviour.
- `DownloadEventHub.cs:50-69` — `DownloadCompletedEvent` handler skips removal when `Status == Downloading` — this happens for some torrent clients that return `Downloading` even during seed. Order matters: `MarkItemAsImported` runs first (so the post-import category is set), then `RemoveItem` may be a no-op.
- `Blackhole/ScanWatchFolder.cs` — uses `ScanGracePeriod` to avoid picking up files the OS hasn't finished writing.
- `PendingRelease.cs:18` — `RemoteEpisode` deliberately not persisted (rebuilt by `PendingReleaseService.IncludeRemoteEpisodes`) — port must replicate.

## Test files (the port must keep passing equivalents)
Service-level fixtures (`src/NzbDrone.Core.Test/Download/`):
- `DownloadServiceFixture.cs`
- `DownloadClientStatusServiceFixture.cs`
- `NzbValidationServiceFixture.cs`
- `Aggregation/Aggregators/*` (language aggregator tests)
- `CompletedDownloadServiceTests/{ImportFixture,ProcessFixture}.cs`
- `DownloadApprovedReportsTests/DownloadApprovedFixture.cs`
- `DownloadHistoryTests/DownloadHistoryRepositoryFixture.cs`
- `FailedDownloadServiceTests/{ProcessFailedFixture,ProcessFixture}.cs`
- `DownloadClientFactory/*`
- `DownloadClientProvider/*`
- `TrackedDownloads/{TrackedDownloadAlreadyImportedFixture,TrackedDownloadServiceFixture}.cs`
- `Pending/PendingReleaseServiceTests/{AddFixture,PendingReleaseServiceFixture,RemoveGrabbedFixture,RemovePendingFixture,RemovePendingObsoleteFixture,RemoveRejectedFixture}.cs`

Per-client fixtures (`src/NzbDrone.Core.Test/Download/DownloadClientTests/`):
- `DownloadClientFixtureBase.cs`
- `Blackhole/{ScanWatchFolderFixture,TorrentBlackholeFixture,UsenetBlackholeFixture}.cs`
- `DelugeTests/DelugeFixture.cs`
- `DownloadStationTests/{DownloadStationsTaskStatusJsonConverterFixture,SerialNumberProviderFixture,SharedFolderResolverFixture,TorrentDownloadStationFixture,UsenetDownloadStationFixture}.cs`
- `FreeboxDownloadTests/TorrentFreeboxDownloadFixture.cs`
- `HadoukenTests/HadoukenFixture.cs`
- `NzbVortexTests/NzbVortexFixture.cs`
- `NzbgetTests/NzbgetFixture.cs`
- `PneumaticProviderFixture.cs`
- `QBittorrentTests/QBittorrentFixture.cs`
- `RQBitTests/RQBitFixture.cs`
- `RTorrentTests/RTorrentFixture.cs`
- `SabnzbdTests/{SabnzbdFixture.cs,JsonConvertersTests/*}`
- `TransmissionTests/{TransmissionFixture,TransmissionFixtureBase}.cs`
- `UTorrentTests/UTorrentFixture.cs`
- `VuzeTests/VuzeFixture.cs`

(No fixture exists for `Aria2`, `Flood`, or `Tribler` — the port should add coverage parity here. **Port concern.**)

## External services
None directly — every interaction is against the user's own download client over HTTP/RPC. Only outbound HTTP from this subsystem is the indexer download URL (handled by `IIndexer.GetDownloadRequest`) and the client API URL.

## Open questions
- **DOWNLOAD-Q1**: Does the Go port keep the static `_pendingReleases` cache in `PendingReleaseService` (Pending/PendingReleaseService.cs:67) or move to a request-scoped read of the repo? Static state is hostile to multi-instance / clustered deploys — but the existing semantics depend on `Handle(...)` events keeping it in sync. → unresolved.
- **DOWNLOAD-Q2**: When `actualHash != expectedHash` from `AddFromTorrentFile` / `AddFromMagnetLink` (TorrentClientBase.cs:206), should we surface a queue warning instead of just logging Debug? Today the user has no signal that a torrent was added under a different hash. → unresolved.
- **DOWNLOAD-Q3**: `ProcessDownloadDecisions` short-circuits the entire protocol after the first client failure (ProcessDownloadDecisions.cs:67-72). With multiple Usenet/Torrent clients available, is this the desired behaviour or a v3-era simplification? → unresolved.
- **DOWNLOAD-Q4**: `DownloadClientFactory.DownloadHandlingEnabled` and `DownloadClientProvider.GetFilteredDownloadClients` both apply the blocked-clients filter independently. Do we collapse them in the port? → unresolved.
- **DOWNLOAD-Q5**: `DownloadClientType` enum still exists with only the original 4 entries. Is anything reading it (UI legacy code, migration code)? Decide whether to keep it or drop it in the Go port. → unresolved.
- **DOWNLOAD-Q6**: `CheckForFinishedDownloadCommand` is logged as deprecated but still wired. How long do we keep the alias in the Go port — forever, behind a compatibility flag, or removed? → unresolved.
- **DOWNLOAD-Q7**: Aria2, Flood, and Tribler ship without unit tests. Do we add Go-side parity tests (recorded HTTP corpus) before porting them, or accept reduced coverage? → unresolved.
- **DOWNLOAD-Q8**: `TrackedDownloadService` cache is keyed on `downloadId` (a string from the client). Sabnzbd recycles `nzo_id`s after history wipe; uTorrent reuses hashes after add/remove. Are there cases the C# code already handles via the cache eviction that we need to preserve? → unresolved.
- **DOWNLOAD-Q9**: `CompletedDownloadService.VerifyImport` "EDGE CASE" comment (CompletedDownloadService.cs:235-242) — is the "episode ID stability" assumption still safe with the v5 episode ID regeneration on metadata refresh? → unresolved.
- **DOWNLOAD-Q10**: qBittorrent v1 vs v2 proxy split (`QBittorrentProxySelector`, `QBittorrentProxyV1.cs`, `QBittorrentProxyV2.cs`) — qBittorrent 5.x recently changed authentication. Confirm both versions still in real-world use before deciding which the Go port supports. → unresolved.
