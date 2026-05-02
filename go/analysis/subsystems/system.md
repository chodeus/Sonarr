# System (cross-cutting subsystems)

**Source:** `src/NzbDrone.Core/{Jobs,Queue,History,Blocklisting,Lifecycle,HealthCheck,Housekeeping,Backup,Update}/**`
**Target Go package(s):** `go/internal/system/...` (one sub-package per area)
**Status:** drafted

This card consolidates nine cross-cutting / runtime-glue subsystems. Each is independently small enough that a per-subsystem card would be wasteful; together they form the operational backbone (job scheduler, in-memory grab queue, historical log, blocklist, lifecycle (start/stop/restart) bus, health check engine, housekeeping cleaners, backup/restore, and self-update). All sit on top of `NzbDrone.Common` and the `Datastore` / `Messaging.{Commands,Events}` substrates and are wired together via `IEventAggregator` (`IEvent` / `IHandle<T>`) and the `IManageCommandQueue` (`Command` / `IExecute<T>`) buses.

---

## 1. Jobs (scheduler)

### Purpose
Periodic-task scheduler. On `ApplicationStartedEvent`, `TaskManager` reconciles a hard-coded list of "default tasks" (with intervals in minutes) against the `ScheduledTasks` table, removing rows for unknown task types and upserting the rest. A `System.Timers.Timer` ticks every 30 s in `Scheduler.ExecuteCommands` and pushes any task whose `LastExecution + Interval` has elapsed onto the command queue with `CommandTrigger.Scheduled`. The intervals for `RssSyncCommand` and `BackupCommand` are dynamic — they are recomputed whenever a `ConfigSavedEvent` fires (so that changing RSS sync interval or backup interval in the UI takes effect immediately). `TaskManager` also subscribes to `CommandExecutedEvent` to update each task's `LastExecution` / `LastStartTime` whenever its corresponding command finishes (gated on `Command.UpdateScheduledTask`).

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `ITaskManager` / `TaskManager` | `Jobs/TaskManager.cs:24` | Owns the list of scheduled tasks, the in-memory `ICached<ScheduledTask>` cache, and the default-task seeding logic. |
| `Scheduler` | `Jobs/Scheduler.cs:13` | 30-second `Timer` that pushes pending tasks onto the command queue. |
| `ScheduledTask` | `Jobs/ScheduledTask.cs:7` | DB row — `TypeName`, `Interval` (minutes), `LastExecution`, `Priority`, `LastStartTime`. Default `Priority = CommandPriority.Low`. |
| `IScheduledTaskRepository` / `ScheduledTaskRepository` | `Jobs/ScheduledTaskRepository.cs:8` | `IBasicRepository<ScheduledTask>` plus `GetDefinition(Type)` and `SetLastExecutionTime`. |

### Public services
- `ITaskManager.GetPending() / GetAll() / GetNextExecution(Type)`.

### DB tables
- `ScheduledTasks` (one row per default task; rows for unknown `TypeName` are deleted on every startup).

### Events raised / consumed
- Consumes: `ApplicationStartedEvent`, `ApplicationShutdownRequested`, `CommandExecutedEvent`, `ConfigSavedEvent`.
- Raises: none directly — pushes commands instead.

### Default tasks (every command type and its default schedule)
Hard-coded in `TaskManager.Handle(ApplicationStartedEvent)` (`Jobs/TaskManager.cs:67-135`):

| Command | TypeName | Interval (min) | Priority | Notes |
| --- | --- | --- | --- | --- |
| `RefreshMonitoredDownloadsCommand` | `NzbDrone.Core.Download.RefreshMonitoredDownloadsCommand` | 1 | High | Polls download clients. |
| `MessagingCleanupCommand` | `NzbDrone.Core.Messaging.Commands.MessagingCleanupCommand` | 5 | Low | Trims completed commands from in-memory queue. |
| `ApplicationUpdateCheckCommand` | `NzbDrone.Core.Update.Commands.ApplicationUpdateCheckCommand` | 6 * 60 = 360 | Low | Checks for updater package. |
| `UpdateSceneMappingCommand` | `NzbDrone.Core.DataAugmentation.Scene.UpdateSceneMappingCommand` | 3 * 60 = 180 | Low | Pulls scene-name mapping table. |
| `CheckHealthCommand` | `NzbDrone.Core.HealthCheck.CheckHealthCommand` | 6 * 60 = 360 | Low | Re-runs scheduled health checks. |
| `RefreshSeriesCommand` | `NzbDrone.Core.Tv.Commands.RefreshSeriesCommand` | 12 * 60 = 720 | Low | Refreshes series metadata from Skyhook. |
| `HousekeepingCommand` | `NzbDrone.Core.Housekeeping.HousekeepingCommand` | 24 * 60 = 1440 | Low | Runs all `IHousekeepingTask` impls. |
| `CleanUpRecycleBinCommand` | `NzbDrone.Core.MediaFiles.Commands.CleanUpRecycleBinCommand` | 24 * 60 = 1440 | Low | Empties recycle bin per `RecycleBinCleanupDays`. |
| `ImportListSyncCommand` | `NzbDrone.Core.ImportLists.ImportListSyncCommand` | 5 | Low | NB: 5 minutes (very aggressive — see JOBS-Q1). |
| `BackupCommand` | `NzbDrone.Core.Backup.BackupCommand` | `BackupInterval (1..7) * 60 * 24` | Low | Clamped to `[1, 7]` days. Recomputed on `ConfigSavedEvent`. |
| `RssSyncCommand` | `NzbDrone.Core.Indexers.RssSyncCommand` | `RssSyncInterval` (0 disables, <10 → 10) | Low | Recomputed on `ConfigSavedEvent`. |

`ImportListSyncCommand` is also a "scheduled" default. Note: the actual `ImportListSync` command itself only runs the sync logic when its body decides — the 5-minute interval is just how often the scheduler wakes it up.

### Quirks / TODOs / HACKs
- `Scheduler` and its `Timer` and `_cancellationTokenSource` are `static` fields — only one `Scheduler` instance per process is supported.
- Every startup wipes `ScheduledTasks` rows whose `TypeName` is not in the default list. Any custom/external job type silently disappears.
- `TaskManager.GetBackupInterval` clamps to `[1,7]` days; no warning is logged when the user-set value is out of range.
- `TaskManager.GetRssSyncInterval` returns `0` for negative input (which means "never run") but `10` for `< 10` positive — asymmetric handling.
- `Jobs/` only carries the scheduler. The actual command-bus implementation (`CommandQueue`, `IManageCommandQueue`, `CommandExecutor`, etc.) lives in `Messaging/Commands` and is covered in the `messaging` card.

### Test files
- `src/NzbDrone.Core.Test/JobTests/TestJobs.cs`
- `src/NzbDrone.Core.Test/JobTests/JobRepositoryFixture.cs`

### Open questions
- **JOBS-Q1**: `ImportListSyncCommand` interval is 5 minutes — that seems extremely aggressive given typical import list providers (Trakt list, Plex watchlist, IMDB list). Is this intentional, or a defensive ratchet? Should the Go port honor the same 5-minute cadence, or expose it as user-configurable?
- **JOBS-Q2**: Hard-coded interval/priority for each task in `TaskManager` makes adding new periodic commands a `TaskManager.cs` edit. Should the Go port move this to a registration-by-attribute pattern so that each command's class declares its own `[ScheduledEvery(int minutes)]`?
- **JOBS-Q3**: 30-second polling tick in `Scheduler` — drift up to 30 s on every interval. Is sub-30-s precision ever required? (The 1-minute `RefreshMonitoredDownloadsCommand` is the tightest and could in practice run every 60–90 s.)
- **JOBS-Q4**: `static Timer` and `static CancellationTokenSource` — port deliberately to single-instance? Or use a struct with a `*time.Ticker` per instance?

---

## 2. Queue (in-memory grab queue)

### Purpose
Read-only projection of the download-client tracked-download list, mapped per-episode (or per-tracked-download in the v5 path) for the UI's "Activity → Queue" view. There are *two* parallel implementations: `QueueService` (new, one row per tracked download with `Episodes: List<Episode>`) and `ObsoleteQueueService` (old, one row per episode with `[Obsolete] Episode`). Both subscribe to `TrackedDownloadRefreshedEvent` and rebuild a `static List<Queue>` from `TrackedDownload`s where `IsTrackable` is true, ordered by `RemainingTime`. After rebuild, each publishes its own `*UpdatedEvent` for SignalR push. There is **no DB table** — the queue lives entirely in memory and is recomputed on every refresh.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `Queue` | `Queue/Queue.cs:13` | DTO. Includes `Series`, `SeasonNumber`, `Episodes` (or `[Obsolete] Episode`), `Languages`, `Quality`, `Size`, `SizeLeft`, `TimeLeft`, `EstimatedCompletionTime`, `Status` (`QueueStatus`), `TrackedDownloadStatus`, `TrackedDownloadState`, `StatusMessages`, `DownloadId`, `RemoteEpisode`, `Protocol`, `DownloadClient`, `Indexer`, `OutputPath`, `ErrorMessage`. Id is a stable hash of `trackedDownload-{client}-{downloadId}` (and `-ep{epId}` in the obsolete impl). |
| `IQueueService` / `QueueService` | `Queue/QueueService.cs:14` | New (V5) impl — one row per tracked download. |
| `IObsoleteQueueService` / `ObsoleteQueueService` | `Queue/ObsoleteQueueService.cs:14` | Old impl, retained for the v3 API. Wrapped in `#pragma warning disable CS0612`. |
| `QueueStatus` | `Queue/QueueStatus.cs:3` | `Unknown, Queued, Paused, Downloading, Completed, Failed, Warning, Delay, DownloadClientUnavailable, Fallback`. |
| `DatetimeComparer` | `Queue/DatetimeComparer.cs:5` | `IComparer<DateTime?>` — null sorts last. |
| `TimeleftComparer` | `Queue/TimeleftComparer.cs:5` | `IComparer<TimeSpan?>` — null sorts last. |

### Public services
- `IQueueService.GetQueue() / Find(int) / Remove(int)`.
- `IObsoleteQueueService` mirrors the same surface.

### DB tables
- None — `static List<Queue> _queue` field per service.

### Events raised / consumed
- Consumes: `TrackedDownloadRefreshedEvent` (from `Download/TrackedDownloads/TrackedDownloadService`).
- Raises: `QueueUpdatedEvent` (`Queue/QueueUpdatedEvent.cs:5`), `ObsoleteQueueUpdatedEvent` (`Queue/ObsoleteQueueUpdatedEvent.cs:5`). Both are empty marker events — SignalR uses them as a "refresh queue" trigger.

### Jobs / `IExecute<T>`
- None.

### Quirks / TODOs / HACKs
- Two parallel services with near-identical mapping logic. Diffs: new returns `Episodes` list per row + `MappedSeasonNumber`; old explodes one row per episode and uses `Parser.Parser.RemoveFileExtension` (`FileExtensions.RemoveFileExtension` in the new path).
- `_queue` is `static List<Queue>` — mutated wholesale in `Handle(...)`. There is no lock; readers of `GetQueue()` can observe a list mid-rewrite. In practice the field reassignment is atomic (reference assignment) so the worst case is "stale view".
- `Remove(int)` mutates the in-memory list but does not actually instruct the download client to remove the item (callers need to invoke that separately via `IDownloadService` / `ITrackedDownloadService`).
- Both `DatetimeComparer` and `TimeleftComparer` are unused inside `Queue/` itself — they are referenced from elsewhere (Lookup likely in API paging).
- `Queue.Episode` is `[Obsolete]` but the property still exists because the v3 API uses it.

### Test files
- `src/NzbDrone.Core.Test/QueueTests/QueueServiceFixture.cs`
- `src/NzbDrone.Core.Test/QueueTests/ObsoleteQueueServiceFixture.cs`

### Open questions
- **QUEUE-Q1**: Two parallel implementations during the v3-API → v5-API transition. Does Go port both, or lift v5 only and rely on contract translation in the API layer?
- **QUEUE-Q2**: Static-field shared mutable state. Use `sync.RWMutex` + `[]*Queue` in Go? Or an atomically-swapped `*[]Queue` via `sync/atomic`?
- **QUEUE-Q3**: Should `QueueUpdatedEvent` carry the actual list (or a delta) instead of being a "go look at it" signal? Currently every consumer re-reads the full snapshot.
- **QUEUE-Q4**: `Queue.Id` is `HashConverter.GetHashInt31(...)` — int31 hash collisions are theoretically possible. Should Go port use a string id directly?

---

## 3. History (event log)

### Purpose
Append-only log of episode-level events: `Grabbed`, `SeriesFolderImported`, `DownloadFolderImported`, `DownloadFailed`, `EpisodeFileDeleted`, `EpisodeFileRenamed`, `DownloadIgnored`. Each row references a `SeriesId` + `EpisodeId`, carries `Quality` and `Languages`, and has a free-form `Dictionary<string,string> Data` blob for event-specific metadata (release group, indexer, custom format score, drop path, etc.). `HistoryService` subscribes to all the relevant download/import/delete events and writes rows; `SeriesDeletedEvent` cascades into a hard delete of `History` rows for the deleted series ids. Used by the UI history view, the activity panel, and `FindDownloadId` (linking imported files back to the original grab).

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `EpisodeHistory` | `History/EpisodeHistory.cs:10` | Row model. Constants: `DOWNLOAD_CLIENT`, `SERIES_MATCH_TYPE`, `RELEASE_SOURCE`, `RELEASE_GROUP`, `SIZE`, `INDEXER` (well-known keys in the `Data` dict). |
| `EpisodeHistoryEventType` | `History/EpisodeHistory.cs:37` | enum 0–7. |
| `IHistoryService` / `HistoryService` | `History/HistoryService.cs:17` | Public surface + handlers. |
| `IHistoryRepository` / `HistoryRepository` | `History/HistoryRepository.cs:11` | Joined queries (Series + Episode), paged with quality/language filters. |

### Public services
- `Paged(PagingSpec, languages[], qualities[])`, `MostRecentForEpisode`, `FindByEpisodeId`, `MostRecentForDownloadId`, `Get`, `GetBySeries`, `GetBySeason`, `GetByEpisode`, `Find(downloadId, eventType)`, `FindByDownloadId`, `FindDownloadId(EpisodeImportedEvent)`, `Since(DateTime, eventType?)`.

### DB tables
- `History` (mapped via `TableMapping`). Columns include `EventType`, `Date`, `Quality`, `SourceTitle`, `SeriesId`, `EpisodeId`, `DownloadId`, `Languages`, `Data` (serialized dict).

### Events raised / consumed
- Consumes: `EpisodeGrabbedEvent`, `EpisodeImportedEvent`, `DownloadFailedEvent`, `EpisodeFileDeletedEvent`, `EpisodeFileRenamedEvent`, `SeriesDeletedEvent`, `DownloadIgnoredEvent`.
- Raises: none — purely a write/read model.

### Jobs / `IExecute<T>`
- None.

### Quirks / TODOs / HACKs
- The paged-query language and quality filters use string `LIKE` patterns against the serialized JSON `Languages` / `Quality` columns — `BuildLanguageWhereClause` synthesizes four `LIKE` variants per language id to handle different OS line endings and the historical "single language → array" migration (`HistoryRepository.cs:178-196`).
- Quality filter does the same trick: `LIKE '%_quality_: {id},%'` (`HistoryRepository.cs:198-208`). Will not survive a raw rename of the JSON property.
- `EpisodeFileDeletedEvent` is *suppressed* (no row written) when `Reason == NoLinkedEpisodes` or `ManualOverride` — those are "internal cleanup" deletions, not user-visible.
- `FindDownloadId` walks the entire grabbed-but-not-imported set for a (series, quality) tuple and tries to match by episode id. Returns null if any episode has 0 or >1 candidates — quietly ambiguous.
- `Date` is always `DateTime.UtcNow`. Stored as UTC.
- `Handle(EpisodeGrabbedEvent)` writes one row per episode in `message.Episode.Episodes`, each with the same `DownloadId`. The same is true for import/fail/delete handlers — one history row per episode, not per release.

### Test files
- `src/NzbDrone.Core.Test/HistoryTests/HistoryServiceFixture.cs`
- `src/NzbDrone.Core.Test/HistoryTests/HistoryRepositoryFixture.cs`

### Open questions
- **HIST-Q1**: `Data` is a free-form `Dictionary<string,string>`. Should the Go port keep this loose schema (JSON column) or extract a typed `EventData` per `EventType`?
- **HIST-Q2**: The `LIKE`-pattern filter synthesis is fragile. Any change to how `Languages` or `Quality` is serialized breaks history filtering. Should Go port migrate to a normalized `HistoryLanguages` / `HistoryQualities` join table?
- **HIST-Q3**: There is no retention policy. History grows unbounded. Should the Go port add a max-rows-per-series cap, or a `History` housekeeper?

---

## 4. Blocklisting

### Purpose
Per-series blocklist of releases that have failed (or that the user explicitly blocked). Rows store `SeriesId`, `EpisodeIds`, `SourceTitle`, `Quality`, `Date`, `PublishedDate`, `Size`, `Protocol`, `Indexer`, `IndexerFlags`, `ReleaseType`, `Message`, `Source`, `TorrentInfoHash`, `Languages`. Used by the decision engine's `BlocklistedReleaseSpecification` to skip future grabs.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `Blocklist` | `Blocklisting/Blocklist.cs:12` | Row model. |
| `IBlocklistService` / `BlocklistService` | `Blocklisting/BlocklistService.cs:16` | Public surface; also handles `DownloadFailedEvent` (auto-blocklist on failure) and `SeriesDeletedEvent` (cascade delete). |
| `IBlocklistRepository` / `BlocklistRepository` | `Blocklisting/BlocklistRepository.cs:8` | Title and torrent-hash lookup; paged join on `Series`. |
| `ClearBlocklistCommand` | `Blocklisting/ClearBlocklistCommand.cs:5` | Manual "wipe blocklist" command. `SendUpdatesToClient = true`. |

### Public services
- `Blocklisted(seriesId, ReleaseInfo)`, `BlocklistedTorrentHash(seriesId, hash)`, `Paged(PagingSpec)`, `Block(RemoteEpisode, message, source)`, `Delete(int)`, `Delete(List<int>)`.

### DB tables
- `Blocklist`.

### Events raised / consumed
- Consumes: `DownloadFailedEvent` (auto-insert), `SeriesDeletedEvent` (cascade delete via `HandleAsync`).
- Raises: row inserts emit the standard `ModelEvent<Blocklist>` chain.
- Implements `IExecute<ClearBlocklistCommand>` → `_blocklistRepository.Purge()`.

### Jobs / `IExecute<T>`
- `ClearBlocklistCommand` (manual, no schedule).

### Quirks / TODOs / HACKs
- Torrent matching prefers `InfoHash` if available; falls back to `BlocklistedByTitle` + `Protocol == Torrent` + `SameTorrent`. NZB matching is `BlocklistedByTitle` + `Protocol == Usenet` + `SameNzb`. Both delegated to `ReleaseComparer`.
- `BlocklistedByTitle` uses `SourceTitle.Contains(sourceTitle)` — a substring match, not equality. Means a partial title can match the longer historical title.
- `BlocklistRepository.BlocklistedByTorrentInfoHash` also uses `Contains`, then refines in service-layer `Equals(..., InvariantCultureIgnoreCase)`. The double-filter is a defensive paranoid match.
- On `DownloadFailedEvent` the blocklist row is written via the *event handler*, not by the failure originator. This means a series that has been deleted between the grab and the failure can leave a dangling `SeriesId`. Cleanup is handled by `CleanupOrphanedBlocklist` housekeeper.
- `Block(RemoteEpisode, message, source)` is also exposed — used by the manual "block release" UI action.

### Test files
- `src/NzbDrone.Core.Test/Blocklisting/BlocklistRepositoryFixture.cs`
- `src/NzbDrone.Core.Test/Blocklisting/BlocklistServiceFixture.cs`
- `src/NzbDrone.Integration.Test/ApiTests/BlocklistFixture.cs`

### Open questions
- **BLOCKLIST-Q1**: Substring matching on `SourceTitle` is fragile (and can produce false positives — e.g., blocking "S01E01" and matching "S01E01.5"). Should Go port migrate to exact-equality + normalized-title?
- **BLOCKLIST-Q2**: There is no expiry / TTL on blocklist rows. A 5-year-old failure stays blocking forever. Add a configurable max-age?
- **BLOCKLIST-Q3**: `Block(RemoteEpisode, ...)` does not write `IndexerFlags` or `ReleaseType` — only the `DownloadFailedEvent` handler does. Inconsistent. Symbol of a missed migration?

---

## 5. Lifecycle

### Purpose
Tiny pubsub layer for the application's start/stop/restart flow. Three lifecycle events (`ApplicationStartingEvent`, `ApplicationStartedEvent`, `ApplicationShutdownRequested`) are emitted by the host and consumed throughout the codebase by services that need to do init / dispose / drain work. `LifecycleService` exposes `Shutdown()` and `Restart()` and implements two commands so that both can be triggered from the API.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `ILifecycleService` / `LifecycleService` | `Lifecycle/LifecycleService.cs:9` | `Shutdown()` / `Restart()` + `IExecute<ShutdownCommand>` / `IExecute<RestartCommand>`. `Restart()` sets `IRuntimeInfo.RestartPending = true` then publishes `ApplicationShutdownRequested(true)`. |
| `ApplicationStartingEvent` | `Lifecycle/ApplicationStartingEvent.cs:6` | `[LifecycleEvent]` marker. Emitted before service init. |
| `ApplicationStartedEvent` | `Lifecycle/ApplicationStartedEvent.cs:6` | `[LifecycleEvent]` marker. Emitted after init complete. |
| `ApplicationShutdownRequested` | `Lifecycle/ApplicationShutdownRequested.cs:6` | `[LifecycleEvent]`. Carries `bool Restarting`. |
| `ShutdownCommand` | `Lifecycle/Commands/ShutdownCommand.cs:5` | Empty command. |
| `RestartCommand` | `Lifecycle/Commands/RestartCommand.cs:5` | Empty command. |

### Public services
- `ILifecycleService.Shutdown() / Restart()`.

### DB tables
- None.

### Events raised / consumed
- Raises: `ApplicationShutdownRequested` (with `Restarting` set per call).
- Consumes: nothing internally — `LifecycleService` is event source, not sink.
- Note: `ApplicationStartingEvent` and `ApplicationStartedEvent` are raised from outside this directory (`NzbDrone.Host` startup pipeline) but their *type definitions* live here.

### Jobs / `IExecute<T>` registered
- `ShutdownCommand` (manual).
- `RestartCommand` (manual).

### Quirks / TODOs / HACKs
- `[LifecycleEvent]` attribute is from `NzbDrone.Common.Messaging` — it tells the `EventAggregator` to dispatch synchronously rather than via a thread-pool task, so handlers can do clean shutdown work in order.
- Subscribers of `ApplicationStartedEvent` are numerous and run in non-deterministic order (it's a `foreach` over registered handlers). `TaskManager.Handle(ApplicationStartedEvent)` and `Scheduler.Handle(ApplicationStartedEvent)` are particularly order-sensitive — `TaskManager` must populate the cache before `Scheduler.ExecuteCommands` ticks. In practice the timer waits 30 s before its first tick, which masks the ordering.

### Test files
- None directly; lifecycle wiring is exercised via host-level integration tests.

### Open questions
- **LIFE-Q1**: No explicit ordering of `ApplicationStartedEvent` handlers. Should the Go port introduce a dependency-aware startup graph (or run handlers serially in registration order)?
- **LIFE-Q2**: `_runtimeInfo.RestartPending` is the only state machinery for "restart vs. shutdown" — set by `Restart()` and read by the host post-shutdown. Good enough or worth modeling as a richer command result?

---

## 6. HealthCheck

### Purpose
Periodic and event-driven self-diagnostics. Each `IProvideHealthCheck` returns a `HealthCheck { Source, Type (Ok/Notice/Warning/Error), Reason (enum), Message, WikiUrl }`. `HealthCheckService` keeps a per-`Source.Name` cache of *failing* checks. On execution it pushes new failures (`HealthCheckFailedEvent`) and clears recovered ones (`HealthCheckRestoredEvent`); a `HealthCheckCompleteEvent` fires after every batch. Three execution paths:

1. **Startup**: on `ApplicationStartedEvent` it runs all checks where `CheckOnStartup == true` (default true).
2. **Scheduled**: `IExecute<CheckHealthCommand>` — runs `_scheduledHealthChecks` (those with `CheckOnSchedule == true`) on the manual/scheduled tick. If the command's `Trigger == Manual`, runs *all* checks.
3. **Event-driven**: `[CheckOn(typeof(SomeEvent))]` attributes register a check to fire when that event is raised. Filtered through `CheckOnCondition (Always | FailedOnly | SuccessfulOnly)` and an optional `ICheckOnCondition<TEvent>.ShouldCheckOnEvent` predicate. Debounced 5 s.

There is also a 15-minute startup grace period: until `_startupGracePeriodEndTime`, failure events are tagged with `IsInStartupGracePeriod = true` (so notifiers can suppress noisy startup failures). After the grace period elapses, the *first* unrelated event triggers a re-run of all startup checks.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `IHealthCheckService` / `HealthCheckService` | `HealthCheck/HealthCheckService.cs:16` | Engine. |
| `IProvideHealthCheck` | `HealthCheck/IProvideHealthCheck.cs:3` | `Check()`, `CheckOnStartup`, `CheckOnSchedule`. |
| `HealthCheckBase` | `HealthCheck/HealthCheckBase.cs:5` | Default `CheckOnStartup = true`, `CheckOnSchedule = true`. Holds `_localizationService`. |
| `HealthCheck` | `HealthCheck/HealthCheck.cs:8` | Result DTO. `WikiUrl = https://wiki.servarr.com/sonarr/system{#fragment}`. |
| `HealthCheckResult` | `HealthCheck/HealthCheck.cs:48` | `Ok=0, Notice=1, Warning=2, Error=3`. |
| `HealthCheckReason` | `HealthCheck/HealthCheck.cs:56` | ~58-value enum, one per distinct failure mode. |
| `CheckOnAttribute` | `HealthCheck/CheckOnAttribute.cs:5` | `(Type EventType, CheckOnCondition Condition = Always)`. AllowMultiple. |
| `CheckOnCondition` | `HealthCheck/CheckOnAttribute.cs:18` | `Always, FailedOnly, SuccessfulOnly`. |
| `ICheckOnCondition<TEvent>` | `HealthCheck/ICheckOnCondition.cs:3` | Optional per-event predicate. |
| `IEventDrivenHealthCheck` / `EventDrivenHealthCheck<T>` | `HealthCheck/EventDrivenHealthCheck.cs:5` | Internal wrapper. |
| `HealthCheckFailedEvent` / `HealthCheckRestoredEvent` / `HealthCheckCompleteEvent` | `HealthCheck/*.cs` | Pubsub. |
| `CheckHealthCommand` | `HealthCheck/CheckHealthCommand.cs:5` | Trigger command. |
| `ServerSideNotificationService` | `HealthCheck/ServerSideNotificationService.cs:14` | Special "remote notification" health check; talks to `services.sonarr.tv/notification`. |

### Public services
- `IHealthCheckService.Results()` — list of currently failing checks.

### DB tables
- None — failure cache is in-memory `ICached<HealthCheck>`.

### Events raised / consumed
- Raises: `HealthCheckFailedEvent`, `HealthCheckRestoredEvent`, `HealthCheckCompleteEvent`.
- Consumes: `ApplicationStartedEvent`, `IEvent` (catch-all for event-driven dispatch), `CheckHealthCommand`.

### Jobs / `IExecute<T>`
- `CheckHealthCommand` (scheduled at 6h via `TaskManager`, also manual).

### HealthChecks (every concrete `IHealthCheck`)
All under `src/NzbDrone.Core/HealthCheck/Checks/`. Default `CheckOnStartup = true`, `CheckOnSchedule = true` unless overridden.

| File | Class | Triggers (`CheckOn` attrs) | What it checks |
| --- | --- | --- | --- |
| `ApiKeyValidationCheck.cs` | `ApiKeyValidationCheck` | `ApplicationStartedEvent`, `ConfigSavedEvent` | API key length ≥ 20. |
| `AppDataLocationCheck.cs` | `AppDataLocationCheck` | (startup/schedule only) | AppData folder is not inside the startup folder (prevents wiping on update). |
| `DownloadClientCheck.cs` | `DownloadClientCheck` | `ProviderAdded/Updated/Deleted/StatusChanged<IDownloadClient>` | At least one client exists; each enabled client's `GetItems()` succeeds. |
| `DownloadClientRemovesCompletedDownloadsCheck.cs` | `DownloadClientRemovesCompletedDownloadsCheck` | `ProviderUpdated/Deleted<IDownloadClient>`, `ModelEvent<RootFolder>`, `ModelEvent<RemotePathMapping>` | Warns if any download client is configured to auto-remove completed downloads (which would race Sonarr's import). |
| `DownloadClientRootFolderCheck.cs` | `DownloadClientRootFolderCheck` | same as above | Warns if a download client's output folder equals or is a parent of any configured Sonarr root folder. |
| `DownloadClientSortingCheck.cs` | `DownloadClientSortingCheck` | same as above | Warns if a download client has a non-empty sorting mode (qBittorrent etc.) — that conflicts with Sonarr's import. |
| `DownloadClientStatusCheck.cs` | `DownloadClientStatusCheck` | `ProviderUpdated/Deleted/StatusChanged<IDownloadClient>` | Reports clients that are in back-off (any vs. all). |
| `ImportListRootFolderCheck.cs` | `ImportListRootFolderCheck` | `ProviderUpdated/Deleted<IImportList>`, `ModelEvent<RootFolder>`, `SeriesDeletedEvent`, `SeriesMovedEvent`, `EpisodeImportedEvent (FailedOnly)`, `EpisodeImportFailedEvent (SuccessfulOnly)` | Each import list's `RootFolderPath` exists, is OS-valid, and is one of the configured root folders. |
| `ImportListStatusCheck.cs` | `ImportListStatusCheck` | `ProviderUpdated/Deleted/StatusChanged<IImportList>` | Reports import lists in back-off (any vs. all). |
| `ImportMechanismCheck.cs` | `ImportMechanismCheck` | `ProviderUpdated/Deleted<IDownloadClient>`, `ConfigSavedEvent` | If `EnableCompletedDownloadHandling` not yet defined (legacy), nudges the user. If disabled, warns. Special-cases Sabnzbd / Nzbget / multi-computer setups. |
| `IndexerDownloadClientCheck.cs` | `IndexerDownloadClientCheck` | `ProviderUpdated/Deleted<IIndexer>`, `ProviderUpdated/Deleted<IDownloadClient>` | Indexers configured with a specific `DownloadClientId` whose target client has been deleted/disabled. |
| `IndexerJackettAllCheck.cs` | `IndexerJackettAllCheck` | `ProviderAdded/Updated/Deleted/StatusChanged<IIndexer>` | Warns about Jackett `/torznab/all/api` aggregate endpoint usage (loses per-indexer error/state). |
| `IndexerLongTermStatusCheck.cs` | `IndexerLongTermStatusCheck` | `ProviderUpdated/Deleted/StatusChanged<IIndexer>` | Indexers in back-off whose initial failure was > 6 hours ago (any vs. all). |
| `IndexerRssCheck.cs` | `IndexerRssCheck` | `ProviderAdded/Updated/Deleted/StatusChanged<IIndexer>` | At least one indexer with RSS enabled is available. |
| `IndexerSearchCheck.cs` | `IndexerSearchCheck` | same as RssCheck | At least one indexer with automatic search enabled and one with interactive search; at least one currently active. |
| `IndexerStatusCheck.cs` | `IndexerStatusCheck` | `ProviderUpdated/Deleted/StatusChanged<IIndexer>` | Same as long-term but for `< 6 hours ago` failures (short-term). |
| `MountCheck.cs` | `MountCheck` | (startup/schedule only) | None of the series-folder mounts are read-only. |
| `NotificationStatusCheck.cs` | `NotificationStatusCheck` | `ProviderUpdated/Deleted/StatusChanged<INotification>` | Notification providers in back-off (any vs. all). |
| `PackageGlobalMessageCheck.cs` | `PackageGlobalMessageCheck` | (startup/schedule only) | Surfaces a `IDeploymentInfoProvider.PackageGlobalMessage` (set by package maintainers) — `Error:` / `Warn:` prefixes upgrade severity. |
| `ProxyCheck.cs` | `ProxyCheck` | `ConfigSavedEvent` | If proxy enabled: hostname resolves AND `services.sonarr.tv/ping` does not return 400. |
| `RecyclingBinCheck.cs` | `RecyclingBinCheck` | `EpisodeImportedEvent (FailedOnly)`, `EpisodeImportFailedEvent (SuccessfulOnly)` | If `RecycleBin` config set, that folder is writable. |
| `RemotePathMappingCheck.cs` | `RemotePathMappingCheck` | `ProviderUpdated/Deleted<IDownloadClient>`, `ModelEvent<RemotePathMapping>`, `EpisodeImportFailedEvent (SuccessfulOnly)` | Big check: download-client output folders are valid for current OS, exist, and (on import failure) the failed file's path is reachable. Has a separate `Check(IEvent)` overload for failure-event diagnosis. |
| `RemovedSeriesCheck.cs` | `RemovedSeriesCheck` | `SeriesUpdatedEvent`, `SeriesDeletedEvent`, `SeriesRefreshCompleteEvent` | Series with `Status == SeriesStatusType.Deleted` (ie. removed from TheTVDB). Implements `ICheckOnCondition` for both events to skip when the deleted-status flag isn't set. |
| `RootFolderCheck.cs` | `RootFolderCheck` | `SeriesDeletedEvent`, `SeriesMovedEvent`, `EpisodeImportedEvent (FailedOnly)`, `EpisodeImportFailedEvent (SuccessfulOnly)` | All series root folders are OS-valid + exist; warns if any is empty. |
| `SystemTimeCheck.cs` | `SystemTimeCheck` | (startup/schedule only) | System UTC time is within ±1 day of `services.sonarr.tv/time`. |
| `UpdateCheck.cs` | `UpdateCheck` | `ConfigFileSavedEvent` | If automatic BuiltIn update enabled (and not Docker): startup folder + UI folder writable, no AppTranslocation; if build is older than 14 days, warn that an update is available. |
| `ServerSideNotificationService.cs` | `ServerSideNotificationService` | (startup/schedule only) | Reads `/notification` from services.sonarr.tv and surfaces the first notification as a health check. (This file lives in the parent `HealthCheck/` folder, not `Checks/`.) |

**Total concrete `IHealthCheck` implementations: 27** (26 in `Checks/` + 1 `ServerSideNotificationService`).

### Quirks / TODOs / HACKs
- `HealthCheckService.HandleAsync(IEvent)` is the single dispatcher — invoked for every event in the system. It returns early for `HealthCheckCompleteEvent` and `ApplicationStartedEvent`, then for everything else does the grace-period rerun + event-driven dispatch dance.
- "Grace period rerun" logic in `HandleAsync(IEvent)` is subtle: the *first* event after the grace window expires re-runs all startup checks, then explicitly republishes `HealthCheckFailedEvent` for everything still failing (so notifiers that suppressed startup-grace-period failures get a real notification).
- 5-second debouncer for event-driven checks.
- `RemotePathMappingCheck.Check(IEvent)` — a sibling overload to `Check()` — is **not invoked by the engine**. The engine always calls `IProvideHealthCheck.Check()`. This overload exists but appears to be dead code as wired (or expected to be invoked through some other path). Investigate.
- `UpdateCheck` listens for `ConfigFileSavedEvent` (the file-on-disk one) rather than the more common `ConfigSavedEvent` (DB row).
- `WikiUrl` builder lowercases + strips non-letters + replaces spaces with hyphens to derive a wiki anchor from the message — fragile if the wiki rewrites a heading.
- `HealthCheck.Source` is `Type` — the *type itself*, used as a stable identity key.

### Test files
- `src/NzbDrone.Core.Test/HealthCheck/HealthCheckFixture.cs`
- `src/NzbDrone.Core.Test/HealthCheck/HealthCheckServiceFixture.cs`
- `src/NzbDrone.Core.Test/HealthCheck/Checks/*Fixture.cs` (one per check that has a fixture).

### Open questions
- **HEALTH-Q1**: 27 health checks with a hand-coded `HealthCheckReason` enum (~58 values). Should Go port keep the enum, or use string keys + a registry?
- **HEALTH-Q2**: `RemotePathMappingCheck.Check(IEvent)` overload — is it dead code, or invoked through reflection somewhere we missed?
- **HEALTH-Q3**: `_pendingHealthChecks` is a `HashSet` guarded by a `lock`. The "check inflight while another check requests it" semantics is "drop the duplicate" — is that correct?
- **HEALTH-Q4**: 15-minute startup grace period is hard-coded. Should it be configurable, or removed entirely (notifiers now gate on `IsInStartupGracePeriod`)?
- **HEALTH-Q5**: How does the `IEvent` catch-all handler interact with `[LifecycleEvent]` attribute (which forces sync dispatch)? Lifecycle events still flow through it.

---

## 7. Housekeeping

### Purpose
Periodic database/disk cleanup. Runs once a day (1440 min) via `HousekeepingCommand`. `HousekeepingService` iterates every registered `IHousekeepingTask`, swallows per-task exceptions (so one failure doesn't abort the rest), then `VACUUM`s the main DB (regardless of which tasks ran). Tasks fall into three buckets: **orphan cleanup** (delete child rows whose FK target was deleted), **format/state repair** (e.g. clean future timestamps), and **disk cleanup** (sandbox folders, bad images, unused tags). All except `CleanupCommandQueue`, `CleanupExtraFilesInExcludedFolders`, `CleanupQualityProfileFormatItems`, `CleanupTemporaryUpdateFiles`, `DeleteBadMediaCovers`, `FixFutureProviderStatusTimes` derivatives, `TrimLogDatabase`, and `UpdateCleanTitleForSeries` are raw `Dapper` `DELETE` SQL.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `IHousekeepingTask` | `Housekeeping/IHousekeepingTask.cs:3` | Single-method `void Clean()`. |
| `HousekeepingService` | `Housekeeping/HousekeepingService.cs:9` | `IExecute<HousekeepingCommand>` — runs all `IHousekeepingTask`s, then `VACUUM`s `IMainDatabase`. |
| `HousekeepingCommand` | `Housekeeping/HousekeepingCommand.cs:5` | `IsExclusive = true` (won't overlap itself). |

### Public services
- None — internal-only, runs via command bus.

### DB tables
- Touches every major table indirectly via the DELETE statements.

### Events raised / consumed
- Consumes: `HousekeepingCommand` (via `IExecute`).
- Raises: row-level `ModelEvent<T>` cascades from each delete.

### Jobs / `IExecute<T>`
- `HousekeepingCommand` — scheduled daily by `TaskManager`.

### Housekeeping tasks (every concrete `IHousekeepingTask`)
All under `src/NzbDrone.Core/Housekeeping/Housekeepers/`.

| File | Class | What it cleans up |
| --- | --- | --- |
| `CleanupAbsolutePathMetadataFiles.cs` | `CleanupAbsolutePathMetadataFiles` | `MetadataFiles` rows whose `RelativePath` is actually an absolute path (`X:\…`, `\…`, `/…`) — historical bug residue. SQLite vs. PostgreSQL split. |
| `CleanupAdditionalNamingSpecs.cs` | `CleanupAdditionalNamingSpecs` | All but the first row of `NamingConfig` (singleton config). |
| `CleanupAdditionalUsers.cs` | `CleanupAdditionalUsers` | All but the first row of `Users`. |
| `CleanupCommandQueue.cs` | `CleanupCommandQueue` | Calls `IManageCommandQueue.CleanCommands()` — trims completed commands from the in-memory queue. |
| `CleanupDownloadClientUnavailablePendingReleases.cs` | `CleanupDownloadClientUnavailablePendingReleases` | `PendingReleases` older than 14 days with `Reason ∈ {DownloadClientUnavailable, Fallback}`. SQLite vs. PostgreSQL split. |
| `CleanupDuplicateMetadataFiles.cs` | `CleanupDuplicateMetadataFiles` | Duplicate `MetadataFiles` rows for series-metadata (Type=1), episode-metadata (Type=2), and episode-images (Type=5) — keeps the highest Id. |
| `CleanupExtraFilesInExcludedFolders.cs` | `CleanupExtraFilesInExcludedFolders` | Per-series, removes `ExtraFiles` rows whose path is filtered out by `IDiskScanService.FilterPaths` (subtitles in excluded sample folders, etc.). |
| `CleanupOrphanedBlocklist.cs` | `CleanupOrphanedBlocklist` | `Blocklist` rows whose `SeriesId` no longer exists. |
| `CleanupOrphanedDownloadClientStatus.cs` | `CleanupOrphanedDownloadClientStatus` | `DownloadClientStatus` rows whose `ProviderId` no longer exists in `DownloadClients`. |
| `CleanupOrphanedEpisodeFiles.cs` | `CleanupOrphanedEpisodeFiles` | `EpisodeFiles` rows that no `Episodes.EpisodeFileId` references. |
| `CleanupOrphanedEpisodes.cs` | `CleanupOrphanedEpisodes` | `Episodes` whose `SeriesId` no longer exists. |
| `CleanupOrphanedExtraFiles.cs` | `CleanupOrphanedExtraFiles` | `ExtraFiles` orphaned by series, by episode-file, or where `EpisodeFileId = 0`. |
| `CleanupOrphanedHistoryItems.cs` | `CleanupOrphanedHistoryItems` | `History` rows whose `SeriesId` or `EpisodeId` no longer exists. |
| `CleanupOrphanedImportListStatus.cs` | `CleanupOrphanedImportListStatus` | `ImportListStatus` rows orphaned in `ImportLists`. |
| `CleanupOrphanedIndexerStatus.cs` | `CleanupOrphanedIndexerStatus` | `IndexerStatus` rows orphaned in `Indexers`. |
| `CleanupOrphanedMetadataFiles.cs` | `CleanupOrphanedMetadataFiles` | `MetadataFiles` orphaned by series, by episode-file, or where `EpisodeFileId = 0` and `Type ∈ {2,5}`. |
| `CleanupOrphanedNotificationStatus.cs` | `CleanupOrphanedNotificationStatus` | `NotificationStatus` rows orphaned in `Notifications`. |
| `CleanupOrphanedPendingReleases.cs` | `CleanupOrphanedPendingReleases` | `PendingReleases` rows whose `SeriesId` no longer exists. |
| `CleanupOrphanedSubtitleFiles.cs` | `CleanupOrphanedSubtitleFiles` | `SubtitleFiles` orphaned by series, by episode-file, or where `EpisodeFileId = 0`. |
| `CleanupQualityProfileFormatItems.cs` | `CleanupQualityProfileFormatItems` | For each `QualityProfile`: removes references to deleted `CustomFormat`s; inserts entries for newly-added formats with `Score=0`; resets `MinFormatScore/CutoffFormatScore/MinUpgradeFormatScore` if profile now has no formats. Only writes back changed profiles via `SetFields`. |
| `CleanupTemporaryUpdateFiles.cs` | `CleanupTemporaryUpdateFiles` | Recursively deletes the update sandbox folder (`IAppFolderInfo.GetUpdateSandboxFolder()`). |
| `CleanupUnusedTags.cs` | `CleanupUnusedTags` | Deletes `Tags` rows not referenced by any of: Series, Notifications, DelayProfiles, ReleaseProfiles, ImportLists, Indexers, AutoTagging, DownloadClients, or AutoTagging tag-specifications. SQLite vs. PostgreSQL split. |
| `DeleteBadMediaCovers.cs` | `DeleteBadMediaCovers` | One-shot (gated on `IConfigService.CleanupMetadataImages`): scans every `*.jpg` MetadataFile newer than 2014-12-27 and deletes it if its first 10 bytes look like HTML (i.e., a stale 404-error blob). Sets the config flag to false after running. |
| `FixFutureDownloadClientStatusTimes.cs` | `FixFutureDownloadClientStatusTimes` | Generic `FixFutureProviderStatusTimes<DownloadClientStatus>` — clamps `DisabledTill`, `InitialFailure`, `MostRecentFailure` to `≤ now`. |
| `FixFutureImportListStatusTimes.cs` | `FixFutureImportListStatusTimes` | Same for `ImportListStatus`. |
| `FixFutureIndexerStatusTimes.cs` | `FixFutureIndexerStatusTimes` | Same for `IndexerStatus`. |
| `FixFutureNotificationStatusTimes.cs` | `FixFutureNotificationStatusTimes` | Same for `NotificationStatus`. |
| `FixFutureProviderStatusTimes.cs` | `FixFutureProviderStatusTimes<TModel>` | **Abstract base class** — the four subclasses above add the concrete repository binding. Recomputes `DisabledTill = now + EscalationBackOff.Periods[level]`. |
| `FixFutureRunScheduledTasks.cs` | `FixFutureRunScheduledTasks` | `UPDATE ScheduledTasks SET LastExecution = now WHERE LastExecution > now`. (Skipped in debug builds: logs only.) |
| `TrimLogDatabase.cs` | `TrimLogDatabase` | Calls `ILogRepository.Trim()` (gated on `IConfigFileProvider.LogDbEnabled`). |
| `UpdateCleanTitleForSeries.cs` | `UpdateCleanTitleForSeries` | Recomputes `Series.CleanTitle = Title.CleanSeriesTitle()` and saves rows where it changed. |

**Total concrete `IHousekeepingTask` implementations: 32** (31 implementing-only classes + 1 abstract base — 31 if you count only those directly registered. The four `FixFuture*ProviderStatusTimes<>` subclasses and 27 others = 31. Let's call it **31 housekeeping tasks**.)

(Recount: the directory contains 33 .cs files. `FixFutureProviderStatusTimes.cs` is abstract (not registered). `IHousekeepingTask` is the interface in the parent dir. So **registered concrete tasks = 31**.)

### Quirks / TODOs / HACKs
- Per-task `try/catch` swallows exceptions — silent failure of a single housekeeper just produces an error log line, the rest continue.
- Final `_mainDb.Vacuum()` runs unconditionally (after all tasks). Comment notes the log DB has its own vacuum elsewhere.
- `CleanupAdditionalUsers` and `CleanupAdditionalNamingSpecs` enforce singleton tables by deleting all-but-first. Curiously aggressive — multiple users / configs are quietly dropped.
- `FixFutureRunScheduledTasks` no-ops in debug (`BuildInfo.IsDebug`) — for fast iteration.
- `DeleteBadMediaCovers` self-disables: sets `CleanupMetadataImages = false` after one run. This is a "fix" for a 2014-vintage bug; it lives on as a one-shot reset.
- SQLite/PostgreSQL split SQL for `IN` vs. `ANY(...)` and `LIKE` patterns with backslashes. The Go port should pick a single SQL shape (parameterized arrays are ANSI-friendly via PostgreSQL anyway).
- `CleanupQualityProfileFormatItems` is the only housekeeper that *modifies* rows rather than deleting — it normalizes profile.FormatItems against the current `CustomFormat` set.

### Test files
- 22 fixtures under `src/NzbDrone.Core.Test/Housekeeping/Housekeepers/*Fixture.cs` (not all 31 tasks have tests — the simple "delete rows whose FK is null" ones often share patterns).

### Open questions
- **HOUSE-Q1**: Many housekeepers are raw SQL `DELETE`s — Go port options: keep raw SQL, or wrap in a single `OrphanCleaner[T]` generic? The latter is cleaner but loses the ability to do compound deletes (e.g., the three-step `CleanupOrphanedExtraFiles`).
- **HOUSE-Q2**: SQLite-vs-PG branching is repeated in three places (`CleanupAbsolutePathMetadataFiles`, `CleanupDownloadClientUnavailablePendingReleases`, `CleanupUnusedTags`). Centralize via the database abstraction?
- **HOUSE-Q3**: `DeleteBadMediaCovers` is a one-shot that's been around for a decade. Drop entirely in Go port?
- **HOUSE-Q4**: Per-task try/catch swallows everything. Should we surface a `HousekeepingFailedEvent` so health checks can warn the user?
- **HOUSE-Q5**: Daily cadence is hard-coded. Configurable?
- **HOUSE-Q6**: `Vacuum` after every run can be expensive for large DBs. Worth gating on a "rows deleted > threshold" signal?

---

## 8. Backup

### Purpose
On-demand or scheduled (default 7-day) backup of `sonarr.db` + `config.xml` into a zip under `Backups/{scheduled|manual|update}/sonarr_backup_v{ver}_{yyyy.MM.dd_HH.mm.ss}.zip`. Scheduled and `Update`-type backups also run a retention sweep (`BackupRetention` days). Restore unzips and moves `Config.xml` and `sonarr.db` (or legacy `nzbdrone.db`) into the AppData folder, where they'll be applied next startup.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `Backup` | `Backup/Backup.cs:5` | DTO: `Name`, `Type`, `Size`, `Time`. |
| `BackupType` | `Backup/BackupCommand.cs:25` | `Scheduled=0, Manual=1, Update=2`. |
| `IBackupService` / `BackupService` | `Backup/BackupService.cs:20` | `Backup(BackupType) / GetBackups() / Restore(filename) / GetBackupFolder() / GetBackupFolder(BackupType)`. |
| `BackupCommand` | `Backup/BackupCommand.cs:5` | Command. `Type` derived from `Trigger` (Scheduled→Scheduled, else Manual). `SendUpdatesToClient = true`. `UpdateScheduledTask = (Type == Scheduled)`. |
| `IMakeDatabaseBackup` / `MakeDatabaseBackup` | `Backup/MakeDatabaseBackup.cs:8` | SQLite `BackupDatabase` API wrapper. Forces `journal_mode=truncate` (NOT WAL) on the backup target — comment says "WAL has issues when page sizes change". |
| `RestoreBackupFailedException` | `Backup/RestoreBackupFailedException.cs:6` | API client exception (HTTP-ish status). |

### Public services
- `IBackupService.{Backup, GetBackups, Restore, GetBackupFolder}`.

### DB tables
- None.

### Events raised / consumed
- Consumes: `BackupCommand` (via `IExecute`).
- Raises: none directly.

### Jobs / `IExecute<T>` registered
- `BackupCommand` — scheduled at `BackupInterval * 60 * 24` minutes (`BackupInterval` clamped to 1..7 days). Recomputed on `ConfigSavedEvent`.

### Quirks / TODOs / HACKs
- Backup zip filename regex `(nzbdrone|sonarr)_backup_(v[0-9.]+_)?[._0-9]+\.zip` — the optional `v...` group means pre-versioning backups (`nzbdrone_backup_2018.01.01_12.00.00.zip`) still match.
- `BackupDatabase()` only runs for `DatabaseType.SQLite`. PostgreSQL is silently a no-op (the `config.xml` is still backed up; the DB has to be backed up at the operator level).
- `Restore()` accepts both raw `.db` files and `.zip`s. For zips: extracts to a temp folder, moves matching files into AppData, deletes temp folder. Raw `.db`: moves directly. Either way the actual DB swap happens at next startup.
- The journal file `sonarr.db-journal` is explicitly deleted before zipping — would otherwise be a stale snapshot.
- `Cleanup()` empties `sonarr_backup` temp folder before each run.
- Manual backups are NOT subject to retention sweep.
- The forced `journal_mode=truncate` on the backed-up DB is a defensive workaround for SQLite page-size mismatches between source and backup.

### Test files
- (No `BackupServiceFixture` found in the standard test tree; the service is integration-tested via the host.)

### Open questions
- **BACKUP-Q1**: PostgreSQL backup is a no-op. Should the Go port shell out to `pg_dump`, or remain explicitly off-loaded to operator?
- **BACKUP-Q2**: Backup retention applies only to scheduled+update backups; manual backups grow unbounded. Intentional?
- **BACKUP-Q3**: The zip names embed `BuildInfo.Version` — restore from a newer-version backup is allowed, with no compatibility check. Add a guard?
- **BACKUP-Q4**: `journal_mode=truncate` workaround — does the SQLite Go driver (modernc/mattn) need an analog?

---

## 9. Update (self-update)

### Purpose
Periodically polls `services.sonarr.tv/v1/update/{branch}` for an available newer build; if `UpdateAutomatically` is on (and the deployment isn't external — Docker/Apt etc.), downloads, verifies SHA-256, extracts to a sandbox, takes an `Update`-type backup, then either runs the configured `UpdateScript` or hands off to `Sonarr.Update.exe` (the bundled updater process), which restarts the parent. There's also a "post-install update" path: on `ApplicationStartingEvent`, if the file `update_required` exists in AppData, run the install flow synchronously before the rest of the host comes up.

### Key public types
| Type | File | Role |
| --- | --- | --- |
| `UpdateMechanism` | `Update/UpdateMechanism.cs:3` | enum `BuiltIn=0, Script=1, External=10, Apt=11, Docker=12`. |
| `UpdatePackage` | `Update/UpdatePackage.cs:5` | `Version, ReleaseDate, FileName, Url, Changes, Hash, Branch`. |
| `UpdatePackageAvailable` | `Update/UpdatePackageAvailable.cs:3` | `bool Available; UpdatePackage`. |
| `UpdateChanges` | `Update/UpdateChanges.cs:5` | `New[]`, `Fixed[]`. |
| `IUpdatePackageProvider` / `UpdatePackageProvider` | `Update/UpdatePackageProvider.cs:11` | Calls `services.sonarr.tv/update/{branch}` and `…/changes`. |
| `ICheckUpdateService` / `CheckUpdateService` | `Update/UpdateCheckService.cs:7` | Thin wrapper around the package provider, fixing branch and current version. |
| `IRecentUpdateProvider` / `RecentUpdateProvider` | `Update/RecentUpdateProvider.cs:9` | Returns the changelog list since the previously-installed version (per `UpdateHistoryService`). |
| `IVerifyUpdates` / `UpdateVerification` | `Update/UpdateVerification.cs:8` | SHA-256 comparison. |
| `InstallUpdateService` | `Update/InstallUpdateService.cs:23` | The big one — `IExecute<ApplicationUpdateCommand>`, `IExecute<ApplicationUpdateCheckCommand>`, `IHandle<ApplicationStartingEvent>`. |
| `IUpdaterConfigProvider` / `UpdaterConfigProvider` | `Update/ConfigureUpdateMechanism.cs:11` | On `ApplicationStartedEvent`, reconciles configured `UpdateMechanism` against the package's `PackageUpdateMechanism` (Docker, Apt, etc.) — if they conflict, switches to the package one and may also switch the `Branch`. |
| `ApplicationUpdateCheckCommand` | `Update/Commands/ApplicationUpdateCheckCommand.cs:5` | "Check, then push an `ApplicationUpdateCommand` if there's an update". `InstallMajorUpdate` flag. |
| `ApplicationUpdateCommand` | `Update/Commands/ApplicationUpdateCommand.cs:5` | "Actually install". `IsExclusive = true`. |
| `UpdateInstalledEvent` | `Update/Events/UpdateInstalledEvent.cs:5` | `(Version Previous, Version New)`. Emitted by `UpdateHistoryService.HandleAsync(ApplicationStartedEvent)` when the build version differs from the last-recorded `UpdateHistory.Installed`. |
| `UpdateHistory` | `Update/History/UpdateHistory.cs:6` | Row: `Date, Version, EventType (Initiated|Installed)`. |
| `UpdateHistoryEventType` | `Update/History/UpdateHistoryEventType.cs:3` | `Unknown=0, Initiated=1, Installed=2`. |
| `IUpdateHistoryRepository` / `UpdateHistoryRepository` | `Update/History/UpdateHistoryRepository.cs:9` | `LastInstalled / PreviouslyInstalled / InstalledSince(date)`. **Stored in the LOG database** (`ILogDatabase`), not the main DB. |
| `IUpdateHistoryService` / `UpdateHistoryService` | `Update/History/UpdateHistoryService.cs:14` | On `ApplicationStartedEvent`: insert a new row if `BuildInfo.Version` differs from `LastInstalled`, then async-publish `UpdateInstalledEvent`. |
| `UpdateFailedException`, `UpdateFolderNotWritableException`, `UpdateVerificationFailedException` | `Update/Update*Exception.cs` | Specific failure modes. |

### Public services
- `ICheckUpdateService.AvailableUpdate()`.
- `IRecentUpdateProvider.GetRecentUpdatePackages()`.
- `IUpdateHistoryService.PreviouslyInstalled() / InstalledSince(date)`.

### DB tables
- `UpdateHistory` (in the **log** database, not main).

### Events raised / consumed
- Raises: `UpdateInstalledEvent`.
- Consumes: `ApplicationStartingEvent` (post-install path), `ApplicationStartedEvent` (history record + config reconciliation).

### Jobs / `IExecute<T>` registered
- `ApplicationUpdateCheckCommand` — scheduled at 6h via `TaskManager`.
- `ApplicationUpdateCommand` — invoked by the check command (or manually). `IsExclusive = true`.

### Quirks / TODOs / HACKs
- `UpdateHistoryRepository` writes to `ILogDatabase` — i.e. update history survives main-DB restore. Comment in `UpdateHistoryService.Handle`: dev-version (`Version.Major == 10`) skips writes, "they change constantly".
- "Post-install update check" gated on a sentinel file `update_required` in AppData. If present and BuiltIn enabled and an update exists, install it synchronously, with a 30-second sleep loop after launching the updater (and an Error log if the updater hasn't taken over by then).
- `UpdaterConfigProvider` (`ConfigureUpdateMechanism.cs`) auto-corrects mismatched `UpdateMechanism`: if the deployment's `PackageUpdateMechanism` (Docker/Apt) doesn't match the user's configured choice, force-switches to the package one. Also force-switches `Branch` to `PackageBranch` if `IsExternalUpdateMechanism` and they differ.
- `EnsureAppDataSafety` throws if AppData is inside StartUp folder — same check as `AppDataLocationCheck`, but enforced at install time.
- Updater downloads to `TempFolder`, warns (but doesn't abort) if `< 1 GiB` free.
- `EnsureValidBranch` rewrites `Branch` config to match the package's branch (silent).
- "Running in developer environment" detection: `StartUpFolder.EndsWith("_output")` → bail without installing.
- `executable flag (chmod 755)` set on `Sonarr.Update` exe on macOS/Linux post-extract.
- `InstallUpdate` returns `bool` for "did we actually start the updater" — only false on dev environment or missing update client exe; `UpdateFolderNotWritableException` / `UpdateVerificationFailedException` are thrown.
- `UpdatePackageProvider` adds an `active` query param if `IAnalyticsService.IsEnabled` so the cloud service knows which versions are still in real use.
- `UpdateCheck` (the *health check*) reads `BuildInfo.BuildDateTime` and warns if older than 14 days AND a newer update exists. Lives in `HealthCheck/Checks/UpdateCheck.cs`.

### Test files
- `src/NzbDrone.Core.Test/UpdateTests/UpdateServiceFixture.cs`
- `src/NzbDrone.Core.Test/UpdateTests/UpdatePackageProviderFixture.cs`

### Open questions
- **UPDATE-Q1**: SHA-256 only — no signature verification. Should Go port add code-signing / Sigstore?
- **UPDATE-Q2**: `update_required` sentinel file is a flat-file lock between sessions. Stable across upgrades?
- **UPDATE-Q3**: `UpdaterConfigProvider` silently rewrites `Branch` config. Should that be surfaced to the user as a notice/health check rather than silent?
- **UPDATE-Q4**: `Version.Major == 10` is the dev-marker (10.x.x.x). Hard-coded magic number — keep this convention in Go port?
- **UPDATE-Q5**: `UpdateHistoryRepository` lives in the log DB. If a user disables `LogDbEnabled`, update history is silently dropped (`UpdateHistoryService.Handle` early-returns). Intentional? Should the previously-installed prompt still work?
- **UPDATE-Q6**: 30-second hard-coded post-install timeout in `Handle(ApplicationStartingEvent)`. Configurable?

---

## Cross-subsystem notes

### Single-instance / static state
- `Jobs.Scheduler` uses `static Timer` and `static CancellationTokenSource`.
- `Queue.QueueService` and `Queue.ObsoleteQueueService` use `static List<Queue>`.

These all assume a single in-process instance, which holds in the current host but is worth flagging for the Go port.

### Database split
- Main DB tables touched here: `ScheduledTasks`, `History`, `Blocklist`. (Plus everything housekeeping deletes from.)
- Log DB tables: `UpdateHistory`.
- No tables: `Queue`, `HealthCheck`, `Lifecycle`, `Backup`.

### Event-driven coupling
The `[CheckOn]` attribute system in HealthCheck is a reflection-based event-routing extension layered on top of the standard `IEventAggregator`. The Go port should consider whether to (a) replicate the attribute-based dispatch, or (b) make each health check explicitly register its event subscriptions in code.

### Counts (summary)
- **Default scheduled commands enumerated: 11** (`RefreshMonitoredDownloads`, `MessagingCleanup`, `ApplicationUpdateCheck`, `UpdateSceneMapping`, `CheckHealth`, `RefreshSeries`, `Housekeeping`, `CleanUpRecycleBin`, `ImportListSync`, `Backup`, `RssSync`).
- **Concrete `IHealthCheck` impls: 27** (26 in `Checks/` + `ServerSideNotificationService`).
- **Concrete `IHousekeepingTask` impls: 31** (32 .cs files in `Housekeepers/` minus 1 abstract base).
- **Commands defined in this card's subsystems: 8** (`BackupCommand`, `ClearBlocklistCommand`, `RestartCommand`, `ShutdownCommand`, `CheckHealthCommand`, `HousekeepingCommand`, `ApplicationUpdateCheckCommand`, `ApplicationUpdateCommand`).
- **Events defined in this card's subsystems: 8** (`ApplicationStartingEvent`, `ApplicationStartedEvent`, `ApplicationShutdownRequested`, `QueueUpdatedEvent`, `ObsoleteQueueUpdatedEvent`, `HealthCheckCompleteEvent`, `HealthCheckFailedEvent`, `HealthCheckRestoredEvent`, `UpdateInstalledEvent` — that's actually 9).
