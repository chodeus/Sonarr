# Messaging (event bus) + ProgressMessaging

**Source:**
- `src/NzbDrone.Core/Messaging/Events/**` — `EventAggregator`, `IEventAggregator`, `IHandle<T>`, `IHandleAsync<T>`, `CommandExecutedEvent`
- `src/NzbDrone.Core/Messaging/EventHandleOrderAttribute.cs`
- `src/NzbDrone.Core/Messaging/IProcessMessage.cs`
- `src/NzbDrone.Common/Messaging/{IMessage,IEvent,LifecycleEventAttribute}.cs`
- `src/NzbDrone.Core/ProgressMessaging/{CommandUpdatedEvent,ProgressMessageContext,ProgressMessageTarget}.cs`

**Cross-references (do not re-derive here):**
- The *command bus* (`Command`, `CommandModel`, `IExecute<T>`, `CommandQueue`, `CommandQueueManager`, `CommandExecutor`, scheduling) is owned by `system.md` ("Jobs / commands"). Anything below that touches the command bus is for context only.
- The *SignalR fan-out* of `CommandUpdatedEvent` → `command` resource and `CommandExecutedEvent` → `system/task` is owned by `signalr/inventory.md`. We cite specific lines but do not duplicate the catalogue.

**Target Go package:** `go/internal/messaging/event` (or `internal/event`); `go/internal/progress` for the NLog-target equivalent.

**Status:** drafted

## Purpose
Two distinct in-process pub/sub mechanisms live in this subsystem:

1. **Event bus** (`IEventAggregator`) — a synchronous-first reflection-driven event dispatcher built on top of the DryIoc DI container. Every domain event in Sonarr (60+ classes, see catalogue below) is published through `IEventAggregator.PublishEvent<T>` and routed to all registered `IHandle<T>` (sync) and `IHandleAsync<T>` (fire-and-forget) implementations. Used for everything from `ApplicationStartedEvent` (lifecycle) through `EpisodeImportedEvent` (domain) to `ModelEvent<TModel>` (datastore-level CRUD notifications).
2. **ProgressMessaging** — a tiny coupling between the command-execution thread (owned by `system.md`) and the NLog logging pipeline. A custom `Target` (`ProgressMessageTarget`) intercepts log lines emitted *during* a command execution, attributes them to the running command via an `AsyncLocal<CommandModel>` thread context, and re-emits them as `CommandUpdatedEvent`s back through the event bus so the SignalR `command` channel can ship per-command status updates to the web UI.

The event bus is **strictly in-process** — no events are serialized, persisted, or shipped between processes. The only events that ever leave the host are those the SignalR layer chooses to forward in its own envelope (`SignalRMessage`, `signalr/inventory.md:25-42`).

## API surface

### `IEventAggregator` — the entire interface

`src/NzbDrone.Core/Messaging/Events/IEventAggregator.cs:5-9`:

```csharp
public interface IEventAggregator
{
    void PublishEvent<TEvent>(TEvent @event) where TEvent : class, IEvent;
}
```

That is the *only* method. There is no `Unsubscribe`, no `PublishEventAsync`, no per-handler subscription bookkeeping; the implementation is "publish to all registered handlers, synchronously call sync ones, fire-and-forget the async ones." Single concrete impl: `EventAggregator` (`src/NzbDrone.Core/Messaging/Events/EventAggregator.cs:15-176`), DI-registered as a singleton via the `RegisterMany` pass in `Common/Composition/Extensions.cs:27-29`.

### `IHandle<T>` / `IHandleAsync<T>`

`src/NzbDrone.Core/Messaging/Events/IHandle.cs:5-15`:

```csharp
public interface IHandle<TEvent> : IProcessMessage<TEvent> where TEvent : IEvent
{
    void Handle(TEvent message);
}

public interface IHandleAsync<TEvent> : IProcessMessageAsync<TEvent> where TEvent : IEvent
{
    void HandleAsync(TEvent message);   // NOTE: returns void, not Task — see MSG-Q1
}
```

There is also a single global "all events" handler form: `IHandleAsync<IEvent>` (the magic generic argument is `IEvent` itself, not a concrete event). The aggregator detects this separately and broadcasts every event to every `IHandleAsync<IEvent>` implementation in addition to the per-event handlers (`EventAggregator.cs:36-38`, `:120-130`). Only one production class uses this: `HealthCheckService` (`src/NzbDrone.Core/HealthCheck/HealthCheckService.cs:24`) — it acts as a global event tap to drive `[CheckOn(...)]` health checks (see `system.md` "Health checks").

### `IProcessMessage` and `IProcessMessage<T>`

`src/NzbDrone.Core/Messaging/IProcessMessage.cs` defines four marker interfaces:

```csharp
public interface IProcessMessage { }
public interface IProcessMessageAsync : IProcessMessage { }
public interface IProcessMessage<TMessage> : IProcessMessage { }
public interface IProcessMessageAsync<TMessage> : IProcessMessageAsync { }
```

These are **pure marker bases** for `IHandle<T>` / `IHandleAsync<T>`. No code outside the interface declarations of `IHandle.cs` even references them. They appear vestigial — likely a remnant of an earlier, more elaborate messaging hierarchy. **Implication for port:** they can be dropped; the Go target only needs the equivalent of `IHandle<T>`. See **MSG-Q2**.

### `IEvent` / `IMessage` markers

`src/NzbDrone.Common/Messaging/IMessage.cs` and `IEvent.cs`:

```csharp
public interface IMessage { }
public interface IEvent : IMessage { }
```

Both are empty markers. `IMessage` is otherwise unused in the codebase — the only thing that references it is `IEvent : IMessage`. See **MSG-Q3**.

### `EventHandleOrderAttribute`

`src/NzbDrone.Core/Messaging/EventHandleOrderAttribute.cs:5-22`:

```csharp
[AttributeUsage(AttributeTargets.Method)]
public class EventHandleOrderAttribute : Attribute
{
    public EventHandleOrder EventHandleOrder { get; set; }
    public EventHandleOrderAttribute(EventHandleOrder o) { EventHandleOrder = o; }
}

public enum EventHandleOrder { First, Any, Last }   // 0, 1, 2
```

- Three legal values: `First` (0), `Any` (1, default), `Last` (2). Sort is by enum int value (`EventAggregator.cs:174` returns the cast int).
- The attribute is read by reflection on the *method* `Handle(TEvent)` of each `IHandle<TEvent>` (`EventAggregator.cs:160-167`). If the type does not declare a `Handle(TEvent)` method exactly (none found by `GetMethod`), the order defaults to `Any`. If the method exists but has no attribute, the order also defaults to `Any`.
- **Only sync handlers (`IHandle<T>`) are sorted.** Async handlers (`IHandleAsync<T>`) and global handlers (`IHandleAsync<IEvent>`) ignore the attribute entirely — they fire in container-discovery order, which is effectively non-deterministic. See **MSG-Q4**.
- **Two production sites use it**, both `Last`:
  - `src/NzbDrone.Core/Download/RedownloadFailedDownloadService.cs:31` — `Handle(DownloadFailedEvent)` runs *after* `BlocklistService.Handle(DownloadFailedEvent)` and `HistoryService.Handle(DownloadFailedEvent)` so the redownload trigger sees the failure already recorded.
  - `src/NzbDrone.Core/MediaFiles/MediaFileDeletionService.cs:215` — `Handle(EpisodeFileDeletedEvent)` runs after history/notifications; the comment-free intent looks like "let other listeners process the event before we delete the actual file row." See **MSG-Q5**.
- No production handler uses `First`. The `Any` default covers everyone else.

## Concrete implementation: `EventAggregator`

Single file: `src/NzbDrone.Core/Messaging/Events/EventAggregator.cs`.

### Subscriber resolution and caching

```csharp
private readonly Dictionary<string, object> _eventSubscribers;   // key: event name, value: EventSubscribers<TEvent>
```

`EventSubscribers<TEvent>` (`:23-42`) is a per-event-type cache containing three arrays:
- `_syncHandlers : IHandle<TEvent>[]` — sorted by `EventHandleOrder` (`OrderBy(GetEventHandleOrder)` at `:33`).
- `_asyncHandlers : IHandleAsync<TEvent>[]` — unsorted.
- `_globalHandlers : IHandleAsync<IEvent>[]` — unsorted; same array contents for *every* event type (it is the set of `IHandleAsync<IEvent>` registrations).

The first time an event of a given name is published, the aggregator:
1. Locks `_eventSubscribers` (`:86`).
2. Builds `EventSubscribers<TEvent>` by calling `serviceFactory.BuildAll<IHandle<TEvent>>()`, `BuildAll<IHandleAsync<TEvent>>()`, `BuildAll<IHandleAsync<IEvent>>()` (`:32-40`).
3. Caches it forever under `GetEventName(eventType)`.

`GetEventName` (`:147-155`) collapses generic events: `ModelEvent<Series>` becomes `"ModelEvent<Series>"` keyed by *type-argument name only*, not full namespace. **Implication:** two `ModelEvent<X>` whose `X` types share the simple name (e.g. one `Foo.Series`, one `Bar.Series`) would collide in the subscriber cache. There are no such collisions in practice. See **MSG-Q6**.

The cache is *never invalidated*. Late-bound DI registrations (none happen in production, but tests sometimes mock individual services) would not be picked up after the first publish of that event type. The lock is held only across the *build*, not across the publish — handlers are read from the (now-final) array outside the lock.

`BuildAll<T>` itself dedupes by FullName: `ServiceFactory.cs:36` — `_container.GetServices<T>().GroupBy(c => c.GetType().FullName).Select(g => g.First())`. So even if a handler class is registered twice (once as `IHandle<X>` and once as `Service`), it fires only once.

If `BuildAll` throws (e.g. during shutdown when the DryIoc container is being disposed), the catch at `:94-98` logs a warning and *swallows* the publish — the event is dropped silently. **MSG-Q7**.

### Publish path

`PublishEvent<TEvent>` (`:53-145`) does, in order:

1. **Null check** via `Ensure.That(@event).IsNotNull()` (`:56`).
2. **Shutdown gating** (`:60-64`): if `IRuntimeInfo.IsExiting` is true *and* the event type carries `[LifecycleEvent]` (`NzbDrone.Common/Messaging/LifecycleEventAttribute.cs`), the publish is logged-and-blocked. Lifecycle events are `ApplicationStartedEvent`, `ApplicationStartingEvent`, `ApplicationShutdownRequested`. So once a shutdown is in progress, *more* lifecycle events cannot be raised — but everything else still flows. **MSG-Q8** for what this protects against.
3. **Resolve / build subscriber arrays** (locked, see above).
4. **Sync-handler loop** (`:104-118`): for each `IHandle<TEvent>` in sorted order, call `handler.Handle(event)` *synchronously on the publishing thread*. Per-handler try/catch logs `"<HandlerName> failed while processing [<EventName>]"` and continues to the next handler. **No retry. No rethrow.** A throwing sync handler does not stop or affect later sync handlers — verified by the test `EventAggregatorFixture.broken_handler_should_not_effect_others_handler` (`src/NzbDrone.Core.Test/Messaging/Events/EventAggregatorFixture.cs:67-81`).
5. **Global async fan-out** (`:120-130`): for each `IHandleAsync<IEvent>` (i.e. `HealthCheckService`), `_taskFactory.StartNew(...)` with `TaskCreationOptions.PreferFairness`, then `.LogExceptions()`. Fire-and-forget. The publishing thread does not await.
6. **Per-event async fan-out** (`:132-144`): same pattern for each `IHandleAsync<TEvent>`. Each handler runs on its own pool task; ordering between handlers is undefined.

`_taskFactory` is a default `new TaskFactory()` (`:49`). It runs work on the default `TaskScheduler.Default` — i.e. the standard ThreadPool, with no concurrency cap. The commented-out block at `:66-81` shows a debugging snippet that once logged thread-pool saturation; left in as a comment. The disabled test at `EventAggregatorFixture.cs:84-125` *aspires* to verify "should_queue_multiple_async_events" with a max of 3 concurrent async handlers, but it is `/* commented out */` and the production aggregator has **no concurrency cap** at all. **MSG-Q9** — what is the intended bound, if any?

### Handler discovery (DI)

There is **no scan-on-publish** and **no attribute-based registration**. Handlers are found because DryIoc auto-registers every concrete type that implements an interface in a non-`System.*` namespace, as a singleton, via:

```csharp
container.RegisterMany(assemblies,
    serviceTypeCondition: type => type.IsInterface && !string.IsNullOrWhiteSpace(type.FullName)
                              && !type.FullName.StartsWith("System"),
    reuse: Reuse.Singleton);
```
(`src/NzbDrone.Common/Composition/Extensions.cs:27-29`)

So if a class implements `IHandle<EpisodeImportedEvent>`, it is automatically registered as `IHandle<EpisodeImportedEvent>` (and as itself, and as every other interface it implements). At first publish of `EpisodeImportedEvent`, `serviceFactory.BuildAll<IHandle<EpisodeImportedEvent>>()` returns every such class. **There is no opt-in or opt-out attribute** — declaring the interface *is* subscribing.

This has two important consequences:

- **A class that accidentally implements `IHandle<X>` is silently subscribed.** The compiler does not warn; tests do not by default cover the "is this class registered as a handler?" question. Renaming `Handle` will simply unsubscribe the class. The port should preserve this behavior or compensate with an explicit registration table (per `CONVERSION.md` direction in `common.md`) and a static check that no orphan handlers exist.
- **Handler instances are singletons.** Across all handlers for one event, all events of one type, and the lifetime of the process. State stored in handler fields persists across events. `EpisodeImportedEvent` consumers like `NotificationService` and `HistoryService` rely on this — they cache config, debouncers, etc. in fields populated at construction.

### Threading model summary

| Handler shape | Runs on | Order | Error isolation | Concurrency cap |
|--------------|---------|-------|-----------------|-----------------|
| `IHandle<T>` (sync) | Publisher thread | `EventHandleOrder` then DI order | Per-handler try/catch logs and continues | N/A (serial) |
| `IHandleAsync<T>` (async) | ThreadPool via `Task.Factory.StartNew` | DI order | `LogExceptions()` continuation | None — uses `TaskScheduler.Default` |
| `IHandleAsync<IEvent>` (global) | Same as above | DI order | Same | Same |

**Re-entrancy.** Nothing prevents a sync handler from calling `IEventAggregator.PublishEvent` again. The `_eventSubscribers` lock is non-recursive only on dictionary mutation; the actual handler calls happen *outside* the lock. So `HandlerA.Handle(EventA) → publish(EventB) → HandlerB.Handle(EventB) → publish(EventA) → ...` would simply recurse on the publisher's own stack. No cycle detection. There is no production cycle of this shape, but `ProgressMessageTarget.Write` (which publishes `CommandUpdatedEvent` from inside an NLog write triggered by a log call inside a handler) needs an explicit guard — see ProgressMessaging below. **MSG-Q10** for whether the port should add a depth guard.

### Cross-process / persistence

**None.** `IEvent` is not serialized anywhere. There is no on-disk event log, no outbox table, no broker. Handlers run only in the same process that called `PublishEvent`. The only data the bus *touches* persistently is whatever each handler chooses to write to the database. So the bus is volatile: a process restart drops every in-flight async-handler task. This is consistent with the system.md treatment of `CommandQueue` (which *does* persist commands) — the event bus does not.

The single exception is the SignalR forwarding layer: `RestControllerWithSignalR<TResource, TModel>.Handle(ModelEvent<TModel>)` (`src/Sonarr.Http/REST/RestControllerWithSignalR.cs:10`) is itself an `IHandle<ModelEvent<TModel>>` — when the bus delivers the event, the controller serializes a `SignalRMessage` and pushes it through `IBroadcastSignalRMessage` (`signalr/inventory.md:55-61`). That hop is in-process; the wire serialization belongs to SignalR, not the event bus.

## ProgressMessaging

Three files, total ~120 lines.

### `CommandUpdatedEvent`

`src/NzbDrone.Core/ProgressMessaging/CommandUpdatedEvent.cs:6-14`:

```csharp
public class CommandUpdatedEvent : IEvent
{
    public CommandModel Command { get; set; }
    public CommandUpdatedEvent(CommandModel command) { Command = command; }
}
```

**Producers (3):**
- `CommandExecutor.BroadcastCommandUpdate(CommandModel)` (`Messaging/Commands/CommandExecutor.cs:118-124`) — fires once when the command starts and once when it ends. Only when `command.Body.SendUpdatesToClient` is true.
- `ProgressMessageTarget.Write(LogEventInfo)` (`ProgressMessaging/ProgressMessageTarget.cs:39`) — every status-tagged log line during the run.

**Consumers (2):**
- `Sonarr.Api.V3/Commands/CommandController.cs:23` and `Sonarr.Api.V5/Commands/CommandController.cs:20` — both implement `IHandle<CommandUpdatedEvent>`, debounce 100 ms (`signalr/inventory.md:91`), and broadcast the resulting `CommandResource` over SignalR as `name="command", action="updated"`.

### `ProgressMessageContext`

`src/NzbDrone.Core/ProgressMessaging/ProgressMessageContext.cs:6-46`. A static class holding three fields:

```csharp
private static AsyncLocal<CommandModel> _commandModelAsync = new AsyncLocal<CommandModel>();
[ThreadStatic] private static CommandModel _commandModel;
[ThreadStatic] private static bool _reentrancyLock;

public static CommandModel CommandModel { get => _commandModel ?? _commandModelAsync.Value; set { _commandModel = value; _commandModelAsync.Value = value; } }
public static bool LockReentrancy()    { if (_reentrancyLock) return false; _reentrancyLock = true; return true; }
public static void UnlockReentrancy()  { _reentrancyLock = false; }
```

Two notable design choices:

1. **Both `[ThreadStatic]` and `AsyncLocal` are written.** The getter prefers the `[ThreadStatic]` field but falls back to `AsyncLocal`. The author wanted the value to survive across `await` (AsyncLocal does, ThreadStatic does not), but apparently *also* wanted the synchronous fast path to read the same backing slot. **MSG-Q11 / PROG-Q1**: under what scenario does the `[ThreadStatic]` value differ from the `AsyncLocal` value, and which is the correct fallback ordering? In practice, the command runs on a long-lived `Thread` (see `CommandExecutor.Handle(ApplicationStartedEvent)` at `Messaging/Commands/CommandExecutor.cs:130-134`), which spawns three OS threads; each runs commands serially. So the `[ThreadStatic]` is set once, used for the whole sync execution, then cleared. The `AsyncLocal` mirror exists for any async/await that the handler chooses to do.
2. **Reentrancy is per-thread.** The lock is also `[ThreadStatic]`. It is acquired around the only place that re-publishes from inside the NLog target (`ProgressMessageTarget.Write`, see below). If a `Handle(CommandUpdatedEvent)` somewhere calls a logger that hits the target again, the second invocation early-returns. Cycle prevention only.

The set-on-entry/clear-on-exit pattern lives in `CommandExecutor.ExecuteCommand` (`Messaging/Commands/CommandExecutor.cs:79-82, :106-109`):

```csharp
if (ProgressMessageContext.CommandModel == null) { ProgressMessageContext.CommandModel = commandModel; }
// ... handler.Execute(command) ...
finally {
    ...
    if (ProgressMessageContext.CommandModel == commandModel) {
        ProgressMessageContext.CommandModel = null;
    }
}
```

The `null` guard at entry prevents nested commands (one `IExecute` calling another via the queue) from clobbering the outer context. Whether that ever happens in practice — **PROG-Q2**.

### `ProgressMessageTarget`

`src/NzbDrone.Core/ProgressMessaging/ProgressMessageTarget.cs:10-66`. An NLog `Target` *and* an `IHandle<ApplicationStartedEvent>`.

```csharp
public class ProgressMessageTarget : Target, IHandle<ApplicationStartedEvent>
{
    public void Handle(ApplicationStartedEvent message)   // self-registration on app start
    {
        _rule = new LoggingRule("*", LogLevel.Trace, this);
        LogManager.Configuration.AddTarget("ProgressMessagingLogger", this);
        LogManager.Configuration.LoggingRules.Add(_rule);
        LogManager.ReconfigExistingLoggers();
    }

    protected override void Write(LogEventInfo logEvent)
    {
        var command = ProgressMessageContext.CommandModel;
        if (!IsClientMessage(logEvent, command)) return;
        if (!ProgressMessageContext.LockReentrancy()) return;
        try
        {
            _commandQueueManager.SetMessage(command, logEvent.FormattedMessage);
            _eventAggregator.PublishEvent(new CommandUpdatedEvent(command));
        }
        finally { ProgressMessageContext.UnlockReentrancy(); }
    }

    private bool IsClientMessage(LogEventInfo logEvent, CommandModel command)
    {
        if (command == null || !command.Body.SendUpdatesToClient) return false;
        return logEvent.Properties.ContainsKey("Status");
    }
}
```

Key behaviors:

- **Rule:** `"*"` at `LogLevel.Trace`. Every logger in the process pipes through this target — but the `IsClientMessage` check filters down to log events that explicitly carry a `Status` NLog property. Production code marks lines with `_logger.ProgressMessage(...)` extension or by setting `LogEventInfo.Properties["Status"]` via NLog `WithProperty` — see `Common/Instrumentation/LogEventExtensions.cs` (cited from `common.md`'s Instrumentation section). **PROG-Q3** — confirm the exact extension method name and its callers.
- **Per-command attribution** is via `ProgressMessageContext.CommandModel`, which the *running command's executor thread* set at entry. So a log line emitted from inside `RefreshSeries.Execute(...)` has `Status` → flows through `Write` → reads the per-thread `CommandModel` → tagged to that command. A log line from any other thread has `CommandModel == null` → dropped.
- **Concurrency:** with `THREAD_LIMIT = 3` parallel command threads (`Messaging/Commands/CommandExecutor.cs:14`), three commands can run in parallel; each has its own `[ThreadStatic]` `CommandModel` slot. Status lines from the three never cross-contaminate at the target level. **However**, the downstream `CommandUpdatedEvent` consumer (`CommandController` debouncer, `signalr/inventory.md:91`) coalesces *all* commands into one `_pendingUpdates` dictionary keyed by `command.Id` — see `Sonarr.Api.V3/Commands/CommandController.cs:100-129`. Cross-command coalescing is intentional.
- **The reentrancy lock prevents log → publish → handler-logs → publish → ... loops.** Anything `_eventAggregator.PublishEvent(new CommandUpdatedEvent(...))` does that ends up logging again will hit `Write` with the lock already held and bail.
- **`SetMessage` and `PublishEvent` are non-transactional.** If `SetMessage` (DB update of the in-memory queue, see `system.md` "CommandQueueManager") succeeds but `PublishEvent` fails, the queue state and the SignalR channel disagree. In practice neither is expected to fail — `SetMessage` writes to an in-memory queue (commands that should `SendUpdatesToClient` are not persisted: see `system.md:83-95`), and `PublishEvent` swallows handler exceptions. **PROG-Q4** — call out that the message column in `CommandModel` is in-memory only and survives only as long as the process.

### Frontend Activity / Queue page

The web UI subscribes to the SignalR `command` channel and renders running tasks with their last status message (see `signalr/inventory.md:91`). When a long-running command (e.g. `RefreshSeriesCommand`) emits status lines like "Refreshing series 12 of 48", they:

1. Are emitted as NLog Trace+ messages with a `Status` property.
2. Get caught by `ProgressMessageTarget.Write`.
3. Update `CommandModel.Message` in the in-memory queue (`CommandQueueManager.SetMessage`, see `system.md`).
4. Fire `CommandUpdatedEvent`.
5. `CommandController` debounces 100 ms, broadcasts as a SignalR `{ name: "command", action: "updated", body: { resource: <CommandResource> } }`.
6. The frontend updates the corresponding row.

That is the entire status-message pipeline.

## Producers — `IEventAggregator` injection sites

Every producer takes a constructor `IEventAggregator` and calls `_eventAggregator.PublishEvent(...)`. Locations of all production publishes (excluding `.Test/`), mirroring the grep at the time of writing:

| File | Line | Event published |
|------|------|-----------------|
| `src/NzbDrone.Core/Indexers/RssSyncService.cs` | 64 | `RssSyncCompleteEvent` |
| `src/NzbDrone.Core/Configuration/ConfigService.cs` | 79 | `ConfigSavedEvent` |
| `src/NzbDrone.Core/Configuration/ConfigFileProvider.cs` | 166 | `ConfigFileSavedEvent` |
| `src/NzbDrone.Core/Update/History/UpdateHistoryService.cs` | 99 | `UpdateInstalledEvent` |
| `src/NzbDrone.Core/AutoTagging/AutoTaggingService.cs` | 61, 69, 79 | `AutoTagsUpdatedEvent` |
| `src/NzbDrone.Core/CustomFormats/CustomFormatService.cs` | 68, 78, 91 | `CustomFormatAddedEvent`, `CustomFormatDeletedEvent` |
| `src/NzbDrone.Core/HealthCheck/HealthCheckService.cs` | 113, 122, 134, 198 | `HealthCheckRestoredEvent`, `HealthCheckFailedEvent`, `HealthCheckCompleteEvent` |
| `src/NzbDrone.Core/Lifecycle/LifecycleService.cs` | 33, 40 | `ApplicationShutdownRequested` (twice — restart vs shutdown) |
| `src/NzbDrone.Core/Tv/RefreshEpisodeService.cs` | 131 | `EpisodeInfoRefreshedEvent` |
| `src/NzbDrone.Core/Tv/MoveSeriesService.cs` | 76 | `SeriesMovedEvent` |
| `src/NzbDrone.Core/Tv/RefreshSeriesService.cs` | 77, 132, 179, 186, 215, 284 | `SeriesUpdatedEvent`, `SeriesScanSkippedEvent`, `SeriesRefreshStartingEvent`, `SeriesRefreshCompleteEvent` |
| `src/NzbDrone.Core/Tv/SeriesScannedHandler.cs` | 49 | `SeriesAddCompletedEvent` |
| `src/NzbDrone.Core/Tv/SeriesService.cs` | 76, 84, 167, 230, 260 | `SeriesAddedEvent`, `SeriesImportedEvent`, `SeriesDeletedEvent`, `SeriesEditedEvent`, `SeriesBulkEditedEvent` |
| `src/NzbDrone.Core/Tags/TagService.cs` | 170, 180, 194 | `TagsUpdatedEvent` |
| `src/NzbDrone.Core/Datastore/BasicRepository.cs` | 506 | `ModelEvent<TModel>` |
| `src/NzbDrone.Core/MediaCover/MediaCoverService.cs` | 237 | `MediaCoversUpdatedEvent` |
| `src/NzbDrone.Core/Download/CompletedDownloadService.cs` | 226, 271 | `DownloadCompletedEvent` |
| `src/NzbDrone.Core/Download/CompletedDownloadService.cs` | 291-293 | `ManualInteractionRequiredEvent` |
| `src/NzbDrone.Core/Download/FailedDownloadService.cs` | 129, 146 | `DownloadFailedEvent` |
| `src/NzbDrone.Core/Download/IgnoredDownloadService.cs` | 37, 50 | `DownloadIgnoredEvent` |
| `src/NzbDrone.Core/Download/DownloadService.cs` | 186, 197 | `EpisodeGrabbedEvent` |
| `src/NzbDrone.Core/Download/DownloadProcessingService.cs` | 43, 80 | `DownloadCanBeRemovedEvent`, `DownloadsProcessedEvent` |
| `src/NzbDrone.Core/Download/Pending/PendingReleaseService.cs` | 547, 553 | `PendingReleasesUpdatedEvent` |
| `src/NzbDrone.Core/Download/TrackedDownloads/DownloadMonitoringService.cs` | 78, 188, 195 | `TrackedDownloadRefreshedEvent` |
| `src/NzbDrone.Core/Download/TrackedDownloads/TrackedDownloadService.cs` | 74, 88 | `TrackedDownloadsRemovedEvent` |
| `src/NzbDrone.Core/Download/TrackedDownloads/TrackedDownloadService.cs` | 290, 306, 322, 338, 354 | `TrackedDownloadRefreshedEvent` |
| `src/NzbDrone.Core/DataAugmentation/Scene/SceneMappingService.cs` | 216 | `SceneMappingsUpdatedEvent` |
| `src/NzbDrone.Core/ThingiProvider/ProviderFactory.cs` | 117, 125, 134, 143, 152 | `ProviderAddedEvent<T>`, `ProviderUpdatedEvent<T>`, `ProviderDeletedEvent<T>` |
| `src/NzbDrone.Core/ThingiProvider/Status/ProviderStatusServiceBase.cs` | 81, 138 | `ProviderStatusChangedEvent<T>` |
| `src/NzbDrone.Core/Queue/QueueService.cs` | 104 | `QueueUpdatedEvent` |
| `src/NzbDrone.Core/Queue/ObsoleteQueueService.cs` | 106 | `ObsoleteQueueUpdatedEvent` |
| `src/NzbDrone.Core/MediaFiles/MediaFileService.cs` | 46, 67 | `EpisodeFileAddedEvent`, `EpisodeFileDeletedEvent` |
| `src/NzbDrone.Core/MediaFiles/RenameEpisodeFileService.cs` | 155, 175, 190, 206 | `EpisodeFileRenamedEvent`, `SeriesRenamedEvent`, `RenameCompletedEvent` |
| `src/NzbDrone.Core/MediaFiles/MediaFileDeletionService.cs` | 99, 210 | `DeleteCompletedEvent` |
| `src/NzbDrone.Core/MediaFiles/EpisodeFileMovingService.cs` | 200-225 | `EpisodeFolderCreatedEvent` |
| `src/NzbDrone.Core/MediaFiles/DiskScanService.cs` | 88, 95, 199 | `SeriesScanSkippedEvent`, `SeriesScannedEvent` |
| `src/NzbDrone.Core/MediaFiles/EpisodeImport/ImportApprovedEpisodes.cs` | 172, 177, 191 | `EpisodeImportedEvent`, `EpisodeImportFailedEvent` |
| `src/NzbDrone.Core/MediaFiles/EpisodeImport/Manual/ManualImportService.cs` | 591, 624 | `UntrackedDownloadCompletedEvent`, `DownloadCompletedEvent` |
| `src/NzbDrone.Core/Profiles/Qualities/QualityProfileService.cs` | 62 | `QualityProfileUpdatedEvent` |
| `src/NzbDrone.Core/Messaging/Commands/CommandExecutor.cs` | 104, 122 | `CommandExecutedEvent`, `CommandUpdatedEvent` |
| `src/NzbDrone.Core/ProgressMessaging/ProgressMessageTarget.cs` | 39 | `CommandUpdatedEvent` |
| `src/NzbDrone.Host/Startup.cs` | 330 | `ApplicationStartingEvent` |
| `src/NzbDrone.Host/AppLifetime.cs` | 65 | `ApplicationShutdownRequested`, `ApplicationStartedEvent` (line 65 only the Started one — Shutdown is published by `LifecycleService`) |

This is the full set of in-process event sources. Test fixtures publish a few additional internal events (`EventA`, `EventB`, `FakeEvent`, `FakeEvent2`) that are not part of production code.

## Event-class index (the porter's master "what fires when" table)

Every concrete `IEvent` class found in production code, with its declared payload, primary producer file, and primary consumer files. Sources verified by grep against `src/` excluding `.Test/`. Counts in the **#prod** / **#cons** columns are *file* counts (a file may contain multiple producers/handlers; 0/1+ both possible).

Generic events (`ModelEvent<TModel>`, `ProviderAddedEvent<TProvider>`, etc.) are listed once with the type-arg dimension treated as a parameter.

| Event | Source | Payload (fields) | Primary producers | Primary consumers |
|-------|--------|------------------|-------------------|-------------------|
| `ApplicationStartingEvent` | `Lifecycle/ApplicationStartingEvent.cs` | (empty) `[LifecycleEvent]` | `NzbDrone.Host/Startup.cs:330` | `Update/InstallUpdateService.cs:23` |
| `ApplicationStartedEvent` | `Lifecycle/ApplicationStartedEvent.cs` | (empty) `[LifecycleEvent]` | `NzbDrone.Host/AppLifetime.cs:65` | `Configuration/ConfigFileProvider.cs:25`; `Qualities/QualityDefinitionService.cs:22`; `Update/{ConfigureUpdateMechanism,History/UpdateHistoryService}.cs`; `HealthCheck/HealthCheckService.cs`; `Instrumentation/ReconfigureSentry.cs`; `Authentication/UserService.cs`; `Profiles/Qualities/QualityProfileService.cs`; `ThingiProvider/ProviderFactory.cs`; `ProgressMessaging/ProgressMessageTarget.cs`; `Messaging/Commands/{CommandExecutor,CommandQueueManager}.cs`; `Jobs/{Scheduler,TaskManager}.cs`; `Download/Pending/PendingReleaseService.cs` |
| `ApplicationShutdownRequested` | `Lifecycle/ApplicationShutdownRequested.cs` | `bool Restarting` `[LifecycleEvent]` | `Lifecycle/LifecycleService.cs:33,40` | `Messaging/Commands/CommandExecutor.cs`; `Jobs/Scheduler.cs`; `Instrumentation/DatabaseTarget.cs`; `NzbDrone.Host/AppLifetime.cs` |
| `CommandExecutedEvent` | `Messaging/Events/CommandExecutedEvent.cs` | `CommandModel Command` | `Messaging/Commands/CommandExecutor.cs:104` | `Jobs/TaskManager.cs:31`; `Sonarr.Api.V3/Qualities/QualityDefinitionController.cs:18`; `Sonarr.Api.V5/Qualities/QualityDefinitionController.cs:18`; `Sonarr.Api.V3/System/Tasks/TaskController.cs:15`; `Sonarr.Api.V5/System/Tasks/TaskController.cs:15` |
| `CommandUpdatedEvent` | `ProgressMessaging/CommandUpdatedEvent.cs` | `CommandModel Command` | `Messaging/Commands/CommandExecutor.cs:122`; `ProgressMessaging/ProgressMessageTarget.cs:39` | `Sonarr.Api.V3/Commands/CommandController.cs:23`; `Sonarr.Api.V5/Commands/CommandController.cs:20` |
| `ConfigFileSavedEvent` | `Configuration/Events/ConfigFileSavedEvent.cs` | (empty) | `Configuration/ConfigFileProvider.cs:166` | `Instrumentation/ReconfigureLogging.cs:19` |
| `ConfigSavedEvent` | `Configuration/Events/ConfigSavedEvent.cs` | (empty) | `Configuration/ConfigService.cs:79` | `Localization/LocalizationService.cs:28`; `Jobs/TaskManager.cs:31`; `Download/Pending/PendingReleaseService.cs:47`; `Sonarr.Http/Authentication/UiAuthorizationHandler.cs:14` |
| `ModelEvent<TModel>` | `Datastore/Events/ModelEvent.cs` | `int ModelId; TModel Model; ModelAction Action` (Created/Updated/Deleted/Sync/Unknown) | `Datastore/BasicRepository.cs:506` (only when `PublishModelEvents` overridden — opted-in by `RootFolders/RootFolderRepository.cs`, `RemotePathMappings/RemotePathMappingRepository.cs`); also constructed for type-checks in `HealthCheck/Checks/{DownloadClientRemovesCompletedDownloadsCheck,DownloadClientRootFolderCheck,DownloadClientSortingCheck,ImportListRootFolderCheck,RemotePathMappingCheck}.cs` | `Sonarr.Http/REST/RestControllerWithSignalR.cs:10` (every controller deriving from it — see `signalr/inventory.md` "Server-pushed events" table for the full list of `TModel` types) |
| `EpisodeFileAddedEvent` | `MediaFiles/Events/EpisodeFileAddedEvent.cs` | `EpisodeFile EpisodeFile` | `MediaFiles/MediaFileService.cs:46` | `Tv/EpisodeService.cs:46`; `Sonarr.Api.V3/EpisodeFiles/EpisodeFileController.cs:27`; `Sonarr.Api.V5/EpisodeFiles/EpisodeFileController.cs:26` |
| `EpisodeFileDeletedEvent` | `MediaFiles/Events/EpisodeFileDeletedEvent.cs` | `EpisodeFile EpisodeFile; DeleteMediaFileReason Reason` | `MediaFiles/MediaFileService.cs:67` | `Tv/EpisodeService.cs:45`; `History/HistoryService.cs:37`; `MediaFiles/MediaFileDeletionService.cs:29` (`[Last]`); `Notifications/NotificationService.cs:27`; `Extras/Files/ExtraFileService.cs:30`; `Sonarr.Api.V3/Series/SeriesController.cs:33`; `Sonarr.Api.V5/Series/SeriesController.cs:32`; `Sonarr.Api.V3/EpisodeFiles/EpisodeFileController.cs:28`; `Sonarr.Api.V5/EpisodeFiles/EpisodeFileController.cs:27`; `Sonarr.Api.V3/Episodes/EpisodeControllerWithSignalR.cs:20`; `Sonarr.Api.V5/Episodes/EpisodeControllerWithSignalR.cs:19` |
| `EpisodeFileRenamedEvent` | `MediaFiles/Events/EpisodeFileRenamedEvent.cs` | `Series Series; EpisodeFile EpisodeFile; string OriginalPath` | `MediaFiles/RenameEpisodeFileService.cs:155` | `History/HistoryService.cs:38` |
| `EpisodeFolderCreatedEvent` | `MediaFiles/Events/EpisodeFolderCreatedEvent.cs` | `Series Series; EpisodeFile EpisodeFile; string SeriesFolder; string SeasonFolder; string EpisodeFolder` | `MediaFiles/EpisodeFileMovingService.cs:200,225` | `Extras/ExtraService.cs:27` |
| `EpisodeImportedEvent` | `MediaFiles/Events/EpisodeImportedEvent.cs` | `LocalEpisode EpisodeInfo; EpisodeFile ImportedEpisode; List<DeletedEpisodeFile> OldFiles; bool NewDownload; DownloadClientItemClientInfo DownloadClientInfo; string DownloadId` | `MediaFiles/EpisodeImport/ImportApprovedEpisodes.cs:172` | `Download/History/DownloadHistoryService.cs:21`; `Download/TrackedDownloads/DownloadMonitoringService.cs:17`; `History/HistoryService.cs:35`; `Notifications/NotificationService.cs:21`; `Sonarr.Api.V3/Series/SeriesController.cs:32`; `Sonarr.Api.V5/Series/SeriesController.cs:31`; `Sonarr.Api.V3/Episodes/EpisodeControllerWithSignalR.cs:19`; `Sonarr.Api.V5/Episodes/EpisodeControllerWithSignalR.cs:18` |
| `EpisodeImportFailedEvent` | `MediaFiles/Events/EpisodeImportFailedEvent.cs` | `Exception Exception; LocalEpisode EpisodeInfo; bool NewDownload; DownloadClientItemClientInfo DownloadClientInfo; string DownloadId` | `MediaFiles/EpisodeImport/ImportApprovedEpisodes.cs:177,191` | (none in production — informational; see **MSG-Q12**) |
| `SeriesScannedEvent` | `MediaFiles/Events/SeriesScannedEvent.cs` | `Series Series; List<string> PossibleExtraFiles` | `MediaFiles/DiskScanService.cs:199` | `Tv/EpisodeService.cs:48` (async); `Tv/SeriesScannedHandler.cs:10`; `MediaFiles/UpdateEpisodeFileService.cs:23`; `Extras/{ExistingExtraFileService,ExtraService}.cs`; `MediaFiles/MediaInfo/UpdateMediaInfoService.cs:19` |
| `SeriesScanSkippedEvent` | `MediaFiles/Events/SeriesScanSkippedEvent.cs` | `Series Series; SeriesScanSkippedReason Reason` | `MediaFiles/DiskScanService.cs:88,95`; `Tv/RefreshSeriesService.cs:179,186` | `Tv/SeriesScannedHandler.cs:11` |
| `SeriesRenamedEvent` | `MediaFiles/Events/SeriesRenamedEvent.cs` | `Series Series; List<RenamedEpisodeFile> RenamedFiles` | `MediaFiles/RenameEpisodeFileService.cs:175` | `Notifications/NotificationService.cs:24`; `Extras/ExtraService.cs:29`; `Sonarr.Api.V3/Series/SeriesController.cs:37`; `Sonarr.Api.V5/Series/SeriesController.cs:36` |
| `RenameCompletedEvent` | `MediaFiles/Events/RenameCompletedEvent.cs` | (empty) | `MediaFiles/RenameEpisodeFileService.cs:190,206` | `Notifications/NotificationService.cs:34` (async) |
| `DeleteCompletedEvent` | `MediaFiles/Events/DeleteCompletedEvent.cs` | (empty) | `MediaFiles/MediaFileDeletionService.cs:99,210` | `Notifications/NotificationService.cs:32` (async) |
| `EpisodeGrabbedEvent` | `Download/EpisodeGrabbedEvent.cs` | `RemoteEpisode Episode; int DownloadClientId; string DownloadClient; string DownloadClientName; string DownloadId` | `Download/DownloadService.cs:186,197` | `Download/History/DownloadHistoryService.cs:20`; `Download/Pending/PendingReleaseService.cs:46`; `Download/TrackedDownloads/DownloadMonitoringService.cs:16`; `History/HistoryService.cs:34`; `Notifications/NotificationService.cs:20`; `Sonarr.Api.V3/Episodes/EpisodeControllerWithSignalR.cs:18`; `Sonarr.Api.V5/Episodes/EpisodeControllerWithSignalR.cs:17` |
| `DownloadCompletedEvent` | `Download/DownloadCompletedEvent.cs` | `TrackedDownload TrackedDownload; int SeriesId; List<EpisodeFile> EpisodeFiles; GrabbedReleaseInfo Release` | `Download/CompletedDownloadService.cs:226,271`; `MediaFiles/EpisodeImport/Manual/ManualImportService.cs:624` | `Download/DownloadEventHub.cs:10`; `Download/History/DownloadHistoryService.cs:22`; `Notifications/NotificationService.cs:22` |
| `DownloadFailedEvent` | `Download/DownloadFailedEvent.cs` | `int SeriesId; List<int> EpisodeIds; QualityModel Quality; string SourceTitle; string DownloadClient; string DownloadId; bool SkipRedownload; ...` | `Download/FailedDownloadService.cs:129,146` | `Blocklisting/BlocklistService.cs:28`; `Download/DownloadEventHub.cs:9`; `Download/History/DownloadHistoryService.cs:23`; `Download/RedownloadFailedDownloadService.cs:13` (`[Last]`); `History/HistoryService.cs:36` |
| `DownloadIgnoredEvent` | `Download/DownloadIgnoredEvent.cs` | `int SeriesId; List<int> EpisodeIds; List<Language> Languages; QualityModel Quality; string SourceTitle; DownloadClientItemClientInfo DownloadClientInfo; string DownloadId; TrackedDownload TrackedDownload; string Message` | `Download/IgnoredDownloadService.cs:50` | `Download/History/DownloadHistoryService.cs:24`; `History/HistoryService.cs:40` |
| `DownloadCanBeRemovedEvent` | `Download/DownloadCanBeRemovedEvent.cs` | `TrackedDownload TrackedDownload` | `Download/DownloadProcessingService.cs:43` | `Download/DownloadEventHub.cs:11` |
| `DownloadsProcessedEvent` | `Download/DownloadsProcessedEvent.cs` | (empty) | `Download/DownloadProcessingService.cs:80` | `Download/TrackedDownloads/DownloadMonitoringService.cs:19`; `Notifications/NotificationService.cs:33` (async); `Extras/ExtraService.cs:30` |
| `ManualInteractionRequiredEvent` | `Download/ManualInteractionRequiredEvent.cs` | `RemoteEpisode Episode; TrackedDownload TrackedDownload; GrabbedReleaseInfo Release` | `Download/CompletedDownloadService.cs:291,293` | `Download/TrackedDownloads/DownloadMonitoringService.cs:18`; `Notifications/NotificationService.cs:31` |
| `UntrackedDownloadCompletedEvent` | `Download/UntrackedDownloadCompletedEvent.cs` | `Series Series; List<Episode> Episodes; List<EpisodeFile> EpisodeFiles; ParsedEpisodeInfo ParsedEpisodeInfo; string SourcePath` | `MediaFiles/EpisodeImport/Manual/ManualImportService.cs:591` | `Notifications/NotificationService.cs:23` |
| `PendingReleasesUpdatedEvent` | `Download/Pending/PendingReleasesUpdatedEvent.cs` | (empty) | `Download/Pending/PendingReleaseService.cs:547,553` | `Sonarr.Api.V3/Queue/{QueueController,QueueDetailsController,QueueStatusController}.cs`; `Sonarr.Api.V5/Queue/{QueueController,QueueDetailsController,QueueStatusController}.cs` |
| `TrackedDownloadRefreshedEvent` | `Download/TrackedDownloads/TrackedDownloadRefreshedEvent.cs` | `List<TrackedDownload> TrackedDownloads` | `Download/TrackedDownloads/DownloadMonitoringService.cs:78,188,195`; `Download/TrackedDownloads/TrackedDownloadService.cs:290,306,322,338,354` | `Queue/QueueService.cs:21`; `Queue/ObsoleteQueueService.cs:21` |
| `TrackedDownloadsRemovedEvent` | `Download/TrackedDownloads/TrackedDownloadsRemovedEvent.cs` | `List<TrackedDownload> TrackedDownloads` | `Download/TrackedDownloads/TrackedDownloadService.cs:74,88` | `Download/TrackedDownloads/DownloadMonitoringService.cs:20` |
| `RssSyncCompleteEvent` | `Indexers/RssSyncCompleteEvent.cs` | `ProcessedDecisions ProcessedDecisions` | `Indexers/RssSyncService.cs:64` | `Download/Pending/PendingReleaseService.cs:47` |
| `SeriesAddedEvent` | `Tv/Events/SeriesAddedEvent.cs` | `Series Series` | `Tv/SeriesService.cs:76` | `Tv/SeriesAddedHandler.cs:10`; `DataAugmentation/Scene/SceneMappingService.cs:26`; `Download/TrackedDownloads/TrackedDownloadService.cs:31` |
| `SeriesAddCompletedEvent` | `Tv/Events/SeriesAddCompletedEvent.cs` | `Series Series` | `Tv/SeriesScannedHandler.cs:49` | `Notifications/NotificationService.cs:25` |
| `SeriesImportedEvent` | `Tv/Events/SeriesImportedEvent.cs` | `List<int> SeriesIds` | `Tv/SeriesService.cs:84` | `Tv/SeriesAddedHandler.cs:11`; `DataAugmentation/Scene/SceneMappingService.cs:27` |
| `SeriesUpdatedEvent` | `Tv/Events/SeriesUpdatedEvent.cs` | `Series Series` | `Tv/RefreshSeriesService.cs:77,132` | `MediaCover/MediaCoverService.cs:25` (async); `Download/Pending/PendingReleaseService.cs:44`; `DataAugmentation/Xem/XemService.cs:13`; `Sonarr.Api.V3/Series/SeriesController.cs:34`; `Sonarr.Api.V5/Series/SeriesController.cs:33` |
| `SeriesEditedEvent` | `Tv/Events/SeriesEditedEvent.cs` | `Series Series; Series OldSeries; bool EpisodesChanged` | `Tv/SeriesService.cs:230` | `Tv/SeriesEditedService.cs:9`; `Download/Pending/PendingReleaseService.cs:43`; `Download/TrackedDownloads/TrackedDownloadService.cs:32`; `Sonarr.Api.V3/Series/SeriesController.cs:35`; `Sonarr.Api.V5/Series/SeriesController.cs:34` |
| `SeriesDeletedEvent` | `Tv/Events/SeriesDeletedEvent.cs` | `List<Series> Series; bool DeleteFiles; bool AddImportListExclusion` | `Tv/SeriesService.cs:167` | `Tv/EpisodeService.cs:47` (async); `MediaCover/MediaCoverService.cs:26` (async); `MediaFiles/MediaFileService.cs:30` (async); `MediaFiles/MediaFileDeletionService.cs:28` (async); `Extras/Files/ExtraFileService.cs:29` (async); `History/HistoryService.cs:39`; `ImportLists/Exclusions/ImportListExclusionService.cs:21` (async); `Blocklisting/BlocklistService.cs:29` (async); `Download/History/DownloadHistoryService.cs:25`; `Download/Pending/PendingReleaseService.cs:45`; `Download/TrackedDownloads/TrackedDownloadService.cs:34`; `Notifications/NotificationService.cs:26`; `DataAugmentation/Scene/SceneMappingService.cs` (consumes via dispatch); `Sonarr.Api.V3/Series/SeriesController.cs:36`; `Sonarr.Api.V5/Series/SeriesController.cs:35` |
| `SeriesMovedEvent` | `Tv/Events/SeriesMovedEvent.cs` | `Series Series; string SourcePath; string DestinationPath` | `Tv/MoveSeriesService.cs:76` | (none in production — see **MSG-Q12**) |
| `SeriesBulkEditedEvent` | `Tv/Events/SeriesBulkEditedEvent.cs` | `List<Series> Series` | `Tv/SeriesService.cs:260` | `Download/TrackedDownloads/TrackedDownloadService.cs:33`; `Sonarr.Api.V3/Series/SeriesController.cs:38`; `Sonarr.Api.V5/Series/SeriesController.cs:37` |
| `SeriesRefreshStartingEvent` | `Tv/Events/SeriesRefreshStartingEvent.cs` | `bool ManualTrigger` | `Tv/RefreshSeriesService.cs:215` | `DataAugmentation/Xem/XemService.cs:13`; `DataAugmentation/Scene/SceneMappingService.cs:25` |
| `SeriesRefreshCompleteEvent` | `Tv/Events/SeriesRefreshCompleteEvent.cs` | (empty) | `Tv/RefreshSeriesService.cs:284` | (none in production — see **MSG-Q12**) |
| `EpisodeInfoRefreshedEvent` | `Tv/Events/EpisodeInfoRefreshedEvent.cs` | `Series Series; ReadOnlyCollection<Episode> Added/Updated/Removed` | `Tv/RefreshEpisodeService.cs:131` | `Tv/EpisodeRefreshedService.cs:19`; `Download/TrackedDownloads/TrackedDownloadService.cs:30` |
| `MediaCoversUpdatedEvent` | `MediaCover/MediaCoversUpdatedEvent.cs` | `Series Series; bool Updated` | `MediaCover/MediaCoverService.cs:237` | `Extras/ExtraService.cs:26`; `Sonarr.Api.V3/Series/SeriesController.cs:39`; `Sonarr.Api.V5/Series/SeriesController.cs:38` |
| `UpdateInstalledEvent` | `Update/Events/UpdateInstalledEvent.cs` | `Version PreviousVerison; Version NewVersion` (note typo "Verison" in C# field) | `Update/History/UpdateHistoryService.cs:99` | `Notifications/NotificationService.cs:30` |
| `HealthCheckCompleteEvent` | `HealthCheck/HealthCheckCompleteEvent.cs` | (empty) | `HealthCheck/HealthCheckService.cs:134` | `Notifications/NotificationService.cs:35` (async); `Sonarr.Api.V3/Health/HealthController.cs:16`; `Sonarr.Api.V5/Health/HealthController.cs:15` |
| `HealthCheckFailedEvent` | `HealthCheck/HealthCheckFailedEvent.cs` | `HealthCheck HealthCheck; bool IsInStartupGracePeriod` | `HealthCheck/HealthCheckService.cs:122,198` | `Notifications/NotificationService.cs:28` |
| `HealthCheckRestoredEvent` | `HealthCheck/HealthCheckRestoredEvent.cs` | `HealthCheck PreviousCheck; bool IsInStartupGracePeriod` | `HealthCheck/HealthCheckService.cs:113` | `Notifications/NotificationService.cs:29` |
| `ConfigSavedEvent` | `Configuration/Events/ConfigSavedEvent.cs` | (empty) — see above | (above) | (above) |
| `ConfigFileSavedEvent` | `Configuration/Events/ConfigFileSavedEvent.cs` | (empty) | (above) | (above) |
| `CustomFormatAddedEvent` | `CustomFormats/Events/CustomFormatAddedEvent.cs` | `CustomFormat CustomFormat` | `CustomFormats/CustomFormatService.cs:68` | `Profiles/Qualities/QualityProfileService.cs:29` |
| `CustomFormatDeletedEvent` | `CustomFormats/Events/CustomFormatDeletedEvent.cs` | `CustomFormat CustomFormat` | `CustomFormats/CustomFormatService.cs:78,91` | `Profiles/Qualities/QualityProfileService.cs:30` |
| `QualityProfileUpdatedEvent` | `Profiles/Qualities/QualityProfileUpdatedEvent.cs` | `int Id` (uses C# 12 primary-constructor syntax) | `Profiles/Qualities/QualityProfileService.cs:62` | `Download/Pending/PendingReleaseService.cs:48` |
| `TagsUpdatedEvent` | `Tags/TagsUpdatedEvent.cs` | (empty) | `Tags/TagService.cs:170,180,194` | `Sonarr.Api.V3/Tags/TagController.cs:18`; `Sonarr.Api.V5/Tags/TagController.cs:19` |
| `AutoTagsUpdatedEvent` | `AutoTagging/AutoTagsUpdatedEvent.cs` | (empty) | `AutoTagging/AutoTaggingService.cs:61,69,79` | `Sonarr.Api.V3/Tags/TagController.cs:19`; `Sonarr.Api.V5/Tags/TagController.cs:20` |
| `SceneMappingsUpdatedEvent` | `DataAugmentation/Scene/SceneMappingsUpdatedEvent.cs` | (empty) | `DataAugmentation/Scene/SceneMappingService.cs:216` | (none in production — see **MSG-Q12**) |
| `QueueUpdatedEvent` | `Queue/QueueUpdatedEvent.cs` | (empty) | `Queue/QueueService.cs:104` | `Sonarr.Api.V5/Queue/{QueueController,QueueDetailsController,QueueStatusController}.cs` (V5-only consumers) |
| `ObsoleteQueueUpdatedEvent` | `Queue/ObsoleteQueueUpdatedEvent.cs` | (empty) | `Queue/ObsoleteQueueService.cs:106` | `Sonarr.Api.V3/Queue/{QueueController,QueueDetailsController,QueueStatusController}.cs` (V3-only — the V3/V5 split exists because the queue resource shape changed; see `signalr/inventory.md`) |
| `ProviderAddedEvent<TProvider>` | `ThingiProvider/Events/ProviderAddedEvent.cs` | `ProviderDefinition Definition` | `ThingiProvider/ProviderFactory.cs:117` | `Sonarr.Api.V3/ProviderControllerBase.cs:20`; `Sonarr.Api.V5/Provider/ProviderControllerBase.cs:20` |
| `ProviderUpdatedEvent<TProvider>` | `ThingiProvider/Events/ProviderUpdatedEvent.cs` | `ProviderDefinition Definition` | `ThingiProvider/ProviderFactory.cs:125,134` | `Indexers/CachedIndexerSettingsProvider.cs:17`; `ImportLists/ImportListUpdatedHandler.cs:7`; `Sonarr.Api.V3/ProviderControllerBase.cs:21`; `Sonarr.Api.V5/Provider/ProviderControllerBase.cs:21` |
| `ProviderDeletedEvent<TProvider>` | `ThingiProvider/Events/ProviderDeletedEvent.cs` | `int ProviderId` | `ThingiProvider/ProviderFactory.cs:143,152` | `Indexers/CachedIndexerSettingsProvider.cs:17`; `ImportLists/{ImportListSyncService,ImportListItems/ImportListItemService,Exclusions/ImportListExclusionService}.cs`; `ThingiProvider/Status/ProviderStatusServiceBase.cs:20`; `Sonarr.Api.V3/ProviderControllerBase.cs:22`; `Sonarr.Api.V5/Provider/ProviderControllerBase.cs:22` |
| `ProviderStatusChangedEvent<TProvider>` | `ThingiProvider/Events/ProviderStatusChangedEvent.cs` | `int ProviderId; ProviderStatusBase Status` | `ThingiProvider/Status/ProviderStatusServiceBase.cs:81,138` | (none in production — see **MSG-Q12**) |

**Total event-class count cataloged: 60** distinct concrete `IEvent` types in production code (counting each generic `ModelEvent<T>` / `ProviderXxxEvent<T>` once, since the bus keys on the open type at runtime via `GetEventName`). In addition: 4 test-only events (`EventA`, `EventB`, `FakeEvent`, `FakeEvent2`) live under `src/NzbDrone.Core.Test/`.

## DB tables touched
- **None directly.** The event aggregator and progress messaging do not read or write any database tables. Handlers that subscribe may; the bus itself is in-memory.

## Config keys
- **None.** The bus has no externally configurable knobs (no thread cap, no buffer size, no queue depth). The progress NLog rule is hard-coded `("*", LogLevel.Trace)` at `ProgressMessageTarget.cs:59`.

## Jobs / commands (`IExecute<TCommand>`)
- `MessagingCleanupCommand` → `CleanupCommandMessagingService.Execute` (`src/NzbDrone.Core/Messaging/Commands/CleanupCommandMessagingService.cs:1-17`) — runs `_commandQueueManager.CleanCommands()`. The full `Command` / `CommandQueue` mechanism is documented in `system.md` "Jobs / commands". Cited only because this is the only `IExecute<T>` that lives under `Messaging/`.
- The `TestCommand` / `TestCommandExecutor` (`src/NzbDrone.Core/Messaging/Commands/TestCommand.cs`, `TestCommandExecutor.cs`) is dev/diag-only.

## External services
- **None.** Pure in-process subsystem.

## Notable quirks / TODOs / HACKs found in source

1. **Empty `IProcessMessage` markers (`Messaging/IProcessMessage.cs`).** Vestigial — never referenced outside of `IHandle.cs`'s declaration. Drop in the port. (**MSG-Q2**)
2. **Empty `IMessage` marker (`Common/Messaging/IMessage.cs`).** Vestigial — only `IEvent` references it. Drop. (**MSG-Q3**)
3. **`PublishEvent` swallows handler exceptions.** No way for a producer to know that a handler failed. Tests confirm "broken handler should not affect others handlers" — by design (`EventAggregatorFixture.cs:67-81`). The port should preserve, but consider exposing structured failure metrics.
4. **`PublishEvent` is "fire and partially forget".** Sync handlers run to completion before `PublishEvent` returns; async handlers fan out on the ThreadPool with no completion signal. This means a `BasicRepository.Insert(...)` followed by `PublishEvent(new ModelEvent<T>(.., Created))` returns immediately to the caller while async `IHandleAsync<ModelEvent<T>>` handlers may still be running. Producers that need ordering with respect to async handlers do not get it.
5. **Async-handler concurrency is unbounded.** Comment block at `EventAggregator.cs:66-81` and the disabled test at `EventAggregatorFixture.cs:84-125` suggest the original intent was to cap at 3. Currently uses `TaskScheduler.Default` (unbounded ThreadPool). (**MSG-Q9**)
6. **First-publish-of-an-event-type is `O(reflection × DI scan)` and locked.** A new event class published for the first time blocks all other publishes briefly while the subscriber arrays are built. Ongoing publishes of already-seen events do not contend (the lookup is read-only after build). For the Go port: a `sync.Map` of `reflect.Type → handlerSet` populated lazily would behave similarly without a global lock.
7. **`GetEventName` collapses `Foo.Bar.SomeEvent<X>` to `"SomeEvent<X>"` using simple type name only** (`EventAggregator.cs:147-155`). No production collisions; brittle for the future. (**MSG-Q6**)
8. **`LifecycleEventAttribute` shutdown gate** (`EventAggregator.cs:60-64`) blocks `ApplicationStartedEvent` / `ApplicationShutdownRequested` etc. once `IRuntimeInfo.IsExiting` is true. Effect: lifecycle events don't fire reentrantly during shutdown. (**MSG-Q8**)
9. **`ProgressMessageTarget` self-installs from `IHandle<ApplicationStartedEvent>`.** Side-effecting NLog mutation from a handler is unusual; in tests this leaks into other tests if not isolated. (**PROG-Q5**)
10. **`ProgressMessageContext` has both `[ThreadStatic]` and `AsyncLocal`** (`ProgressMessageContext.cs:9-12`). Read-prefers ThreadStatic; write-sets-both. Reasoning unclear. (**PROG-Q1**)
11. **`CommandModel.Message` is in-memory only.** `_commandQueueManager.SetMessage` updates the queue (`system.md` "Job scheduler / command queue"), but commands with `SendUpdatesToClient = true` are not persisted. A process restart loses the latest progress message. (**PROG-Q4**)
12. **No re-entrancy detection on the event bus.** A handler can publish back into the bus indefinitely; no depth limit. The only guard is `ProgressMessageContext._reentrancyLock`, which protects only the NLog→bus loop. (**MSG-Q10**)
13. **`UpdateInstalledEvent` field typo: `PreviousVerison`** (`Update/Events/UpdateInstalledEvent.cs:5`). Carries through to JSON if anyone serializes it.
14. **`HealthCheckService : IHandleAsync<IEvent>`** is the only global event tap. Used to drive `[CheckOnAttribute]`-tagged event-driven health checks. Be careful porting: every event in the system passes through this path (and through `_taskFactory.StartNew`).

## Test files (the port must keep passing equivalents)
- `src/NzbDrone.Core.Test/Messaging/Events/EventAggregatorFixture.cs` — sync fan-out, type filtering, broken-handler isolation. Async test commented out.
- `src/NzbDrone.Core.Test/Messaging/Commands/CommandExecutorFixture.cs` — exercises the command bus including `BroadcastCommandUpdate` / `CommandUpdatedEvent` / `CommandExecutedEvent` publishing (cross-references `system.md` jobs tests).
- `src/NzbDrone.Core.Test/Messaging/Commands/CommandQueueManagerFixture.cs` — adjacent (queue mechanics, owned by `system.md`).
- `src/NzbDrone.Core.Test/Messaging/Commands/CommandEqualityComparerFixture.cs` — adjacent.
- No `ProgressMessaging` tests exist in the repository. (**PROG-Q6**)

## Open questions

### Event bus (MSG-*)

| ID | Question | Status | Blocks phase |
|----|----------|--------|--------------|
| MSG-Q1 | `IHandleAsync<T>.HandleAsync` returns `void` (not `Task`). Why was this not made awaitable? Goes through `Task.Factory.StartNew(() => handler.HandleAsync(...))` which awaits nothing. Port intent: define `Handle(ctx, event) error` and accept that errors are logged, not propagated? | unresolved | Phase 2 (event bus port) |
| MSG-Q2 | Are the empty `IProcessMessage` / `IProcessMessageAsync` / `IProcessMessage<T>` / `IProcessMessageAsync<T>` markers (`Messaging/IProcessMessage.cs`) safe to drop in the Go port? Confirm no external assembly (`NzbDrone.Mono`, `NzbDrone.Windows`, plugins) implements them. | unresolved | Phase 2 |
| MSG-Q3 | Same question for `IMessage` (`Common/Messaging/IMessage.cs`) — only `IEvent` references it. Drop? | unresolved | Phase 2 |
| MSG-Q4 | `EventHandleOrderAttribute` only sorts sync handlers. Should the Go port preserve that (sync only, async unordered) or support per-handler ordering across both? Existing two production callers are both sync `[Last]`, so the limitation is invisible in practice. | unresolved | Phase 2 |
| MSG-Q5 | Why is `MediaFileDeletionService.Handle(EpisodeFileDeletedEvent)` ordered `[Last]`? No code comment explains. Hypothesis: history/notifications must observe the event with the file row still queryable; the deletion happens after they finish. Confirm by reading the handler. | unresolved | Phase 1 (mediafiles port) |
| MSG-Q6 | `GetEventName` collapses generic events to `"Name<ArgSimpleName>"`. Is the namespace-stripping a problem for any current event type? `ModelEvent<Series>` (`Tv.Series`) is the only ambiguous case if a different `Series` type ever gets ported. | unresolved | Phase 2 |
| MSG-Q7 | If `serviceFactory.BuildAll` throws (container disposed during shutdown), the publish is silently dropped (`EventAggregator.cs:94-98`). What events are typically affected — is this a real risk during the shutdown sequence? | unresolved | Phase 2 |
| MSG-Q8 | `LifecycleEventAttribute` blocks lifecycle events during shutdown. What concrete bug does this prevent? Re-entrant `ApplicationShutdownRequested`? | unresolved | Phase 2 |
| MSG-Q9 | `IHandleAsync<T>` runs on `TaskScheduler.Default` with no concurrency cap. The disabled test at `EventAggregatorFixture.cs:84-125` references "max 3 concurrent". Is there a real bound we should enforce in the Go port? Or accept "as much as the runtime allows"? | unresolved | Phase 2 |
| MSG-Q10 | Should the Go port introduce a depth/recursion guard (e.g. context-stored "events being published") to prevent runaway re-entrancy? Existing C# has none beyond `ProgressMessageContext._reentrancyLock`. | unresolved | Phase 2 |
| MSG-Q11 | `ProgressMessageContext.CommandModel` reads `[ThreadStatic]` first, falls back to `AsyncLocal`. Is there a real scenario where they differ? Suspect: `await` inside a handler could leave `[ThreadStatic]` null on the resumed thread but `AsyncLocal` still set. (Also indexed under PROG-Q1.) | unresolved | Phase 2 |
| MSG-Q12 | Several events have **zero production consumers**: `EpisodeImportFailedEvent`, `SeriesMovedEvent`, `SeriesRefreshCompleteEvent`, `SceneMappingsUpdatedEvent`, `ProviderStatusChangedEvent<T>`. Are they dead? Used only by external plugins/notifications? Or are they consumed via `IHandleAsync<IEvent>` global tap (only `HealthCheckService`, which uses `[CheckOnAttribute]`, so only events listed in `[CheckOn(...)]` attributes flow into health checks). Decision: keep, for parity, but confirm. | unresolved | Phase 2 |

### Progress messaging (PROG-*)

| ID | Question | Status | Blocks phase |
|----|----------|--------|--------------|
| PROG-Q1 | `ProgressMessageContext` mirrors writes into both `[ThreadStatic]` and `AsyncLocal`. Read-prefers `[ThreadStatic]`. Why? The Go equivalent is `context.Context`-passed, which subsumes both — likely no fallback needed. (Same as MSG-Q11.) | unresolved | Phase 2 |
| PROG-Q2 | Does any `IExecute<TCommand>` ever execute a *nested* command synchronously such that `ProgressMessageContext.CommandModel != null` at entry? The null-check at `CommandExecutor.cs:79` exists for this case. | unresolved | Phase 2 |
| PROG-Q3 | What is the exact API used to mark a log line with a `Status` property so it gets caught by `ProgressMessageTarget`? (`logEvent.Properties.ContainsKey("Status")`.) Find the `_logger.ProgressMessage(...)` extension or `WithProperty("Status", ...)` callers across `Tv/`, `MediaFiles/`, `Indexers/`. | unresolved | Phase 2 |
| PROG-Q4 | `CommandModel.Message` is in-memory only (since `SendUpdatesToClient = true` commands are not persisted, per `system.md`). Is the loss-on-restart behavior intentional? Should the Go port persist last-status to DB, or replicate the volatile model? | unresolved | Phase 2 |
| PROG-Q5 | `ProgressMessageTarget.Handle(ApplicationStartedEvent)` mutates `LogManager.Configuration` at runtime. In Go terms: a logger middleware that registers itself on app startup. How robust is this when `LogManager.ReconfigExistingLoggers()` is called for other reasons later (e.g. `ReconfigureLogging` which handles `ConfigFileSavedEvent`)? Does the rule survive? | unresolved | Phase 2 |
| PROG-Q6 | No `ProgressMessaging` tests exist. Should we add equivalence tests in Go (status line → CommandUpdatedEvent → SignalR `command` resource) as part of the port, or accept the gap? | unresolved | Phase 2 |

---

**Cross-cutting note for the porter:** the event bus is the *only* in-process pub/sub primitive in Sonarr. Almost every cross-subsystem effect — "when an episode is imported, also update history, also update SignalR, also fire notifications, also refresh the queue" — is wired through `PublishEvent` + `IHandle<T>`. The Go target should keep one event-bus implementation and one set of marker types; do not multiply small bus instances per subsystem. Conversely, in-Go alternatives like channels-of-events have very different propagation semantics (one-to-one, async by default, can backpressure) — they are the wrong shape for Sonarr's "publish to whoever happens to subscribe" model. A `map[reflect.Type][]handler` + `sync.RWMutex` + a worker pool for the async tier is the closest port; alternatively, a `interface{ Handle(context.Context, Event) error }` registry keyed by type-asserted dispatch.
