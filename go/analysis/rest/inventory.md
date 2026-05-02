# REST Endpoint Inventory (V3 + V5)

Phase 0.5 deep-dive. All endpoint metadata derived statically from the C# source. File:line citations point at the source method.

## Summary

| Metric                 | Value |
|------------------------|-------|
| V3 controllers         | 65    |
| V3 controller endpoints (REST methods)| 184 |
| V5 controllers         | 55    |
| V5 controller endpoints (REST methods)| 159 |
| Shared infrastructure controllers (`Sonarr.Http`) | 3 (`ApiInfoController`, `PingController`, `AuthenticationController`) plus `InitializeJsonController` (UI bootstrap) |

Counts include every action method on every concrete controller, including base-class actions inherited from `RestController<T>`, `RestControllerWithSignalR<T,M>`, `ProviderControllerBase<...>`, `EpisodeControllerWithSignalR`, `LogFileControllerBase`, `ConfigController<T>`, `SettingsController<T>` and `ReleaseControllerBase`. Methods marked `[NonAction]` are excluded; methods marked `[Obsolete]` are included with the `Deprecated: true` flag.

### Routing convention

- Per-controller route templates are injected by attribute, not by global prefix middleware.
- `Sonarr.Http/VersionedApiControllerAttribute.cs:8-19` defines `[VersionedApiController(version, resource)]` which builds `Template = $"api/v{version}/{resource}"`. Subclasses `[V3ApiController]` and `[V5ApiController]` (lines 29-43) hard-code the version. Default `resource` is `[controller]` (the controller's class name minus `Controller`, so `SeriesController` → `series`).
- `Sonarr.Http/VersionedFeedControllerAttribute.cs:6-18` defines `[VersionedFeedController(version, resource)]` → `feed/v{version}/{resource}` for `[V3FeedController]`/`[V5FeedController]`. Used by `CalendarFeedController` to expose `feed/v3/calendar/Sonarr.ics` and `feed/v5/calendar/Sonarr.ics`.
- The base `RestController<T>` injects implicit `GET /{id:int}` via `[RestGetById]` (`Sonarr.Http/REST/Attributes/RestGetByIdAttribute.cs:11`); `[RestPostById]`, `[RestPutById]`, `[RestDeleteById]` similarly attach `POST /` / `PUT /{id:int}` / `DELETE /{id:int}`.
- A handful of routes live outside the versioned scheme: `GET /api` (`Sonarr.Http/ApiInfoController.cs:9`), `GET|HEAD /ping` (`Sonarr.Http/Ping/PingController.cs:24-25`), `POST /login` and `GET /logout` (`Sonarr.Http/Authentication/AuthenticationController.cs:35,88`), `GET /initialize.json` (`Sonarr.Http/Frontend/InitializeJsonController.cs:33`). Frontend static assets and media-cover serving are routed through `StaticResourceController` and the `IMapHttpRequestsToDisk` mappers.

### Authentication / authorization

- Defined in `Sonarr.Http/Authentication/AuthenticationBuilderExtensions.cs:33-67`.
- Three primary auth schemes registered (in this order):
  - `None`/`External` → `NoAuthenticationHandler` (no-op).
  - `Forms` → ASP.NET cookie auth (`AddCookie`). Cookie name `<InstanceName>Auth`, login at `/login`, 7-day sliding expiration, returnUrl param `returnUrl`. `EventOnRedirectCookiesLogin` returns 401/403 for XHR rather than redirecting.
  - `API` → `ApiKeyAuthenticationHandler` (`Sonarr.Http/Authentication/ApiKeyAuthenticationHandler.cs:24`). Reads key from `X-Api-Key` header, `?apikey=` query, or `Authorization: Bearer …`. On match, issues a `ClaimsPrincipal` with `ApiKey=true`. On miss, returns `NoResult` (so cookie auth can still match).
  - `SignalR` → second `ApiKeyAuthenticationHandler` instance for SignalR negotiations (header `X-Api-Key`, query `access_token`).
- Authorization is policy-driven, not attribute-driven: `Sonarr.Http/Authentication/UiAuthorizationPolicyProvider.cs:27-37` resolves the policy named `"UI"` to a `BypassableDenyAnonymousAuthorizationRequirement` whose authentication scheme is the *user-configured* `IConfigFileProvider.AuthenticationMethod`. `UiAuthorizationHandler` (`Sonarr.Http/Authentication/UiAuthorizationHandler.cs:25-41`) succeeds the requirement when `AuthenticationRequired == DisabledForLocalAddresses` and the remote IP is local or trusted-CGNAT. `[Authorize(Policy = "UI")]` is the way controllers opt in (used on `InitializeJsonController` and globally as the default policy on all `[VersionedApiController]` controllers — see `IApiBehaviorMetadata` marker).
- API endpoints inherit a default policy that requires either the `API` scheme (the API key) or `Forms` cookie. Endpoints marked `[AllowAnonymous]` (`AuthenticationController`, `PingController.GetStatus`) bypass.
- Effective per-endpoint auth in this inventory:
  - `Auth: api-key | cookie` for every `[V3ApiController]` / `[V5ApiController]` endpoint (`UI` policy with API or Forms scheme).
  - `Auth: anonymous` for `/api`, `/ping`, `/login`, `/logout`, `/initialize.json` (the last requires the UI policy itself, but `[Authorize(Policy = "UI")]` is honored by browser cookies).
  - `Auth: api-key | cookie` for SignalR negotiate (separate scheme).
- There is no Windows-only auth path. The legacy `Basic` auth type is rejected by the validator at `Sonarr.Api.V3/Config/HostConfigController.cs:48-51` and the equivalent V5 controller.

### Error envelope

`Sonarr.Http/ErrorManagement/SonarrErrorPipeline.cs:24-86` registered as ASP.NET's `IExceptionHandlerPathFeature` middleware. Handler:

- `ApiException` (and `BadRequestException`, `MethodNotAllowedException`, `NotFoundException`, `UnsupportedMediaTypeException` deriving from it) — uses `apiException.StatusCode`, body is `ErrorModel { Message, Description, Content }` (`Sonarr.Http/ErrorManagement/ErrorModel.cs:9-31`).
- FluentValidation `ValidationException` — 400 with body `STJson.ToJson(validationException.Errors)` (raw `ValidationFailure[]`, NOT wrapped in `ErrorModel`).
- `NzbDroneClientException` — uses its own `StatusCode`, body is `ErrorModel`.
- `ModelNotFoundException` — 404, `ErrorModel`.
- `ModelConflictException` — 409, `ErrorModel`.
- `SQLiteException` — 409 if it's a constraint failure on PUT/POST, otherwise 500.
- Anything else — 500, `ErrorModel { Message = ex.Message, Description = ex.ToString() }` (full stack trace leaks to client; logged at Fatal).

`ErrorModel` body shape (camelCase by STJson default):

```json
{ "message": "string", "description": "string|null", "content": "object|null" }
```

`ValidationException` body shape:

```json
[ { "propertyName": "...", "errorMessage": "...", "attemptedValue": ..., "severity": 0, "errorCode": "...", ... }, ... ]
```

### ClientSchema (provider field schemas)

`Sonarr.Http/ClientSchema/SchemaBuilder.cs:14-348` is a reflection-driven serializer/deserializer for "provider settings" types (download clients, indexers, notifications, import lists, metadata providers, custom-format specs, autotag specs). Its job:

- Reads simple properties on a settings class that are decorated with `[FieldDefinition(Order, Label, HelpText, Type, Privacy, ...)]` (defined in `NzbDrone.Core.Annotations`) and produces a list of `Field` objects (`ClientSchema/Field.cs:6-30`) — the JSON shape clients render as a form.
- Supports nested objects (recurses with property name as prefix), enums (auto-builds `SelectOptions`), `ISelectOptionsConverter`, localization of `Label`/`HelpText`/`HelpTextWarning` via `ILocalizationService`, and `PrivacyLevel.ApiKey|Password` masking (replaces value with `"********"` on read; on write, restores existing value if client sends back the mask).
- Mappings cached per type in `_mappings`. `ToSchema(model)` and `ReadFromSchema(fields, type, model)` are the public entry points called by `ProviderResource`/`ProviderControllerBase` to round-trip provider-settings JSON.
- Field `Type` enum values become camelCase strings (`textbox`, `select`, `checkbox`, `path`, `tagSelect`, ...). Drives the dynamic form-field rendering in the React UI.

In the Go port the equivalent is a tagged-struct serializer that walks `reflect` and emits the same `Field[]` envelope.

---

## V3 controllers

Every `[V3ApiController]` mounts at `/api/v3/<resource>`. Auth defaults to api-key|cookie. Default `Produces` is JSON unless noted.

### `Sonarr.Api.V3/AutoTagging/AutoTaggingController.cs`

Base route: `/api/v3/autotagging`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET    | /{id:int}   | — | — | AutoTaggingResource | 200/404 | — |
| GET    | /           | — | — | AutoTaggingResource[] | 200 | — |
| POST   | /           | — | AutoTaggingResource | AutoTaggingResource | 201 | — |
| PUT    | /{id:int}   | — | AutoTaggingResource | AutoTaggingResource | 202 | — |
| DELETE | /{id:int}   | — | — | — | 200 | — |
| GET    | /schema     | — | — | object[] (specifications) | 200 | — |

Cited at `Sonarr.Api.V3/AutoTagging/AutoTaggingController.cs:54,65,78,84,89` plus inherited GET-by-id from `RestController<T>`.

### `Sonarr.Api.V3/Blocklist/BlocklistController.cs`

Base route: `/api/v3/blocklist`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET    | /          | PagingRequestResource, seriesIds: int[]?, protocols: DownloadProtocol[]? | — | PagingResource&lt;BlocklistResource&gt; | 200 | — |
| DELETE | /{id:int}  | — | — | — | 200 | — |
| DELETE | /bulk      | — | BlocklistBulkResource | `{}` | 200 | — |

Cited at `Sonarr.Api.V3/Blocklist/BlocklistController.cs:30,57,63`.

### `Sonarr.Api.V3/Calendar/CalendarController.cs`

Base route: `/api/v3/calendar`
Auth: api-key | cookie. Inherits from `EpisodeControllerWithSignalR`, so it also exposes the inherited GET-by-id and PUT-by-id from `RestController<EpisodeResource>` (Sonarr.Http/REST/RestController.cs:55).

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET    | /          | start: DateTime?, end: DateTime?, unmonitored: bool=false, includeSeries: bool=false, includeEpisodeFile: bool=false, includeEpisodeImages: bool=false, tags: string="" | — | EpisodeResource[] | 200 | — |
| GET    | /{id:int}  | (inherited) | — | EpisodeResource | 200/404 | — |

(EpisodeControllerWithSignalR also defines event handlers but no extra HTTP verbs.) `Sonarr.Api.V3/Calendar/CalendarController.cs:34`.

### `Sonarr.Api.V3/Calendar/CalendarFeedController.cs`

Base route: `/feed/v3/calendar`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET | /Sonarr.ics | pastDays: int=7, futureDays: int=28, tags: string="", unmonitored: bool=false, premieresOnly: bool=false, asAllDay: bool=false | — | text/calendar | 200 | — |

`Sonarr.Api.V3/Calendar/CalendarFeedController.cs:30`.

### `Sonarr.Api.V3/Commands/CommandController.cs`

Base route: `/api/v3/command`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<CommandResource, CommandModel>`, so resource-broadcasts are emitted via `command:updated|sync` (debounced, see `SendUpdates` at line 111).

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET    | /{id:int} | — | — | CommandResource | 200/404 | — |
| POST   | /         | — | CommandResource (subtype dispatched via `name`) | CommandResource | 201 | command:updated, command:sync (after MessagingCleanupCommand completes) |
| GET    | /         | — | — | CommandResource[] | 200 | — |
| DELETE | /{id:int} | — | — | — | 200 | — |

Cited at `Sonarr.Api.V3/Commands/CommandController.cs:54,83,92` (POST line 54, GET line 83, DELETE line 92). SignalR broadcasts at lines 120, 125.

### `Sonarr.Api.V3/Config/ConfigController.cs` (abstract base)

`ConfigController<TResource>` (`Sonarr.Api.V3/Config/ConfigController.cs:25,37`) is the base for the eight config controllers below. It exposes:

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | — | — | TResource | 200 |
| PUT    | /{id:int} | — | TResource | TResource | 202 |
| GET    | /{id:int} | — | — | TResource | 200 (returns the same as GET /, ignoring id) |

Subclasses: `DownloadClientConfigController` (`config/downloadclient`), `ImportListConfigController` (`config/importlist`), `IndexerConfigController` (`config/indexer`), `MediaManagementConfigController` (`config/mediamanagement`), `UiConfigController` (`config/ui`). Each only adjusts validators and the `ToResource(IConfigService)` mapping.

### `Sonarr.Api.V3/Config/HostConfigController.cs`

Base route: `/api/v3/config/host`
Auth: api-key | cookie. Does *not* extend `ConfigController` — it has its own GET / PUT.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | — | — | HostConfigResource | 200 |
| GET | /{id:int} | — | — | HostConfigResource | 200 (id ignored) |
| PUT | /{id:int} | — | HostConfigResource | HostConfigResource | 202 |

Cited at `Sonarr.Api.V3/Config/HostConfigController.cs:105,120`.

### `Sonarr.Api.V3/Config/NamingConfigController.cs`

Base route: `/api/v3/config/naming`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /          | — | — | NamingConfigResource | 200 |
| GET    | /{id:int}  | — | — | NamingConfigResource | 200 (id ignored) |
| PUT    | /{id:int}  | — | NamingConfigResource | NamingConfigResource | 202 |
| GET    | /examples  | NamingConfigResource (model-bound) | — | NamingExampleResource | 200 |

Cited at `Sonarr.Api.V3/Config/NamingConfigController.cs:47,57,67`.

### `Sonarr.Api.V3/CustomFilters/CustomFilterController.cs`

Base route: `/api/v3/customfilter`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | — | — | CustomFilterResource[] | 200 |
| GET    | /{id:int} | — | — | CustomFilterResource | 200/404 |
| POST   | /         | — | CustomFilterResource | CustomFilterResource | 201 |
| PUT    | /{id:int} | — | CustomFilterResource | CustomFilterResource | 202 |
| DELETE | /{id:int} | — | — | — | 200 |

`Sonarr.Api.V3/CustomFilters/CustomFilterController.cs:25,33,41,49`.

### `Sonarr.Api.V3/CustomFormats/CustomFormatController.cs`

Base route: `/api/v3/customformat`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | — | — | CustomFormatResource[] | 200 |
| GET    | /{id:int} | — | — | CustomFormatResource | 200/404 |
| POST   | /         | — | CustomFormatResource | CustomFormatResource | 201 |
| PUT    | /{id:int} | — | CustomFormatResource | CustomFormatResource | 202 |
| PUT    | /bulk     | — | CustomFormatBulkResource | CustomFormatResource[] | 202 |
| DELETE | /{id:int} | — | — | — | 200 |
| DELETE | /bulk     | — | CustomFormatBulkResource | `{}` | 200 |
| GET    | /schema   | — | — | object[] (specifications + presets) | 200 |

`Sonarr.Api.V3/CustomFormats/CustomFormatController.cs:50,59,70,81,103,109,118`.

### `Sonarr.Api.V3/DiskSpace/DiskSpaceController.cs`

Base route: `/api/v3/diskspace`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | — | — | DiskSpaceResource[] | 200 |

`Sonarr.Api.V3/DiskSpace/DiskSpaceController.cs:18`.

### `Sonarr.Api.V3/DownloadClient/DownloadClientController.cs`

Base route: `/api/v3/downloadclient`
Auth: api-key | cookie. Inherits all CRUD from `ProviderControllerBase<DownloadClientResource, DownloadClientBulkResource, IDownloadClient, DownloadClientDefinition>` (see ProviderControllerBase section below). SignalR: `downloadclient:created|updated|deleted` via `ProviderAdded/Updated/DeletedEvent` (lines 278/284/290 of ProviderControllerBase).

### `Sonarr.Api.V3/EpisodeFiles/EpisodeFileController.cs`

Base route: `/api/v3/episodefile`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<EpisodeFileResource, EpisodeFile>`.

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET    | /{id:int} | — | — | EpisodeFileResource | 200/404 | — |
| GET    | /         | seriesId: int?, episodeFileIds: int[]? | — | EpisodeFileResource[] | 200/400 | — |
| PUT    | /{id:int} | — | EpisodeFileResource | EpisodeFileResource | 202 | — |
| PUT    | /editor  *(deprecated)* | — | EpisodeFileListResource | EpisodeFileResource[] | 202 | — |
| DELETE | /{id:int} | — | — | — | 200/404 | — |
| DELETE | /bulk     | — | EpisodeFileListResource | `{}` | 200 | — |
| PUT    | /bulk     | — | EpisodeFileResource[] | EpisodeFileResource[] | 202 | — |

Event handlers (`Handle(EpisodeFileAdded/Deleted)` lines 234/240) emit `episodefile:updated` and `episodefile:deleted`.

`Sonarr.Api.V3/EpisodeFiles/EpisodeFileController.cs:62,94,115,152,167,182`.

### `Sonarr.Api.V3/Episodes/EpisodeController.cs`

Base route: `/api/v3/episode`
Auth: api-key | cookie. Extends `EpisodeControllerWithSignalR`.

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET    | /{id:int} | (inherited) | — | EpisodeResource | 200/404 | — |
| GET    | /         | seriesId: int?, seasonNumber: int?, episodeIds: int[]?, episodeFileId: int?, includeSeries: bool, includeEpisodeFile: bool, includeImages: bool | — | EpisodeResource[] | 200/400 | — |
| PUT    | /{id:int} | — | EpisodeResource (only `Monitored` is honored) | EpisodeResource | 202 | — |
| PUT    | /monitor  | includeImages: bool=false | EpisodesMonitoredResource | EpisodeResource[] | 202 | — |

Inherited from base `EpisodeControllerWithSignalR`: handlers for `EpisodeGrabbedEvent`, `EpisodeImportedEvent`, `EpisodeFileDeletedEvent` broadcast `episode:updated` (line 131,140,149).

`Sonarr.Api.V3/Episodes/EpisodeController.cs:28,53,62`.

### `Sonarr.Api.V3/Episodes/RenameEpisodeController.cs`

Base route: `/api/v3/rename`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /     | seriesId: int (required), seasonNumber: int? | — | RenameEpisodeResource[] | 200 |
| GET | /bulk | seriesIds: int[] | — | RenameEpisodeResource[] | 200/400 |

`Sonarr.Api.V3/Episodes/RenameEpisodeController.cs:21,32`.

### `Sonarr.Api.V3/FileSystem/FileSystemController.cs`

Base route: `/api/v3/filesystem`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /            | path: string, includeFiles: bool=false, allowFoldersWithoutTrailingSlashes: bool=false | — | FileSystemResult | 200 |
| GET | /type        | path: string | — | `{ type: "file"\|"folder" }` | 200 |
| GET | /mediafiles  | path: string | — | `{ Path,RelativePath,Name }[]` | 200 |

`Sonarr.Api.V3/FileSystem/FileSystemController.cs:29,35,48`.

### `Sonarr.Api.V3/Health/HealthController.cs`

Base route: `/api/v3/health`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<HealthResource, HealthCheck>`. `Handle(HealthCheckCompleteEvent)` (line 44) emits `health:sync`.

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET | / | — | — | HealthResource[] | 200 | — |

`Sonarr.Api.V3/Health/HealthController.cs:37`.

### `Sonarr.Api.V3/History/HistoryController.cs`

Base route: `/api/v3/history`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET  | /                | PagingRequestResource, includeSeries: bool, includeEpisode: bool, eventType: int[], episodeId: int?, downloadId: string?, seriesIds: int[]?, languages: int[]?, quality: int[]? | — | PagingResource&lt;HistoryResource&gt; | 200 |
| GET  | /since           | date: DateTime, eventType: EpisodeHistoryEventType?, includeSeries: bool=false, includeEpisode: bool=false | — | HistoryResource[] | 200 |
| GET  | /series          | seriesId: int, seasonNumber: int?, eventType: EpisodeHistoryEventType?, includeSeries: bool=false, includeEpisode: bool=false | — | HistoryResource[] | 200 |
| POST | /failed/{id:int} | — | — | `{}` | 200 |

`Sonarr.Api.V3/History/HistoryController.cs:64,100,107,131`.

### `Sonarr.Api.V3/ImportLists/ImportListController.cs`

Base route: `/api/v3/importlist`
Auth: api-key | cookie. CRUD via `ProviderControllerBase<ImportListResource, ImportListBulkResource, IImportList, ImportListDefinition>`. SignalR: `importlist:created|updated|deleted`.

### `Sonarr.Api.V3/ImportLists/ImportListExclusionController.cs`

Base route: `/api/v3/importlistexclusion`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /          *(deprecated)* | — | — | ImportListExclusionResource[] | 200 |
| GET    | /paged                    | PagingRequestResource | — | PagingResource&lt;ImportListExclusionResource&gt; | 200 |
| GET    | /{id:int}                 | — | — | ImportListExclusionResource | 200/404 |
| POST   | /                         | — | ImportListExclusionResource | ImportListExclusionResource | 201 |
| PUT    | /{id:int}                 | — | ImportListExclusionResource | ImportListExclusionResource | 202 |
| DELETE | /{id:int}                 | — | — | — | 200 |
| DELETE | /bulk                     | — | ImportListExclusionBulkResource | `{}` | 200 |

`Sonarr.Api.V3/ImportLists/ImportListExclusionController.cs:37,45,63,72,80,86`.

### `Sonarr.Api.V3/Indexers/IndexerController.cs`

Base route: `/api/v3/indexer`
Auth: api-key | cookie. CRUD via `ProviderControllerBase<IndexerResource, IndexerBulkResource, IIndexer, IndexerDefinition>`. SignalR: `indexer:created|updated|deleted`.

### `Sonarr.Api.V3/Indexers/IndexerFlagController.cs`

Base route: `/api/v3/indexerflag`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | — | — | IndexerFlagResource[] | 200 |

`Sonarr.Api.V3/Indexers/IndexerFlagController.cs:13`.

### `Sonarr.Api.V3/Indexers/ReleaseController.cs`

Base route: `/api/v3/release`
Auth: api-key | cookie. Extends `ReleaseControllerBase`.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | seriesId: int?, episodeId: int?, seasonNumber: int? | — | ReleaseResource[] | 200/400/500 |
| POST   | /         | — | ReleaseResource | ReleaseResource | 200/404/409 |
| GET    | /{id:int} | (inherited from RestController, throws NotImplementedException at runtime) | — | — | 500 |

`Sonarr.Api.V3/Indexers/ReleaseController.cs:69,171`.

### `Sonarr.Api.V3/Indexers/ReleasePushController.cs`

Base route: `/api/v3/release/push`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| POST | / | — | ReleaseResource | ReleaseResource[] (single-element) | 200/400 |

`Sonarr.Api.V3/Indexers/ReleasePushController.cs:49`.

### `Sonarr.Api.V3/Localization/LocalizationController.cs`

Base route: `/api/v3/localization`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /          | — | — | LocalizationResource | 200 |
| GET | /{id:int}  | — | — | LocalizationResource | 200 (id ignored) |
| GET | /language  | — | — | LocalizationLanguageResource | 200 |

`Sonarr.Api.V3/Localization/LocalizationController.cs:23,30`.

### `Sonarr.Api.V3/Logs/LogController.cs`

Base route: `/api/v3/log`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | PagingRequestResource, level: string? (`fatal\|error\|warn\|info\|debug\|trace`) | — | PagingResource&lt;LogResource&gt; | 200 |

`Sonarr.Api.V3/Logs/LogController.cs:25`.

### `Sonarr.Api.V3/Logs/LogFileController.cs` & `UpdateLogFileController.cs`

Both extend `LogFileControllerBase` (`Sonarr.Api.V3/Logs/LogFileControllerBase.cs:28,54`):

LogFileController base route: `/api/v3/log/file`. UpdateLogFileController base route: `/api/v3/log/file/update`.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /                                                 | — | — | LogFileResource[] | 200 |
| GET | /{filename:regex([-.a-zA-Z0-9]+?\.txt)}            | — | — | text/plain (file contents) | 200/404 |

### `Sonarr.Api.V3/ManualImport/ManualImportController.cs`

Base route: `/api/v3/manualimport`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET  | / | folder: string?, downloadId: string?, seriesId: int?, seasonNumber: int?, filterExistingFiles: bool=true | — | ManualImportResource[] | 200 |
| POST | / | — | ManualImportReprocessResource[] | ManualImportReprocessResource[] | 200/400 |

`Sonarr.Api.V3/ManualImport/ManualImportController.cs:25,37`.

### `Sonarr.Api.V3/MediaCovers/MediaCoverController.cs`

Base route: `/api/v3/mediacover`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /{seriesId:int}/{filename:regex((.+)\.(jpg\|png\|gif))} | — | — | image bytes | 200/404 |

`Sonarr.Api.V3/MediaCovers/MediaCoverController.cs:28`.

### `Sonarr.Api.V3/Metadata/MetadataController.cs`

Base route: `/api/v3/metadata`
Auth: api-key | cookie. CRUD via `ProviderControllerBase<MetadataResource, MetadataBulkResource, IMetadata, MetadataDefinition>` BUT bulk PUT and bulk DELETE are explicitly disabled (`[NonAction]`) (`Sonarr.Api.V3/Metadata/MetadataController.cs:20,26`). SignalR: `metadata:created|updated|deleted`.

### `Sonarr.Api.V3/Notifications/NotificationController.cs`

Base route: `/api/v3/notification`
Auth: api-key | cookie. Same as Metadata: bulk PUT and bulk DELETE are `[NonAction]` (`Sonarr.Api.V3/Notifications/NotificationController.cs:20,26`). CRUD via ProviderControllerBase. SignalR: `notification:created|updated|deleted`.

### `Sonarr.Api.V3/Parse/ParseController.cs`

Base route: `/api/v3/parse`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | title: string, path: string? | — | ParseResource | 200 (returns `null` body if title empty) |

`Sonarr.Api.V3/Parse/ParseController.cs:29`.

### `Sonarr.Api.V3/Profiles/Delay/DelayProfileController.cs`

Base route: `/api/v3/delayprofile`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /                | — | — | DelayProfileResource[] | 200 |
| GET    | /{id:int}        | — | — | DelayProfileResource | 200/404 |
| POST   | /                | — | DelayProfileResource | DelayProfileResource | 201 |
| PUT    | /{id:int}        | — | DelayProfileResource | DelayProfileResource | 202 |
| DELETE | /{id:int}        | — | — | — | 200/405 (id==1 returns 405) |
| PUT    | /reorder/{id:int}| after: int? | — | DelayProfileResource[] | 200 |

`Sonarr.Api.V3/Profiles/Delay/DelayProfileController.cs:36,46,57,71,78`.

### `Sonarr.Api.V3/Profiles/Languages/LanguageController.cs`

Base route: `/api/v3/language`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | — | — | LanguageResource[] | 200 |
| GET | /{id:int} | — | — | LanguageResource (id is the Language enum value, no DB lookup) | 200 |

`Sonarr.Api.V3/Profiles/Languages/LanguageController.cs:24`.

### `Sonarr.Api.V3/Profiles/Languages/LanguageProfileController.cs` *(deprecated)*

Base route: `/api/v3/languageprofile` — `[Obsolete]`. All endpoints return a hard-coded "Deprecated" English-only profile.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | — | — | LanguageProfileResource[] | 200 |
| GET    | /{id:int} | — | — | LanguageProfileResource | 200 |
| POST   | /         | — | LanguageProfileResource | LanguageProfileResource | 202 |
| PUT    | /{id:int} | — | LanguageProfileResource | LanguageProfileResource | 202 |
| DELETE | /{id:int} | — | — | — | 200 (no-op) |

`Sonarr.Api.V3/Profiles/Languages/LanguageProfileController.cs:15,23,28,36,57`.

### `Sonarr.Api.V3/Profiles/Languages/LanguageProfileSchemaController.cs` *(deprecated)*

Base route: `/api/v3/languageprofile/schema`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | — | — | LanguageProfileResource | 200 |

### `Sonarr.Api.V3/Profiles/Quality/QualityProfileController.cs`

Base route: `/api/v3/qualityprofile`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /          | — | — | QualityProfileResource[] | 200 |
| GET    | /{id:int}  | — | — | QualityProfileResource | 200/404 |
| POST   | /          | — | QualityProfileResource | QualityProfileResource | 201 |
| PUT    | /{id:int}  | — | QualityProfileResource | QualityProfileResource | 202 |
| DELETE | /{id:int}  | — | — | — | 200 |

`Sonarr.Api.V3/Profiles/Quality/QualityProfileController.cs:49,58,64,80`.

### `Sonarr.Api.V3/Profiles/Quality/QualityProfileSchemaController.cs`

Base route: `/api/v3/qualityprofile/schema`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | — | — | QualityProfileResource | 200 |

`Sonarr.Api.V3/Profiles/Quality/QualityProfileSchemaController.cs:17`.

### `Sonarr.Api.V3/Profiles/Release/ReleaseProfileController.cs`

Base route: `/api/v3/releaseprofile`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | — | — | ReleaseProfileResource[] | 200 |
| GET    | /{id:int} | — | — | ReleaseProfileResource | 200/404 |
| POST   | /         | — | ReleaseProfileResource | ReleaseProfileResource | 201 |
| PUT    | /{id:int} | — | ReleaseProfileResource | ReleaseProfileResource | 202 |
| DELETE | /{id:int} | — | — | — | 200 |

`Sonarr.Api.V3/Profiles/Release/ReleaseProfileController.cs:53,61,67,82`.

### `Sonarr.Api.V3/Qualities/QualityDefinitionController.cs`

Base route: `/api/v3/qualitydefinition`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<QualityDefinitionResource, QualityDefinition>`. Handles `CommandExecutedEvent("ResetQualityDefinitions")` → `qualitydefinition:sync` (line 92).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | — | — | QualityDefinitionResource[] | 200 |
| GET | /{id:int} | — | — | QualityDefinitionResource | 200/404 |
| PUT | /{id:int} | — | QualityDefinitionResource | QualityDefinitionResource | 202 |
| PUT | /update   | — | QualityDefinitionResource[] | QualityDefinitionResource[] | 202 |
| GET | /limits   | — | — | QualityDefinitionLimitsResource | 200 |

`Sonarr.Api.V3/Qualities/QualityDefinitionController.cs:36,55,61,83`.

### `Sonarr.Api.V3/Queue/QueueController.cs`

Base route: `/api/v3/queue`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<QueueResource, Queue>`. Handles `ObsoleteQueueUpdatedEvent` and `PendingReleasesUpdatedEvent` → `queue:sync` (lines 401, 407).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | PagingRequestResource, includeUnknownSeriesItems: bool=false, includeSeries: bool=false, includeEpisode: bool=false, seriesIds: int[]?, protocol: DownloadProtocol?, languages: int[]?, quality: int[]?, status: QueueStatus[]? | — | PagingResource&lt;QueueResource&gt; | 200 |
| DELETE | /{id:int} | removeFromClient: bool=true, blocklist: bool=false, skipRedownload: bool=false, changeCategory: bool=false | — | — | 200/404 |
| DELETE | /bulk     | removeFromClient,blocklist,skipRedownload,changeCategory (same) | QueueBulkResource | `{}` | 200 |

`Sonarr.Api.V3/Queue/QueueController.cs:75,98,139`.

### `Sonarr.Api.V3/Queue/QueueActionController.cs`

Base route: `/api/v3/queue` (note collision with QueueController — actions use sub-paths only)
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| POST | /grab/{id:int} | — | — | `{}` | 200/404 |
| POST | /grab/bulk     | — | QueueBulkResource | `{}` | 200/404 |

`Sonarr.Api.V3/Queue/QueueActionController.cs:23,38`.

### `Sonarr.Api.V3/Queue/QueueDetailsController.cs`

Base route: `/api/v3/queue/details`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<QueueResource, Queue>` → `queue/details:sync`.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | seriesId: int?, episodeIds: int[]?, includeSeries: bool=false, includeEpisode: bool=false | — | QueueResource[] | 200 |

`Sonarr.Api.V3/Queue/QueueDetailsController.cs:42`.

### `Sonarr.Api.V3/Queue/QueueStatusController.cs`

Base route: `/api/v3/queue/status`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<QueueStatusResource, Queue>` → `queue/status:updated` (debounced, line 72).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | — | — | QueueStatusResource | 200 |

`Sonarr.Api.V3/Queue/QueueStatusController.cs:45`.

### `Sonarr.Api.V3/RemotePathMappings/RemotePathMappingController.cs`

Base route: `/api/v3/remotepathmapping`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | — | — | RemotePathMappingResource[] | 200 |
| GET    | /{id:int} | — | — | RemotePathMappingResource | 200/404 |
| POST   | /         | — | RemotePathMappingResource | RemotePathMappingResource | 201 |
| PUT    | /{id:int} | — | RemotePathMappingResource | RemotePathMappingResource | 202 |
| DELETE | /{id:int} | — | — | — | 200 |

`Sonarr.Api.V3/RemotePathMappings/RemotePathMappingController.cs:54,63,70,76`.

### `Sonarr.Api.V3/RootFolders/RootFolderController.cs`

Base route: `/api/v3/rootfolder`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<RootFolderResource, RootFolder>` (no extra event handlers; SignalR only emits via `ModelEvent<RootFolder>` propagated through DB layer → `rootfolder:created|updated|deleted|sync`).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | — | — | RootFolderResource[] | 200 |
| GET    | /{id:int} | timeout: bool=true | — | RootFolderResource | 200/404 |
| POST   | /         | — | RootFolderResource | RootFolderResource | 201 |
| DELETE | /{id:int} | — | — | — | 200 |

`Sonarr.Api.V3/RootFolders/RootFolderController.cs:51,60,67`.

### `Sonarr.Api.V3/SeasonPass/SeasonPassController.cs`

Base route: `/api/v3/seasonpass`
Auth: api-key | cookie

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| POST | / | — | SeasonPassResource | `{}` | 202 |

`Sonarr.Api.V3/SeasonPass/SeasonPassController.cs:20`.

### `Sonarr.Api.V3/Series/SeriesController.cs`

Base route: `/api/v3/series`
Auth: api-key | cookie. Extends `RestControllerWithSignalR<SeriesResource, Series>`. Event handlers (`Sonarr.Api.V3/Series/SeriesController.cs:299-360`) emit `series:updated|deleted` on EpisodeImported, EpisodeFileDeleted (non-upgrade), SeriesUpdated, SeriesEdited, SeriesDeleted, SeriesRenamed, SeriesBulkEdited, MediaCoversUpdated.

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET    | /         | tvdbId: int?, includeSeasonImages: bool=false | — | SeriesResource[] | 200 | — |
| GET    | /{id:int} | includeSeasonImages: bool=false | — | SeriesResource | 200/404 | — |
| POST   | /         | — | SeriesResource | SeriesResource | 201 | series:created (via ModelEvent) |
| PUT    | /{id:int} | moveFiles: bool=false | SeriesResource | SeriesResource | 202 | series:updated |
| DELETE | /{id:int} | deleteFiles: bool=false, addImportListExclusion: bool=false | — | — | 200 | series:deleted |

`Sonarr.Api.V3/Series/SeriesController.cs:109,141,172,179,209`.

### `Sonarr.Api.V3/Series/SeriesEditorController.cs`

Base route: `/api/v3/series/editor`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| PUT    | / | — | SeriesEditorResource | SeriesResource[] | 202 |
| DELETE | / | — | SeriesEditorResource | `{}` | 200 |

`Sonarr.Api.V3/Series/SeriesEditorController.cs:27,109`.

### `Sonarr.Api.V3/Series/SeriesFolderController.cs`

Base route: `/api/v3/series`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /{id}/folder | — | — | `{ folder: string }` | 200 |

`Sonarr.Api.V3/Series/SeriesFolderController.cs:20`.

### `Sonarr.Api.V3/Series/SeriesImportController.cs`

Base route: `/api/v3/series/import`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| POST | / | — | SeriesResource[] | SeriesResource[] | 200 |

`Sonarr.Api.V3/Series/SeriesImportController.cs:18`.

### `Sonarr.Api.V3/Series/SeriesLookupController.cs`

Base route: `/api/v3/series/lookup`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | term: string | — | SeriesResource[] | 200 |

`Sonarr.Api.V3/Series/SeriesLookupController.cs:26`.

### `Sonarr.Api.V3/System/Backup/BackupController.cs`

Base route: `/api/v3/system/backup`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /                       | — | — | BackupResource[] | 200 |
| DELETE | /{id:int}               | — | — | `{}` | 200/404 |
| POST   | /restore/{id:int}       | — | — | `{ RestartRequired: true }` | 200/404 |
| POST   | /restore/upload         | — | multipart/form-data file | `{ RestartRequired: true }` | 200/400/415 |

`Sonarr.Api.V3/System/Backup/BackupController.cs:34,52,74,94`.

### `Sonarr.Api.V3/System/SystemController.cs`

Base route: `/api/v3/system`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET  | /status            | — | — | SystemResource | 200 |
| GET  | /routes            | — | — | text/plain (DFA dump) | 200 |
| GET  | /routes/duplicate  | — | — | object | 200 |
| POST | /shutdown          | — | — | `{ ShuttingDown: true }` | 200 |
| POST | /restart           | — | — | `{ Restarting: true }` | 200 |

`Sonarr.Api.V3/System/SystemController.cs:56,97,109,116,123`.

### `Sonarr.Api.V3/System/Tasks/TaskController.cs`

Base route: `/api/v3/system/task`. Extends `RestControllerWithSignalR<TaskResource, ScheduledTask>`. Handles `CommandExecutedEvent` → `system/task:sync` (line 64).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | — | — | TaskResource[] | 200 |
| GET | /{id:int} | — | — | TaskResource | 200/404 |

`Sonarr.Api.V3/System/Tasks/TaskController.cs:25`.

### `Sonarr.Api.V3/Tags/TagController.cs`

Base route: `/api/v3/tag`. Extends `RestControllerWithSignalR<TagResource, Tag>`. Handles `TagsUpdatedEvent` and `AutoTagsUpdatedEvent` → `tag:sync` (lines 71, 77).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET    | /         | — | — | TagResource[] | 200 |
| GET    | /{id:int} | — | — | TagResource | 200/404 |
| POST   | /         | — | TagResource | TagResource | 201 |
| PUT    | /{id:int} | — | TagResource | TagResource | 202 |
| DELETE | /{id:int} | — | — | — | 200 |

`Sonarr.Api.V3/Tags/TagController.cs:40,47,54,62`.

### `Sonarr.Api.V3/Tags/TagDetailsController.cs`

Base route: `/api/v3/tag/detail`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | — | — | TagDetailsResource[] | 200 |
| GET | /{id:int} | — | — | TagDetailsResource | 200/404 |

`Sonarr.Api.V3/Tags/TagDetailsController.cs:24`.

### `Sonarr.Api.V3/Update/UpdateController.cs`

Base route: `/api/v3/update`

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | / | — | — | UpdateResource[] | 200 |

`Sonarr.Api.V3/Update/UpdateController.cs:26`.

### `Sonarr.Api.V3/Wanted/CutoffController.cs`

Base route: `/api/v3/wanted/cutoff`. Inherits GET-by-id from `EpisodeControllerWithSignalR`.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | PagingRequestResource, includeSeries: bool=false, includeEpisodeFile: bool=false, includeImages: bool=false, monitored: bool=true | — | PagingResource&lt;EpisodeResource&gt; | 200 |
| GET | /{id:int} | (inherited) | — | EpisodeResource | 200/404 |

`Sonarr.Api.V3/Wanted/CutoffController.cs:31`.

### `Sonarr.Api.V3/Wanted/MissingController.cs`

Base route: `/api/v3/wanted/missing`. Inherits GET-by-id from `EpisodeControllerWithSignalR`.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | PagingRequestResource, includeSeries: bool=false, includeImages: bool=false, monitored: bool=true | — | PagingResource&lt;EpisodeResource&gt; | 200 |
| GET | /{id:int} | (inherited) | — | EpisodeResource | 200/404 |

`Sonarr.Api.V3/Wanted/MissingController.cs:27`.

### `Sonarr.Api.V3/ProviderControllerBase.cs` (abstract base — endpoint set inherited by DownloadClient/ImportList/Indexer/Metadata/Notification controllers)

| Method | Path | Query | Request body | Response | Status codes | SignalR |
|--------|------|-------|--------------|----------|--------------|---------|
| GET    | /                | — | — | TProviderResource[] | 200 | — |
| GET    | /{id:int}        | — | — | TProviderResource | 200/404 | — |
| POST   | /                | forceSave: bool=false | TProviderResource | TProviderResource | 201 | `<resource>:created` |
| PUT    | /{id:int}        | forceSave: bool=false | TProviderResource | TProviderResource | 202/404 | `<resource>:updated` |
| PUT    | /bulk            | — | TBulkProviderResource | TProviderResource[] | 202/400 | `<resource>:updated` (per id) |
| DELETE | /{id:int}        | — | — | `{}` | 200 | `<resource>:deleted` |
| DELETE | /bulk            | — | TBulkProviderResource | `{}` | 200 | `<resource>:deleted` (per id) |
| GET    | /schema          | — | — | TProviderResource[] (with `Presets`) | 200 | — |
| POST   | /test            | forceTest: bool=false | TProviderResource | `"{}"` (JSON-as-string) | 200/400 | — |
| POST   | /testall         | — | — | ProviderTestAllResult[] | 200/400 | — |
| POST   | /action/{name}   | (any) | TProviderResource | application/json (provider-defined) | 200/400 | — |

`Sonarr.Api.V3/ProviderControllerBase.cs:60,78,95,127,180,188,197,221,233,260`.

---

## V5 controllers

V5 mounts every `[V5ApiController]` at `/api/v5/<resource>`. V5 systematically uses ASP.NET `TypedResults`/`Results<>` so 404 is a first-class response shape. Where a controller is structurally identical to V3 (only `[V5ApiController]` plus `Ok<T>` instead of `T`), it is summarized; differences are called out.

### `Sonarr.Api.V5/Series/SeriesController.cs`

Base route: `/api/v5/series`. Major differences vs V3:

- New query enum `includeSubresources: SeriesSubresource[]` replaces flat `includeSeasonImages` boolean (`SeriesSubresource.SeasonImages` is currently the only value).
- New `PUT /{id}/season` endpoint that updates season-level monitoring (`Sonarr.Api.V5/Series/SeriesController.cs:223`); broadcasts `series:updated`.
- Returns `Results<Created<...>, NotFound>` / `Results<Accepted<...>, NotFound>` etc.

| Method | Path | Query | Request body | Response | Status codes | SignalR events emitted |
|--------|------|-------|--------------|----------|--------------|------------------------|
| GET    | /                | tvdbId: int?, includeSubresources: SeriesSubresource[]? | — | SeriesResource[] | 200 | — |
| GET    | /{id:int}        | includeSubresources: SeriesSubresource[]? | — | SeriesResource | 200/404 | — |
| POST   | /                | — | SeriesResource | SeriesResource | 201/404 | series:created |
| PUT    | /{id:int}        | moveFiles: bool=false | SeriesResource | SeriesResource | 202/404 | series:updated |
| PUT    | /{id:int}/season | — | SeasonResource | SeasonResource | 200/404 | series:updated |
| DELETE | /{id:int}        | deleteFiles: bool=false, addImportListExclusion: bool=false | — | — | 204 | series:deleted |

`Sonarr.Api.V5/Series/SeriesController.cs:110,143,186,196,223,248`.

### `Sonarr.Api.V5/Connections/ConnectionController.cs`

Base route: `/api/v5/connection`. Replaces V3 `/notification`. CRUD via V5 ProviderControllerBase. Bulk PUT and Bulk DELETE are `[NonAction]` (`ConnectionController.cs:21,27`). Endpoints same as V5 ProviderControllerBase. SignalR: `connection:created|updated|deleted`.

### `Sonarr.Api.V5/Localization/LanguageController.cs`

Base route: `/api/v5/language`. Was `/api/v3/language` under `Profiles/Languages` in V3.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | — | — | LanguageResource[] | 200 |
| GET | /{id:int} | — | — | LanguageResource | 200 |

`Sonarr.Api.V5/Localization/LanguageController.cs:24`.

### `Sonarr.Api.V5/Localization/LocalizationController.cs`

Same routes as V3 (`/api/v5/localization`, `/{id:int}`, `/language`). Returns `Ok<T>` typed results. `Sonarr.Api.V5/Localization/LocalizationController.cs:25,32`.

### `Sonarr.Api.V5/Provider/ProviderControllerBase.cs` (abstract base)

V5 reshapes the provider-CRUD signature:

- `forceSave: bool` → split into `skipTesting: bool=false` + `skipValidation: SkipValidation=None` enum (`SkipValidation = None | Warnings | All`).
- Returns `Results<Created<T>, NotFound>` / `Results<Accepted<T>, NotFound>` etc.
- `DELETE /{id:int}` returns 204.
- `POST /test` returns 204 instead of `"{}"`.
- `RequestAction` returns `Results<ContentHttpResult, BadRequest>`.

| Method | Path | Query | Request body | Response | Status codes | SignalR |
|--------|------|-------|--------------|----------|--------------|---------|
| GET    | /                | — | — | TProviderResource[] | 200 | — |
| GET    | /{id:int}        | — | — | TProviderResource | 200/404 | — |
| POST   | /                | skipTesting: bool=false, skipValidation: SkipValidation=None | TProviderResource | TProviderResource | 201/404 | `<resource>:created` |
| PUT    | /{id:int}        | skipTesting: bool=false, skipValidation: SkipValidation=None | TProviderResource | TProviderResource | 202/404 | `<resource>:updated` |
| PUT    | /bulk            | — | TBulkProviderResource | TProviderResource[] | 200/400 | `<resource>:updated` (per id) |
| DELETE | /{id:int}        | — | — | — | 204 | `<resource>:deleted` |
| DELETE | /bulk            | — | TBulkProviderResource | — | 204 | `<resource>:deleted` (per id) |
| GET    | /schema          | — | — | TProviderResource[] | 200 | — |
| POST   | /test            | skipValidation: SkipValidation=None | TProviderResource | — | 204/400 | — |
| POST   | /testall         | — | — | ProviderTestAllResult[] | 200/400 | — |
| POST   | /action/{name}   | (any) | TProviderResource | application/json | 200/400 | — |

`Sonarr.Api.V5/Provider/ProviderControllerBase.cs:60,78,95,127,180,188,197,220,233,259`.

### `Sonarr.Api.V5/Settings/SettingsController.cs` (abstract base)

V5 collapses V3's `ConfigController<TResource>` into a new SettingsController flavor that takes both `IConfigFileProvider` and `IConfigService` (`Sonarr.Api.V5/Settings/SettingsController.cs:31,38`).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | — | — | TResource | 200 |
| GET | /{id:int} | — | — | TResource | 200 (id ignored) |
| PUT | /{id:int} | — | TResource | TResource | 202/404 |

V5 rebrands the configuration controllers as "settings", replacing the V3 `/config/...` URL prefixes:

- `GeneralSettingsController` (`/api/v5/settings/general`) — combines V3's `/config/host`. Override of `SaveSettings` upserts user credentials before calling base (`Sonarr.Api.V5/Settings/GeneralSettingsController.cs:105`).
- `IndexerSettingsController` (`/api/v5/settings/indexer`) — was V3 `/config/indexer`.
- `MediaManagementSettingsController` (`/api/v5/settings/mediamanagement`) — was V3 `/config/mediamanagement`.
- `NamingSettingsController` (`/api/v5/settings/naming`) — *not* a SettingsController subclass; extends RestController directly. Same shape as V3 NamingConfigController (`GET /`, `GET /{id:int}`, `PUT /{id:int}`, `GET /examples`).
- `UiSettingsController` (`/api/v5/settings/ui`) — was V3 `/config/ui`.
- `UpdateSettingsController` (`/api/v5/settings/update`) — V5-only. Wraps `UpdateMechanism` config previously inside HostConfig.

V5 has *removed* V3's `/api/v3/config/downloadclient` and `/api/v3/config/importlist` controllers (no equivalent V5 endpoints).

### `Sonarr.Api.V5/Calendar/CalendarController.cs`

Base route: `/api/v5/calendar`. Difference: query params replaced with `includeUnmonitored` (renamed from `unmonitored`), `includeSpecials` (new), and `includeSubresources: CalendarSubresource[]?` (was three booleans). Otherwise same.

`Sonarr.Api.V5/Calendar/CalendarController.cs:33`.

### `Sonarr.Api.V5/Calendar/CalendarFeedController.cs`

Base route: `/feed/v5/calendar/Sonarr.ics`. Adds `includeSpecials: bool=true` query param; otherwise identical.

`Sonarr.Api.V5/Calendar/CalendarFeedController.cs:30`.

### `Sonarr.Api.V5/Commands/CommandController.cs`

Base route: `/api/v5/command`. Same endpoints as V3. Differences: `Results<Created<T>, NotFound>` returns and `commandResource.Priority` is now honored on POST (V3 hardcoded normal/high based on type).

| Method | Path | Query | Request body | Response | Status codes | SignalR |
|--------|------|-------|--------------|----------|--------------|---------|
| GET    | /         | — | — | CommandResource[] | 200 | — |
| GET    | /{id:int} | — | — | CommandResource | 200/404 | — |
| POST   | /         | — | CommandResource | CommandResource | 201/404 | command:updated, command:sync |
| DELETE | /{id:int} | — | — | — | 204 | — |

`Sonarr.Api.V5/Commands/CommandController.cs:48,79,89`.

### `Sonarr.Api.V5/Episodes/EpisodeController.cs`

Base route: `/api/v5/episode`. Difference: `includeSubresources: EpisodeSubresource[]?` query (`Series|EpisodeFile|Images`) replaces flat booleans. PUT-by-id and PUT /monitor unchanged.

`Sonarr.Api.V5/Episodes/EpisodeController.cs:28,55,66`.

### `Sonarr.Api.V5/Episodes/RenameEpisodeController.cs`

Base route: `/api/v5/rename`. Same shape as V3.

### `Sonarr.Api.V5/EpisodeFiles/EpisodeFileController.cs`

Base route: `/api/v5/episodefile`. Differences vs V3:

- The deprecated `PUT /editor` endpoint has been removed.
- `DELETE /bulk` returns 204 (was 200/`{}`).
- `DELETE /{id:int}` returns 204.

| Method | Path | Query | Request body | Response | Status codes | SignalR |
|--------|------|-------|--------------|----------|--------------|---------|
| GET    | /{id:int} | — | — | EpisodeFileResource | 200/404 | — |
| GET    | /         | seriesId: int?, episodeFileIds: int[]? | — | EpisodeFileResource[] | 200/400 | — |
| PUT    | /{id:int} | — | EpisodeFileResource | EpisodeFileResource | 202/404 | — |
| DELETE | /{id:int} | — | — | — | 204/404 | — |
| DELETE | /bulk     | — | EpisodeFileListResource | — | 204 | — |
| PUT    | /bulk     | — | EpisodeFileResource[] | EpisodeFileResource[] | 200 | — |

Event handlers identical to V3.

`Sonarr.Api.V5/EpisodeFiles/EpisodeFileController.cs:60,92,113,130,145`.

### `Sonarr.Api.V5/Indexers/IndexerController.cs`

Base route: `/api/v5/indexer`. Inherits the entire endpoint set from V5 ProviderControllerBase. SignalR: `indexer:created|updated|deleted`.

### `Sonarr.Api.V5/Indexers/IndexerFlagController.cs`

Base route: `/api/v5/indexerflag`. Same single GET as V3.

### `Sonarr.Api.V5/Metadata/MetadataController.cs`

Base route: `/api/v5/metadata`. Inherits V5 ProviderControllerBase but bulk PUT/DELETE are `[NonAction]`. SignalR: `metadata:created|updated|deleted`.

### `Sonarr.Api.V5/Release/ReleaseController.cs`

Base route: `/api/v5/release`. Major reshape:

- Now extends `RestController<ReleaseResource>` directly (no `ReleaseControllerBase`).
- POST body is `ReleaseGrabResource` (with nested `Override` and `SearchInfo` records); response is `ReleaseGrabResource`.
- GET response is `ReleaseResource[]` augmented with `History` (Grabbed/Failed timestamps).
- `Results<Ok<T>, BadRequest>` / `Results<Ok<T>, NotFound>` envelopes.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET  | /         | seriesId: int?, episodeId: int?, seasonNumber: int? | — | ReleaseResource[] | 200/400/500 |
| POST | /         | — | ReleaseGrabResource | ReleaseGrabResource | 200/404/409 |
| GET  | /{id:int} | (inherited; throws NotImplementedException) | — | — | 500 |

`Sonarr.Api.V5/Release/ReleaseController.cs:86,190`.

### `Sonarr.Api.V5/Release/ReleasePushController.cs`

Base route: `/api/v5/release/push`. Differences: extends `RestController<ReleasePushResource>` (V3 used `ReleaseControllerBase`); `DownloadClient` field renamed to `DownloadClientName`; returns `Results<Ok<ReleaseResource>, BadRequest>` (no longer wraps in array).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| POST | / | — | ReleasePushResource | ReleaseResource | 200/400 |

`Sonarr.Api.V5/Release/ReleasePushController.cs:53`.

### `Sonarr.Api.V5/Profiles/Quality/QualityProfileController.cs`

Same shape as V3, returns typed results. `Sonarr.Api.V5/Profiles/Quality/QualityProfileController.cs:49,58,66,82`.

### `Sonarr.Api.V5/Profiles/Quality/QualityProfileSchemaController.cs`

Same single GET as V3.

### `Sonarr.Api.V5/Profiles/Release/ReleaseProfileController.cs`

V5 changes the validator: `IndexerId` (single int) → `IndexerIds` (array). Endpoints unchanged.

### `Sonarr.Api.V5/Qualities/QualityDefinitionController.cs`

Base route: `/api/v5/qualitydefinition`. Difference: V3 had `PUT /update`; V5 collapses it to plain `PUT /` (no segment) (`Sonarr.Api.V5/Qualities/QualityDefinitionController.cs:58`). Also drops `GET /limits`.

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /         | — | — | QualityDefinitionResource[] | 200 |
| GET | /{id:int} | — | — | QualityDefinitionResource | 200/404 |
| PUT | /         | — | QualityDefinitionResource[] | QualityDefinitionResource[] | 200 |
| PUT | /{id:int} | — | QualityDefinitionResource | QualityDefinitionResource | 202/404 |

### `Sonarr.Api.V5/Queue/QueueController.cs`

Base route: `/api/v5/queue`. Differences vs V3:

- `includeSeries`/`includeEpisode` booleans replaced with `includeSubresources: QueueSubresource[]?` (`Series|Episodes`).
- DELETE/{id} and DELETE /bulk both accept new `message: string?` query (used as blocklist reason); both return 204.
- Listens to V5 `QueueUpdatedEvent` (V3 used `ObsoleteQueueUpdatedEvent`).

`Sonarr.Api.V5/Queue/QueueController.cs:72,97,138`.

### `Sonarr.Api.V5/Queue/QueueActionController.cs`

Same as V3 but returns 204.

### `Sonarr.Api.V5/Queue/QueueDetailsController.cs`

Difference: `includeSubresources` query.

### `Sonarr.Api.V5/Queue/QueueStatusController.cs`

Same as V3.

### `Sonarr.Api.V5/RemotePathMappings/RemotePathMappingController.cs`

Same five endpoints as V3 with typed results. PUT now returns `Results<Ok<RemotePathMappingResource>, NotFound>` (was `ActionResult` 202).

### `Sonarr.Api.V5/RootFolders/RootFolderController.cs`

Same as V3, returns 204 on DELETE.

### `Sonarr.Api.V5/SeasonPass/SeasonPassController.cs`

Returns 204 instead of 202.

### `Sonarr.Api.V5/Series/SeriesEditorController.cs`

V5: PUT returns `Results<Ok<List<SeriesResource>>, BadRequest>`; DELETE returns 204.

### `Sonarr.Api.V5/Series/SeriesFolderController.cs`

Same as V3.

### `Sonarr.Api.V5/Series/SeriesImportController.cs`

Same as V3.

### `Sonarr.Api.V5/Series/SeriesLookupController.cs`

V5 adds `IsExcluded` field on each resource by querying `IImportListExclusionService.FindByTvdbId` (line 53).

### `Sonarr.Api.V5/System/Backup/BackupController.cs`

V5 differences:

- `DELETE /{id:int}` → 204.
- `POST /restore/{id:int}` → `Results<Ok<object>, NotFound>` returning `{ RestartRequired: true }`.
- `POST /restore/upload` returns `Results<Ok<object>, BadRequest<object>>`; bad-extension now returns 400 (with `{ error: "..." }` envelope) instead of 415.

`Sonarr.Api.V5/System/Backup/BackupController.cs:32,53,75,92`.

### `Sonarr.Api.V5/System/SystemController.cs`

V5: `IsContainerized` field removed from response (V3 had it at line 79). Adds `LastDuration` to TaskController response (see below). Otherwise same five endpoints.

### `Sonarr.Api.V5/System/Tasks/TaskController.cs`

Same routes; resource adds `LastDuration: TimeSpan` (line 56).

### `Sonarr.Api.V5/Tags/TagController.cs` & `TagDetailsController.cs`

Same shape as V3.

### `Sonarr.Api.V5/Update/UpdateController.cs`

Same shape as V3.

### `Sonarr.Api.V5/Wanted/CutoffController.cs` & `MissingController.cs`

Replace flat booleans with `includeSubresources: CutoffSubresource[]?` / `MissingSubresource[]?`. `MissingController` adds `includeSpecials: bool=true` query.

### `Sonarr.Api.V5/Blocklist/BlocklistController.cs`

V5: DELETE endpoints return 204 (V3 returned `{}`).

### `Sonarr.Api.V5/CustomFilters/CustomFilterController.cs`

Same shape as V3.

### `Sonarr.Api.V5/DiskSpace/DiskSpaceController.cs`

Same shape as V3.

### `Sonarr.Api.V5/FileSystem/FileSystemController.cs`

Same three endpoints as V3.

### `Sonarr.Api.V5/Health/HealthController.cs`

Same single GET; SignalR `health:sync`.

### `Sonarr.Api.V5/History/HistoryController.cs`

V5 differences:

- `includeSeries`/`includeEpisode` booleans replaced with `includeSubresources: HistorySubresource[]?`.
- New endpoints: `GET /season` (line 131) and `GET /episode` (line 147).
- `GET /series` no longer takes `seasonNumber` (use new `/season`).

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET  | /                | PagingRequestResource, eventType: int[]?, episodeId: int?, downloadId: string?, seriesIds: int[]?, languages: int[]?, quality: int[]?, includeSubresources: HistorySubresource[]? | — | PagingResource&lt;HistoryResource&gt; | 200 |
| GET  | /since           | date: DateTime, eventType: EpisodeHistoryEventType?, includeSubresources: HistorySubresource[]? | — | HistoryResource[] | 200 |
| GET  | /series          | seriesId: int, eventType?, includeSubresources? | — | HistoryResource[] | 200 |
| GET  | /season          | seriesId: int, seasonNumber: int, eventType?, includeSubresources? | — | HistoryResource[] | 200 |
| GET  | /episode         | episodeId: int, eventType?, includeSubresources? | — | HistoryResource[] | 200 |
| POST | /failed/{id:int} | — | — | — | 204 |

`Sonarr.Api.V5/History/HistoryController.cs:65,105,115,131,147,166`.

### `Sonarr.Api.V5/ImportLists/ImportListExclusionController.cs`

V5 collapses V3's `/` (deprecated array) and `/paged`: there is now a single paged `GET /` (line 38). Other endpoints unchanged; DELETE and DELETE /bulk return 204.

### `Sonarr.Api.V5/Logs/LogController.cs`

Same shape as V3.

### `Sonarr.Api.V5/Logs/LogFileController.cs` & `UpdateLogFileController.cs`

Same shape as V3.

### `Sonarr.Api.V5/ManualImport/ManualImportController.cs`

V5: `downloadId: string` query is now `downloadIds: string[]?` (line 25). POST returns `Results<Ok<List<ManualImportResource>>, BadRequest>`. Behavior of reprocessing logic also tightened (item.Quality fallback differs).

### `Sonarr.Api.V5/Parse/ParseController.cs`

Same single GET. Returns `Ok<ParseResource>` (always populated, never null).

---

## Shared infrastructure (`Sonarr.Http`)

These are not V3- or V5-versioned; they sit at the root.

### `Sonarr.Http/ApiInfoController.cs`

Auth: anonymous

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /api | — | — | ApiInfoResource `{ Current: "v5", Deprecated: ["v3"] }` | 200 |

`Sonarr.Http/ApiInfoController.cs:9`.

### `Sonarr.Http/Ping/PingController.cs`

Auth: anonymous

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET  | /ping | — | — | PingResource `{ Status: "OK"\|"Error" }` | 200/500 |
| HEAD | /ping | — | — | — | 200/500 |

`Sonarr.Http/Ping/PingController.cs:24-25`.

### `Sonarr.Http/Authentication/AuthenticationController.cs`

Auth: anonymous (`[AllowAnonymous]`)

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| POST | /login  | returnUrl: string? | LoginResource (form `Username`, `Password`, `RememberMe`) | redirect | 302/401 |
| GET  | /logout | — | — | redirect | 302 |

`Sonarr.Http/Authentication/AuthenticationController.cs:35,88`.

### `Sonarr.Http/Frontend/InitializeJsonController.cs`

Auth: cookie (`[Authorize(Policy = "UI")]`)

| Method | Path | Query | Request body | Response | Status codes |
|--------|------|-------|--------------|----------|--------------|
| GET | /initialize.json | — | — | application/json (apiRoot, apiKey, version, urlBase, ...) | 200 |

`Sonarr.Http/Frontend/InitializeJsonController.cs:33`.

### Frontend / static assets

`Sonarr.Http/Frontend/StaticResourceController.cs` plus the `IMapHttpRequestsToDisk` mapper chain handle: `GET /` (index.html), `GET /login` (login html), `GET /content/...`, `GET /static/...`, `GET /favicon.ico`, `GET /robots.txt`, `GET /manifest.json`, `GET /Content/Images/...` (media-cover proxy), `GET /logfile/{name}`, `GET /updatelogfile/{name}`. These are not REST endpoints in the inventory sense; the Go port can serve them with a generic static-file handler keyed off `IMapHttpRequestsToDisk` analogues.

---

## Notes for the porter

- `[RestPostById]` is misnamed: it produces `POST /` (no `{id}`), it just shares the attribute family.
- Many controllers expose both an explicit `GET /` and an inherited `GET /{id:int}`. Even controllers whose model doesn't really make sense per-id (e.g. Health, Queue) still surface the inherited route (it throws `NotImplementedException` at runtime — see `Health/HealthController.cs:32`, `Queue/QueueController.cs:72`). The Go port may want to drop these dead-end routes.
- ASP.NET model-binding default-fills query params with the C# default-value semantics — the Go port must mirror these defaults to avoid silently changing behavior (especially for the dozens of `bool=false` toggles).
- `ProviderControllerBase` POST/PUT auto-runs both validate-on-save and test-on-save unless `forceSave=true` (V3) / `skipTesting=true` and `skipValidation=All` (V5). The test results route through an entirely separate code path (`Test()` and `TestAll()` actions) and surface as 400 with FluentValidation's raw error array.
- V3's `bool moveFiles = false` on `PUT /api/v3/series/{id}` enqueues a `MoveSeriesCommand` but still returns 202 immediately — the move is async via the command queue.
- Several response bodies are anonymous types (`new { foo = bar }`); the Go port should mirror these as inline structs rather than reusing named types where the field set is trivial.
