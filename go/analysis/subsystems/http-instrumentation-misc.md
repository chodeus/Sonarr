# Http / Instrumentation / Misc (long-tail Core directories)

**Source:** `src/NzbDrone.Core/{Http,Instrumentation,Analytics,DataAugmentation,CustomFilters,SeriesStats,IndexerSearch,MediaCover,Annotations,Exceptions,Properties}/**`
**Target Go package(s):** `go/internal/{http,instrumentation,analytics,dataaugmentation/{dailyseries,scene,xem},customfilters,seriesstats,indexersearch,mediacover,annotations,coreerrors}` (one sub-package per area)
**Status:** drafted

This card consolidates eleven long-tail Core directories that are individually small but cross-cut the rest of the system. None of them stands alone: `Http` adds Sonarr-flavoured policies on top of `NzbDrone.Common.Http`; `Instrumentation` plugs an NLog target into the log database; `Analytics` is a one-property service used by the updater and Sentry gating; `DataAugmentation` and `CustomFilters` are tiny IO/state subsystems; `SeriesStats` is a 2-query rollup powering the series UI; `IndexerSearch` is the search-orchestration entry that fans `decisionengine` calls out across `indexers`; `MediaCover` is image cache + ImageSharp; `Annotations` is the C# decoration layer that drives the frontend's settings schema; `Exceptions` is the small registry of HTTP-mapped error types; `Properties` is just `[InternalsVisibleTo]`.

Cross-references:
- The HTTP client itself lives in `NzbDrone.Common.Http/**` (the `HttpClient`, dispatcher, request builder, cookie cache, rate limiter). Covered in the `common` card. **This** card only describes the Core-level extensions: CloudFlare interceptor, TorCache interceptor, proxy-settings provider.
- The NLog configuration (file/console/Sentry/global filters) lives in `NzbDrone.Common.Instrumentation/**` (`NzbDroneLogger.Register`, `CleansingFileTarget`, `SentryTarget`). Covered in the `common` card. **This** card describes the `DatabaseTarget` (DB write of log rows) and the runtime reconfiguration handlers (`ReconfigureLogging` / `ReconfigureSentry`).
- Search orchestration calls into `decisionengine` (`IMakeDownloadDecision`) and `indexers` (`IIndexerFactory` / `IIndexer.Fetch`). Cited inline; not re-derived here.

---

## 1. Http (Core/Http)

### Purpose
`NzbDrone.Core/Http/` contains three classes that sit *on top of* the Common HTTP stack:
1. `HttpProxySettingsProvider` — turns `IConfigService` proxy settings into an `HttpProxySettings` value, with bypass-list support.
2. `TorCacheHttpRequestInterceptor` — site-specific request rewriter for `torcache.net`.
3. `CloudFlare/` — a post-response interceptor that detects CloudFlare CAPTCHA challenge pages and converts them into a typed exception (`CloudFlareCaptchaException`) carrying the parsed sitekey/ray/stoken.

The interceptors are registered as `IHttpRequestInterceptor` and picked up by the `HttpClient` constructor via DI (`NzbDrone.Common/Http/HttpClient.cs:51-63`). Every HTTP request the application issues runs `PreRequest`/`PostResponse` on every interceptor in order.

### Public types
| Type | File | Kind | Notes |
| --- | --- | --- | --- |
| `HttpProxySettingsProvider : IHttpProxySettingsProvider` | `Http/HttpProxySettingsProvider.cs:11` | service | Reads 7 `IConfigService.Proxy*` keys; returns `null` when proxy disabled or URL is bypassed. |
| `TorCacheHttpRequestInterceptor : IHttpRequestInterceptor` | `Http/TorcacheHttpInterceptor.cs:5` | interceptor | Pre-request only — strips query string and adds a `Referer` header for `torcache.net`. |
| `CloudFlareHttpInterceptor : IHttpRequestInterceptor` | `Http/CloudFlare/CloudFlareHttpInterceptor.cs:8` | interceptor | Post-response only — on `403 + cdn-cgi/scripts/cf.challenge.js` content, parses `data-ray`/`data-sitekey`/`data-stoken` and throws `CloudFlareCaptchaException`. |
| `CloudFlareCaptchaException : NzbDroneException` | `Http/CloudFlare/CloudFlareCaptchaException.cs:6` | exception | Carries the failing `HttpResponse` and the parsed `CloudFlareCaptchaRequest`. `IsExpired` returns true if the request already had a `cf_clearance` cookie. |
| `CloudFlareCaptchaRequest` | `Http/CloudFlare/CloudFlareCaptchaRequest.cs:5` | DTO | `Host, SiteKey, Ray, SecretToken, ResponseUrl` (= `request.Url + /cdn-cgi/l/chk_captcha`). |

### CloudFlare regex (parsed out of the challenge page)
Hard-coded at `Http/CloudFlare/CloudFlareHttpInterceptor.cs:14`:
```
data-ray="(?<Ray>[\w-_]+)".*?data-sitekey="(?<SiteKey>[\w-_]+)".*?data-stoken="(?<SecretToken>[\w-_]+)"
```
Fragile by design: any change in CloudFlare's challenge HTML order breaks detection. The detection signal itself is `response.Content.Contains("cdn-cgi/scripts/cf.challenge.js")` (`CloudFlareHttpInterceptor.cs:28`).

### Proxy bypass logic
`HttpProxySettingsProvider.ShouldProxyBeBypassed` (`Http/HttpProxySettingsProvider.cs:52-58`) instantiates a real `System.Net.WebProxy` to reuse Microsoft's bypass-list parser, *then* additionally checks each entry as an `IPAddressRange` (NetTools NuGet). This means Sonarr supports CIDR/range entries in `ProxyBypassFilter` even though the raw `WebProxy` doesn't (HTTP-Q3).

### Config keys (read from `IConfigService`)
- `ProxyEnabled` (default `false`) — `Configuration/ConfigService.cs:396`
- `ProxyType` (default `Http`; values `Http|Socks4|Socks5`) — `ConfigService.cs:398`
- `ProxyHostname` (default `""`) — `ConfigService.cs:400`
- `ProxyPort` (default `8080`) — `ConfigService.cs:402`
- `ProxyUsername` / `ProxyPassword` (default `""`) — `ConfigService.cs:404,406`
- `ProxyBypassFilter` (default `""`, comma-separated, supports `*.host` wildcards) — `ConfigService.cs:408`
- `ProxyBypassLocalAddresses` (default `true`) — `ConfigService.cs:410`

Settings keys live in the `Config` table (`name → value`) and are surfaced through the v3 `HostConfig` and v5 `GeneralSettings` endpoints.

### Side effects
- `HttpProxySettingsProvider` is read-only; constructed per request when the dispatcher chooses a proxy.
- `TorCacheHttpRequestInterceptor.PreRequest` mutates `request.Url` (clears query) and `request.Headers` (adds `Referer`).
- `CloudFlareHttpInterceptor.PostResponse` *throws* on detection (does not return a normal response). Callers in `Indexers/HttpIndexerBase.cs` and `ImportLists/HttpImportListBase.cs` catch and surface this as a health-check / status update.

### Quirks / TODOs / HACKs
- `TorcacheHttpInterceptor.cs:11` — torcache.net has been defunct for years; the interceptor still runs unconditionally on every request because it's a global `IHttpRequestInterceptor` (HTTP-Q1).
- The CloudFlare detection only handles the *legacy* CAPTCHA challenge (sitekey-based). Modern Turnstile / managed challenges are not detected.
- The bypass-list parser shoves a leading `;` in front of `*.host` entries (`HttpProxySettings.cs:37-41`) because `WebProxy` expects a different separator format. Easy port-time foot-gun.

### Test files
- `src/NzbDrone.Core.Test/Http/TorCacheHttpRequestInterceptorFixture.cs`
- (CloudFlare interceptor has no dedicated fixture — exercised via integration tests against indexers.)

### Open questions
- **HTTP-Q1**: Should the Go port carry the torcache.net interceptor at all? The site has been dead for years. Recommend deleting unless we find an active code path.
- **HTTP-Q2**: CloudFlare CAPTCHA detection — the regex assumes a specific data-attribute order that real CloudFlare pages no longer match. Is this code dead? If yes, port to a stub that always returns the response unmodified. If no, it needs a Turnstile/managed-challenge upgrade in parallel.
- **HTTP-Q3**: NetTools `IPAddressRange` is used for bypass-list CIDR support beyond what `System.Net.WebProxy` natively allows. The Go port needs a bypass-list parser that mirrors both the `*.host` glob pattern *and* CIDR support — flag for the http-client spike.
- **HTTP-Q4**: Should the Go port keep "interceptors" as a chain pattern, or move to `http.RoundTripper` middleware? The chain is small (3 interceptors total across Core+Common), so a slice of `RoundTripper` wrappers seems sufficient.

---

## 2. Instrumentation (Core/Instrumentation)

### Purpose
All things logging-database related. The folder configures an NLog target that writes log rows into the **secondary** log database (`ILogDatabase`, separate from `IMainDatabase`), provides a paged read API for the System → Logs UI, and registers two reconfiguration hooks that re-apply config-file settings to NLog when the user edits log levels in the UI.

### Public types
| Type | File | Role |
| --- | --- | --- |
| `Log : ModelBase` | `Instrumentation/Log.cs:6` | DB row. `Message, Time, Logger, Exception, ExceptionType, Level`. |
| `ILogRepository / LogRepository` | `Instrumentation/LogRepository.cs:7,12` | `IBasicRepository<Log>` plus `Trim()`. Constructed against `ILogDatabase`, not `IMainDatabase`. |
| `ILogService / LogService` | `Instrumentation/LogService.cs:7,12` | `Paged(PagingSpec<Log>)` for the UI; also `IExecute<ClearLogCommand>`. |
| `DatabaseTarget : NLog.TargetWithLayout` | `Instrumentation/DatabaseTarget.cs:16` | NLog target that INSERTs each log row directly into `Logs`. Wrapped in `SlowRunningAsyncTargetWrapper`. Also `IHandle<ApplicationShutdownRequested>`. |
| `SlowRunningAsyncTargetWrapper : NLog.AsyncTargetWrapper` | `Instrumentation/SlowRunningAsyncTargetWrapper.cs:9` | Wrapper that *only* runs the lazy-writer timer when there are pending events. Drops the timer back to idle once the queue drains. |
| `DeleteLogFilesService` | `Instrumentation/DeleteLogFilesService.cs:14` | `IExecute<DeleteLogFilesCommand>` and `IExecute<DeleteUpdateLogFilesCommand>`. Empties the log folder / update-log folder. |
| `ReconfigureLogging` | `Instrumentation/ReconfigureLogging.cs:19` | `IHandleAsync<ConfigFileSavedEvent>` — re-applies log levels, file rotation, syslog target, console layout, and Sentry on/off when `config.xml` is saved. |
| `ReconfigureSentry` | `Instrumentation/ReconfigureSentry.cs:12` | `IHandleAsync<ApplicationStartedEvent>` — calls `SentryTarget.UpdateScope(...)` with DB version, branch, platform info. |
| `ClearLogCommand`, `DeleteLogFilesCommand`, `DeleteUpdateLogFilesCommand` | `Instrumentation/Commands/*.cs` | All three: `Command` with `SendUpdatesToClient => true`. Manual-only triggers from the UI. |

### DB schema for `Logs` table
Defined entirely by INSERT signature at `Instrumentation/DatabaseTarget.cs:18-19`:
```
INSERT INTO "Logs" ("Message","Time","Logger","Exception","ExceptionType","Level")
VALUES (@Message,@Time,@Logger,@Exception,@ExceptionType,@Level)
```
Plus the implicit `Id` PK from `BasicRepository<Log>`. Both SQLite and Postgres branches exist (`DatabaseTarget.cs:112-152`). Time is converted to UTC before insert.

### Async / batching behavior of `DatabaseTarget`
- Wrapped in `SlowRunningAsyncTargetWrapper` with `TimeToSleepBetweenBatches = 500` (ms) at `DatabaseTarget.cs:30`. The default `AsyncTargetWrapper.BatchSize` from NLog (200) and `QueueLimit` (10000) apply unchanged.
- The custom wrapper at `SlowRunningAsyncTargetWrapper.cs:9-44` differs from NLog's stock `AsyncTargetWrapper` by stopping the polling timer when the queue is empty (state `_state` cycles `0→2→1→0`). Saves a wakeup every 500ms when no logs are flowing — relevant on idle ARM boxes.
- Each insert opens a fresh `SQLiteConnection`/`NpgsqlConnection`, executes one row, closes. No batching of multiple rows into a single statement (INSTR-Q1).
- On `SQLiteException` the row is logged via `InternalLogger.Error` and re-thrown (`DatabaseTarget.cs:105-108`). NLog's async wrapper will then back off / retry per its `OverflowAction`.
- Cleanse pass at `DatabaseTarget.cs:88,92`: every message and exception text passes through `CleanseLogMessage.Cleanse(...)` (defined in `NzbDrone.Common.Instrumentation`) before being persisted — this is how API keys are scrubbed from log lines.
- `Logger` field is rewritten at `DatabaseTarget.cs:70-73` — every logger name starting with `NzbDrone.` has that prefix stripped, so DB rows say `Core.Tv.SeriesService` instead of `NzbDrone.Core.Tv.SeriesService`.

### Registration lifecycle
- `DatabaseTarget` is constructed by DI (`IConnectionStringFactory`).
- It is *not* auto-registered by NLog config. `NzbDrone.Host/Startup.cs:317-321` calls `dbTarget.Register()` — but only when `LogDbEnabled` is true.
- `Register()` (`DatabaseTarget.cs:28-38`) wraps `this` in a `SlowRunningAsyncTargetWrapper`, adds it as the NLog target named `"DbLogger"` with rule `"*", LogLevel.Info`, and subscribes to `LogManager.ConfigurationChanged` to re-register whenever NLog config is reloaded.
- `Handle(ApplicationShutdownRequested)` calls `UnRegister()` so the target stops accepting new events before app shutdown.

### Trim / retention
- `LogRepository.Trim()` (`Instrumentation/LogRepository.cs:19-24`) deletes rows older than **7 days** (`DateTime.UtcNow.AddDays(-7).Date`) and `Vacuum()`s the connection.
- Trim is invoked by `Housekeeping/Housekeepers/TrimLogDatabase.cs:7` which is one of many `IHousekeepingTask` impls run by the daily `HousekeepingCommand`. The trim is gated on `_configFileProvider.LogDbEnabled` (`TrimLogDatabase.cs:19-22`).
- `ClearLogCommand` invokes `LogRepository.Purge(vacuum: true)` (deletes *all* rows) — surfaced via the UI "Clear Logs" button.
- `DeleteLogFilesCommand` / `DeleteUpdateLogFilesCommand` empty the **filesystem** log folders (`AppFolderInfo.GetLogFolder()` and `GetUpdateLogFolder()`), nothing to do with the DB.

### `ReconfigureLogging` (config-driven NLog reconfiguration)
On `ConfigFileSavedEvent` (raised when `config.xml` is rewritten), `ReconfigureLogging.Reconfigure()` (`ReconfigureLogging.cs:28-71`) does, in order:
1. Resolve `LogLevel` from config (`debug` default).
2. Resolve `ConsoleLogLevel` — defaults to `Info` if main level is `<= Info`, else mirrors main level.
3. Reconfigure syslog target (one-shot; if `SyslogServer` set, rebuilds the entire syslog rule). **Note:** there's no symmetric "remove syslog rule when `SyslogServer` is cleared" branch — see INSTR-Q3.
4. Adjust `consoleLogger` rule's enabled levels.
5. Adjust `appFileInfo`/`appFileDebug`/`appFileTrace` levels — only enable the matching file rule; others go to `LogLevel.Off`.
6. Iterate every `CleansingFileTarget` and update `MaxArchiveFiles = LogRotate` and `ArchiveAboveSize = LogSizeLimit.Megabytes()`.
7. Toggle `SqlBuilderExtensions.LogSql` — switches Dapper SQL logging on/off process-wide.
8. `ReconfigureSentry()` — sets `SentryTarget.SentryEnabled = (IsProduction && AnalyticsEnabled) || IsDevelopment` and `FilterEvents = FilterSentryEvents`.
9. `LogManager.ReconfigExistingLoggers()`.

### `ReconfigureSentry` (separate class, separate trigger)
Different responsibility from the function inside `ReconfigureLogging`: this is `IHandleAsync<ApplicationStartedEvent>`. On startup it calls `SentryTarget.UpdateScope(_database.Version, _database.Migration, _configFileProvider.Branch, _platformInfo)` (`ReconfigureSentry.cs:33`) so every Sentry event has the DB schema version and migration number attached.

### Config keys
- `LogDbEnabled` (default `true`) — `Configuration/ConfigFileProvider.cs:268`
- `LogSql` (default `false`) — `:269`
- `LogRotate` (default `50`) — `:270`
- `LogSizeLimit` (default `1`, clamped to `[0, 10]` MB) — `:271`
- `LogLevel` (default `"debug"`) — `:250`
- `ConsoleLogLevel` (default `""` → derived from `LogLevel`) — `:251`
- `ConsoleLogFormat` (`Standard | Clef`) — `:253`
- `FilterSentryEvents` (default `true`) — `:272`
- `SyslogServer`, `SyslogPort`, `SyslogLevel` (no defaults shown here — read at `ReconfigureLogging.cs:46-49`)
- `AnalyticsEnabled` (default `true`) — `:246`

### DB tables touched
- `Logs` (insert by `DatabaseTarget`, paged read by `LogRepository`, delete by `Trim`/`Purge`).

### Events raised / consumed
- `ReconfigureLogging`: consumes `ConfigFileSavedEvent`. Produces nothing (mutates NLog config).
- `ReconfigureSentry`: consumes `ApplicationStartedEvent`. Produces nothing.
- `DatabaseTarget`: consumes `ApplicationShutdownRequested` (to unregister target before exit).

### Jobs / `IExecute<TCommand>`
- `LogService.Execute(ClearLogCommand)` → `_logRepository.Purge(vacuum: true)`.
- `DeleteLogFilesService.Execute(DeleteLogFilesCommand)` → empty `GetLogFolder()`.
- `DeleteLogFilesService.Execute(DeleteUpdateLogFilesCommand)` → empty `GetUpdateLogFolder()`.

### Quirks / TODOs / HACKs
- `DatabaseTarget` has *separate* SQLite and Postgres write paths with copy-pasted parameter wiring (`DatabaseTarget.cs:112-152`). Easy to port directly; awkward to maintain.
- `Logger` prefix-stripping (`DatabaseTarget.cs:70-73`) — every `NzbDrone.X.Y` becomes `X.Y` in the DB. The Go port has no equivalent prefix; decide whether to (a) write the full Go package path, (b) strip a chosen prefix, (c) match the C# scheme by stripping the first segment.
- `SlowRunningAsyncTargetWrapper`'s `_state` integer is a CAS-style state machine; the comments at `SlowRunningAsyncTargetWrapper.cs:11` are the only documentation of the invariant.
- `ReconfigureLogging.Reconfigure` has no "tear down syslog when server cleared" path — pre-existing log-rule from a previous save will linger.
- `LogService` mixes a query API (`Paged`) and a write command (`Execute(ClearLogCommand)`); the v3/v5 controllers only call `Paged`.
- `Logs.Time` is stored UTC for Postgres but raw `log.Time` (which is `LogEventInfo.TimeStamp`, local time per NLog default) for SQLite. Inconsistent (INSTR-Q4).

### Test files
- `src/NzbDrone.Core.Test/InstrumentationTests/DatabaseTargetFixture.cs`
- `src/NzbDrone.Core.Test/InstrumentationTests/SlowRunningAsyncTargetWrapperFixture.cs`

### Open questions
- **INSTR-Q1**: Each log row is a fresh DB connection + 1-row INSERT. Is the throughput acceptable, or should the Go port batch within a single transaction per flush window? (NLog's wrapper batches at the queue level but each event still does its own INSERT.)
- **INSTR-Q2**: 7-day retention is hard-coded at `LogRepository.cs:21`. Should this become a config knob in the Go port?
- **INSTR-Q3**: When `SyslogServer` config is cleared, the existing logging rule for syslog is *not* removed by `ReconfigureLogging`. Bug or intentional?
- **INSTR-Q4**: SQLite path stores local time; Postgres path stores UTC. Port should standardise to UTC everywhere.
- **INSTR-Q5**: The Go port will not use NLog. Pick a logging library that supports (a) async writer, (b) per-logger min level, (c) a custom DB sink. Candidates: `slog` + custom handler, `zerolog` + writer, `zap` + core. Flag for spike.
- **INSTR-Q6**: Sentry — same as `ANALYTICS-Q*` below. Do we phone home at all in v5-Go?

---

## 3. Analytics (Core/Analytics)

### Purpose
Tiny shim: a one-property service (`IsEnabled`, `InstallIsActive`) that gates two behaviours:
1. Whether to set `?active=true|false` on update-package and update-changes requests to `services.sonarr.tv` (so Sonarr's update server knows which versions still have active installs and can deprecate accordingly).
2. Whether the Sentry NLog target is allowed to send events (`ReconfigureLogging.cs:110`).

The frontend also reads `IsEnabled` from `/initialize.json` (`Sonarr.Http/Frontend/InitializeJsonController.cs:55`) so the React app can decide whether to load its own analytics scripts.

There is **no direct phone-home payload** from `AnalyticsService` itself; it is a flag, not a sender. The phone-home traffic is:
- Sentry (see `NzbDrone.Common/Instrumentation/Sentry/SentryTarget.cs` — covered in `common` card). Sends crash dumps to `sentry.sonarr.tv`.
- Update server (`Update/UpdatePackageProvider.cs:35-90`). Sends `version, os, arch, runtime, runtimeVer, dbType, includeMajorVersion, branch` always, and additionally `active=true|false` when `IsEnabled` is true.

### Public types
| Type | File | Role |
| --- | --- | --- |
| `IAnalyticsService / AnalyticsService` | `Analytics/AnalyticsService.cs:10,16` | Two readonly properties. Constructed with `IHistoryService` and `IConfigFileProvider`. |

### Logic
- `IsEnabled` (`AnalyticsService.cs:27`): `(AnalyticsEnabled && IsProduction) || IsDevelopment`. Dev builds always count as enabled; production respects the user's setting. Default config value is **`true`** (`ConfigFileProvider.cs:246`).
- `InstallIsActive` (`AnalyticsService.cs:30-37`): queries the most recent `EpisodeHistory` row, returns true if its `Date > UtcNow - 1 month`. So "active" means "downloaded *something* in the last 30 days".

### External services
- `https://services.sonarr.tv/v1/update/{branch}` — update package metadata (sent on every update check). With `?active=…` if `IsEnabled`. Defined `Common/Cloud/SonarrCloudRequestBuilder.cs:15`.
- `https://services.sonarr.tv/v1/update/{branch}/changes` — update changelog. Same pattern.
- `https://services.sonarr.tv/v1/dailyseries` — see DataAugmentation/DailySeries.
- `https://services.sonarr.tv/v1/scenemapping` — see DataAugmentation/Scene.
- `https://sentry.sonarr.tv/...` — four DSNs hard-coded at `NzbDrone.Common/Instrumentation/NzbDroneLogger.cs:75-83` (production+update / production+main / dev+update / dev+main).

### Config keys
- `AnalyticsEnabled` (default `true`) — `ConfigFileProvider.cs:246`. Persisted as a non-table config value (read on every access — `persist: false`).
- Surfaced via v3 `HostConfig` (`Sonarr.Api.V3/Config/HostConfigResource.cs:19,66`) and v5 `GeneralSettings` (`Sonarr.Api.V5/Settings/GeneralSettingsResource.cs:19,64`).

### Quirks
- `_logOptions.AnalyticsEnabled` is sourced from environment / command line first (`NzbDrone.Common/Options/LogOptions.cs:12`) and only falls back to the config file. So `SONARR__LOG__ANALYTICSENABLED=false` wins over the UI setting — useful for self-hosters who want to hard-disable.
- The default is **true**, i.e. analytics is opt-out, not opt-in.

### Quirks / TODOs / HACKs
- `InstallIsActive` reads only the most recent history row to make its decision (`AnalyticsService.cs:33-37`). If the most-recent history row's `Date` is somehow `> UtcNow - 30d` because of clock skew or timezone bug, the install registers as active even if nothing meaningful happened. Edge case but real.

### Test files
- `src/NzbDrone.Core.Test/Analytics/AnalyticsServiceFixture.cs` (if present — verify during port).

### Open questions (stop-the-world for the Go port)
- **ANALYTICS-Q1**: Do we phone home at all in the Go port? The `?active=…` query parameter on update checks effectively tells `services.sonarr.tv` "this install is alive and using version X" — useful for the project's deprecation decisions but does correlate one-bit-of-uptime with the user's IP. Options: (a) keep as-is (default true, opt-out via UI), (b) flip default to false (opt-in), (c) drop the `?active=` param entirely and only send the bare update query, (d) drop the entire update-from-services.sonarr.tv path and require manual updates.
- **ANALYTICS-Q2**: Sentry — same question. Default DSN points at sentry.sonarr.tv. The Go port will need its own DSN if Sentry is kept; or a different crash reporter; or none at all.
- **ANALYTICS-Q3**: `services.sonarr.tv` is also the source for daily-series and scene-mapping data. Even if we drop telemetry, those endpoints stay (they're functional dependencies). Document the split clearly.
- **ANALYTICS-Q4**: Frontend reads `analytics: true|false` from `/initialize.json` and uses it to gate its own loaded scripts (e.g. potentially Sentry browser SDK). Confirm whether the React bundle does anything different when the flag is false.
- **ANALYTICS-Q5**: `InstallIsActive` requires `IHistoryService`. The Go port should inject the equivalent of `HistoryService` not `AnalyticsService` if we redesign — keep service surface tiny.

---

## 4. DataAugmentation (Core/DataAugmentation)

Three independent sub-areas. Each has its own external HTTP source, its own cache, its own consumer.

### 4.1 DailySeries (`DataAugmentation/DailySeries/`)

#### Purpose
Returns a list of TVDB IDs that are "daily series" — used by `Series` import/parsing to default `SeriesType = Daily` for shows like late-night talk shows. Just an ID-list cache.

#### Types
| Type | File | Role |
| --- | --- | --- |
| `DailySeries` (DTO) | `DailySeries/DailySeries.cs:3` | `{ TvdbId int }`. |
| `IDailySeriesDataProxy / DailySeriesDataProxy` | `DailySeries/DailySeriesDataProxy.cs:10,15` | HTTP fetch of `/dailyseries` from `services.sonarr.tv`. Returns `IEnumerable<int>`. On exception logs warning and returns empty. |
| `IDailySeriesService / DailySeriesService` | `DailySeries/DailySeriesService.cs:8,13` | One method `IsDailySeries(int tvdbid)`. Backed by an in-memory `ICached<List<int>>` (TTL 1 hour). |

#### External service
- `GET https://services.sonarr.tv/v1/dailyseries` → `[{ tvdbId: int }, ...]`. Cached 1 hour in-memory (`DailySeriesService.cs:26`).

#### Quirks
- No DB persistence — list disappears on restart and is refetched on first call.
- Failure mode is silent: an HTTP error returns an empty list, which means `IsDailySeries` would say "no" for every show until the next successful refetch.

#### Open questions
- **DA-Q1**: 1-hour cache TTL is hard-coded at `DailySeriesService.cs:26`. Is this aggressive enough to detect when a show is reclassified daily? Not user-tuneable.
- **DA-Q2**: Silent failure means a Sonarr that's offline at startup will misclassify daily shows as standard. The Go port should at least surface a health check ("daily-series feed unreachable").

### 4.2 Scene (`DataAugmentation/Scene/`)

#### Purpose
Scene-name aliases for series. Maps "scene" titles (e.g. `marvels.agents.of.shield`) to a TVDB ID + optional season-number remapping. Used during release parsing (`Parser.ParsingService` resolves a release's scene title back to a tvdbId via this service).

The repository is the cache-of-record: rows live in the **main** DB table `SceneMappings` and are refreshed in-place by `SceneMappingService.UpdateMappings()`. Two `ISceneMappingProvider`s feed it: `ServicesProvider` (= the JSON dump from `services.sonarr.tv`) and `XemService` (= XEM "alternate names" feed; see 4.3 below).

#### Types
| Type | File | Role |
| --- | --- | --- |
| `SceneMapping` (model) | `Scene/SceneMapping.cs:5` | DB row: `MappingId, Title, ParseTerm, SearchTerm, TvdbId, SeasonNumber?, SceneSeasonNumber?, SceneOrigin, SearchMode?, Comment, FilterRegex, Type`. |
| `ISceneMappingProvider` | `Scene/ISceneMappingProvider.cs:4` | One method `GetSceneMappings()`. Two impls (`ServicesProvider`, `XemService`). |
| `ServicesProvider : ISceneMappingProvider` | `Scene/ServicesProvider.cs:5` | Wraps `ISceneMappingProxy`. |
| `ISceneMappingProxy / SceneMappingProxy` | `Scene/SceneMappingProxy.cs:7,12` | HTTP fetch of `services.sonarr.tv/v1/scenemapping` → `List<SceneMapping>`. |
| `ISceneMappingRepository / SceneMappingRepository` | `Scene/SceneMappingRepository.cs:7,13` | `IBasicRepository<SceneMapping>` plus `FindByTvdbid(int)` and `GetAllByType(string)`. |
| `ISceneMappingService / SceneMappingService` | `Scene/SceneMappingService.cs:15,24` | The big one — handles incoming events, manages two in-memory dictionaries keyed by tvdbId / parsed-title, computes mappings for searches and parsers. |
| `SceneMappingsUpdatedEvent` | `Scene/SceneMappingsUpdatedEvent.cs:5` | Empty marker event. |
| `InvalidSceneMappingException : NzbDroneException` | `Scene/InvalidSceneMappingException.cs:7` | Thrown when two distinct tvdbIds match the same series-title + release-title pair. |
| `UpdateSceneMappingCommand : Command` | `Scene/UpdateSceneMappingCommand.cs:5` | Scheduled task body — see Jobs section. |
| `SearchMode` (enum) | `Scene/SearchMode.cs:6` | `Default=0, SearchID=1, SearchTitle=2`. `[Flags]`. |

#### External services
- `GET https://services.sonarr.tv/v1/scenemapping` (via `SceneMappingProxy.Fetch`).
- `https://thexem.info/map/...` — see XemService at 4.3.

#### Refresh / persistence
- The `UpdateSceneMappingCommand` is scheduled every **180 minutes** (3 hours) — `Jobs/TaskManager.cs` (covered in `system` card) registers it with that interval.
- `SceneMappingService.UpdateMappings()` (`SceneMappingService.cs:132-217`) iterates every registered `ISceneMappingProvider`, fetches a list, validates it (`Title` and `SearchTerm` non-empty; `MappingId` non-empty), computes `ParseTerm = Title.CleanSeriesTitle()`, then **diffs** the incoming list against existing rows of the same `Type` keyed by `MappingId` and applies a 3-step `Insert/Update/Delete`. Per-provider, all-or-nothing.
- Empty-response guard at `:204-207`: if a provider returns empty, the existing rows for that provider are **not** deleted (defensive against transient HTTP failures).
- After every `UpdateMappings`, raises `SceneMappingsUpdatedEvent`.
- Event-driven re-fetch:
  - `Handle(SeriesRefreshStartingEvent)` — refresh on manual trigger if cache > 1 minute old or first-time after startup (`:312-317`).
  - `Handle(SeriesAddedEvent) / Handle(SeriesImportedEvent)` — refresh on first add after startup (`:319-333`).

#### In-memory caches
- `_getTvdbIdCache`: `Dictionary<parseTerm, List<SceneMapping>>` (keyed by cleaned series title). Used by `FindMappings(seriesTitle, releaseTitle)`.
- `_findByTvdbIdCache`: `Dictionary<tvdbId.ToString(), List<SceneMapping>>`.
- Both are `ICachedDictionary<List<SceneMapping>>` from `NzbDrone.Common.Cache` — these are not bounded, just per-process state.
- Refilled from the DB by `RefreshCache()` (`:256-262`).

#### Search-mapping algorithm
The really intricate part is `FindMappings(seriesTitle, releaseTitle)` (`SceneMappingService.cs:219-254`) and `FilterSceneMappings` overloads (`:264-309`). High level:
1. Look up candidates by cleaned series title.
2. Apply `FilterRegex` to the release title (e.g. only match if the release looks like a specific scene group's output).
3. If multiple match, prefer exact title; otherwise pick the one with smallest `LevenshteinDistance` to the input.
4. For season-aware filtering, pick the highest `SceneSeasonNumber <= sceneSeasonNumber` per Title.

#### DB tables
- `SceneMappings` (`Type` column distinguishes `ServicesProvider` rows from `XemService` rows).

#### Events raised / consumed
- Consumes: `SeriesRefreshStartingEvent`, `SeriesAddedEvent`, `SeriesImportedEvent`, `UpdateSceneMappingCommand`.
- Raises: `SceneMappingsUpdatedEvent`.

#### Quirks / TODOs / HACKs
- `MappingId` is the natural key; rows without it are skipped on insert (`:179-182`). The C# code defends against malformed feed entries but doesn't reject the entire fetch.
- `Fate/Zero` hack at `XemProxy.cs:97-100` (TVDB id `79151`): silently drop season > 1 mappings. Specific show, no comment why.
- Two providers + one repository means a single `Type` switch lets you wipe & rebuild only that provider's rows — convenient for testing one feed.

#### Test files
- `src/NzbDrone.Core.Test/DataAugmentation/Scene/SceneMappingServiceFixture.cs`
- `src/NzbDrone.Core.Test/DataAugmentation/Scene/SceneMappingProxyFixture.cs`

#### Open questions
- **DA-Q3**: 3-hour scheduled refresh + opportunistic refresh on series-add is the policy. Should the Go port allow user override?
- **DA-Q4**: `Fate/Zero` hardcoded TVDB-ID hack — is this still needed in 2026? Verify by hitting the XEM endpoint and checking if those mappings are still present.
- **DA-Q5**: `InvalidSceneMappingException`'s message says "Please notify Sonarr developers" — the Go port should change that wording (or at minimum surface the conflict to a logger that the user actually reads, not a phone-home).

### 4.3 Xem (`DataAugmentation/Xem/`)

#### Purpose
Pulls **scene-numbering** data from TheXEM (`thexem.info`) for shows that air with a different episode-numbering scheme on torrents than on TVDB (most commonly anime where torrents use absolute numbering). Updates `Episodes.SceneSeasonNumber/SceneEpisodeNumber/SceneAbsoluteEpisodeNumber/UnverifiedSceneNumbering` in the main DB, and toggles `Series.UseSceneNumbering`. Also feeds alternate-name SceneMappings into the SceneMapping pipeline (4.2 above).

#### Types
| Type | File | Role |
| --- | --- | --- |
| `IXemProxy / XemProxy` | `Xem/XemProxy.cs:12,19` | Three HTTP calls. Hard-codes `https://thexem.info/map/` and `?origin=tvdb`. |
| `XemService : ISceneMappingProvider, IHandle<SeriesUpdatedEvent>, IHandle<SeriesRefreshStartingEvent>` | `Xem/XemService.cs:13` | Owns the per-series scene-numbering update; also implements `ISceneMappingProvider` so its alt-name feed flows into `SceneMappingService`. |
| `XemResult<T>` | `Xem/Model/XemResult.cs:3` | `{ Result string, Data T, Message string }`. XEM's outer envelope. |
| `XemSceneTvdbMapping` | `Xem/Model/XemSceneTvdbMapping.cs:3` | `{ Scene XemValues, Tvdb XemValues }`. |
| `XemValues` | `Xem/Model/XemValues.cs:3` | `{ Season, Episode, Absolute }`. |

#### External service / endpoints
- `GET https://thexem.info/map/havemap?origin=tvdb` → list of TVDB ids that have any mapping. Cached as `_cache: ICachedDictionary<bool>` keyed by `tvdbId.ToString()`. TTL **3 hours** (`XemService.cs:237`); on manual refresh, also re-fetched if older than **1 minute** (`:259`).
- `GET https://thexem.info/map/all?origin=tvdb&id={tvdbId}` → list of `{Scene, Tvdb}` mapping pairs.
- `GET https://thexem.info/map/allNames?origin=tvdb&seasonNumbers=true` → `Dictionary<tvdbId, List<{name → season}>>`. Wrapped into `SceneMapping` rows with `MappingId = "x-{tvdbId}_S{season}_{name}"`, fed back through `SceneMappingService` as a provider.

#### Mapping algorithm
`PerformUpdate(Series)` (`XemService.cs:34-98`):
1. Fetch all mappings for the series.
2. Reset every existing episode's scene fields.
3. For each mapping, find the corresponding episode by `(SeasonNumber, EpisodeNumber)` and set `SceneSeasonNumber`/`SceneEpisodeNumber`/`SceneAbsoluteEpisodeNumber`.
4. If any mapping has a non-zero scene-season, call `ExtrapolateMappings` to mark *unmapped* episodes as `UnverifiedSceneNumbering` based on heuristics (mapped seasons, future episodes in last-mapped season, etc.) — see `XemService.cs:100-204`.
5. Persist via `_episodeService.UpdateEpisodes` and `_seriesService.UpdateSeries`.

`Handle(SeriesUpdatedEvent)` (`:235-255`): if cache > 3h old, refetch the `havemap` list. If the series isn't in the havemap *and* doesn't already use scene numbering, skip (cheap exit). Otherwise call `PerformUpdate`.

#### DB tables touched
- `Episodes` (write `SceneAbsoluteEpisodeNumber`, `SceneSeasonNumber`, `SceneEpisodeNumber`, `UnverifiedSceneNumbering`).
- `Series` (write `UseSceneNumbering`).

#### Events consumed
- `SeriesUpdatedEvent`, `SeriesRefreshStartingEvent`.

#### Error tolerance
- `IgnoredErrors` = `{ "no single connection", "no show with the tvdb_id" }` (`XemProxy.cs:27`). XEM returns these as `failure` results but they're benign; only other failure messages throw.

#### Quirks / TODOs / HACKs
- The "Fate/Zero" hack lives at `XemProxy.cs:97-100`, mentioned above.
- `ExtrapolateMappings` has a `// TODO: SceneAbsoluteEpisodeNumber.` left at `XemService.cs:201` — extrapolated absolute numbers are not computed when crossing a TVDB-only later-season boundary.
- `_cache.ExtendTTL()` is called on failure (`:217-225`) — keeps using the stale list.

#### Open questions
- **DA-Q6**: TheXEM is a single point of failure for anime scene-numbering. If it's down for a week, episodes get reset to "no scene mapping" on each `SeriesUpdatedEvent` — *unless* `_cache.IsExpired(3h)` saves us. Verify the cache-extend behavior actually shields from outage.
- **DA-Q7**: `ExtrapolateMappings` is 100 lines of control flow with very implicit invariants. It's a good candidate for a pure function with table-driven tests in the Go port.
- **DA-Q8**: The XEM "alternate names" feed creates `SceneMapping` rows (4.2). Is the dedup (`series.Key == 79151`) hack still needed? Cross-check with current XEM data.

---

## 5. CustomFilters (Core/CustomFilters)

### Purpose
Saved frontend filter views ("My Anime", "Continuing series with cutoff unmet", etc.) — opaque-from-backend JSON describing UI filter expressions. Backend exists only as a CRUD pipe.

### Public types
| Type | File | Role |
| --- | --- | --- |
| `CustomFilter : ModelBase` | `CustomFilters/CustomFilter.cs:5` | DB row: `Type`, `Label`, `Filters` (string — *raw JSON text*). |
| `ICustomFilterRepository / CustomFilterRepository` | `CustomFilters/CustomFilterRepository.cs:6,10` | `IBasicRepository<CustomFilter>`. |
| `ICustomFilterService / CustomFilterService` | `CustomFilters/CustomFilterService.cs:6,15` | CRUD only: `Add, Update, Delete, Get, All`. |

### JSON shape
- The `Filters` column is stored as a JSON string. The API resource (`Sonarr.Api.V5/CustomFilters/CustomFilterResource.cs:12`) deserialises it as `List<ExpandoObject>`. The actual contents are dictated entirely by the React client.
- This is **not** an `IEmbeddedDocument`-backed shape — it's a hand-rolled `STJson.Deserialize/ToJson` round-trip at `CustomFilterResource.cs:24,35`. The schema is open-ended on purpose so the frontend can evolve filter semantics without touching the server.

### API surface
- `Sonarr.Api.V5/CustomFilters/CustomFilterController.cs:11`:
  - `GET    /api/v5/customfilter` → `List<CustomFilterResource>`
  - `GET    /api/v5/customfilter/{id}` → `CustomFilterResource`
  - `POST   /api/v5/customfilter` → `Created<CustomFilterResource>`
  - `PUT    /api/v5/customfilter/{id}` → `Accepted<CustomFilterResource>`
  - `DELETE /api/v5/customfilter/{id}` → `NoContent`
- v3 mirror at `Sonarr.Api.V3/CustomFilters/CustomFilterController.cs`.

### DB tables
- `CustomFilters` (id, type, label, filters).

### Events / jobs
- None. Pure CRUD.

### Quirks / TODOs / HACKs
- The `Filters` column is stored as a string and re-serialised through `ExpandoObject` on the way out. There is no validation. A malformed filter persisted by an old client will simply round-trip bad data — the frontend has to defensively parse.
- No deduplication on `(Type, Label)` — multiple custom filters with the same label are allowed.

### Test files
- `src/NzbDrone.Core.Test/CustomFilters/...` (verify on port).

### Open questions
- **CFLT-Q1**: Should the Go port keep the schema as a free-form JSON string, or switch to `jsonb` (Postgres) / `json` (SQLite) so filter contents are queryable? Backwards compat says keep as text.
- **CFLT-Q2**: Type column says it's *the resource type the filter applies to* (`series`, `wantedMissing`, etc.). Document the enumeration values used by the frontend.

---

## 6. SeriesStats (Core/SeriesStats)

### Purpose
The aggregate query that powers every series/season card in the UI: episode counts, file counts, "next airing", "previous airing", size-on-disk, distinct release groups, distinct release types, and distinct quality IDs per season — and aggregated up per series. Powers the Series list page and the Series detail page.

### Public types
| Type | File | Role |
| --- | --- | --- |
| `SeasonStatistics : ResultSet` | `SeriesStats/SeasonStatistics.cs:12` | Per-season row. Has `*String` raw fields (e.g. `ReleaseGroupsString`) plus computed-property mirrors that parse them into typed lists. |
| `SeriesStatistics : ResultSet` | `SeriesStats/SeriesStatistics.cs:9` | Per-series rollup: same fields plus `List<SeasonStatistics> SeasonStatistics`. |
| `ISeriesStatisticsRepository / SeriesStatisticsRepository` | `SeriesStats/SeriesStatisticsRepository.cs:11,17` | Raw Dapper SQL. Two methods (all-series, single-series). |
| `ISeriesStatisticsService / SeriesStatisticsService` | `SeriesStats/SeriesStatisticsService.cs:9,15` | Aggregates season rows into series rows; sorts qualities by quality-profile index. |

### Query structure
Two SQL queries, joined in C# (`SeriesStatisticsRepository.cs:44-57`):

**A — episodes** (always identical SQLite/Postgres modulo true/false literal):
```
SELECT
  "Episodes"."SeriesId" AS SeriesId,
  "Episodes"."SeasonNumber",
  COUNT(*) AS TotalEpisodeCount,
  SUM(CASE WHEN "AirDateUtc" <= @currentDate OR "EpisodeFileId" > 0 THEN 1 ELSE 0 END) AS AvailableEpisodeCount,
  SUM(CASE WHEN ("Monitored" = TRUE AND "AirDateUtc" <= @currentDate) OR "EpisodeFileId" > 0 THEN 1 ELSE 0 END) AS EpisodeCount,
  SUM(CASE WHEN "EpisodeFileId" > 0 THEN 1 ELSE 0 END) AS EpisodeFileCount,
  SUM(CASE WHEN "Monitored" = TRUE THEN 1 ELSE 0 END) AS MonitoredEpisodeCount,
  MIN(CASE WHEN "AirDateUtc" < @currentDate OR "Monitored" = FALSE THEN NULL ELSE "AirDateUtc" END) AS NextAiringString,
  MAX(CASE WHEN "AirDateUtc" >= @currentDate OR "Monitored" = FALSE THEN NULL ELSE "AirDateUtc" END) AS PreviousAiringString,
  MAX("AirDate") AS LastAiredString
FROM "Episodes"
GROUP BY "SeriesId", "SeasonNumber"
```

**B — episode files** (SQLite vs Postgres branched at `SeriesStatisticsRepository.cs:95-117`):
- SQLite (no DISTINCT in `GROUP_CONCAT` so dups appear in the pipe-separated string):
  ```
  SELECT "SeriesId", "SeasonNumber",
    SUM(COALESCE("Size", 0)) AS SizeOnDisk,
    GROUP_CONCAT("ReleaseGroup", '|') AS ReleaseGroupsString,
    GROUP_CONCAT("ReleaseType", '|') AS ReleaseTypesString,
    GROUP_CONCAT(JSON_EXTRACT("Quality", '$.quality'), '|') AS EpisodeFileQualitiesString
  FROM "EpisodeFiles"
  GROUP BY "SeriesId", "SeasonNumber"
  ```
- Postgres (uses `string_agg(DISTINCT …)` after the recent fix — see git log "Produce distinct values in aggregated series stats from Postgres"):
  ```
  SELECT "SeriesId", "SeasonNumber",
    SUM(COALESCE("Size", 0)) AS SizeOnDisk,
    string_agg(DISTINCT "ReleaseGroup", '|') AS ReleaseGroupsString,
    string_agg(DISTINCT "ReleaseType"::text, '|') AS ReleaseTypesString,
    string_agg(DISTINCT "Quality"::json->>'quality', '|') AS EpisodeFileQualitiesString
  FROM "EpisodeFiles"
  GROUP BY "SeriesId", "SeasonNumber"
  ```

### Dedup-on-read in `SeasonStatistics`
The `*String` columns are pipe-joined raw text. `SeasonStatistics.ReleaseGroups` (`SeasonStatistics.cs:98-116`) splits, distincts, sorts. Same for `ReleaseTypes` (`:118-135`) and `EpisodeFileQualities` (`:137-153`). So the SQLite path produces non-distinct strings on the wire and de-dups in C#; the Postgres path produces distinct strings *and* still de-dups again in C# — defensive double-dedup but cheap.

### `SeriesStatisticsService` aggregation
- For "all-series" (`SeriesStatisticsService.cs:30-45`): query repo, then `GroupBy(SeriesId)`, then for each series look up the quality profile via `_seriesService.GetAllSeriesQualityProfiles()`, then `MapSeriesStatistics(seasons, profile)`.
- `MapSeriesStatistics` (`:61-86`): sums counts, takes min/max of airing dates across seasons, distincts release-groups across seasons, sorts qualities by `profile.GetIndex(q.Id).Index`.

### Date parsing (`SeasonStatistics.NextAiring` etc.)
`NextAiringString` is a raw text column that comes back from Dapper as the SQLite TEXT/Postgres timestamptz string. The property does `DateTime.TryParse(..., DateTimeStyles.AssumeUniversal, ...)` and explicitly catches `ArgumentOutOfRangeException` because of mono bug GHI 3518 (`SeasonStatistics.cs:43-46`). The `try`/`catch` is repeated for `PreviousAiring` and `LastAired`.

### DB tables
- `Episodes` (read).
- `EpisodeFiles` (read).
- (`Series` is read indirectly through `_seriesService.GetAllSeriesQualityProfiles()` for the profile lookup.)

### Events / jobs
- None. Called on demand by `SeriesController` and similar (V3 + V5).

### Quirks / TODOs / HACKs
- The `*String` round-trip exists because Dapper has no native typed-list reader. Pipe-separated strings parsed in C# is the workaround.
- Empty-results: `SeriesStatistics(int seriesId, int qualityProfileId)` returns `new SeriesStatistics()` with all fields default (no nulls) when no rows match (`SeriesStatisticsService.cs:50-54`). The Go port should mirror this — clients depend on the field shape.
- `LastAired` only considers `SeasonNumber > 0` (excludes specials) at `SeriesStatisticsService.cs:79`. `NextAiring` and `PreviousAiring` do not — bug or by-design difference between specials and non-specials?
- The git log entry "Produce distinct values in aggregated series stats from Postgres" (commit `1c3497784` per the on-disk git log) added the `DISTINCT` to the Postgres path of (B) above. SQLite lacks `DISTINCT` inside `GROUP_CONCAT` — there's a workaround pattern (`group_concat(DISTINCT x)` does work in SQLite but not for the `JSON_EXTRACT` version) — verify whether the SQLite branch should also use it.

### Test files
- `src/NzbDrone.Core.Test/SeriesStatsTests/SeriesStatisticsRepositoryFixture.cs`
- `src/NzbDrone.Core.Test/SeriesStatsTests/SeriesStatisticsServiceFixture.cs`

### Open questions
- **SS-Q1**: SQLite's `GROUP_CONCAT` does support `DISTINCT` for plain columns. Should the SQLite branch be updated to match Postgres semantics (server-side dedup) for performance on large series?
- **SS-Q2**: Why does `LastAired` exclude season 0 but `NextAiring`/`PreviousAiring` do not? Document or fix.
- **SS-Q3**: The `*String` pipe-encoded column pattern is a Dapper-ism. The Go port can use `pq.StringArray` (Postgres) and `string_split` (SQLite text → splice in Go) — or just keep the pipe-encoding as-is to minimise behavioural drift. Decision point.
- **SS-Q4**: Mono GHI 3518 catch (`SeasonStatistics.cs:42-44`) — the Go port doesn't run on mono, so the try/catch can be dropped, but we should document that the property is best-effort because the underlying string can be malformed if a migration corrupts data.

---

## 7. IndexerSearch (Core/IndexerSearch)

### Purpose
Search-orchestration layer. Five `IExecute<TCommand>` entry points (`EpisodeSearchCommand`, `MissingEpisodeSearchCommand`, `CutoffUnmetEpisodeSearchCommand`, `SeasonSearchCommand`, `SeriesSearchCommand`) all funnel into `ISearchForReleases.{EpisodeSearch, SeasonSearch}` → fans out across enabled indexers (filtered by tag), maps releases through `IMakeDownloadDecision`, dedupes, returns `List<DownloadDecision>` to be processed by `IProcessDownloadDecisions`.

This card describes the *orchestration*. The actual per-indexer fetch lives in `Indexers/HttpIndexerBase.cs` (covered in `indexers.md`); decision-making lives in `DecisionEngine/MakeDownloadDecision.cs` (covered in `decisionengine.md`); `IProcessDownloadDecisions` lives in `Download/DownloadDecisionMaker.cs` (covered in `download.md`).

### Public types
| Type | File | Role |
| --- | --- | --- |
| `EpisodeSearchCommand` | `IndexerSearch/EpisodeSearchCommand.cs:6` | `List<int> EpisodeIds`. |
| `MissingEpisodeSearchCommand` | `IndexerSearch/MissingEpisodeSearchCommand.cs:5` | `int? SeriesId, bool Monitored=true`. |
| `CutoffUnmetEpisodeSearchCommand` | `IndexerSearch/CutoffUnmetEpisodeSearchCommand.cs:5` | Same shape as Missing. |
| `SeasonSearchCommand` | `IndexerSearch/SeasonSearchCommand.cs:5` | `int SeriesId, int SeasonNumber`. |
| `SeriesSearchCommand` | `IndexerSearch/SeriesSearchCommand.cs:5` | `int SeriesId`. |
| `EpisodeSearchService : IExecute<{Episode, Missing, CutoffUnmet}EpisodeSearchCommand>` | `IndexerSearch/EpisodeSearchService.cs:17` | Three-handler service. |
| `SeasonSearchService : IExecute<SeasonSearchCommand>` | `IndexerSearch/SeasonSearchService.cs:8` | Single-handler. |
| `SeriesSearchService : IExecute<SeriesSearchCommand>` | `IndexerSearch/SeriesSearchService.cs:12` | Walks all monitored seasons; falls back to per-episode search if no seasons monitored. |
| `ISearchForReleases / ReleaseSearchService` | `IndexerSearch/ReleaseSearchService.cs:20,28` | The big one — owns the scene-mapping/criteria/dispatch logic. |
| `EpisodeSearchGroup` | `IndexerSearch/EpisodeSearchGroup.cs:6` | Internal: `{ SeriesId, SeasonNumber, Episodes }` for bulk-search batching. |

### Search criteria hierarchy (`Definitions/`)
| Type | Distinguishing fields | Used by |
| --- | --- | --- |
| `SearchCriteriaBase` | `Series, SceneTitles, Episodes, SearchMode, MonitoredEpisodesOnly, UserInvokedSearch, InteractiveSearch`; static helper `GetCleanSceneTitle(string)` (`SearchCriteriaBase.cs:28-42`). | abstract base |
| `SingleEpisodeSearchCriteria` | `EpisodeNumber, SeasonNumber` | non-anime/non-daily single ep |
| `SeasonSearchCriteria` | `SeasonNumber` | non-anime full-season pack |
| `AnimeEpisodeSearchCriteria` | `AbsoluteEpisodeNumber, EpisodeNumber, SeasonNumber, IsSeasonSearch` | anime single ep |
| `AnimeSeasonSearchCriteria` | `SeasonNumber` | anime season |
| `DailyEpisodeSearchCriteria` | `DateTime AirDate` | daily single ep |
| `DailySeasonSearchCriteria` | `int Year` | daily season-as-year |
| `SpecialEpisodeSearchCriteria` | `string[] EpisodeQueryTitles` | season 0 / specials |
| `SceneEpisodeMapping` | `Episode, SearchMode, SceneTitles, SeasonNumber, EpisodeNumber, AbsoluteEpisodeNumber?` (overrides Equals/GetHashCode by `SeasonNumber/EpisodeNumber/SearchMode`). | internal |
| `SceneSeasonMapping` | `Episodes, EpisodeMapping, SearchMode, SceneTitles, SeasonNumber` (Equals/GetHashCode by `SeasonNumber/SearchMode`). | internal |

### Call graph
**Commands:**
1. `EpisodeSearchCommand`:
   - `EpisodeSearchService.Execute(EpisodeSearchCommand)` (`EpisodeSearchService.cs:110-119`):
     - For each `episodeId`, `_releaseSearchService.EpisodeSearch(episodeId, manual?, false).GetAwaiter().GetResult()`, then `_processDownloadDecisions.ProcessDecisions(decisions)`.
2. `MissingEpisodeSearchCommand`:
   - `EpisodeSearchService.Execute(MissingEpisodeSearchCommand)` (`:121-161`):
     - If `SeriesId.HasValue`: filter episodes by series.
     - Else: paged scan across `_episodeService.EpisodesWithoutFiles` with monitored filter.
     - Subtract anything currently in queue (`GetQueuedEpisodeIds` at `:197-203`).
     - Hand off to `SearchForBulkEpisodes` (`:43-103`) which groups by season and uses Season-search for multi-episode groups, Episode-search for singletons.
3. `CutoffUnmetEpisodeSearchCommand`:
   - Same shape as Missing but uses `_episodeCutoffService.EpisodesWhereCutoffUnmet` (`:163-195`).
4. `SeasonSearchCommand`:
   - `SeasonSearchService.Execute` (`SeasonSearchService.cs:23-30`):
     - `_releaseSearchService.SeasonSearch(seriesId, seasonNumber, missingOnly: false, monitoredOnly: true, manual?, false)`.
5. `SeriesSearchCommand`:
   - `SeriesSearchService.Execute` (`SeriesSearchService.cs:33-75`):
     - If no seasons monitored: per-episode search across all monitored aired episodes.
     - Else: per-monitored-season `SeasonSearch(missingOnly = !UpgradeAllowed, monitoredOnly = true)`.

**`ReleaseSearchService.EpisodeSearch(Episode)`** (`ReleaseSearchService.cs:59-94`):
- Branches on `series.SeriesType`:
  - `Daily` → `SearchDaily` → builds `DailyEpisodeSearchCriteria{AirDate}`.
  - `Anime` → `SearchAnime` (or `SearchSpecial` if season 0 with no abs/scene number).
  - else → `SearchSingle` if `SeasonNumber > 0`, `SearchSpecial` if `SeasonNumber == 0`.

**`ReleaseSearchService.SeasonSearch`** (`:96-161`):
- Anime → `SearchAnimeSeason`. Daily → `SearchDailySeason` (group by year). Standard → `GetSceneSeasonMappings` then per-mapping pick `SingleEpisodeSearchCriteria` (1 ep) or `SeasonSearchCriteria` (multi).

**`Dispatch`** (`:520-550`):
- Picks indexers via `_indexerFactory.{InteractiveSearchEnabled, AutomaticSearchEnabled}` based on `criteriaBase.InteractiveSearch`.
- Filters out indexers whose `Tags` intersect-empty with `criteriaBase.Series.Tags` (untagged indexers always included).
- Calls `indexer.Fetch(searchSpec)` for each selected indexer in parallel via `Task.WhenAll`.
- After dispatch, every episode in `criteriaBase.Episodes` gets `LastSearchTime = UtcNow` and is persisted via `_episodeService.UpdateLastSearchTime(...)`.
- Calls `_makeDownloadDecision.GetSearchDecision(reports, criteriaBase)` to filter/score the batch.

**`DispatchIndexer`** (`:552-563`): swallows per-indexer exceptions, logs, returns `Array.Empty<ReleaseInfo>()`. Failures of one indexer never abort the whole search.

### Scene-mapping interaction
`GetSceneEpisodeMappings` (`:236-321`) is the central translation between TVDB-numbered episodes and scene-numbered queries. Implements two mapping kinds (release-season-mapped vs episode-season-mapped) and decides per-mapping whether to inject the *series original title* as an "implied global" mapping (gated by `includeGlobal` at `:309-320` to avoid duplicates if an explicit mapping by the same name exists).

### Dedup
`DeDupeDecisions` (`:566-572`): groups by `RemoteEpisode.Release.Guid`, keeps the variant with smallest `Rejections.Count` then highest `IndexerPriority`.

### DB tables touched
- Read: `Series`, `Seasons`, `Episodes`, `EpisodeFiles`.
- Write: `Episodes.LastSearchTime` after every dispatch.

### Events raised / consumed
- None (uses commands and synchronous service calls).

### Jobs / commands
- All five commands are `SendUpdatesToClient => true` so the UI shows progress.
- Triggered by user actions (UI buttons, queue interactions) and by other services (e.g. monitor-toggling).

### Quirks / TODOs / HACKs
- Sync-over-async (`.GetAwaiter().GetResult()` everywhere in the `IExecute` paths) — unavoidable in C# `void Execute(T)` but the Go port can keep these natively async.
- `EpisodeSearchService.SearchForBulkEpisodes` has a 30-day "stale-first" ordering by `Episodes.Min(e.LastSearchTime ?? DateTime.MinValue)` so newly-failed episodes don't starve forever (`:62`).
- The `groupedEpisode` computation at `:170` uses `((SceneSeasonNumber ?? SeasonNumber) * 100000) + SeasonNumber` as a composite key — assumes season numbers don't exceed 100k. Real-world fine, theoretically gross.
- `SearchSpecial.EpisodeQueryTitles` is built from cartesian-product of `CleanSceneTitles × episode.Title` (`ReleaseSearchService.cs:374-379`). For shows with many specials this grows multiplicatively.
- `IsMonitored(bool, bool)` at `EpisodeSearchService.cs:105-108` is dead code — declared private and never called.

### Test files
- `src/NzbDrone.Core.Test/IndexerSearchTests/EpisodeSearchServiceFixture.cs`
- `src/NzbDrone.Core.Test/IndexerSearchTests/ReleaseSearchServiceFixture.cs`
- `src/NzbDrone.Core.Test/IndexerSearchTests/Definitions/SearchCriteriaBaseFixture.cs`

### Open questions
- **IS-Q1**: `SeriesSearchService.Execute` decides "no seasons monitored → per-episode search" vs "any season monitored → per-season search". `_releaseSearchService.SeasonSearch(missingOnly: !profile.UpgradeAllowed, monitoredOnly: true)` is called for the latter. Verify behaviour: when `UpgradeAllowed` is true, `missingOnly=false` means season-search returns *all* releases for that season — desired? (User's intent on series-search is usually "fill missing", not "upgrade everything".)
- **IS-Q2**: `Task.WhenAll` over indexers without bounded concurrency — N indexers fire simultaneously. With rate limiting at the indexer level this is OK, but for N > 20 it can spike connection counts. Worth a bounded semaphore in the Go port? Cross-reference with `indexers.md` rate-limit policy.
- **IS-Q3**: `DispatchIndexer` swallows all exceptions including transient HTTP failures. Should the Go port surface a per-indexer health-check from this path, or keep the silent log-and-continue?
- **IS-Q4**: `EpisodeSearchService.IsMonitored` is dead code (`:105-108`). Remove during port.

---

## 8. MediaCover (Core/MediaCover)

### Purpose
Series posters / banners / fanart / clearlogo / headshots / screenshots. Downloaded from the remote (TheTVDB Skyhook) on `SeriesUpdatedEvent`, cached on disk under `<AppData>/MediaCover/<seriesId>/`, and pre-resized to fixed pixel heights using ImageSharp. Exposed to the frontend at `/MediaCover/<seriesId>/<file>` (static file mapper) and at `/api/v3/MediaCover/<seriesId>/<file>` (controller; deprecated direct route still present).

### Public types
| Type | File | Role |
| --- | --- | --- |
| `MediaCover : IEmbeddedDocument` | `MediaCover/MediaCover.cs:16` | `{ MediaCoverTypes CoverType, string Url (local), string RemoteUrl }`. Stored inside `Series.Images` and `Series.AlternateTitles`-style embedded doc lists. |
| `MediaCoverTypes` enum | `MediaCover/MediaCover.cs:5` | `Unknown=0, Poster=1, Banner=2, Fanart=3, Screenshot=4, Headshot=5, Clearlogo=6`. |
| `IImageResizer / ImageResizer` | `MediaCover/ImageResizer.cs:10,15` | Wraps `SixLabors.ImageSharp`. Single method `Resize(source, destination, height)`. Configures `JpegEncoder.Quality = 92` and `SimpleGcMemoryAllocator`. |
| `ICoverExistsSpecification / CoverAlreadyExistsSpecification` | `MediaCover/CoverAlreadyExistsSpecification.cs:6,11` | HTTP `HEAD` against the remote URL; compares `Content-Length` to local file size. |
| `IMediaCoverProxy / MediaCoverProxy` | `MediaCover/MediaCoverProxy.cs:12,20` | For series **not yet in DB** (e.g. add-series search). Hashes a remote URL into a 24-hour-cached token; serves bytes via `/MediaCoverProxy/<hash>/<filename>`. |
| `MediaCoverService : IHandleAsync<SeriesUpdatedEvent>, IHandleAsync<SeriesDeletedEvent>, IMapCoversToLocal` | `MediaCover/MediaCoverService.cs:24` | Drives the whole pipeline. |
| `MediaCoversUpdatedEvent : IEvent` | `MediaCover/MediaCoversUpdatedEvent.cs:6` | `{ Series, bool Updated }`. Raised after every `EnsureCovers`. |

### Pipeline (per `SeriesUpdatedEvent`)
`MediaCoverService.HandleAsync(SeriesUpdatedEvent)` (`MediaCoverService.cs:233-238`) → `EnsureCovers(series)`:
1. For every `cover` in `series.Images`:
   - Skip if `CoverType == Unknown`.
   - Compute target path `<AppData>/MediaCover/<seriesId>/<lowername>[<jpg|png>]`.
   - `_coverExistsSpecification.AlreadyExists(remoteUrl, fileName)`:
     - Skip if local file exists and `Content-Length` matches.
     - Else `_httpClient.DownloadFile(remoteUrl, fileName)` (`:172-174`).
   - Append to `toResize` list.
2. Concurrency-bound resize (`:151-163`) using a static `SemaphoreSlim((int)Math.Ceiling(ProcessorCount/2.0))`.
3. For each cover, `EnsureResizedCovers(series, cover, forceResize: !alreadyExists)`:
   - Heights matrix:
     - `Poster` / `Headshot`: `[500, 250]`
     - `Banner`: `[70, 35]`
     - `Fanart` / `Screenshot`: `[360, 180]`
     - `Clearlogo` / `Unknown`: no resize (default branch returns).
   - For each height: write `<lowername>-<height>.<ext>` if missing, zero-byte, or `forceResize`.
4. Publish `MediaCoversUpdatedEvent(series, updated)`.

### Pipeline (per `SeriesDeletedEvent`)
`HandleAsync(SeriesDeletedEvent)` (`:240-250`): for each deleted series, recursively delete `<AppData>/MediaCover/<seriesId>/` if it exists.

### `ConvertToLocalUrls(seriesId, covers)`
Used by API resource mappers to rewrite `RemoteUrl` to a Sonarr-served URL.
- `seriesId == 0` (lookup result, not yet added): proxy the URL through `MediaCoverProxy.RegisterUrl`. Produces `<UrlBase>/MediaCoverProxy/<sha256>/<filename>` (`MediaCoverService.cs:74-82` + `MediaCoverProxy.cs:33-48`).
- `seriesId != 0` (real series): produce `<UrlBase>/MediaCover/<seriesId>/<lowercover>[<.jpg|.png>]`. Append `?lastWrite=<ticks>` query string for cache-busting if file exists (`:96-100`).

### `MediaCoverProxy` (in-memory hash → URL cache)
- `RegisterUrl(url)` → SHA-256 of the URL; stores `hash → url` in an `ICached<string>` with 24h TTL (`MediaCoverProxy.cs:42`); returns `<UrlBase>/MediaCoverProxy/<hash>/<filename>`.
- `GetImage(hash)` → resolves URL, fetches via `HttpClient.GetAsync`, returns bytes. Throws `KeyNotFoundException` if hash expired.
- Served by `Sonarr.Http/Frontend/Mappers/MediaCoverProxyMapper.cs:12` — matches `/MediaCoverProxy/<hash>/<filename>.(jpg|png|gif)` and returns `FileContentResult`.

### Frontend serving
- `/MediaCover/...` is handled by `Sonarr.Http/Frontend/Mappers/MediaCoverMapper.cs:11` (a `StaticResourceMapperBase`). It resolves `<AppData>/<resourceUrl>`. If the requested resized variant doesn't exist, it falls back to the full-size base file via the regex `RegexResizedImage = -\d+\.jpg($|\?)`.
- There is *also* a v3 controller route at `/api/v3/MediaCover/{seriesId:int}/{filename}` (`Sonarr.Api.V3/MediaCovers/MediaCoverController.cs:28-47`) that does the same fallback. Marked `// TODO: This code can be removed later once everyone had the update for a while.` at `:36`.

### Image library
- `SixLabors.ImageSharp` (`ImageResizer.cs:3-5`). `Image.Load(source)` → `image.Mutate(x => x.Resize(0, height))` (width auto-computed) → `image.Save(destination)`.
- Memory tuned with `SimpleGcMemoryAllocator` (less aggressive caching, slightly slower, reduces RSS on long-running processes). JPEG quality 92.
- The constructor comment at `MediaCoverService.cs:40-42` notes ImageSharp is slow on ARM — hence the semaphore at `Math.Ceiling(ProcessorCount/2.0)`. On a 4-core box that's 2 concurrent resizes; on 8-core it's 4.
- `_enabled` field in `ImageResizer` is set to `true` unconditionally (`ImageResizer.cs:22-24`) — the previous-revision feature flag is now effectively a no-op.

### Static file path layout
- `<AppData>/MediaCover/<seriesId>/poster.jpg` (full)
- `<AppData>/MediaCover/<seriesId>/poster-500.jpg` (resize)
- `<AppData>/MediaCover/<seriesId>/poster-250.jpg`
- … same pattern for `banner`, `fanart`, `screenshot`, `headshot`, `clearlogo`.
- `Clearlogo` is `.png` (`MediaCoverService.cs:228-230`). Everything else is `.jpg`.

### `CacheableSpecification` integration
The static-file middleware excludes `/MediaCover` from cache-control rewriting (`Sonarr.Http/Middleware/CacheableSpecification.cs` — search for `IgnoredPaths.Add("/MediaCover")` referenced via `Startup.cs:281`).

### DB tables
- None directly. Reads `Series.Images` (`MediaCover` is `IEmbeddedDocument`, stored inline as JSON in `Series` row).

### Events raised / consumed
- Consumes: `SeriesUpdatedEvent`, `SeriesDeletedEvent`.
- Raises: `MediaCoversUpdatedEvent` (consumed by SignalR for poster refresh push).

### Quirks / TODOs / HACKs
- Per-process `static` semaphore at `MediaCoverService.cs:42`. Fine for single-instance runtime.
- `ImageResizer._enabled = true` constant at `:22-24` — was once a feature flag, now hardcoded. Easy to delete during port.
- `CoverAlreadyExistsSpecification.AlreadyExists` does an HTTP HEAD for *every* poster on *every* series-update — that's a lot of round trips for a fully-synced library. Cross-reference: this is gated only by file existence, not last-modified time.
- Resize failure deletes the destination file (`ImageResizer.cs:51-55`) and re-throws — ensures we don't leave a half-written image. Caller (`MediaCoverService.cs:213`) catches the throw silently and logs at debug.
- `MediaCoverProxy` does **not** stream — it loads the full image into memory before returning bytes.
- The v3 controller's `// TODO: This code can be removed later` (`MediaCoverController.cs:36`) — the static-file mapper is the canonical path.

### Test files
- `src/NzbDrone.Core.Test/MediaCoverTests/MediaCoverServiceFixture.cs`
- `src/NzbDrone.Core.Test/MediaCoverTests/ImageResizerFixture.cs`

### Open questions
- **MC-Q1**: ImageSharp → Go equivalent. Candidates: `disintegration/imaging`, `h2non/bimg` (libvips), `chai2010/webp`. Already covered by `spikes/imaging-imagesharp.md` per the README; align with the spike's verdict.
- **MC-Q2**: Per-cover HEAD on every refresh — feasible to add a cheaper check (`If-Modified-Since`) or persist `last-modified` per cover? Not a port-blocker.
- **MC-Q3**: `MediaCoverProxy` 24h cache TTL is hard-coded (`MediaCoverProxy.cs:42`). Verify cache eviction policy in the Go port: do we honor TTL or evict on add-series-completed?
- **MC-Q4**: The static-file mapper uses a regex fallback to serve full-size when resize is missing. Should the Go port pre-render on demand instead (lazy resize) to save startup cost on huge libraries?
- **MC-Q5**: `_enabled = true` constant in `ImageResizer` — was there once a path that disabled resizing? If so, port-time decision: keep the field, drop it, or expose as config?

---

## 9. Annotations (Core/Annotations)

### Purpose
The C#-side decoration that drives the frontend's "settings page schema" output. Every provider (indexer, download client, notification, import list, custom format spec, root folder, etc.) declares its settings as a class with `[FieldDefinition]`-annotated properties; the API serialises these into `Field` objects (in `Sonarr.Http.ClientSchema`) that the React app renders into form widgets. **API-contract sensitive** — the frontend depends on field-shape stability.

### Public types
| Type | File | Role |
| --- | --- | --- |
| `FieldDefinitionAttribute : Attribute` | `Annotations/FieldDefinitionAttribute.cs:8` | The big one. See properties below. |
| `FieldOptionAttribute : Attribute` | `:32` | Decorates *enum members* with display label/hint and a `[CallerLineNumber]` default `Order`. |
| `FieldTokenAttribute : Attribute` | `:47` | Lets a property declare token-substitutions for `Label`/`HelpText`/`HelpTextWarning` localised strings. `[AllowMultiple = true]`. |
| `FieldSelectOption<T>` | `:63` | Generic "value/name/order/hint/parentValue/isDisabled/additionalProperties" record. Used by select-options endpoints. |
| `FieldSelectStringOption` | `:75` | String-typed sibling of the above. |
| `FieldType` enum | `:86` | `Textbox, Number, Password, Checkbox, Select, Path, FilePath, Tag, Action, Url, Captcha, OAuth, Device, TagSelect, RootFolder, QualityProfile, SeriesTag, KeyValueList`. |
| `HiddenType` enum | `:108` | `Visible, Hidden, HiddenIfNotSet`. |
| `PrivacyLevel` enum | `:115` | `Normal, Password, ApiKey, UserName`. |
| `TokenField` enum | `:123` | `Label, HelpText, HelpTextWarning`. |
| `SelectOption` | `Annotations/SelectOption.cs:3` | `{ Value int, Name, Order int, Hint }`. Non-generic int-keyed sibling of `FieldSelectOption<T>`. |
| `ISelectOptionsConverter` | `Annotations/SelectOptionsConverter.cs:5` | One method `List<SelectOption> GetSelectOptions()`. Implementations live across `Indexers/`, `DownloadClients/`, etc. |

### `FieldDefinitionAttribute` — every property
| Property | Type | Meaning |
| --- | --- | --- |
| `Order` (ctor arg) | `int` | Display order. Renumbered after recursion in `SchemaBuilder.GetFieldMappings` (`SchemaBuilder.cs:96-100`). |
| `Label` | `string` | i18n key resolved via `ILocalizationService.GetLocalizedString(...)`. |
| `Unit` | `string` | UI suffix (e.g. "minutes"). |
| `HelpText` | `string` | i18n-resolved tooltip. |
| `HelpTextWarning` | `string` | i18n-resolved warning text. |
| `HelpLink` | `string` | URL. |
| `Type` | `FieldType` | One of 18 widget kinds. |
| `Advanced` | `bool` | If true, only shows under "Advanced settings" toggle. |
| `SelectOptions` | `Type` | Either an `enum` or an `ISelectOptionsConverter` impl class. Used for `Select`/`TagSelect`. |
| `SelectOptionsProviderAction` | `string` | Alternative to `SelectOptions` — a backend action name the frontend should call to populate the options dynamically. |
| `Section` | `string` | UI section grouping. |
| `Hidden` | `HiddenType` | `Visible | Hidden | HiddenIfNotSet`. |
| `Privacy` | `PrivacyLevel` | `Normal | Password | ApiKey | UserName`. Drives schema masking with `********` (`SchemaBuilder.cs:18,40-44`). |
| `Placeholder` | `string` | Input placeholder text. |

### Schema produced
`Sonarr.Http/ClientSchema/SchemaBuilder.ToSchema(model)` (`SchemaBuilder.cs:27-50`) walks `[FieldDefinition]` properties and emits `Field` objects with name/label/unit/helpText/helpTextWarning/helpLink/order/advanced/type (lowerCamelCase string)/section/privacy/placeholder/hidden/selectOptions|selectOptionsProviderAction/isFloat. Privacy-Password / Privacy-ApiKey values are replaced with `********` before serialization.

### Read-back
`SchemaBuilder.ReadFromSchema` (`:52-84`) re-applies values posted from the frontend onto a fresh instance. If the user's posted value is `********` and the field is privacy-sensitive, it preserves the existing model's value so the user doesn't have to re-enter every save (`:67-75`).

### `FieldOptionAttribute` (enum-side)
Used like `[FieldOption(label: "RSS Sync", hint: "Default")]` on enum members. `Order` defaults to `[CallerLineNumber]` so source order is preserved unless overridden. `SchemaBuilder.GetSelectOptions(enum)` (`:216-253`) reads it.

### `FieldTokenAttribute`
Allows `[FieldToken(TokenField.Label, "MyLabel", "amount", 5)]` to inject `{amount}` into the i18n key resolution. Multiple per property allowed. `SchemaBuilder.GetTokens` (`:198-214`) collects them.

### Tests
- `src/NzbDrone.Api.Test/ClientSchemaTests/SchemaBuilderFixture.cs` (verify on port).

### Quirks / TODOs / HACKs
- Privacy-Password values become `********` over the wire **and** `********` is treated as a sentinel value on the way back in. If a user actually wants their password to be `********` they cannot — minor edge case.
- `SchemaBuilder._mappings` is a static `Dictionary<Type, FieldMapping[]>` (`:19`). Cached forever per process. Reflection traversal happens once per type.
- The `_mappings.TryGetValue` lock at `:90-107` is a coarse global lock; for a single-instance app it's fine.

### Open questions
- **ANNO-Q1**: The Go port has no `[FieldDefinition]` equivalent. Options: (a) struct tags (`json:"…" field:"order=10,label=…"`), (b) explicit registration of a `FieldDefinition{Name, Label, …}` slice next to the struct, (c) generate the schema from JSON-Schema or OpenAPI. (a) maps cleanly to existing reflection-based builders. (b) is more verbose but fully type-safe. Pick a pattern early — every provider is touched.
- **ANNO-Q2**: `********` sentinel-value in privacy round-trip — keep semantics or move to a structured `{ value, redacted: true }` shape? Frontend currently depends on the string sentinel.
- **ANNO-Q3**: `FieldType` has 18 values; React renders them with hand-coded widgets. Any new widget must land in both repos. Document the contract.
- **ANNO-Q4**: `SelectOptions` accepts *either* an enum type *or* an `ISelectOptionsConverter` class. Two paths in `GetSelectOptions(Type)` (`SchemaBuilder.cs:218-260`). Document the polymorphism for porters; the Go side could unify by always implementing `ISelectOptionsConverter`-equivalent.
- **ANNO-Q5**: `[CallerLineNumber]` default on `FieldOptionAttribute.Order` means refactoring an enum re-orders its options if the user didn't specify `order:`. Easy footgun for porters.

---

## 10. Exceptions (Core/Exceptions)

### Purpose
Small registry of typed exceptions used to convey error-with-status-code through the call stack. The mapping to HTTP status codes happens in the API layer (`SonarrErrorPipeline` in `Sonarr.Http`) — this directory only declares the types.

### Inventory
| Type | File | Base | Carries | Default HTTP status |
| --- | --- | --- | --- | --- |
| `NzbDroneClientException` | `Exceptions/NzbDroneClientException.cs:7` | `NzbDroneException` | `HttpStatusCode StatusCode` | (caller-supplied) |
| `DownstreamException` | `Exceptions/DownstreamException.cs:6` | `NzbDroneException` | `HttpStatusCode StatusCode` | (caller-supplied) |
| `BadRequestException` | `Exceptions/BadRequestException.cs:5` | `DownstreamException` | — | `400 BadRequest` |
| `ReleaseDownloadException` | `Exceptions/ReleaseDownloadException.cs:7` | `NzbDroneException` | `ReleaseInfo Release` | (caller-supplied) |
| `DownloadClientRejectedReleaseException` | `Exceptions/DownloadClientRejectedReleaseException.cs:6` | `ReleaseDownloadException` | inherits `Release` | (caller-supplied; see download.md) |
| `ReleaseBlockedException` | `Exceptions/ReleaseBlockedException.cs:6` | `ReleaseDownloadException` | inherits `Release` | (caller-supplied) |
| `ReleaseUnavailableException` | `Exceptions/ReleaseUnavailableException.cs:6` | `ReleaseDownloadException` | inherits `Release` | (caller-supplied) |
| `SearchFailedException` | `Exceptions/SearchFailedException.cs:5` | `NzbDroneException` | — | (caller-supplied) |
| `SeriesNotFoundException` | `Exceptions/SeriesNotFoundException.cs:5` | `NzbDroneException` | `int TvdbSeriesId` | typically `404 NotFound` |

### `StatusCodeToExceptions.VerifyStatusCode` extension
At `Exceptions/StatusCodeToExceptions.cs:8-30`. Convenience translator for callers parsing third-party HTTP responses:
- `400 BadRequest` → `BadRequestException(message)`
- `401 Unauthorized` → `System.UnauthorizedAccessException(message)` (note: stdlib type, not Sonarr's hierarchy)
- `402 PaymentRequired` → `DownstreamException(402, message)`
- `500 InternalServerError` → `DownstreamException(500, message)`
- All other status codes → no exception (silently passes).

### Quirks / TODOs / HACKs
- `StatusCodeToExceptions.VerifyStatusCode` only knows 4 codes — every other 4xx/5xx slips through as a no-op. Callers in `Indexers/Newznab/Newznab.cs` etc. use it as a "throw on these specific bad codes" helper; *not* an exhaustive mapping.
- `UnauthorizedAccessException` is a `System` type, breaking the `NzbDroneException` invariant for one branch.
- `SeriesNotFoundException`'s default message says "removed from TheTVDB" (`SeriesNotFoundException.cs:11`) — that's only one cause. The other constructors let you override.
- `CloudFlareCaptchaException` is also an exception type but lives under `Http/CloudFlare/` not here. See area 1.
- `InvalidSceneMappingException` lives under `DataAugmentation/Scene/`. See area 4.2.
- `TooManyRequestsException` and `HttpException` and `TlsFailureException` all live under `NzbDrone.Common.Http/`. See `common.md`.

### Open questions
- **EXC-Q1**: Go has no exception hierarchy. Map to `errors.Is`/`errors.As` with `StatusCoded` interface? Keep `StatusCode int` field on a `CoreError` struct and wrap with `%w`.
- **EXC-Q2**: `UnauthorizedAccessException` is `System.UnauthorizedAccessException` — a stdlib type. Decide whether to keep the inconsistency (matches existing handler logic in `SonarrErrorPipeline`) or flatten to a Sonarr-typed `UnauthorizedException`.
- **EXC-Q3**: `StatusCodeToExceptions` only maps four status codes. Document the call sites that use it (`grep` for `VerifyStatusCode`) and decide whether to broaden coverage in the Go port or keep narrow-by-design.

---

## 11. Properties (Core/Properties)

### Purpose
`AssemblyInfo.cs` — the file is one line:
```
[assembly: InternalsVisibleTo("Sonarr.Core.Test")]
```
Declares that the test assembly can see `internal` members of `NzbDrone.Core`. No version metadata.

### How `BuildInfo` reads version
`NzbDrone.Common/EnvironmentInfo/BuildInfo.cs` (covered in `common.md`) reads `Assembly.GetExecutingAssembly().GetName().Version` and `AssemblyConfigurationAttribute` from the **`NzbDrone.Common.dll`** assembly itself. The actual `Version` value comes from MSBuild props (`<Version>`/`<AssemblyVersion>`/`<FileVersion>` in `Directory.Build.props` / project files), not from this `AssemblyInfo.cs`. So `Properties/AssemblyInfo.cs` is purely a build-time access modifier and contributes nothing to runtime metadata.

### Open questions
- **PROP-Q1**: Go has package-level visibility (lower-case unexported), no `InternalsVisibleTo`. Tests live in the same package (`foo` + `foo_test.go`) for full access, or use the `_test` package suffix for black-box. No port action needed for this directory.
- **PROP-Q2**: Where does the runtime version come from in the Go port? Recommendation: `runtime/debug.ReadBuildInfo()` at startup, plus `-ldflags "-X main.version=..."` injection at build for a stable tag. Capture in the build/release card.

---

## Recommended split

The card is large but each area is genuinely small. One area justifies its own card:

- **`indexersearch.md`** — Area 7 alone is ~200 lines. The orchestration is intricate (scene-mapping → criteria → indexer dispatch → decision dedup) and intersects three other cards (`indexers.md`, `decisionengine.md`, `download.md`). Splitting it out gives each Phase-1 PR one referenceable card per cross-cutting concern. Keep the rest as a long-tail card.

The other ten areas are genuinely tiny enough that bundling is correct (analytics is a 40-line file, properties is 1 line, exceptions is 9 small classes, etc.). Splitting any of those further would just create card-overhead.

---

## Open questions

### HTTP-Q* (Core/Http)
- **HTTP-Q1**: Drop torcache.net interceptor in Go port? — unresolved, blocks Phase 1 (decision needed before porting Common.Http).
- **HTTP-Q2**: CloudFlare CAPTCHA detection — is the legacy regex still hitting any real responses? — unresolved, blocks Phase 1.
- **HTTP-Q3**: Bypass-list parser must support `*.host` glob + CIDR — unresolved, blocks http-client spike.
- **HTTP-Q4**: Interceptor chain vs `http.RoundTripper` middleware — unresolved, blocks Phase 1.

### INSTR-Q* (Core/Instrumentation)
- **INSTR-Q1**: One-row-per-INSERT vs batched commit window — unresolved, blocks Phase 1 logging design.
- **INSTR-Q2**: 7-day retention should be config-driven? — unresolved, blocks Phase 1.
- **INSTR-Q3**: `ReconfigureLogging` doesn't tear down stale syslog rule — unresolved, file as bug-or-by-design.
- **INSTR-Q4**: SQLite local-time vs Postgres UTC inconsistency — unresolved, blocks Phase 1 DB schema parity.
- **INSTR-Q5**: Pick Go logging library (slog vs zerolog vs zap) — unresolved, blocks Phase 1 logging spike.
- **INSTR-Q6**: Sentry / phone-home — unresolved, blocks Phase 0.5 ANALYTICS gate.

### ANALYTICS-Q* (Core/Analytics)
- **ANALYTICS-Q1**: Phone home at all? Default opt-in vs opt-out vs none — unresolved, **stop-the-world** for Phase 1.
- **ANALYTICS-Q2**: Sentry DSN — unresolved, blocks Phase 1.
- **ANALYTICS-Q3**: services.sonarr.tv functional dependencies (daily-series, scene-mapping) survive even if telemetry dropped — unresolved doc decision.
- **ANALYTICS-Q4**: Frontend behaviour when `analytics: false` in `/initialize.json` — unresolved, blocks frontend port checklist.
- **ANALYTICS-Q5**: Service surface (inject `HistoryService` directly) — unresolved, blocks Phase 1.

### DA-Q* (Core/DataAugmentation)
- **DA-Q1**: Daily-series 1h cache TTL hard-coded — unresolved, low priority.
- **DA-Q2**: Daily-series silent failure surfaces no health check — unresolved, low priority.
- **DA-Q3**: Scene-mapping refresh policy user-tunable? — unresolved, low priority.
- **DA-Q4**: Fate/Zero hardcoded TVDB-id hack still needed? — unresolved, blocks Phase 1 only if we want to delete dead code.
- **DA-Q5**: `InvalidSceneMappingException` "Please notify Sonarr developers" wording — unresolved, doc/wording fix.
- **DA-Q6**: XEM cache-extend behaviour during outage — unresolved, verification needed.
- **DA-Q7**: `XemService.ExtrapolateMappings` is heuristic-heavy and untested — unresolved, blocks Phase 1 (port with table-driven tests).
- **DA-Q8**: XEM dedup hack persistence — unresolved, low priority.

### CFLT-Q* (Core/CustomFilters — note prefix `CFLT` to avoid collision with `CustomFormats`)
- **CFLT-Q1**: Free-form JSON column vs `jsonb` — unresolved, blocks Phase 1 schema decision (likely keep as text).
- **CFLT-Q2**: `Type` enumeration values used by frontend — unresolved, doc only.

### SS-Q* (Core/SeriesStats)
- **SS-Q1**: SQLite `GROUP_CONCAT(DISTINCT …)` parity — unresolved, perf optimization.
- **SS-Q2**: `LastAired` excludes season 0 but `NextAiring`/`PreviousAiring` don't — unresolved, doc/bug.
- **SS-Q3**: Pipe-encoded string column vs `pq.StringArray` in Go port — unresolved, blocks Phase 1.
- **SS-Q4**: Mono GHI 3518 try/catch can be dropped — unresolved, port-time cleanup.

### IS-Q* (Core/IndexerSearch)
- **IS-Q1**: `SeriesSearchService` `missingOnly = !UpgradeAllowed` semantics — unresolved, blocks Phase 1.
- **IS-Q2**: Bounded concurrency in `Dispatch` (Task.WhenAll over indexers) — unresolved, perf decision.
- **IS-Q3**: Per-indexer exception swallowing vs health-check surfacing — unresolved, blocks indexer health design.
- **IS-Q4**: Dead code `EpisodeSearchService.IsMonitored` — unresolved, port-time cleanup.

### MC-Q* (Core/MediaCover)
- **MC-Q1**: ImageSharp → Go (covered by `spikes/imaging-imagesharp.md`) — track in spike.
- **MC-Q2**: HEAD-on-every-poster cost — unresolved, low priority.
- **MC-Q3**: 24h proxy-cache TTL — unresolved, low priority.
- **MC-Q4**: Lazy-resize vs eager-resize — unresolved, blocks Phase 1 design.
- **MC-Q5**: `_enabled = true` constant in `ImageResizer` — unresolved, port-time cleanup.

### ANNO-Q* (Core/Annotations)
- **ANNO-Q1**: Go pattern for `[FieldDefinition]` (struct tags vs explicit slice) — unresolved, **blocks Phase 1** (every provider uses this).
- **ANNO-Q2**: `********` sentinel-value contract with frontend — unresolved, blocks Phase 1.
- **ANNO-Q3**: `FieldType`-vs-React-widget contract — unresolved, doc only.
- **ANNO-Q4**: `SelectOptions` type vs `ISelectOptionsConverter` polymorphism — unresolved, port design.
- **ANNO-Q5**: `[CallerLineNumber]` default on `FieldOptionAttribute.Order` — unresolved, port-time pitfall doc.

### EXC-Q* (Core/Exceptions)
- **EXC-Q1**: Go error-typing strategy (`errors.As` + `StatusCoded` iface) — unresolved, blocks Phase 1.
- **EXC-Q2**: Stdlib `UnauthorizedAccessException` mapping — unresolved, port design.
- **EXC-Q3**: `StatusCodeToExceptions` four-status coverage — unresolved, port decision.

### PROP-Q* (Core/Properties)
- **PROP-Q1**: `InternalsVisibleTo` has no Go equivalent — resolved (use same-package tests / `_test` suffix). Documentation only.
- **PROP-Q2**: Runtime version source in Go port (`debug.ReadBuildInfo` + `-ldflags`) — unresolved, blocks build/release card.
