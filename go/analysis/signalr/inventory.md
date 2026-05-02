# SignalR Inventory

Phase 0.5 deep-dive. Everything below is read straight from the C# source — the dynamic recording (capturing live message envelopes) happens later, once the C# binary is bootable.

## The hub

There is exactly one SignalR hub: `MessageHub` (`NzbDrone.SignalR/MessageHub.cs:26`).

- Mount path: registered in the host as `/signalr/messages` (the actual `app.MapHub<MessageHub>("/signalr/messages")` lives in the host bootstrap; the SignalR project itself does not declare the route). SignalR clients also negotiate at `/signalr/messages/negotiate`.
- Auth: clients authenticate to SignalR using the `SignalR` API-key scheme (`Sonarr.Http/Authentication/AuthenticationBuilderExtensions.cs:62-66`), which reads the API key from `X-Api-Key` header or `?access_token=` query (per SignalR JS client convention).
- Hub class is a stock `Microsoft.AspNetCore.SignalR.Hub` — it has no client-callable RPC methods. Client→server traffic is just connection lifecycle (negotiate / connect / disconnect). Servers are the only senders of business messages.
- Tracks live connections in a static `HashSet<string> _connections` keyed by `Hub.Context.ConnectionId`. `MessageHub.IsConnected` is the global "any client subscribed?" flag used as a short-circuit by `IBroadcastSignalRMessage.IsConnected` (see `Broadcaster` below).

### Hub methods

| Direction | Name | Trigger | Payload (Body) | Notes |
|-----------|------|---------|----------------|-------|
| server→client | `receiveMessage` | `OnConnectedAsync` (`MessageHub.cs:41-58`) | `SignalRMessage { Name="version", Body=new { Version: "<BuildInfo.Version>" } }` | Sent to *all* connected clients (`Clients.All`) when *any* client connects. The new client receives the version string as its first event. |
| server→client | `receiveMessage` | `IBroadcastSignalRMessage.BroadcastMessage(...)` (`MessageHub.cs:18-22`) | `SignalRMessage` (see envelope below) | Sent to `Clients.All`. This is the single broadcast pipeline used by every emitter in the codebase. |
| client→server | (none) | — | — | The hub has zero `[HubMethodName]`s; client cannot call the server. |
| connection lifecycle | `OnConnectedAsync` / `OnDisconnectedAsync` | implicit | — | Tracks `_connections`. |

### Envelope shape

Defined by `SignalRMessage` (`NzbDrone.SignalR/SignalRMessage.cs:5-15`):

```text
SignalRMessage {
  Name:    string                // resource name, e.g. "series", "queue", "command", "health", "version"
  Body:    object                // payload — see "Body shapes" below
  Action:  ModelAction           // [JsonIgnore] in JSON — internal-only, not visible to clients
  Version: int? | null           // 3 or 5, set by RestControllerWithSignalR; null for non-versioned messages (e.g. "version")
}
```

`ModelAction` is excluded from the wire format (`JsonIgnore` at line 11). The action is communicated *via the body shape* instead — see next section.

The wire JSON (camelCase by `STJson` default) is therefore:

```json
{ "name": "<resource>", "body": { ... }, "version": 3 }
```

For resource-change events, the `body` is a `ResourceChangeMessage<TResource>` (`Sonarr.Http/ResourceChangeMessage.cs:7-28`) which serializes to:

```json
{ "resource": <TResource> | null, "action": "updated"|"created"|"deleted"|"sync" }
```

- `Action.Created` → body has populated `resource`.
- `Action.Updated` → body has populated `resource`.
- `Action.Deleted` → body has `resource` populated only with the `Id` (if known) — see `RestControllerWithSignalR.BroadcastResourceChange(action, id)` at `Sonarr.Http/REST/RestControllerWithSignalR.cs:59-67`. For SeriesDeletedEvent the V3 path passes a *full* resource via `GetSeriesResource(series)` first.
- `Action.Sync` → body has `resource = null` (constructor at `ResourceChangeMessage.cs:13` rejects any non-Delete/Sync without a resource).

## The broadcaster

`SignalRMessageBroadcaster` (`NzbDrone.SignalR/MessageHub.cs:9-24`) implements `IBroadcastSignalRMessage` (`NzbDrone.SignalR/IBroadcastSignalRMessage.cs:5-9`).

- `BroadcastMessage(SignalRMessage)` → `_hubContext.Clients.All.SendAsync("receiveMessage", message)` — every business event fans out to *every* connected client.
- `IsConnected` short-circuits the broadcast when nobody is listening (callers all check this first).
- Singleton DI lifetime, injected wherever broadcasting is needed.

## How messages get produced

There are two concrete `new SignalRMessage(...)` allocations in the entire codebase:

1. `MessageHub.cs:48` — the version handshake on connect.
2. `Sonarr.Http/REST/RestControllerWithSignalR.cs:81` and `:104` — the resource-change path (with-resource and without-resource flavors). All controller-level broadcasts funnel through `BroadcastResourceChange(...)` here.

`RestControllerWithSignalR<TResource, TModel>` (`Sonarr.Http/REST/RestControllerWithSignalR.cs:10-115`) is the engine:

- Constructor reads `[VersionedApiController]` to set `Resource` (the URL fragment, e.g. `"series"`, `"queue/status"`) and `Version` (3 or 5).
- Implements `IHandle<ModelEvent<TModel>>` — listens on the in-process event bus. For every persisted `ModelEvent`, it calls `BroadcastResourceChange(action, model.Id)` for non-Sync, and additionally `BroadcastResourceChange(Sync)` for `Deleted`/`Sync`.
- Controllers also emit *manually* by calling `BroadcastResourceChange(...)` from inside event-handler methods on application-domain events (e.g. `EpisodeImportedEvent`, `HealthCheckCompleteEvent`).
- Namespace check at `RestControllerWithSignalR.cs:79,102`: only controllers whose namespace contains `V3` or `V5` actually broadcast. (So the same DI-resolved instance can exist in V3 and V5 — both fire, with different `Version` values, and the client filters.)

The `Resource` field maps to `Name` on the wire. So `SeriesController` (V3) broadcasts `{ name: "series", version: 3, body: { resource, action } }` while `SeriesController` (V5) broadcasts `{ name: "series", version: 5, body: { resource, action } }`.

## Server-pushed events catalogue

Every server-pushed event currently emitted, derived from every concrete `BroadcastResourceChange(...)` call site plus every `RestControllerWithSignalR<,>` subclass (which implicitly handles `ModelEvent<TModel>`).

The "Body shape" column is the JSON `body.resource` payload class (camelCase'd) when present. When `action` is `sync`, body has no `resource`. When `action` is `deleted`, body has `resource` populated minimally (id only) unless noted.

| `name` | Versions | Triggers | `action` values | Body shape | Source |
|--------|----------|----------|-----------------|-------------|--------|
| `version`     | (no version)  | Hub `OnConnectedAsync` | (n/a — uses different body shape; not a `ResourceChangeMessage`) | `{ Version: string }` | `MessageHub.cs:48` |
| `series`      | 3, 5          | `ModelEvent<Series>` (DB), `EpisodeImportedEvent`, `EpisodeFileDeletedEvent`, `SeriesUpdatedEvent`, `SeriesEditedEvent`, `SeriesDeletedEvent`, `SeriesRenamedEvent`, `SeriesBulkEditedEvent`, `MediaCoversUpdatedEvent` | created, updated, deleted, sync | SeriesResource | `Sonarr.Api.V3/Series/SeriesController.cs:299-360`, `Sonarr.Api.V5/Series/SeriesController.cs:340-422`, plus `PUT /{id}` and `PUT /{id}/season` (V5) at `:218,242` |
| `episode`     | 3, 5          | `ModelEvent<Episode>`, `EpisodeGrabbedEvent`, `EpisodeImportedEvent`, `EpisodeFileDeletedEvent` | updated, deleted, sync | EpisodeResource (with `grabbed: true` flag for grabbed events) | `Sonarr.Api.V3/Episodes/EpisodeControllerWithSignalR.cs:123-151`, `Sonarr.Api.V5/Episodes/EpisodeControllerWithSignalR.cs:122-150` |
| `episodefile` | 3, 5          | `ModelEvent<EpisodeFile>`, `EpisodeFileAddedEvent`, `EpisodeFileDeletedEvent` | created, updated, deleted, sync | EpisodeFileResource | `Sonarr.Api.V3/EpisodeFiles/EpisodeFileController.cs:231-241`, `Sonarr.Api.V5/EpisodeFiles/EpisodeFileController.cs:194-204` |
| `command`     | 3, 5          | `CommandUpdatedEvent` (debounced 100ms via `Debouncer.Execute` at `:107`); after `MessagingCleanupCommand` completes also fires `sync` | updated, sync | CommandResource | `Sonarr.Api.V3/Commands/CommandController.cs:111-128`, `Sonarr.Api.V5/Commands/CommandController.cs:111-129` |
| `health`      | 3, 5          | `HealthCheckCompleteEvent` | sync | (no body resource) | `Sonarr.Api.V3/Health/HealthController.cs:44-48`, `Sonarr.Api.V5/Health/HealthController.cs:43-47` |
| `queue`       | 3, 5          | `ModelEvent<Queue>`, `ObsoleteQueueUpdatedEvent` (V3) / `QueueUpdatedEvent` (V5), `PendingReleasesUpdatedEvent` | sync | (no body resource) | `Sonarr.Api.V3/Queue/QueueController.cs:398-408`, `Sonarr.Api.V5/Queue/QueueController.cs:397-407` |
| `queue/details` | 3, 5        | `ObsoleteQueueUpdatedEvent` (V3) / `QueueUpdatedEvent` (V5), `PendingReleasesUpdatedEvent` | sync | (no body resource) | `Sonarr.Api.V3/Queue/QueueDetailsController.cs:63-73`, `Sonarr.Api.V5/Queue/QueueDetailsController.cs:65-75` |
| `queue/status`  | 3, 5        | `ObsoleteQueueUpdatedEvent` (V3) / `QueueUpdatedEvent` (V5), `PendingReleasesUpdatedEvent`; debounced 5s via `Debouncer` at `:31` | updated | QueueStatusResource (full snapshot) | `Sonarr.Api.V3/Queue/QueueStatusController.cs:70-86`, `Sonarr.Api.V5/Queue/QueueStatusController.cs:63-78` |
| `tag`         | 3, 5          | `ModelEvent<Tag>`, `TagsUpdatedEvent`, `AutoTagsUpdatedEvent` | sync, created, updated, deleted | TagResource | `Sonarr.Api.V3/Tags/TagController.cs:68-78`, `Sonarr.Api.V5/Tags/TagController.cs:71-81` |
| `qualitydefinition` | 3, 5    | `ModelEvent<QualityDefinition>`, `CommandExecutedEvent("ResetQualityDefinitions")` | sync, updated | QualityDefinitionResource | `Sonarr.Api.V3/Qualities/QualityDefinitionController.cs:91-98`, `Sonarr.Api.V5/Qualities/QualityDefinitionController.cs:79-86` |
| `system/task` | 3, 5          | `ModelEvent<ScheduledTask>`, `CommandExecutedEvent` | sync | (no body resource) | `Sonarr.Api.V3/System/Tasks/TaskController.cs:63-67`, `Sonarr.Api.V5/System/Tasks/TaskController.cs:60-64` |
| `rootfolder`  | 3, 5          | `ModelEvent<RootFolder>` only (no manual emitters) | created, updated, deleted, sync | RootFolderResource | `Sonarr.Api.V3/RootFolders/RootFolderController.cs:14`, `Sonarr.Api.V5/RootFolders/RootFolderController.cs:16` (event handler is inherited from `RestControllerWithSignalR`) |
| `downloadclient` | 3 only       | `ProviderAddedEvent<IDownloadClient>`, `ProviderUpdatedEvent<IDownloadClient>`, `ProviderDeletedEvent<IDownloadClient>` | created, updated, deleted | DownloadClientResource | `Sonarr.Api.V3/ProviderControllerBase.cs:275-291` |
| `importlist`  | 3 only        | `ProviderAddedEvent<IImportList>`, etc. | created, updated, deleted | ImportListResource | (V3 only) `Sonarr.Api.V3/ImportLists/ImportListController.cs:11` |
| `indexer`     | 3, 5          | `ProviderAddedEvent<IIndexer>`, etc. | created, updated, deleted | IndexerResource | `Sonarr.Api.V3/Indexers/IndexerController.cs:10`, `Sonarr.Api.V5/Indexers/IndexerController.cs:11` |
| `metadata`    | 3, 5          | `ProviderAddedEvent<IMetadata>`, etc. | created, updated, deleted | MetadataResource | `Sonarr.Api.V3/Metadata/MetadataController.cs:11`, `Sonarr.Api.V5/Metadata/MetadataController.cs:11` |
| `notification`| 3 only        | `ProviderAddedEvent<INotification>`, etc. | created, updated, deleted | NotificationResource | `Sonarr.Api.V3/Notifications/NotificationController.cs:11` |
| `connection`  | 5 only        | `ProviderAddedEvent<INotification>`, etc. (`ConnectionController` is the V5 rebranding of `NotificationController`) | created, updated, deleted | ConnectionResource | `Sonarr.Api.V5/Connections/ConnectionController.cs:11` |

### Notes on inherited / implicit emitters

`RestControllerWithSignalR<TResource, TModel>` is itself an `IHandle<ModelEvent<TModel>>` (`RestControllerWithSignalR.cs:10`). Any controller that extends it automatically broadcasts on every `ModelEvent` for that model — even without an explicit `Handle` method. This is how `tag`, `qualitydefinition`, `rootfolder`, `series`, `episode`, `episodefile`, `command`, `queue`, `queue/details`, `queue/status`, `health`, `system/task`, `downloadclient`/`importlist`/`indexer`/`metadata`/`notification`/`connection` *all* receive the standard `created/updated/deleted/sync` envelope automatically — the explicit `Handle(...)` overrides only add additional broadcast triggers on top.

Some controllers don't make use of the `ModelEvent<>` path at all because their model type is synthetic:
- `QueueController` (`TModel = Queue`): there is no DB-level `ModelEvent<Queue>` — `Queue` is computed in memory. All broadcasts happen via the manual `Handle` overrides on `QueueUpdatedEvent` / `PendingReleasesUpdatedEvent`.
- `HealthController` (`TModel = HealthCheck`): same. `HealthCheck` isn't persisted; the manual `Handle(HealthCheckCompleteEvent)` is the only emitter.
- `CommandController` (`TModel = CommandModel`): same. Commands live in an in-memory queue.
- `TaskController` (`TModel = ScheduledTask`): same.

### Special V5-only emitters worth noting

- `series:updated` is also emitted directly from V5 `SeriesController.UpdateSeasonMonitored` (`PUT /api/v5/series/{id}/season`) at `Sonarr.Api.V5/Series/SeriesController.cs:242` — V3 has no equivalent endpoint.

## Producer/consumer wiring

Internal in-process event bus (`NzbDrone.Core.Messaging.Events.IEventAggregator`, not part of this inventory) carries the application-domain events that controllers subscribe to. Relevant types referenced from controllers:

- DB-layer: `ModelEvent<TModel>`, `ModelAction { Created, Updated, Deleted, Sync }` — fired from `BasicRepository<T>` after each insert/update/delete and from sync points like `ScheduledTaskService` / `TagsUpdatedEvent`.
- Provider lifecycle: `ProviderAddedEvent<T>`, `ProviderUpdatedEvent<T>`, `ProviderDeletedEvent<T>` (`NzbDrone.Core.ThingiProvider.Events`) fired from `ProviderRepository<T>`.
- Command lifecycle: `CommandUpdatedEvent`, `CommandExecutedEvent` (`NzbDrone.Core.Messaging.Events`).
- Series lifecycle: `SeriesAddedEvent`, `SeriesUpdatedEvent`, `SeriesEditedEvent`, `SeriesDeletedEvent`, `SeriesRenamedEvent`, `SeriesBulkEditedEvent`, `MediaCoversUpdatedEvent` (`NzbDrone.Core.Tv.Events`).
- Episode/file lifecycle: `EpisodeGrabbedEvent`, `EpisodeImportedEvent`, `EpisodeFileAddedEvent`, `EpisodeFileDeletedEvent` (`NzbDrone.Core.MediaFiles.Events` / `NzbDrone.Core.Download`).
- Queue lifecycle: `QueueUpdatedEvent` (V5), `ObsoleteQueueUpdatedEvent` (V3 — to be removed), `PendingReleasesUpdatedEvent`.
- Health: `HealthCheckCompleteEvent`.
- Tags / autotag: `TagsUpdatedEvent`, `AutoTagsUpdatedEvent`.

Each of these events is fired by a service inside `NzbDrone.Core` (e.g. `SeriesService`, `EpisodeService`, `CommandQueueManager`, `HealthCheckService`); the controllers are *not* the producers of those events — they only subscribe and translate to SignalR.

## Notes for the porter

- The Go port can model the SignalR layer as a single hub with a single broadcast topic `receiveMessage`. The fan-out to clients is unconditional (`Clients.All`) — there is no per-user filtering on the server side, no groups. Multi-user filtering, if needed later, will require new infrastructure.
- The `[JsonIgnore]` on `Action` (`SignalRMessage.cs:11`) is load-bearing: clients infer the action from `body.action`, not from the envelope. Keep this.
- `IsConnected` short-circuit (`MessageHub.cs:23`) is purely an optimization — when no clients are connected, no broadcast goes out and the controllers skip building the resource. Replicate this in Go to avoid loading models just to drop them.
- Debouncing: two controllers hand-roll a `Debouncer` (Sonarr's `NzbDrone.Common.TPL.Debouncer`) to coalesce rapid bursts:
  - `CommandController` debounces `CommandUpdatedEvent` at 100ms.
  - `QueueStatusController` debounces queue events at 5s (via `_broadcastDebounce.Execute()` from event handlers; `Pause`/`Resume` are wrapped around `GetQueueStatus()` so an HTTP read doesn't immediately retrigger a broadcast).
  - The Go port needs equivalents — naive event-by-event broadcasting will spam the websocket.
- Both V3 and V5 controllers run side-by-side in the same process and *both* broadcast for the same underlying events (different `name` namespacing — `series` from V3 vs `series` from V5 — disambiguated by `version`). The wire stream therefore typically duplicates the same logical event twice (`version: 3` and `version: 5`). UI clients filter by version. The Go port should preserve this dual-emit while V3 is supported.
- There are no SignalR groups, no per-connection state beyond the static `_connections` set. Reconnect logic on the client is responsible for replaying any state drift via REST — there is no server-side "missed-event" buffer.
