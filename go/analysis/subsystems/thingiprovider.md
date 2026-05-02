# ThingiProvider

**Source:** `src/NzbDrone.Core/ThingiProvider/**`, plus the per-subsystem factories in `src/NzbDrone.Core/Indexers/`, `Download/`, `Notifications/`, `ImportLists/`, `Extras/Metadata/`, and the schema renderer in `src/Sonarr.Http/ClientSchema/**`.
**Target Go package:** `go/internal/thingiprovider/` (infrastructure) + per-subsystem `go/internal/<subsystem>/factory.go`.
**Status:** drafted

## Purpose

ThingiProvider is the generic "pluggable type" infrastructure that backs every list-of-user-configured-things in Sonarr: indexers, download clients, notifications, import lists, and metadata writers. It supplies five things: (1) a uniform interface every concrete provider implements (`IProvider`); (2) a uniform DB-backed definition row (`ProviderDefinition` / `Indexers`, `DownloadClients`, `Notifications`, `ImportLists`, `Metadata` tables); (3) a generic factory that hydrates an instance from a stored definition by reflecting the C# class name in the `Implementation` column and the settings-class name in the `ConfigContract` column; (4) a reflection-based field-schema renderer (`SchemaBuilder`) that emits the dynamic settings form the React UI uses for "Add new X"; (5) an escalating cool-down mechanism for failing providers (`ProviderStatusBase` + `ProviderStatusServiceBase` + the `IndexerStatus` / `DownloadClientStatus` / `ImportListStatus` / `NotificationStatus` tables). `Sonarr.Api.V3/ProviderControllerBase.cs` is the generic REST controller every provider list reuses, so the same JSON shape handles `/api/v3/indexer`, `/downloadclient`, `/notification`, `/importlist`, and `/metadata`.

CustomFormats specifications and AutoTagging specifications use a parallel-but-different "specification factory" pattern (separate `Specifications` JSON column; no `Implementation`/`ConfigContract`/`Settings` columns; no escalating-failure tracking) — they are noted in the diff section but are not strictly ThingiProvider.

## Public types

| Type | File | Kind | Notes |
|------|------|------|-------|
| `IProvider` | `IProvider.cs:7` | interface | Base contract: `Name`, `ConfigContract`, `Message`, `DefaultDefinitions`, `Definition` (mutable getter/setter), `Test()`, `RequestAction(stage, query)` |
| `IProviderConfig` | `IProviderConfig.cs:5` | interface | `NzbDroneValidationResult Validate()` — every settings class implements this |
| `NullConfig` | `NullConfig.cs:5` | sentinel | `IProviderConfig.Instance` returned by `ProviderRepository.Query` when the persisted `ConfigContract` type can't be located in the assembly (i.e. plugin removed or class renamed) |
| `ProviderDefinition` | `ProviderDefinition.cs:8` | abstract base model | Extends `ModelBase`. `Name`, `ImplementationName` (runtime-only, `[JsonIgnore]`), `Implementation` (persisted class-name string), `ConfigContract` (persisted class-name string), `Enable` (virtual), `Message`, `Tags`, `Settings` (`IProviderConfig`, hydrated in repo). Setting `Settings` auto-stamps `ConfigContract` from `value.GetType().Name` (`ProviderDefinition.cs:36–43`) |
| `ProviderMessage` / `ProviderMessageType` | `ProviderMessage.cs` | model + enum | `{Message, Type}` where Type ∈ `Info`, `Warning`, `Error`. Surfaces in the UI as a yellow banner on the provider card |
| `IProviderRepository<TProvider>` | `IProviderRepository.cs:5` | repository contract | `IBasicRepository<TProvider>` plus a commented-out `DeleteImplementations(string)` left as a TODO (see Notable quirks) |
| `ProviderRepository<TProviderDefinition>` | `ProviderRepository.cs:13` | abstract repository | Custom `Query(SqlBuilder)` that reads the `Settings` column as a string, deserializes it into the concrete type located via `typeof(IProviderConfig).Assembly.FindTypeByName(item.ConfigContract)` (`ProviderRepository.cs:55`), and falls back to `NullConfig.Instance` when either the body is empty or the type lookup fails |
| `IProviderFactory<TProvider, TProviderDefinition>` | `IProviderFactory.cs:6` | factory contract | 21 methods — see "Lifecycle" below |
| `ProviderFactory<TProvider, TProviderDefinition>` | `ProviderFactory.cs:13` | abstract factory | The shared implementation. Every concrete factory inherits this. `IHandle<ApplicationStartedEvent>` is wired here |
| `ConfigContractNotFoundException` | `ConfigContractNotFoundException.cs:5` | exception | Defined but **not currently thrown anywhere** in the codebase (`grep -rn ConfigContractNotFoundException src/` returns only the file itself). The case it nominally describes — missing config contract — is silently masked by the `NullConfig.Instance` fallback in the repo. Note: this is dead code; flag during port |
| `ProviderAddedEvent<TProvider>` | `Events/ProviderAddedEvent.cs:5` | event | `Definition` payload |
| `ProviderUpdatedEvent<TProvider>` | `Events/ProviderUpdatedEvent.cs:5` | event | `Definition` payload |
| `ProviderDeletedEvent<TProvider>` | `Events/ProviderDeletedEvent.cs:5` | event | `ProviderId` payload |
| `ProviderStatusChangedEvent<TProvider>` | `Events/ProviderStatusChangedEvent.cs:5` | event | `ProviderId, Status` payload |
| `ProviderStatusBase` | `Status/ProviderStatusBase.cs:7` | abstract model | `ProviderId, InitialFailure, MostRecentFailure, EscalationLevel, DisabledTill`. `IsDisabled() => DisabledTill > UtcNow` |
| `IProviderStatusRepository<TModel>` / `ProviderStatusRepository<TModel>` | `Status/ProviderStatusRepository.cs` | repo | `FindByProviderId(int)`, `DeleteByProviderId(int)` |
| `IProviderStatusServiceBase<TModel>` / `ProviderStatusServiceBase<TProvider, TModel>` | `Status/ProviderStatusServiceBase.cs:11,20` | service | `RecordSuccess`, `RecordFailure(minimumBackOff)`, `RecordConnectionFailure`. Auto-handles `ProviderDeletedEvent<TProvider>` to clean up status rows |
| `EscalationBackOff` | `Status/EscalationBackOff.cs:3` | static | The 10-step `Periods` array (see Cool-down) |

## Public services (the per-subsystem factory contracts)

The base contract `IProviderFactory<TProvider, TProviderDefinition>` declares:

| Method | Purpose | Side effects |
|---|---|---|
| `All()` | All persisted definitions | DB read |
| `GetAvailableProviders()` | Hydrated provider instances filtered by `Active()` (subclass-defined) | DB read + DI resolve |
| `Exists(int)`, `Find(int)`, `Get(int)`, `Get(IEnumerable<int>)` | DB lookups | DB read |
| `Create(definition)` | Insert + raise `ProviderAddedEvent` | DB write, event |
| `Update(definition)` / `Update(IEnumerable<...>)` | Update + raise `ProviderUpdatedEvent` (per row) | DB write, events |
| `Delete(int)` / `Delete(IEnumerable<int>)` | Delete + raise `ProviderDeletedEvent` | DB write, events |
| `GetDefaultDefinitions()` | One template per provider class — what the "Add new" UI lists | None (DI resolve only) |
| `GetPresetDefinitions(template)` | Pre-named templates for one provider class (e.g. Newznab → DOGnzb, NZBgeek, …) | None |
| `SetProviderCharacteristics(definition)` / `(provider, definition)` | Stamp runtime-only fields onto a definition before serialization (e.g. `ImplementationName`, capabilities flags) | Stateless on def |
| `GetInstance(definition)` | Resolve a provider instance from DI by the type whose name matches `definition.Implementation`; assigns `Definition` and stamps characteristics | DI resolve |
| `Test(definition)` | Invoke the provider's `Test()` method | Network I/O (varies) |
| `RequestAction(definition, action, query)` | Invoke the provider's `RequestAction(stage, query)` (used for OAuth flows, "browse libraries", category lookups…) | Network I/O |
| `AllForTag(int tagId)` | All defs whose `Tags` contains the tag | DB read |

Concrete factories (see `IndexerFactory`, `DownloadClientFactory`, `NotificationFactory`, `ImportListFactory`, `MetadataFactory`) each:

1. Add subsystem-specific accessor methods (`OnGrabEnabled`, `RssEnabled`, `DownloadHandlingEnabled`, `AutomaticAddEnabled`, `Enabled`).
2. Override `Active()` to add `c.Enable` to the `Settings.Validate().IsValid` filter.
3. Override `SetProviderCharacteristics(provider, definition)` to copy provider capability flags onto the definition (e.g. `IndexerDefinition.Protocol/SupportsRss/SupportsSearch`, `NotificationDefinition.SupportsOnGrab/...`).
4. Override `Test(definition)` to record success/failure into the matching `*StatusService` (`IndexerFactory.cs:140–159`, `NotificationFactory.cs:210–229`, `DownloadClientFactory.cs:111–130`, `ImportListFactory.cs:74–93`).
5. Optionally override `InitializeProviders()` to seed the table from `_providers` on startup. Only `MetadataFactory` does this (`MetadataFactory.cs:25–48`) — it inserts one row per metadata writer class so the user sees Kodi/Plex/Roksbox/Wdtv enabled-disabled toggles instead of an "Add new metadata" UI. All other factories leave the table empty and rely on the user to add rows via the UI.

## Lifecycle

### Discovery + DI
Every concrete provider class is registered with the DI container by **DryIoc convention scanning** at host startup. The factory takes `IEnumerable<TProvider>` in its constructor (`ProviderFactory.cs:24–35`); DryIoc fills this with one instance of every class implementing `TProvider`. `_providers.ToList()` is cached for the lifetime of the factory. There is no plugin model — adding a new provider means adding a class that implements `IIndexer` / `INotification` / etc. to the assembly.

### Startup hook
`ProviderFactory : IHandle<ApplicationStartedEvent>` (`ProviderFactory.cs:13`). On `ApplicationStartedEvent`:

1. `RemoveMissingImplementations()` (`ProviderFactory.cs:199–208`) — for every persisted definition whose `Implementation` string no longer resolves to a registered provider class, **delete the row**. This is the silent-data-loss path that fires when a class gets renamed or removed (see "Notable quirks").
2. `InitializeProviders()` — virtual, default no-op; `MetadataFactory` overrides to seed.

### Per-request hydration (`GetInstance`)
`ProviderFactory.GetInstance(definition)`:
```
type     = _providers.Select(p => p.GetType()).SingleOrDefault(t => t.Name.EqualsIgnoreCase(definition.Implementation));
instance = (TProvider)container.GetRequiredService(type);
instance.Definition = definition;          // every method on the provider reads its config off this
SetProviderCharacteristics(instance, definition);
return instance;
```
(`ProviderFactory.cs:156–168`). The lookup is **case-insensitive** but otherwise an exact match on `Type.Name`. **Providers are resolved from DI on every call**; whether that returns a fresh instance or a cached one depends on the container registration. Most providers are stateful per-call (they hold `Definition` and read from it inside `Fetch`/`OnGrab`), so they must be effectively per-call instances — the DI scoping is what guarantees this. **Porting note**: the Go port can't reuse this pattern verbatim — see TP-Q5.

### Settings JSON serialization
Settings are persisted as a single JSON blob in the `Settings` column. The repo uses STJ (`System.Text.Json`) with these options (`ProviderRepository.cs:21–35`):

- `AllowTrailingCommas = true`
- `DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull`
- `PropertyNameCaseInsensitive = true`
- `DictionaryKeyPolicy = JsonNamingPolicy.CamelCase`
- `PropertyNamingPolicy = JsonNamingPolicy.CamelCase`
- `WriteIndented = true` (so the column actually contains pretty-printed JSON in the DB)
- Converters: `JsonStringEnumConverter(camelCase, allowIntegerValues=true)`, `STJTimeSpanConverter`, `STJUtcConverter`

Reading: `ProviderRepository.Query` (`ProviderRepository.cs:38–71`) reads the `Settings` column as a string ordinal lookup, locates the type via `typeof(IProviderConfig).Assembly.FindTypeByName(item.ConfigContract)` (case-insensitive single-match across `GetExportedTypes()` — `ReflectionExtensions.cs:70–73`), then deserializes. If `body` is null/whitespace OR the type lookup misses, `Settings = NullConfig.Instance`. **Critical**: this means a row with a stale `ConfigContract` survives load — the UI will render it with no fields and `Validate()` returns `IsValid=true` (because `NullConfig.Validate` is unconditional). The `RemoveMissingImplementations` startup hook only checks `Implementation`, not `ConfigContract`, so a config-contract rename without an `Implementation` rename leaves an unconfigurable zombie row.

### Definition-update path
`UpdateProvider` REST handler → `ProviderControllerBase.cs:98–125`:

1. `_providerFactory.Find(id)` → existing definition.
2. `_resourceMapper.ToModel(resource, existingDefinition)` → reads `Fields[]` back through `SchemaBuilder.ReadFromSchema` against the `ConfigContract` type from the request.
3. `existingDefinition.Equals(providerDefinition) && existingDefinition.Settings.Equals(providerDefinition.Settings)` → `hasDefinitionChanged` (uses `Equ` library `MemberwiseEqualityComparer`, see definitions per subsystem). If unchanged, **no DB write, no events, no Test()**.
4. If `Enable && !forceSave && hasDefinitionChanged` → `Test(providerDefinition, includeWarnings: true)` → throws on failure.
5. `_providerFactory.Update` → DB upsert + `ProviderUpdatedEvent`.

The "secret" fields (`PrivacyLevel.ApiKey` / `Password`) round-trip via the sentinel string `"********"` (`SchemaBuilder.cs:18`): `ToSchema` replaces non-empty values with the sentinel, `ReadFromSchema` detects the sentinel coming back and copies the value from `existingDefinition.Settings` instead of overwriting. This is the only way the UI can re-save a provider without the user re-entering credentials.

## Field schemas + presets

### `[FieldDefinition]` annotation (`Annotations/FieldDefinitionAttribute.cs:8–29`)
Properties on `IProviderConfig` settings classes get a `[FieldDefinition(order, ...)]` attribute that the UI uses to render each input. Cross-cite `http-instrumentation-misc.md#ANNO-Q1`.

Discovered `FieldType` values (`FieldDefinitionAttribute.cs:86–106`, all referenced in `src/`):

`Textbox`, `Number`, `Password`, `Checkbox`, `Select`, `Path`, `FilePath`, `Tag`, `Action`, `Url`, `Captcha`, `OAuth`, `Device`, `TagSelect`, `RootFolder`, `QualityProfile`, `SeriesTag`, `KeyValueList`.

Other annotation surfaces:

- `[FieldOptionAttribute(order, label)]` — decorates enum members for `FieldType.Select` so the UI renders a localised dropdown. Auto-uses `[CallerLineNumber]` for stable ordering.
- `[FieldTokenAttribute(field, label, token, value)]` — multi-attribute, decorates a property to inject `{token}` substitutions into another field's `Label`/`HelpText`/`HelpTextWarning` localisation key.
- `HiddenType` ∈ `Visible`, `Hidden`, `HiddenIfNotSet` — last value emits `"hidden": "hiddenIfNotSet"` when the value is null/default, so e.g. OAuth tokens disappear from the form post-auth.
- `PrivacyLevel` ∈ `Normal`, `Password`, `ApiKey`, `UserName` — drives the `********` sentinel above and the redaction in HTTP request logging.

### `SchemaBuilder.ToSchema` (`Sonarr.Http/ClientSchema/SchemaBuilder.cs`)
Reflection over the settings type:

1. `GetProperties(type)` selects all `PropertyInfo` whose `[FieldDefinition]` attribute is non-null, sorted by attribute `Order` (`SchemaBuilder.cs:189–196`).
2. For each simple-typed property, build a `Field { Name, Label, HelpText, HelpTextWarning, HelpLink, Order, Advanced, Type (camelCase enum name), Section, Privacy, Placeholder, Hidden? , SelectOptions/SelectOptionsProviderAction (when Select/TagSelect), IsFloat (for double), Value }` (`SchemaBuilder.cs:132–168`).
3. For nested complex properties, recurse with a dotted prefix.
4. Field labels and help text route through `ILocalizationService.GetLocalizedString` with `FieldTokenAttribute` substitutions resolved per call.
5. Final list sorted by `Order` (renumbered globally on first build to disambiguate nested duplicates).
6. The mapping `Type → FieldMapping[]` is cached in `_mappings` for the process lifetime — first call per type pays the reflection cost.

### `SchemaBuilder.ReadFromSchema` (the inbound mapping)
For an incoming `List<Field>` + `Type targetType`:

1. `Activator.CreateInstance(targetType)` for an empty target.
2. For each mapping, find the matching field by `Name`. If `Privacy ∈ {ApiKey, Password}` and the incoming `Value == "********"`, copy from `existingModel`. Otherwise call the cached value converter.
3. Value converters for `int/long/double/int?/long?/double?/IEnumerable<int>/IEnumerable<string>` parse from string-or-`JsonElement`. All other types are JSON-deserialized via `STJson.Deserialize(rawText, propertyType)` (`SchemaBuilder.cs:265–342`).

### Default definitions (the "Add new" list)
`GET /api/v3/{provider}/schema` (`ProviderControllerBase.cs:197–218`) calls `factory.GetDefaultDefinitions()`:

```
foreach provider in _providers:
    template = provider.DefaultDefinitions
                       .OfType<TProviderDefinition>()
                       .FirstOrDefault(v => v.Name == null || v.Name == provider.GetType().Name);
    if template == null:
        template = new TProviderDefinition { Name="", ConfigContract=provider.ConfigContract.Name, Implementation=provider.GetType().Name, Settings = Activator.CreateInstance(provider.ConfigContract) };
    SetProviderCharacteristics(provider, template);
    yield template;
```
(`ProviderFactory.cs:42–65`). The default is "one entry per provider class". `IndexerBase.DefaultDefinitions` (`IndexerBase.cs:49–65`) yields exactly one definition with `Implementation == GetType().Name`; `NotificationBase` / `DownloadClientBase` / `MetadataBase` all yield empty (`new List<ProviderDefinition>()`), so the factory falls into the `template == null` branch and constructs a synthetic one.

### Presets
`GetPresetDefinitions(template)` (`ProviderFactory.cs:67–77`) returns `provider.DefaultDefinitions.Where(d => d.Name != null && d.Name != provider.GetType().Name)`. The schema endpoint hangs each preset list off `providerResource.Presets` (`ProviderControllerBase.cs:208–214`).

The canonical preset producer is `Indexers/Newznab/Newznab.cs:47–61`, which yields nine named `IndexerDefinition`s (DOGnzb, DrunkenSlug, Nzb.life, NZBCat, NZBFinder.ws, NZBgeek, nzbplanet.net, SimplyNZBs, Tabula Rasa) with pre-filled URLs and category lists. Torznab does the equivalent for trackers.

## Test action

`POST /api/v3/{provider}/test` (`ProviderControllerBase.cs:220–231`):

1. Build a definition off the request body (loading the existing one if `Id > 0` to inherit `********` masked secrets).
2. `Validate(definition, includeWarnings)` first — runs `definition.Settings.Validate()` and throws `ValidationException` on errors **or** warnings.
3. Call `factory.Test(definition)` which calls `GetInstance(definition).Test()`. There is **no enforced timeout** at the framework level — each provider's `Test()` makes its own HTTP calls with whatever timeout `IHttpClient` supplies (default `HttpClient` timeout is 100s).
4. If the result has errors-or-warnings, throw `ValidationException`. Returns `"{}"` body on success.

The factory override path persists test outcome to status when `definition.Id > 0`: success → `RecordSuccess`, failure → `RecordFailure` (`IndexerFactory.cs:140–159`, `NotificationFactory.cs:210–229`, etc.). For new (unsaved) definitions there is no persistence.

`POST /api/v3/{provider}/testall` (`ProviderControllerBase.cs:233–257`) iterates every enabled definition with valid settings, runs `Settings.Validate()` then `factory.Test()` synchronously in-process, and returns `400 BadRequest` if any failed. There is no parallelism — bulk-testing 20 indexers serializes the wall-clock.

## RequestAction

`POST /api/v3/{provider}/action/{name}` (`ProviderControllerBase.cs:259–273`) builds a definition (no validation, no test) and forwards to `factory.RequestAction(definition, name, queryDict)` → `GetInstance(definition).RequestAction(name, queryDict)`. Body is the definition; query string is the action params. Response is the provider's return value JSON-serialized.

The default `IndexerBase.RequestAction` returns `null` (`IndexerBase.cs:69–72`); providers override it. Discovered action names:

| Provider class | Action | Purpose |
|---|---|---|
| `Newznab` / `Torznab` | `newznabCategories` | Fetch category list from the indexer's `caps?` endpoint for the category multi-select |
| `Notifications/Plex/Server/PlexServer` | `startOAuth`, `continueOAuth`, `pollOAuth`, `getOAuthToken`, `servers` | Plex.tv PIN dance; `servers` lists the user's accessible PMS instances |
| `Notifications/Trakt/Trakt` | `startOAuth`, `getOAuthToken` | Trakt OAuth |
| `Notifications/Twitter/Twitter` | `startOAuth`, `getOAuthToken` | Twitter OAuth1 |
| `Notifications/PushBullet` | `getDevices` | List user's PushBullet devices |
| `ImportLists/Plex/PlexImport` | `startOAuth`, `continueOAuth`, `pollOAuth`, `getOAuthToken` | |
| `ImportLists/Trakt/TraktImportBase` | `startOAuth`, `getOAuthToken` | |
| `ImportLists/Simkl/SimklImportBase` | `startOAuth`, `getOAuthToken` | |
| `ImportLists/AniList/AniListImportBase` | `startOAuth`, `getOAuthToken` | |
| `ImportLists/MyAnimeList/MyAnimeListImport` | `startOAuth`, `getOAuthToken` | |
| `ImportLists/Sonarr/SonarrImport` | `getProfiles`, `getLanguageProfiles`, `getTags`, `getRootFolders` | Cross-Sonarr import — pull dropdown options from another Sonarr instance |
| `ImportLists/Custom/CustomImport` | (proxy actions) | Generic custom-list passthrough |

The pattern: any time a settings field's `[FieldDefinition]` has `SelectOptionsProviderAction = "..."`, the React UI calls this endpoint to fill the dropdown live (e.g. categories). OAuth is implemented by a four-stage handshake (`startOAuth` returns a redirect URL, `continueOAuth` / `pollOAuth` poll the IdP, `getOAuthToken` exchanges the code for the token that gets written into the settings).

## Status / cool-down (escalating back-off)

### Tables
Each provider type with failure-tracking has a parallel `*Status` table (see "DB tables" below). All share the columns from `ProviderStatusBase`:

```
Id            int  PK
ProviderId    int  unique  -- FK-by-convention to the provider's Id (no FK constraint declared)
InitialFailure DateTime?
MostRecentFailure DateTime?
EscalationLevel int  not null
DisabledTill DateTime?     -- (DateTimeOffset on NotificationStatus, mig 194)
```
Plus per-subsystem extras: `IndexerStatus.LastRssSyncReleaseInfo` (string-JSONified `ReleaseInfo`), `ImportListStatus.LastSyncListInfo` (string).

### `EscalationBackOff.Periods` (`Status/EscalationBackOff.cs:5–17`)
Index → seconds disabled at this level:
```
0 →     0      (level 0 = healthy, never disabled)
1 →    60      (1 min)
2 →   300      (5 min)
3 →   900      (15 min)
4 →  1800      (30 min)
5 →  3600      (1 hour)
6 → 10800      (3 hours)
7 → 21600      (6 hours)
8 → 43200      (12 hours)
9 → 86400      (24 hours, max)
```

### Algorithm (`Status/ProviderStatusServiceBase.cs:60–150`)
Three entry points, all guarded by `lock(_syncRoot)`:

- **`RecordSuccess(providerId)`** (`:60–83`): if `EscalationLevel == 0` no-op; else **decrement** `EscalationLevel` by 1, clear `DisabledTill`, upsert, raise `ProviderStatusChangedEvent`. So a single success drops one level — it does **not** reset to 0.
- **`RecordFailure(providerId, minimumBackOff = TimeSpan.Zero)`** (`:142–145`) → `RecordFailure(..., escalate: true)`.
- **`RecordConnectionFailure(providerId)`** (`:147–150`) → `RecordFailure(..., escalate: false)`. Network-layer failures do not bump the level (they still disable the provider for the **current** level's period). The use case is "transient DNS / TCP connect failure" — don't hold it against the indexer.

Inside `RecordFailure(providerId, minimumBackOff, escalate)` (`:85–140`):

1. `now = UtcNow`; `status.MostRecentFailure = now`.
2. If `EscalationLevel == 0` → set `InitialFailure = now`, `EscalationLevel = 1`, force `escalate = false` (the first failure always lands on level 1).
3. Compute grace periods:
   - **Startup grace**: `runtimeInfo.StartTime + MinimumTimeSinceStartup (= 15 min default)` > now.
   - **Initial-failure grace**: `status.InitialFailure + MinimumTimeSinceInitialFailure (= 0 default, subclass-overridable)` > now.
4. If `escalate && !inGracePeriod && !inStartupGracePeriod` → `EscalationLevel = min(MaxLevel, EscalationLevel + 1)`.
5. If `minimumBackOff != Zero` → loop-bump `EscalationLevel` until either `MaxLevel` is reached or `BackOffPeriod >= minimumBackOff` (this is how `RetryAfter`/`429 Too Many Requests` is honored — see `HttpIndexerBase.cs:248`).
6. If `!inGracePeriod || minimumBackOff != Zero` → `DisabledTill = now + BackOffPeriod`. (During the initial-failure grace period with no minimum back-off, `DisabledTill` is left null — i.e. the first failure within the configurable grace window does not disable, only logs.)
7. **Startup-grace cap** (`:127–133`): if `inStartupGracePeriod && minimumBackOff == Zero && DisabledTill > now + Periods[2] (5 min)` → cap `DisabledTill` at `now + 5 min`. Boot-time spurts can't immediately push a provider into the 24-hour level.
8. Upsert + `ProviderStatusChangedEvent`.

### Subclass tunables
`MaximumEscalationLevel` (default `Periods.Length - 1 = 9`), `MinimumTimeSinceInitialFailure` (default `Zero`), `MinimumTimeSinceStartup` (default `15 min`). None of the four shipping `*StatusService` classes currently override these.

### `IsDisabled()` and the consumer side (`Status/ProviderStatusBase.cs:14–17`)
A provider is filtered from execution if `DisabledTill > UtcNow`. `IndexerFactory.FilterBlockedIndexers` (`:124–138`), `DownloadClientFactory.FilterBlockedClients` (`:95–109`), `NotificationFactory.FilterBlockedNotifications` (`:175–189`), `ImportListFactory.FilterBlockedImportLists` (`:58–72`) all share the same shape: build a `Dictionary<ProviderId, Status>` of currently-disabled, then filter and log a debug line per blocked provider with its `DisabledTill.ToLocalTime()`.

### Who calls `RecordFailure`
`HttpIndexerBase.cs:222–303` is the canonical caller and shows every entry-point case:

- `WebException(NameResolutionFailure | ConnectFailure)` → `RecordConnectionFailure` (no escalation).
- `WebException` other → `RecordFailure`.
- `TooManyRequestsException` / `RequestLimitReachedException` → `RecordFailure(retryTime)` where `retryTime` is `RetryAfter` if set, else `MinimumBackoff`.
- `HttpException`, `ApiKeyException`, `CloudFlareCaptchaException`, `TaskCanceledException`, `IndexerException`, generic `Exception` → `RecordFailure`.
- Success → `RecordSuccess` after `UpdateRssSyncStatus`.

`ImportLists/AniList/List/AniListImport.cs:82–127` follows the same shape with `TimeSpan.FromHours(1)` as a fixed minimum back-off for "connection-ish" errors.

## Events raised (IEventAggregator)

| Event | Payload | Producers | Consumers |
|---|---|---|---|
| `ProviderAddedEvent<TProvider>` | `Definition` | `ProviderFactory.Create:117` | `Sonarr.Api.V3/ProviderControllerBase.Handle:276` (broadcasts SignalR `ModelAction.Created`); `HealthCheck/Checks/IndexerSearchCheck`, `IndexerRssCheck`, `DownloadClientCheck` (re-runs on add) |
| `ProviderUpdatedEvent<TProvider>` | `Definition` | `ProviderFactory.Update:125,134` | `ProviderControllerBase.Handle:282` (SignalR Updated); `Indexers/CachedIndexerSettingsProvider:64`; `ImportLists/ImportListUpdatedHandler:16` (queues `ImportListSyncCommand` for the touched list); ten HealthChecks subscribe |
| `ProviderDeletedEvent<TProvider>` | `ProviderId` | `ProviderFactory.Delete:143,152` | `ProviderControllerBase.Handle:288` (SignalR Deleted); `Indexers/CachedIndexerSettingsProvider:69`; `ImportLists/ImportListSyncService:338`; `ImportLists/ImportListItems/ImportListItemService:74` (deletes all `ImportListItems` rows whose `ImportListId == ProviderId` — see "Cascade behavior"); `ProviderStatusServiceBase.HandleAsync:152` (deletes the status row); ten HealthChecks |
| `ProviderStatusChangedEvent<TProvider>` | `ProviderId, Status` | `ProviderStatusServiceBase.RecordSuccess:81`, `RecordFailure:138` | `HealthCheck/Checks/Indexer*StatusCheck`, `ImportListStatusCheck` (forces a re-evaluation of the corresponding health item) |

## Events consumed by the provider machinery

| Event | Handler | Behavior |
|---|---|---|
| `ApplicationStartedEvent` | `ProviderFactory.Handle:170–177` | Removes definitions whose `Implementation` no longer resolves; calls `InitializeProviders()` |
| `ProviderDeletedEvent<TProvider>` | `ProviderStatusServiceBase.HandleAsync:152–155` | `_providerStatusRepository.DeleteByProviderId(message.ProviderId)` |

## DB tables touched

### Canonical schema (cited from `Datastore/Migration/042_add_download_clients_table.cs`)
This is the cleanest of the seven; everything else is a near-duplicate.

```csharp
Create.TableForModel("DownloadClients")
      .WithColumn("Enable").AsBoolean().NotNullable()
      .WithColumn("Name").AsString().NotNullable()
      .WithColumn("Implementation").AsString().NotNullable()
      .WithColumn("Settings").AsString().NotNullable()
      .WithColumn("ConfigContract").AsString().NotNullable()
      .WithColumn("Protocol").AsInt32().NotNullable();
```

The `Id` PK and any FluentMigrator audit columns come from `TableForModel` plumbing in `Datastore/Migration/Framework/`. `Tags HashSet<int>` is added by a later migration as a JSON-serialized string column.

### Provider tables — one row per user-configured provider

| Table | Migration | Columns beyond the canonical 6 | Notes |
|---|---|---|---|
| `Notifications` | 001 (`001_initial_setup.cs:84–89`) → 025 (`ConfigContract`) | `OnGrab`, `OnDownload` (initial); columns expanded in many later migs (`OnUpgrade`, `OnRename`, `OnSeriesAdd`, `OnSeriesDelete`, `OnEpisodeFileDelete`, `OnHealthIssue`, `OnHealthRestored`, `OnApplicationUpdate`, `OnImportComplete`, `OnManualInteractionRequired`, `IncludeHealthWarnings`, `Tags`) | `Settings` is **nullable**. `Name` is **not unique** on this table |
| `Indexers` | 001 (`001_initial_setup.cs:96–100`) → 022 (`ConfigContract`) | `EnableRss`, `EnableAutomaticSearch`, `EnableInteractiveSearch`, `Priority`, `DownloadClientId`, `SeasonSearchMaximumSingleEpisodeAge`, `Tags` (mig 159), `Protocol` | `Name` is **unique**. `Settings` is **nullable** |
| `DownloadClients` | 042 (`042_add_download_clients_table.cs`) | `Protocol`, `Priority`, `RemoveCompletedDownloads`, `RemoveFailedDownloads`, `Tags` | `Settings` is **not nullable**. `Name` is **not unique** here |
| `Metadata` | 039 (`039_add_metadata_tables.cs`) | none beyond canonical | `Settings` is **not nullable**. `MetadataFactory` seeds rows on startup |
| `ImportLists` | 142 (`142_import_lists.cs:11–21`) | `EnableAutomaticAdd`, `RootFolderPath`, `ShouldMonitor`, `QualityProfileId`, `LanguageProfileId` (now obsolete), `Tags`, plus later mig adds `MonitorNewItems`, `SeriesType`, `SeasonFolder`, `SearchForMissingEpisodes` | `Name` is **unique**. `Settings` and `ConfigContract` are **nullable**. Adds `ImportListExclusions` table same migration |

### Status tables — one row per failing provider

| Table | Migration | Diff from `ProviderStatusBase` columns |
|---|---|---|
| `IndexerStatus` | 091 (`091_added_indexerstatus.cs`) | adds `LastRssSyncReleaseInfo` (string, JSON) |
| `DownloadClientStatus` | 115 (`115_add_downloadclient_status.cs`) | none |
| `ImportListStatus` | 142 (`142_import_lists.cs:23–29`) | adds `LastSyncListInfo` (string) |
| `NotificationStatus` | 194 (`194_add_notification_status.cs`) | uses `DateTimeOffset` instead of `DateTime` for the timestamp columns — this is the **only schema-shape difference** between the four status tables |

`IndexerStatus` uses `IndexerId` as the unique-FK column name; the other three use `ProviderId`. The C# model is the same (`ProviderStatusBase.ProviderId`) — Dapper maps the column name via the table's column ordinal regardless. Note this for the Go ORM choice (see TP-Q7).

### Specification tables — parallel pattern, NOT ThingiProvider

`CustomFormats` (`171_add_custom_formats.cs:20–23`):
```
Name string unique, Specifications string default '[]', IncludeCustomFormatWhenRenaming bool default false
```

`AutoTagging` (`179_add_auto_tagging.cs:11–15`):
```
Name string unique, Specifications string default '[]', RemoveTagsAutomatically bool default false, Tags string default '[]'
```

Both store all spec configs as one JSON array in `Specifications` rather than one row per spec. There is no `Implementation`/`ConfigContract`/`Settings` triple, no `Test()` button, no escalating-failure tracking, and the Go port should treat this as a separate sub-pattern. The settings shape inside the JSON does still use `[FieldDefinition]` annotations rendered via `SchemaBuilder` — see `quality-profiles-formats-organizer.md` for that side.

## Cascade behavior on provider delete

`ProviderFactory.Delete(id)` does **not** cascade in SQL — there is no `ON DELETE CASCADE` on any of these tables (FluentMigrator default). The cascade is event-driven:

1. `_providerRepository.Delete(id)` — only the row in the provider table goes.
2. `ProviderDeletedEvent<TProvider>` is raised.
3. Status row: `ProviderStatusServiceBase.HandleAsync` (`:152–155`) → `DeleteByProviderId`.
4. `ImportListItems` (only `IImportList`): `ImportListItemService.HandleAsync` (`:74–78`) deletes all rows where `ImportListId == ProviderId`.
5. Cached indexer settings (only `IIndexer`): `CachedIndexerSettingsProvider.Handle` evicts the cache entry.
6. SignalR `ModelAction.Deleted` broadcast for client-side state update.
7. **Indexer history rows are NOT deleted.** History keeps the orphan `IndexerId` indefinitely; the UI displays "Indexer (Removed)" client-side.

The `Housekeeping` subsystem mops up status rows whose providers got deleted *without* the event firing (e.g. direct DB intervention) — `Housekeeping/Housekeepers/CleanupOrphanedDownloadClientStatus.cs:15–25` runs:
```sql
DELETE FROM "DownloadClientStatus"
 WHERE "Id" IN (SELECT s."Id" FROM "DownloadClientStatus" s
                LEFT JOIN "DownloadClients" d ON s."ProviderId" = d."Id"
                WHERE d."Id" IS NULL)
```
Mirror housekeepers exist for `IndexerStatus`, `ImportListStatus`, `NotificationStatus`, plus `FixFutureIndexerStatusTimes` (clamps DST/clock-skew `DisabledTill > UtcNow + maxBackOff`). Cross-cite `system.md` for the housekeeping schedule.

## The `Implementation` string — load-bearing C# class names

**Critical for the port.** The `Implementation` column persists the **C# class name** (`Provider.GetType().Name`, `ProviderFactory.cs:56,61`). On startup `RemoveMissingImplementations` (`:199–208`) **silently deletes** any row whose `Implementation` no longer matches a known provider class. The same name lookup happens on every `GetInstance` (`:165–168`). This means:

1. Renaming a C# provider class is a **breaking migration**: every existing user installation loses every configured indexer/download client/notification of that type. Sonarr's history shows multiple cases where the migration framework explicitly updates the column to keep the rename safe — e.g. `046_fix_nzb_su_url.cs`, `049_fix_dognzb_url.cs`, `082_add_fanzub_settings.cs`, `090_update_kickass_url.cs`, `104_remove_kickass.cs`, `219_nzb_su_url_to_nzb_life.cs`. This is not optional housekeeping — every provider rename forces a migration.
2. Likewise `ConfigContract` persists the **C# settings class name** (`ProviderDefinition.cs:36–43` auto-stamps from `Settings.GetType().Name`; `ProviderRepository.cs:55` resolves it via `Assembly.FindTypeByName`). Renaming a settings class without a migration silently downgrades every persisted row to `NullConfig.Instance` — a configurationless zombie, since `RemoveMissingImplementations` doesn't touch `ConfigContract`.

Go has no concept of a stable type name (Go types live in packages, qualified names include import paths, and `reflect.Type.Name` returns the unqualified name without the `Type.Name`-equality semantics .NET assumes). The Go port **must decouple the persisted "implementation" string from the Go type name** by introducing a stable string registry per provider type. Suggested: a `ProviderRegistry` keyed by an explicit string ID that providers self-declare via a `ProviderID() string` method (or registration call). Existing user data must keep loading — the registry IDs MUST equal the C# `GetType().Name` values for every shipped class. Same approach for `ConfigContract` → settings struct registry. See **TP-Q1** below.

## Notable quirks / TODOs / HACKs found in source

- `IProviderRepository.cs:8` — commented-out `// void DeleteImplementations(string implementation);` — leftover from when class renames were handled programmatically; now done via FluentMigrator `Update.Table(...)`.
- `ConfigContractNotFoundException` is **defined but never thrown** — the case is masked by `NullConfig.Instance` (`ProviderRepository.cs:57–60`). Either fire it or delete the file. Flag during port.
- `ProviderFactory.GetImplementation` (`:165–168`) uses `SingleOrDefault` with case-insensitive name match. Two providers in the same assembly with names that differ only in case (`Plex` and `plex`) will throw `InvalidOperationException` here, not at registration. Currently no such collisions exist.
- `ProviderRepository.Query` (`:55`) hard-codes `typeof(IProviderConfig).Assembly` for the type lookup — i.e. config contracts must live in `NzbDrone.Core.dll`. Plugin providers in another assembly would fail silently to `NullConfig.Instance`. This bakes in "no plugins" as a current architectural assumption.
- `ProviderControllerBase.UpdateProvider` (`:101`) has a `// TODO: Remove fallback to Id from body in next API version bump` — the route `id` is meant to be authoritative but the controller currently falls back to `resource.Id` for compatibility with legacy clients.
- `SchemaBuilder.GetFieldMappings` (`:88–107`) is a process-lifetime cache with a single `lock(_mappings)`; field renames in a settings class require a process restart to be visible. There is no eviction.
- `SchemaBuilder.GetFieldMapping` comment at `:86–87` — "Ideally this function should begin a System.Linq.Expression expression tree since it's faster. But it's probably not needed till performance issues pop up."
- `NotificationFactory` is the only factory **without** a `ResolveBy(id, name)` helper — both `Indexer` and `DownloadClient` factories have one, and the controller patterns assume notifications never need lookup-by-name. If the port adds named notification refs (e.g. for a webhook router), the helper needs to be added.
- The startup `RemoveMissingImplementations` is silent: a user upgrading across a provider-class rename will lose all their indexers with no warning beyond a `_logger.Warn("Removing {0}", invalidDefinition.Name)`. Recommend the port surface this as a `HealthCheck` row instead.
- `ProviderStatusServiceBase.RecordFailure` uses `lock(_syncRoot)` — single-process correctness only. Multi-replica deployments would need a row-level locking strategy (covered in `datastore.md` Postgres advisory locks).

## Test files (the port must keep passing equivalents)

- `src/NzbDrone.Core.Test/ThingiProvider/Status/ProviderStatusServiceFixture.cs` — back-off escalation, grace periods, startup cap.
- `src/NzbDrone.Core.Test/IndexerTests/IndexerStatusServiceFixture.cs` — `RecordRss`/`UpdateRssSyncStatus` plus inheritance from base.
- `src/NzbDrone.Core.Test/Download/DownloadClientStatusServiceFixture.cs`.
- `src/NzbDrone.Core.Test/NotificationTests/NotificationStatusServiceFixture.cs`.
- `src/NzbDrone.Core.Test/ImportListTests/ImportListStatusServiceFixture.cs`.
- `src/NzbDrone.Core.Test/IndexerTests/IndexerFactoryFixture.cs` — `RemoveMissingImplementations`, `GetDefaultDefinitions`, `GetPresetDefinitions`.
- `src/Sonarr.Http.Test/ClientSchema/SchemaBuilderFixture.cs` — round-trip `ToSchema` / `ReadFromSchema`, masking, value converters.
- `src/NzbDrone.Integration.Test/ApiTests/Indexer*` and `Notification*` — REST-level CRUD + schema + test endpoints.

## Open questions

- **TP-Q1** — How does the Go port replace the `Implementation` string lookup? Options: (a) explicit registry keyed by a stable string each provider declares, e.g. `func (n *Newznab) ProviderID() string { return "Newznab" }`; (b) build-time codegen mapping a sealed type-tag to a constructor; (c) reflect via package path. (a) is the only one that preserves "class name strings already in user DBs" without a forced migration. Resolution required before any Go provider lands. **Blocks Phase 1 (any provider port).**
- **TP-Q2** — Same question for `ConfigContract`. The settings struct identity must round-trip through SQLite/Postgres `Settings` JSON. Recommend the same explicit-string registry approach so provider+contract IDs are decoupled from Go type names. **Blocks Phase 1.**
- **TP-Q3** — Should we eagerly throw `ConfigContractNotFoundException` (which the C# code defines but never throws) or keep the silent `NullConfig` fallback? If we throw, do we throw at startup (fail-fast) or per-load (let other providers boot)? The C# behavior is "load successfully with empty settings, leave UI broken until user re-saves". Recommend startup `HealthCheck` row with the offending row ID + a "delete" CTA. **Blocks Phase 2 (provider CRUD UI).**
- **TP-Q4** — `[FieldDefinition]` annotations are read by reflection at runtime. Go has no annotation-equivalent. Options: struct tags (`thingi:"order=2,type=password"`), a `func (s *Settings) Schema() []Field` method, or codegen from a YAML descriptor. Struct tags are closest to the existing UX; method-based loses ordering-by-line-number for OAuth fields (`order = 99` patterns). **Blocks Phase 1 (any settings struct port).**
- **TP-Q5** — `ProviderFactory.GetInstance` resolves a provider from the DI container on every call and stamps `Definition` onto it (`ProviderFactory.cs:159–162`). This is OK in C# because DryIoc returns a transient by default. In Go we'd typically construct providers as concrete values. Decide between (a) provider-as-value: `func (r *Registry) Hydrate(def *Definition) Provider` returns a fresh value-type; (b) provider-as-interface with a `WithDefinition(def)` builder; (c) per-call closures. (a) is simplest and matches Go idiom — drives the registry shape from TP-Q1. **Blocks Phase 1.**
- **TP-Q6** — `Provider.Test()` has no enforced timeout in the C# code — the only bound is the underlying `IHttpClient` timeout (~100s). The Go port should require a `context.Context` per call so the controller can apply a deadline. Decide the default deadline (suggest 30s; per-provider override via setting). **Blocks Phase 2 (provider Test endpoint).**
- **TP-Q7** — Status tables: `IndexerStatus.IndexerId` vs `*Status.ProviderId`, and `NotificationStatus` using `DateTimeOffset` while the others use `DateTime`. Decide whether the port homogenizes the column names + types (one schema migration) or preserves column-name parity for transparent dual-running. Cross-cite `datastore.md` migration policy. **Blocks Phase 2 (status persistence).**
- **TP-Q8** — `ProviderRepository.Query` hard-codes `typeof(IProviderConfig).Assembly` (`ProviderRepository.cs:55`). The Go port has no plugin model planned — confirm this stays single-binary. If yes, the registry can be a package-level `init()`-populated map; if no, we need a discovery hook.
- **TP-Q9** — The `RemoveMissingImplementations` startup hook silently deletes provider rows whose class went away. Decide whether the Go port (a) preserves silent-delete (matches existing UX), (b) raises a `HealthCheck` instead and leaves the row, or (c) emits a one-shot UI banner. (b) seems strictly better. **Blocks Phase 2 (startup behavior parity).**
- **TP-Q10** — Settings JSON serialization: the C# repo writes pretty-printed JSON (`WriteIndented = true`) into the DB. Decide whether the Go port preserves byte-identical column contents (round-trip-without-rewrite) or re-canonicalizes on save (compact, sorted keys — better for diffs but breaks any user's `sqlite3` muscle memory). Cross-cite `datastore.md` JSON conventions. **Blocks Phase 1 dual-running tests.**
- **TP-Q11** — `[FieldDefinition].SelectOptionsProviderAction` cross-references `RequestAction(name, ...)`. The link between annotation string and action handler is by-convention only — there is no compile-time check that the action exists. Should the Go port enforce this (registry of declared actions, schema rendering errors out if a referenced action isn't registered)? Worth doing.
- **TP-Q12** — Concrete factories all duplicate the same `FilterBlocked*` helper (`IndexerFactory.cs:124–138`, `DownloadClientFactory.cs:95–109`, `NotificationFactory.cs:175–189`, `ImportListFactory.cs:58–72`). The Go port can hoist this into the base factory if generics are in scope. Confirm the design before duplicating in Go.
- **TP-Q13** — `RecordFailure(minimumBackOff)` skips levels until `BackOffPeriod >= minimumBackOff` (`ProviderStatusServiceBase.cs:115–120`). The Go port should preserve this semantics so `429` retry-after behavior matches. Add property test.
- **TP-Q14** — `ProviderControllerBase.GetTemplates` calls `factory.GetDefaultDefinitions().OrderBy(p => p.ImplementationName)` — alphabetical by display name. Locale-dependent (`OrderBy` uses current culture). Pin to invariant in the port. Minor but worth noting.
- **TP-Q15** — The CustomFormats / AutoTagging "specification factory" pattern is similar but not identical to ThingiProvider (no `Implementation`/`ConfigContract`/`Settings` triple, JSON-array-of-specs instead of one row per spec). Decide whether the Go port models them as a separate `specfactory` package or unifies them with thingiprovider via a shared `Pluggable` interface. The two have different cool-down semantics (specs have none) and different DB shapes — recommend keeping them separate; cross-cite `quality-profiles-formats-organizer.md`. **Blocks Phase 2 (specifications port).**
